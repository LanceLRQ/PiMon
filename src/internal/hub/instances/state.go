package instances

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// instState 是一个实例的当前状态，内存为准。私有 state 在 report.State 里。
// rev 每次变化加一，flushedRev 是已落盘的版本，两者不等即脏。
// hash 是当前实例行的 config_hash：采集结果带着运行时的 hash 回来，不符即对应旧配置，丢弃。
type instState struct {
	hash          string
	report        *report.Report
	lastSuccessAt int64 // Unix 毫秒，0 表示从未成功
	lastErr       string
	failures      int
	rev           uint64
	flushedRev    uint64
}

func (st *instState) dirty() bool { return st.rev != st.flushedRev }

// stateSnap 是落盘用的快照，读出后不再持锁。
type stateSnap struct {
	id            string
	rev           uint64
	rep           *report.Report
	lastSuccessAt int64
	lastErr       string
	failures      int
}

// Load 从库里恢复每个实例的当前状态（重启后调用）。没有状态行的实例得到空状态。
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.config_hash, st.report_json, st.last_success_at, st.last_error, st.failures
FROM plugin_instances i LEFT JOIN instance_state st ON st.instance_id = i.id`)
	if err != nil {
		return fmt.Errorf("读取实例状态: %w", err)
	}
	defer func() { _ = rows.Close() }()
	loaded := map[string]*instState{}
	for rows.Next() {
		var (
			id      string
			hash    string
			repJSON sql.NullString
			lastOK  sql.NullInt64
			lastErr sql.NullString
			fails   sql.NullInt64
		)
		if err := rows.Scan(&id, &hash, &repJSON, &lastOK, &lastErr, &fails); err != nil {
			return err
		}
		st := &instState{hash: hash, lastSuccessAt: lastOK.Int64, lastErr: lastErr.String, failures: int(fails.Int64)}
		if repJSON.String != "" {
			var rep report.Report
			if err := json.Unmarshal([]byte(repJSON.String), &rep); err != nil {
				s.log.Warn("实例的已存报告无法解析，已忽略", "instance", id, "err", err)
			} else {
				st.report = &rep
			}
		}
		loaded[id] = st
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.states = loaded
	s.mu.Unlock()
	return nil
}

// ensureState 保证实例有状态条目（创建实例、重排时调用），并记下当前行的 config_hash。
func (s *Service) ensureState(id, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states[id] == nil {
		s.states[id] = &instState{}
	}
	s.states[id].hash = hash
}

// trackHash 在实例行写入后更新状态记下的 config_hash（状态不存在时什么也不做）。
func (s *Service) trackHash(id, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.states[id]; st != nil {
		st.hash = hash
	}
}

// resetState 清空实例的当前状态（配置内容变化后旧值不再可信），并标脏以便覆盖库里的旧行。
// 同时换上新的 config_hash，运行中的旧配置采集随后回来会被丢弃。
func (s *Service) resetState(id, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[id] = &instState{hash: hash, rev: 1}
}

func (s *Service) dropState(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, id)
	delete(s.runLocks, id)
	delete(s.badWarned, id)
}

// lastFor 返回传给插件的上次成功报告与私有 state。
func (s *Service) lastFor(id string) (*report.Report, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[id]
	if st == nil || st.report == nil {
		return nil, ""
	}
	if st.lastSuccessAt > 0 {
		return st.report, st.report.State
	}
	return nil, st.report.State
}

// applyResult 把一次采集结果写入当前状态：成功整份替换，失败保留旧值并标记过期
// （report.Merge），错误文字已由运行时脱敏。实例已被删除时忽略。成功时把报告交给历史记录器。
// hash 是运行时实例行的 config_hash，与当前不符说明结果对应旧配置，整份丢弃（不写状态、不写历史）。
func (s *Service) applyResult(id, hash string, rep *report.Report, err error) {
	now := s.clk.Now()
	s.mu.Lock()
	st := s.states[id]
	if st == nil {
		s.mu.Unlock()
		return
	}
	if st.hash != hash {
		s.mu.Unlock()
		s.log.Debug("配置已变更，丢弃旧配置的采集结果", "instance", id)
		return
	}
	cur, _ := report.Merge(st.report, rep, err)
	st.report = cur
	if err == nil {
		st.lastSuccessAt = now.UnixMilli()
		st.lastErr = ""
		st.failures = 0
	} else {
		st.lastErr = err.Error()
		st.failures++
	}
	st.rev++
	hist := s.hist
	s.mu.Unlock()
	if err == nil {
		hist.Record(id, now, rep)
	}
}

// acquireRun 取实例的运行锁，给定时采集用：一直等到拿到锁或 ctx 结束。
func (s *Service) acquireRun(ctx context.Context, id string) (release func(), err error) {
	lock := s.runLock(id)
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// acquireRunWithin 取实例的运行锁，给保存并测试用：最多等 wait（用注入的时钟计时），
// 超时返回 ErrRunBusy；ctx 结束返回 ctx 的错误。
func (s *Service) acquireRunWithin(ctx context.Context, id string, wait time.Duration) (release func(), err error) {
	lock := s.runLock(id)
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	default:
	}
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.clk.After(wait):
		return nil, ErrRunBusy
	}
}

func (s *Service) runLock(id string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.runLocks[id]
	if l == nil {
		l = make(chan struct{}, 1)
		s.runLocks[id] = l
	}
	return l
}

// WriteErrors 返回当前状态落盘失败的累计次数（hub-self 插件用）。
func (s *Service) WriteErrors() int64 { return s.writeErrs.Load() }

// Flush 把脏状态写入库。失败计入 WriteErrors，状态保持为脏，下次再试。
func (s *Service) Flush(ctx context.Context) error {
	snaps := s.dirtySnaps()
	if len(snaps) == 0 {
		return nil
	}
	if err := s.writeSnaps(ctx, snaps); err != nil {
		s.writeErrs.Add(1)
		return fmt.Errorf("落盘实例状态: %w", err)
	}
	s.mu.Lock()
	for _, sn := range snaps {
		if st := s.states[sn.id]; st != nil && st.rev == sn.rev {
			st.flushedRev = sn.rev
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) dirtySnaps() []stateSnap {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []stateSnap
	for id, st := range s.states {
		if st.dirty() {
			out = append(out, stateSnap{id: id, rev: st.rev, rep: st.report,
				lastSuccessAt: st.lastSuccessAt, lastErr: st.lastErr, failures: st.failures})
		}
	}
	return out
}

func (s *Service) writeSnaps(ctx context.Context, snaps []stateSnap) error {
	now := store.FormatTime(s.clk.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, sn := range snaps {
		repJSON := ""
		if sn.rep != nil {
			raw, err := json.Marshal(sn.rep)
			if err != nil {
				return fmt.Errorf("序列化报告: %w", err)
			}
			repJSON = string(raw)
		}
		// 实例已被删除时整行不写（避免外键失败拖垮整批）。
		if _, err := tx.ExecContext(ctx, `INSERT INTO instance_state (instance_id, report_json, last_success_at, last_error, failures, updated_at)
SELECT ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM plugin_instances WHERE id = ?)
ON CONFLICT (instance_id) DO UPDATE SET report_json = excluded.report_json,
    last_success_at = excluded.last_success_at, last_error = excluded.last_error,
    failures = excluded.failures, updated_at = excluded.updated_at`,
			sn.id, repJSON, sn.lastSuccessAt, sn.lastErr, sn.failures, now, sn.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
