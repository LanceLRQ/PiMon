package instances

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// machineHub 是本期唯一的运行位置。
const machineHub = "hub"

// row 是 plugin_instances 的一行。Config 是普通配置（密钥已剥离）。
type row struct {
	ID              string
	PluginID        string
	Name            string
	Config          map[string]any
	SecretsEnc      string
	IntervalSeconds int
	Paused          bool
	ProxyID         string
	ConfigHash      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// Corrupt 非空表示库里的 config_json 已损坏（Config 为空表），内容是给用户看的原因；
	// 这样的实例以 broken 呈现、不调度，但不影响其他实例的读取与调度。
	Corrupt string
}

const rowCols = `id, plugin_id, name, config_json, secrets_enc, interval_seconds, paused, proxy_id, config_hash, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanRow(sc rowScanner) (row, error) {
	var (
		r                   row
		cfgJSON             string
		paused              int
		created, updatedStr string
	)
	if err := sc.Scan(&r.ID, &r.PluginID, &r.Name, &cfgJSON, &r.SecretsEnc, &r.IntervalSeconds, &paused,
		&r.ProxyID, &r.ConfigHash, &created, &updatedStr); err != nil {
		return row{}, err
	}
	r.Paused = paused == 1
	r.Config = map[string]any{}
	if err := json.Unmarshal([]byte(cfgJSON), &r.Config); err != nil {
		r.Config = map[string]any{}
		r.Corrupt = fmt.Sprintf("实例配置已损坏（不是合法 JSON：%v），请删除后重建", err)
	}
	var err error
	if r.CreatedAt, err = store.ParseTime(created); err != nil {
		return row{}, err
	}
	if r.UpdatedAt, err = store.ParseTime(updatedStr); err != nil {
		return row{}, err
	}
	return r, nil
}

func (s *Service) getRow(ctx context.Context, id string) (row, error) {
	r, err := scanRow(s.db.QueryRowContext(ctx, `SELECT `+rowCols+` FROM plugin_instances WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, ErrNotFound
	}
	return r, err
}

// listRows 按名称、id 排序返回全部实例；先读完再返回，避免单连接下嵌套查询。
func (s *Service) listRows(ctx context.Context) ([]row, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+rowCols+` FROM plugin_instances ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []row{}
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		if r.Corrupt != "" && s.firstCorruptReport(r.ID) {
			s.log.Warn("实例配置已损坏，跳过调度", "instance", r.ID, "reason", r.Corrupt)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// firstCorruptReport 同一实例的配置损坏只记一次日志（列表、重排都会走到这里）。
func (s *Service) firstCorruptReport(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.badWarned[id] {
		return false
	}
	s.badWarned[id] = true
	return true
}

func (s *Service) insertRow(ctx context.Context, r row) error {
	cfg, err := json.Marshal(r.Config)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO plugin_instances (`+rowCols+`, machine)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.PluginID, r.Name, string(cfg), r.SecretsEnc, r.IntervalSeconds, boolInt(r.Paused), r.ProxyID,
		r.ConfigHash, store.FormatTime(r.CreatedAt), store.FormatTime(r.UpdatedAt), machineHub)
	return err
}

func (s *Service) updateRow(ctx context.Context, r row) error {
	cfg, err := json.Marshal(r.Config)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE plugin_instances SET name=?, config_json=?, secrets_enc=?,
interval_seconds=?, paused=?, proxy_id=?, config_hash=?, updated_at=? WHERE id=?`,
		r.Name, string(cfg), r.SecretsEnc, r.IntervalSeconds, boolInt(r.Paused), r.ProxyID, r.ConfigHash,
		store.FormatTime(r.UpdatedAt), r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// contentHash 是实例内容（插件、普通配置、密钥密文）的摘要，只用来判断内容是否变化，
// 不含明文密钥。
func contentHash(pluginID string, plain map[string]any, secretsEnc string) string {
	cfg, _ := json.Marshal(plain) // map 的键按字典序输出，结果稳定
	h := sha256.New()
	h.Write([]byte(pluginID))
	h.Write([]byte{0})
	h.Write(cfg)
	h.Write([]byte{0})
	h.Write([]byte(secretsEnc))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) encodeSecrets(secrets map[string]any) (string, error) {
	if len(secrets) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(secrets)
	if err != nil {
		return "", err
	}
	return s.box.Encrypt(string(raw))
}

func (s *Service) decodeSecrets(enc string) (map[string]any, error) {
	out := map[string]any{}
	if enc == "" {
		return out, nil
	}
	raw, err := s.box.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("密钥无法解密: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("密钥内容损坏: %w", err)
	}
	return out, nil
}

// proxyKey 返回 schema 里第一个 proxy 类型字段的键，没有则为空串（Ruling 28）。
func proxyKey(fields []schema.Field) string {
	for _, f := range fields {
		if f.Type == schema.TypeProxy {
			return f.Key
		}
	}
	return ""
}

// proxyOf 取配置里代理字段的值并规范化：空或 direct 为空串（直连）。
func proxyOf(fields []schema.Field, cfg map[string]any) string {
	key := proxyKey(fields)
	if key == "" {
		return ""
	}
	v, _ := cfg[key].(string)
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, proxy.DirectValue) {
		return ""
	}
	return v
}

// ListByProxy 列出引用该代理的实例（proxies.Referrers）。
func (s *Service) ListByProxy(ctx context.Context, proxyID string) ([]model.ProxyReferrer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM plugin_instances WHERE proxy_id = ? AND proxy_id <> '' ORDER BY name, id`, proxyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.ProxyReferrer{}
	for rows.Next() {
		var r model.ProxyReferrer
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResetToDirect 把引用该代理的实例改为直连（proxies.Referrers），幂等：
// 配置里的代理字段改为 direct，随后让受影响的实例按新配置重新排程。
func (s *Service) ResetToDirect(ctx context.Context, proxyID string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	refs, err := s.ListByProxy(ctx, proxyID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		r, err := s.getRow(ctx, ref.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.resetRowToDirect(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) resetRowToDirect(ctx context.Context, r row) error {
	if p, ok := s.reg.Get(r.PluginID); ok {
		if key := proxyKey(p.Manifest.ConfigSchema); key != "" {
			r.Config[key] = proxy.DirectValue
		}
	}
	r.ProxyID = ""
	r.ConfigHash = contentHash(r.PluginID, r.Config, r.SecretsEnc)
	r.UpdatedAt = s.clk.Now()
	if err := s.updateRow(ctx, r); err != nil {
		return err
	}
	s.syncRow(ctx, r)
	return nil
}
