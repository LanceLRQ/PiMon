// Package proxies 管理全局代理列表：CRUD、认证加密入库、连通性测试与引用查询。
//
// 认证（用户名与密码）经 secret.Box 加密后存入 auth_enc，API 永不回显，
// 更新时认证留空表示保留原值。哪些实例引用了代理由 Referrers 接口回答，
// 由实例仓库实现，避免本包依赖实例包。
package proxies

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
)

// DefaultTestURL 是代理测试的默认目标：返回 204 的轻量地址。
const DefaultTestURL = "https://www.google.com/generate_204"

const testTimeout = 10 * time.Second

// ErrNotFound 表示代理不存在。
var ErrNotFound = errors.New("代理不存在")

// InUseError 表示代理仍被实例引用，Referrers 列出引用它的实例。
type InUseError struct {
	Referrers []model.ProxyReferrer
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("代理仍被 %d 个实例引用", len(e.Referrers))
}

// Referrers 回答"谁在用这个代理"，并能把引用改为直连。由实例仓库实现。
type Referrers interface {
	// ListByProxy 按代理 id 列出引用它的实例（至少含 id 与名称）。
	ListByProxy(ctx context.Context, proxyID string) ([]model.ProxyReferrer, error)
	// ResetToDirect 把所有引用该代理的实例改为直连。
	ResetToDirect(ctx context.Context, proxyID string) error
}

// Config 是 Store 的依赖。
type Config struct {
	DB        *store.DB
	Box       *secret.Box
	Clock     clock.Clock
	Referrers Referrers
}

// Store 是代理仓库。
type Store struct {
	db   *store.DB
	box  *secret.Box
	clk  clock.Clock
	refs Referrers
}

// New 创建代理仓库。
func New(c Config) *Store {
	return &Store{db: c.DB, box: c.Box, clk: c.Clock, refs: c.Referrers}
}

type authData struct {
	User string `json:"u"`
	Pass string `json:"p"`
}

