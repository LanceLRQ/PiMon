package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

var rangeRe = regexp.MustCompile(`^([1-9][0-9]{0,5})([mhd])$`)

// ParseRange 解析查询范围：正整数加单位 m（分钟）、h（小时）或 d（天），如 30m、24h、7d。
func ParseRange(s string) (time.Duration, error) {
	m := rangeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("范围 %q 不合法，应形如 1h、24h、7d", s)
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit, nil
}

// Query 按范围自动选档：不超过原始保留期用原始表，不超过 5 分钟表保留期用 5 分钟表，
// 否则用 1 小时表。参数不合法或范围超出 1 小时表保留期时返回 model.FieldErrors；
// 实例不存在返回 ErrInstanceNotFound。聚合档不含尚未结束的当前桶。
func (s *Service) Query(ctx context.Context, q Query) (model.HistoryResult, error) {
	var one int
	switch err := s.db.QueryRowContext(ctx, `SELECT 1 FROM plugin_instances WHERE id = ?`, q.InstanceID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return model.HistoryResult{}, ErrInstanceNotFound
	case err != nil:
		return model.HistoryResult{}, err
	}

	rs := s.retention()
	fe := model.FieldErrors{}
	if q.Item == "" {
		fe["item"] = model.FieldRequired
	}
	if q.Field != "" && !isRecordedField(q.Field) {
		fe["field"] = model.FieldInvalid
	}
	d, err := ParseRange(q.Range)
	switch {
	case q.Range == "":
		fe["range"] = model.FieldRequired
	case err != nil:
		fe["range"] = model.FieldInvalid
	case d > time.Duration(rs.HourDays)*24*time.Hour:
		fe["range"] = model.FieldOutOfRange
	}
	if len(fe) > 0 {
		return model.HistoryResult{}, fe
	}

	tier, table := model.HistoryTierRaw, "history_raw"
	switch {
	case d <= time.Duration(rs.RawHours)*time.Hour:
	case d <= time.Duration(rs.FiveMinDays)*24*time.Hour:
		tier, table = model.HistoryTier5Min, "history_5m"
	default:
		tier, table = model.HistoryTier1H, "history_1h"
	}

	now := s.clk.Now().UnixMilli()
	res := model.HistoryResult{
		InstanceID: q.InstanceID, Item: q.Item, Field: q.Field, Range: q.Range, Tier: tier,
		From: now - d.Milliseconds(), To: now, Points: []model.HistoryPoint{},
	}
	if res.Field == "" {
		f, err := s.defaultField(ctx, q.InstanceID, q.Item)
		if err != nil {
			return model.HistoryResult{}, err
		}
		if f == "" {
			return res, nil
		}
		res.Field = f
	}

	var rows *sql.Rows
	if tier == model.HistoryTierRaw {
		rows, err = s.db.QueryContext(ctx, `SELECT ts, v, v, v FROM history_raw
WHERE instance_id = ? AND item = ? AND field = ? AND ts >= ? AND ts <= ? ORDER BY ts`,
			q.InstanceID, q.Item, res.Field, res.From, res.To)
	} else {
		step := step5m
		if tier == model.HistoryTier1H {
			step = step1h
		}
		// 桶起点晚于 From - step 的桶与窗口有重叠。
		rows, err = s.db.QueryContext(ctx, `SELECT bucket, v_avg, v_min, v_max FROM `+table+`
WHERE instance_id = ? AND item = ? AND field = ? AND bucket > ? AND bucket <= ? ORDER BY bucket`,
			q.InstanceID, q.Item, res.Field, res.From-step, res.To)
	}
	if err != nil {
		return model.HistoryResult{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var p model.HistoryPoint
		if err := rows.Scan(&p.T, &p.Avg, &p.Min, &p.Max); err != nil {
			return model.HistoryResult{}, err
		}
		res.Points = append(res.Points, p)
	}
	return res, rows.Err()
}

// defaultField 在该数据项已有历史的字段里，按 report 包各类型默认字段的顺序选出默认字段；
// 数据项已不在当前报告里（动态成员消失）时也能查到保留的历史。没有任何历史返回空串。
func (s *Service) defaultField(ctx context.Context, id, item string) (string, error) {
	seen := map[string]bool{}
	for _, t := range report.ItemTypes {
		f, ok := report.DefaultField(t)
		if !ok || seen[f] || !isRecordedField(f) {
			continue
		}
		seen[f] = true
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 WHERE EXISTS (SELECT 1 FROM history_raw WHERE instance_id = ?1 AND item = ?2 AND field = ?3)
OR EXISTS (SELECT 1 FROM history_5m WHERE instance_id = ?1 AND item = ?2 AND field = ?3)
OR EXISTS (SELECT 1 FROM history_1h WHERE instance_id = ?1 AND item = ?2 AND field = ?3)`, id, item, f).Scan(&one)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return "", nil
}
