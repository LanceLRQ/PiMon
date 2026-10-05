package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// QueuedEvent 是持久化事件队列中的一行。
type QueuedEvent struct {
	Seq  int64
	ID   string
	Kind string
	Data json.RawMessage
	At   time.Time
}

// EventStats 是队列的当前状态。
type EventStats struct {
	Pending int64 // 队列里的条数
	Dropped int64 // 因淘汰丢弃的累计条数（重启不归零）
	LastSeq int64 // 已分配的最大序号
}

// AppendEvent 在一个事务内入队并淘汰：同 id 已在队列时不入队（inserted=false，不占 seq）；
// 入队后删除入队时间早于 now-maxAge 的条目，再把超出 maxCount 的最旧条目删到上限内，
// 被删条数累加进丢弃计数并通过 evicted 返回。maxAge 或 maxCount ≤ 0 表示不按该维度淘汰。
func (s *Store) AppendEvent(ctx context.Context, ev QueuedEvent, maxCount int, maxAge time.Duration) (seq int64, inserted bool, evicted int64, err error) {
	now := s.clk.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, 0, fmt.Errorf("入队事件: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var last int64
	if err = tx.QueryRowContext(ctx, `SELECT value FROM event_counters WHERE name = 'next_seq'`).Scan(&last); err != nil {
		return 0, false, 0, fmt.Errorf("读取 seq 计数器: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO event_queue (seq, id, kind, data, at, created_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`, last+1, ev.ID, ev.Kind, string(ev.Data), ev.At.UnixMilli(), now.UnixMilli())
	if err != nil {
		return 0, false, 0, fmt.Errorf("入队事件: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, 0, fmt.Errorf("入队事件: %w", err)
	}
	if n > 0 {
		inserted, seq = true, last+1
		if _, err = tx.ExecContext(ctx, `UPDATE event_counters SET value = ? WHERE name = 'next_seq'`, seq); err != nil {
			return 0, false, 0, fmt.Errorf("更新 seq 计数器: %w", err)
		}
	}
	evicted, err = evictLocked(ctx, tx, now, maxCount, maxAge)
	if err != nil {
		return 0, false, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, false, 0, fmt.Errorf("入队事件: %w", err)
	}
	return seq, inserted, evicted, nil
}

// PruneEvents 按入队时间与条数上限淘汰，返回淘汰条数（已计入丢弃计数）。
func (s *Store) PruneEvents(ctx context.Context, maxCount int, maxAge time.Duration) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("淘汰事件: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	n, err := evictLocked(ctx, tx, s.clk.Now(), maxCount, maxAge)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("淘汰事件: %w", err)
	}
	return n, nil
}

func evictLocked(ctx context.Context, tx *sql.Tx, now time.Time, maxCount int, maxAge time.Duration) (int64, error) {
	var total int64
	if maxAge > 0 {
		res, err := tx.ExecContext(ctx, `DELETE FROM event_queue WHERE created_at < ?`, now.Add(-maxAge).UnixMilli())
		if err != nil {
			return 0, fmt.Errorf("淘汰过期事件: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if maxCount > 0 {
		res, err := tx.ExecContext(ctx, `
DELETE FROM event_queue WHERE seq IN (
    SELECT seq FROM event_queue ORDER BY seq DESC LIMIT -1 OFFSET ?)`, maxCount)
		if err != nil {
			return 0, fmt.Errorf("淘汰超限事件: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if total > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE event_counters SET value = value + ? WHERE name = 'dropped'`, total); err != nil {
			return 0, fmt.Errorf("更新丢弃计数: %w", err)
		}
	}
	return total, nil
}

// PendingEvents 按 seq 升序返回 seq > afterSeq 的至多 limit 条事件。
func (s *Store) PendingEvents(ctx context.Context, afterSeq int64, limit int) ([]QueuedEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, id, kind, data, at FROM event_queue WHERE seq > ? ORDER BY seq LIMIT ?`, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("读取未确认事件: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []QueuedEvent
	for rows.Next() {
		var e QueuedEvent
		var data string
		var at int64
		if err := rows.Scan(&e.Seq, &e.ID, &e.Kind, &data, &at); err != nil {
			return nil, fmt.Errorf("读取未确认事件: %w", err)
		}
		e.Data = json.RawMessage(data)
		e.At = time.UnixMilli(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEvents 按 id 删除事件，返回实际删除条数；不存在的 id 忽略。
func (s *Store) DeleteEvents(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("删除事件: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var total int64
	// 分块避免超过 SQLite 的变量个数上限。
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		part := ids[start:end]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM event_queue WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+`)`, args...)
		if err != nil {
			return 0, fmt.Errorf("删除事件: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("删除事件: %w", err)
	}
	return total, nil
}

// EventStats 返回队列条数、累计丢弃数与已分配的最大序号。
func (s *Store) EventStats(ctx context.Context) (EventStats, error) {
	var st EventStats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_queue`).Scan(&st.Pending); err != nil {
		return st, fmt.Errorf("读取队列状态: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM event_counters WHERE name = 'dropped'`).Scan(&st.Dropped); err != nil {
		return st, fmt.Errorf("读取丢弃计数: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM event_counters WHERE name = 'next_seq'`).Scan(&st.LastSeq); err != nil {
		return st, fmt.Errorf("读取 seq 计数器: %w", err)
	}
	return st, nil
}