const selectCols = `id, name, scheme, address, remote_dns, location, auth_enc, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scan(r rowScanner) (model.Proxy, string, error) {
	var (
		p                     model.Proxy
		remote                int
		authEnc, created, upd string
	)
	if err := r.Scan(&p.ID, &p.Name, &p.Scheme, &p.Address, &remote, &p.Location, &authEnc, &created, &upd); err != nil {
		return model.Proxy{}, "", err
	}
	p.RemoteDNS = remote == 1
	p.Auth.Set = authEnc != ""
	var err error
	if p.CreatedAt, err = store.ParseTime(created); err != nil {
		return model.Proxy{}, "", err
	}
	if p.UpdatedAt, err = store.ParseTime(upd); err != nil {
		return model.Proxy{}, "", err
	}
	return p, authEnc, nil
}

// List 按名称排序返回全部代理；无数据时返回空切片。
func (s *Store) List(ctx context.Context) ([]model.Proxy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+selectCols+` FROM proxies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.Proxy{}
	for rows.Next() {
		p, _, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) get(ctx context.Context, id string) (model.Proxy, string, error) {
	p, enc, err := scan(s.db.QueryRowContext(ctx, `SELECT `+selectCols+` FROM proxies WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Proxy{}, "", ErrNotFound
	}
	return p, enc, err
}

// Get 返回单个代理（认证只给"已设置"标记）。
func (s *Store) Get(ctx context.Context, id string) (model.Proxy, error) {
	p, _, err := s.get(ctx, id)
	return p, err
}

// normalized 是校验并规整后的输入。
type normalized struct {
	name, scheme, address, location string
	remoteDNS                       bool
}

// validate 校验输入；失败返回 model.FieldErrors。
func validate(in model.ProxyInput) (normalized, model.FieldErrors) {
	fe := model.FieldErrors{}
	n := normalized{name: strings.TrimSpace(in.Name), scheme: in.Scheme, address: strings.TrimSpace(in.Address), location: in.Location}
	if n.name == "" {
		fe["name"] = model.FieldRequired
	}
	switch n.scheme {
	case model.ProxySchemeHTTP, model.ProxySchemeHTTPS:
		n.remoteDNS = true
	case model.ProxySchemeSOCKS5:
		if in.RemoteDNS {
			n.scheme = model.ProxySchemeSOCKS5H
			n.remoteDNS = true
		}
	case model.ProxySchemeSOCKS5H:
		n.remoteDNS = true
	case "":
		fe["scheme"] = model.FieldRequired
	default:
		fe["scheme"] = model.FieldInvalid
	}
	if n.address == "" {
		fe["address"] = model.FieldRequired
	} else if strings.ContainsAny(n.address, "/@?#") || proxy.ValidateHostPort(n.address) != nil {
		fe["address"] = model.FieldInvalid
	}
	switch n.location {
	case "":
		n.location = model.ProxyLocationAny
	case model.ProxyLocationHub, model.ProxyLocationLAN, model.ProxyLocationAny:
	default:
		fe["location"] = model.FieldInvalid
	}
	if in.Auth != nil && (in.Auth.Username != "" || in.Auth.Password != "") {
		if in.ClearAuth {
			fe["clear_auth"] = model.FieldInvalid
		}
		if in.Auth.Username == "" {
			fe["auth.username"] = model.FieldRequired
		}
	}
	if len(fe) > 0 {
		return n, fe
	}
	return n, nil
}

func hasAuth(in model.ProxyInput) bool {
	return in.Auth != nil && (in.Auth.Username != "" || in.Auth.Password != "")
}

func (s *Store) encryptAuth(a *model.ProxyAuthInput) (string, error) {
	b, err := json.Marshal(authData{User: a.Username, Pass: a.Password})
	if err != nil {
		return "", err
	}
	return s.box.Encrypt(string(b))
}

func (s *Store) decryptAuth(enc string) (authData, error) {
	var a authData
	if enc == "" {
		return a, nil
	}
	plain, err := s.box.Decrypt(enc)
	if err != nil {
		return a, fmt.Errorf("解密代理认证: %w", err)
	}
	err = json.Unmarshal([]byte(plain), &a)
	return a, err
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func isUniqueName(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: proxies.name")
}

// Create 新建代理。校验失败返回 model.FieldErrors（含重名）。
func (s *Store) Create(ctx context.Context, in model.ProxyInput) (model.Proxy, error) {
	n, fe := validate(in)
	if fe != nil {
		return model.Proxy{}, fe
	}
	var enc string
	if hasAuth(in) {
		var err error
		if enc, err = s.encryptAuth(in.Auth); err != nil {
			return model.Proxy{}, err
		}
	}
	id, err := newID()
	if err != nil {
		return model.Proxy{}, err
	}
	now := store.FormatTime(s.clk.Now())
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO proxies (`+selectCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, n.name, n.scheme, n.address, boolInt(n.remoteDNS), n.location, enc, now, now)
	if isUniqueName(err) {
		return model.Proxy{}, model.FieldErrors{"name": model.FieldDuplicate}
	}
	if err != nil {
		return model.Proxy{}, err
	}
	return s.Get(ctx, id)
}

// Update 更新代理。认证缺省或留空保留原值，ClearAuth 清除。
func (s *Store) Update(ctx context.Context, id string, in model.ProxyInput) (model.Proxy, error) {
	_, oldEnc, err := s.get(ctx, id)
	if err != nil {
		return model.Proxy{}, err
	}
	n, fe := validate(in)
	if fe != nil {
		return model.Proxy{}, fe
	}
	enc := oldEnc
	switch {
	case in.ClearAuth:
		enc = ""
	case hasAuth(in):
		if enc, err = s.encryptAuth(in.Auth); err != nil {
			return model.Proxy{}, err
		}
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE proxies SET name=?, scheme=?, address=?, remote_dns=?, location=?, auth_enc=?, updated_at=? WHERE id=?`,
		n.name, n.scheme, n.address, boolInt(n.remoteDNS), n.location, enc, store.FormatTime(s.clk.Now()), id)
	if isUniqueName(err) {
		return model.Proxy{}, model.FieldErrors{"name": model.FieldDuplicate}
	}
	if err != nil {
		return model.Proxy{}, err
	}
	return s.Get(ctx, id)
}

// Delete 删除代理。被引用且 force=false 返回 *InUseError；force=true 先把引用改为直连再删除
// （两步不在同一事务内：改直连成功而删除失败时，引用已是直连，重试即可）。
func (s *Store) Delete(ctx context.Context, id string, force bool) error {
	if _, _, err := s.get(ctx, id); err != nil {
		return err
	}
	refs, err := s.refs.ListByProxy(ctx, id)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		if !force {
			return &InUseError{Referrers: refs}
		}
		if err := s.refs.ResetToDirect(ctx, id); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM proxies WHERE id = ?`, id)
	return err
}

// Resolve 按 id 取解析好的代理；id 为空或 "direct" 表示直连。
// 返回值含认证，可 .URL()（完整 URL，给 Input）或 .Env()（给 exec）。
func (s *Store) Resolve(ctx context.Context, id string) (*proxy.Proxy, error) {
	if id == "" || strings.EqualFold(id, proxy.DirectValue) {
		return proxy.Direct(), nil
	}
	p, enc, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	a, err := s.decryptAuth(enc)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: p.Scheme, Host: p.Address}
	if a.User != "" || a.Pass != "" {
		u.User = url.UserPassword(a.User, a.Pass)
	}
	return proxy.Parse(u.String())
}

// Test 经代理请求 target（空则用 DefaultTestURL）。请求失败不是 error，而是结果里 OK=false；
// 只有代理不存在、目标非法或内部错误才返回 error。只要收到响应（任何状态码）即视为连通。
func (s *Store) Test(ctx context.Context, id, target string) (model.ProxyTestResult, error) {
	if target == "" {
		target = DefaultTestURL
	}
	if u, err := url.Parse(target); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return model.ProxyTestResult{}, model.FieldErrors{"url": model.FieldInvalid}
	}
	p, err := s.Resolve(ctx, id)
	if err != nil {
		return model.ProxyTestResult{}, err
	}
	res := model.ProxyTestResult{URL: target}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return res, err
	}
	tr := p.Transport()
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	start := s.clk.Now()
	resp, err := client.Do(req)
	if err != nil {
		res.Error = redact(err.Error(), p)
		return res, nil
	}
	_ = resp.Body.Close()
	res.OK = true
	res.Status = resp.StatusCode
	res.LatencyMS = s.clk.Now().Sub(start).Milliseconds()
	return res, nil
}

// redact 从错误文本里抹掉代理的完整 URL 与密码，避免泄露到响应。
func redact(msg string, p *proxy.Proxy) string {
	if p.IsDirect() {
		return msg
	}
	msg = strings.ReplaceAll(msg, p.URL(), p.Redacted())
	if u, err := url.Parse(p.URL()); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			msg = strings.ReplaceAll(msg, pw, "***")
		}
	}
	return msg
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
