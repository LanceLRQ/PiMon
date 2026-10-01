package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
)

// syncStore 把当前可用插件的 manifest 写入 manifests 表，
// 不在当前可用集合里的旧记录保留并标为不可用。
func (r *Registry) syncStore(ctx context.Context, plugins []Plugin) error {
	if r.cfg.DB == nil {
		return nil
	}
	now := store.FormatTime(r.cfg.Clock.Now())
	tx, err := r.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	keep := make([]any, 0, len(plugins)*2+1)
	keep = append(keep, MachineHub)
	var cond []string
	for _, p := range plugins {
		raw, err := json.Marshal(p.Manifest)
		if err != nil {
			return fmt.Errorf("序列化 manifest %s: %w", p.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO manifests (machine, plugin_id, version, origin, manifest_json, available, first_seen_at, updated_at)
VALUES (?, ?, ?, ?, ?, 1, ?, ?)
ON CONFLICT (machine, plugin_id, version) DO UPDATE SET
    origin = excluded.origin,
    manifest_json = excluded.manifest_json,
    available = 1,
    updated_at = CASE WHEN manifests.available = 1 AND manifests.manifest_json = excluded.manifest_json
                      AND manifests.origin = excluded.origin
                      THEN manifests.updated_at ELSE excluded.updated_at END`,
			MachineHub, p.ID, p.Manifest.Version, string(p.Origin), string(raw), now, now); err != nil {
			return fmt.Errorf("写入 manifest %s: %w", p.ID, err)
		}
		cond = append(cond, "(plugin_id = ? AND version = ?)")
		keep = append(keep, p.ID, p.Manifest.Version)
	}
	query := `UPDATE manifests SET available = 0, updated_at = ? WHERE machine = ? AND available = 1`
	args := []any{now, MachineHub}
	if len(cond) > 0 {
		query += ` AND NOT (` + strings.Join(cond, " OR ") + `)`
		args = append(args, keep[1:]...)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("标记不可用的 manifest: %w", err)
	}
	return tx.Commit()
}

// StoredManifest 是库里的一条 manifest 记录。
type StoredManifest struct {
	PluginID  string
	Version   string
	Origin    Origin
	Available bool
}

// Stored 列出本机（hub）的全部 manifest 记录，含已不可用的历史版本。
func (r *Registry) Stored(ctx context.Context) ([]StoredManifest, error) {
	rows, err := r.cfg.DB.QueryContext(ctx,
		`SELECT plugin_id, version, origin, available FROM manifests WHERE machine = ? ORDER BY plugin_id, version`, MachineHub)
	if err != nil {
		return nil, fmt.Errorf("查询 manifest: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []StoredManifest
	for rows.Next() {
		var m StoredManifest
		var origin string
		var avail int
		if err := rows.Scan(&m.PluginID, &m.Version, &origin, &avail); err != nil {
			return nil, err
		}
		m.Origin, m.Available = Origin(origin), avail == 1
		out = append(out, m)
	}
	return out, rows.Err()
}
