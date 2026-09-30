// Package settings 提供全局设置服务：内存缓存、校验、持久化与受信任网段解析。
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"sync"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const settingsKey = "global"

// Option 调整 Load 的行为。
type Option func(*options)

type options struct {
	getenv   func(string) string
	readlink func(string) (string, error)
}

// WithGetenv 注入环境变量查询（默认 os.Getenv），用于系统时区探测。
func WithGetenv(f func(string) string) Option { return func(o *options) { o.getenv = f } }

// WithReadlink 注入读符号链接函数（默认 os.Readlink），用于系统时区探测。
func WithReadlink(f func(string) (string, error)) Option { return func(o *options) { o.readlink = f } }

// Service 持有全局设置的内存缓存，并在更新时落库。
type Service struct {
	db *store.DB

	mu   sync.RWMutex
	cur  model.Settings
	nets []netip.Prefix
}

// Load 读取库中的设置；库里没有时使用默认值（不立即写入）。
// 先铺默认值再反序列化，新增字段因此自动取默认值。
func Load(ctx context.Context, db *store.DB, opts ...Option) (*Service, error) {
	o := options{getenv: os.Getenv, readlink: os.Readlink}
	for _, f := range opts {
		f(&o)
	}
	cur := Defaults(DetectTimezone(o.getenv, o.readlink))

	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return nil, fmt.Errorf("读取全局设置: %w", err)
	default:
		if err := json.Unmarshal([]byte(raw), &cur); err != nil {
			return nil, fmt.Errorf("解析全局设置: %w", err)
		}
	}
	normalize(&cur)
	if err := Validate(cur); err != nil {
		return nil, fmt.Errorf("库中的全局设置不合法: %w", err)
	}
	nets, err := parseNets(cur.TrustedProxies)
	if err != nil {
		return nil, err
	}
	return &Service{db: db, cur: cur, nets: nets}, nil
}

// Get 返回当前设置的副本。
func (s *Service) Get() model.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.cur)
}

// TrustedNets 返回预解析好的受信任网段副本。
func (s *Service) TrustedNets() []netip.Prefix {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.nets)
}

// Update 先校验、再落库、最后替换缓存与网段；校验失败返回 model.FieldErrors。
func (s *Service) Update(ctx context.Context, n model.Settings) error {
	n = clone(n)
	normalize(&n)
	if err := Validate(n); err != nil {
		return err
	}
	nets, err := parseNets(n.TrustedProxies)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("序列化全局设置: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKey, string(raw))
	if err != nil {
		return fmt.Errorf("保存全局设置: %w", err)
	}
	s.cur, s.nets = n, nets
	return nil
}

// normalize 保证 trusted_proxies 为非 nil 切片，序列化时永远是数组。
func normalize(v *model.Settings) {
	if v.TrustedProxies == nil {
		v.TrustedProxies = []string{}
	}
}

func clone(v model.Settings) model.Settings {
	v.TrustedProxies = slices.Clone(v.TrustedProxies)
	return v
}

func parseNets(list []string) ([]netip.Prefix, error) {
	nets := make([]netip.Prefix, 0, len(list))
	for _, p := range list {
		n, err := parseProxy(p)
		if err != nil {
			return nil, fmt.Errorf("解析受信任反代 %q: %w", p, err)
		}
		nets = append(nets, n)
	}
	return nets, nil
}
