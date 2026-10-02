package screenstate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// MaxOps 是保留的远程操作记录条数，超出的旧记录在写入新记录时清理。
const MaxOps = 500

// RecordOp 写一条远程操作记录。refresh、switch 初始为未送达，其余操作随状态下发，记为已送达。
func (s *Service) RecordOp(ctx context.Context, action string, params map[string]any, clientIP string) (model.ScreenOp, error) {
	if params == nil {
		params = map[string]any{}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return model.ScreenOp{}, err
	}
	delivered := action != model.ScreenActionRefresh && action != model.ScreenActionSwitch
	at := s.clk.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO screen_ops (action, params_json, client_ip, delivered, at) VALUES (?, ?, ?, ?, ?)`,
		action, string(raw), clientIP, boolInt(delivered), store.FormatTime(at))
	if err != nil {
		return model.ScreenOp{}, fmt.Errorf("写入屏幕操作记录: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return model.ScreenOp{}, err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM screen_ops WHERE id <= (SELECT id FROM screen_ops ORDER BY id DESC LIMIT 1 OFFSET ?)`, MaxOps); err != nil {
		return model.ScreenOp{}, fmt.Errorf("清理屏幕操作记录: %w", err)
	}
	return model.ScreenOp{ID: id, Action: action, Params: params, ClientIP: clientIP, Delivered: delivered, At: at.UTC()}, nil
}

// Ops 返回最近的远程操作记录，新到旧；limit 小于 1 或大于 MaxOps 时取 MaxOps。
func (s *Service) Ops(ctx context.Context, limit int) ([]model.ScreenOp, error) {
	if limit < 1 || limit > MaxOps {
		limit = MaxOps
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, action, params_json, client_ip, delivered, at FROM screen_ops ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.ScreenOp{}
	for rows.Next() {
		var (
			op        model.ScreenOp
			raw, at   string
			delivered int
		)
		if err := rows.Scan(&op.ID, &op.Action, &raw, &op.ClientIP, &delivered, &at); err != nil {
			return nil, err
		}
		op.Delivered = delivered != 0
		if err := json.Unmarshal([]byte(raw), &op.Params); err != nil || op.Params == nil {
			op.Params = map[string]any{}
		}
		if op.At, err = store.ParseTime(at); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// MarkDelivered 把一次性指令标记为已送达屏幕；记录不存在时不报错。
func (s *Service) MarkDelivered(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE screen_ops SET delivered = 1 WHERE id = ?`, id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
