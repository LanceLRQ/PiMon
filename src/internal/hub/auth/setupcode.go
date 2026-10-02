package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

// SetupCodeTTL 是设置码的有效期。
const SetupCodeTTL = 24 * time.Hour

// Crockford base32 字母表（不含 I L O U）。
var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// SetupCodes 管理首次设置用的一次性设置码：库中存 sha256 用于校验，
// 另存加密副本（code_enc）让屏幕会话能向管理员显示设置码。
type SetupCodes struct {
	db  *store.DB
	clk clock.Clock
	box *secret.Box
}

// NewSetupCodes 创建设置码服务；box 用于加密显示用的设置码副本。
func NewSetupCodes(db *store.DB, clk clock.Clock, box *secret.Box) *SetupCodes {
	return &SetupCodes{db: db, clk: clk, box: box}
}

// Generate 生成新设置码并覆盖旧码，返回展示形式（6 组 × 4 位）与过期时间。
func (s *SetupCodes) Generate(ctx context.Context) (string, time.Time, error) {
	raw := make([]byte, 15) // 120 bit，恰好编成 24 位
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	plain := crockford.EncodeToString(raw)
	exp := s.clk.Now().Add(SetupCodeTTL)
	groups := make([]string, 0, 6)
	for i := 0; i < len(plain); i += 4 {
		groups = append(groups, plain[i:i+4])
	}
	display := strings.Join(groups, "-")
	enc, err := s.box.Encrypt(display)
	if err != nil {
		return "", time.Time{}, err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO setup_codes (id, code_hash, expires_at, code_enc) VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET code_hash = excluded.code_hash, expires_at = excluded.expires_at, code_enc = excluded.code_enc`,
		hashToken(plain), store.FormatTime(exp), enc)
	if err != nil {
		return "", time.Time{}, err
	}
	return display, exp, nil
}

// Reveal 返回有效设置码的展示形式与过期时间；无码、已过期、没有加密副本（旧版本生成）
// 或密钥不符时 ok=false。
func (s *SetupCodes) Reveal(ctx context.Context) (code string, exp time.Time, ok bool, err error) {
	if _, exp, ok, err = s.load(ctx); err != nil || !ok {
		return "", time.Time{}, false, err
	}
	var enc string
	if err = s.db.QueryRowContext(ctx, `SELECT code_enc FROM setup_codes WHERE id = 1`).Scan(&enc); err != nil {
		return "", time.Time{}, false, err
	}
	if enc == "" {
		return "", time.Time{}, false, nil
	}
	plain, derr := s.box.Decrypt(enc)
	if derr != nil {
		return "", time.Time{}, false, nil
	}
	return plain, exp, true, nil
}

// load 读取设置码记录；无记录或已过期时 ok=false。
func (s *SetupCodes) load(ctx context.Context) (hash string, exp time.Time, ok bool, err error) {
	var expStr string
	err = s.db.QueryRowContext(ctx, `SELECT code_hash, expires_at FROM setup_codes WHERE id = 1`).Scan(&hash, &expStr)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	exp, err = store.ParseTime(expStr)
	if err != nil {
		return "", time.Time{}, false, err
	}
	if !s.clk.Now().Before(exp) {
		return "", time.Time{}, false, nil
	}
	return hash, exp, true, nil
}

// Active 返回是否存在未过期的设置码及其过期时间。
func (s *SetupCodes) Active(ctx context.Context) (bool, time.Time, error) {
	_, exp, ok, err := s.load(ctx)
	return ok, exp, err
}

// Verify 规范化 input 后与库中哈希常量时间比较；无码或已过期视为无效。
func (s *SetupCodes) Verify(ctx context.Context, input string) (bool, error) {
	hash, _, ok, err := s.load(ctx)
	if err != nil || !ok {
		return false, err
	}
	got := hashToken(normalizeSetupCode(input))
	return subtle.ConstantTimeCompare([]byte(got), []byte(hash)) == 1, nil
}

// Consume 删除设置码（用过即删），重复调用不报错。
func (s *SetupCodes) Consume(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM setup_codes WHERE id = 1`)
	return err
}

// normalizeSetupCode 转大写、去掉分隔符与空白，并把易混字符按 Crockford 规则归一（O→0，I/L→1）。
func normalizeSetupCode(in string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(in) {
		switch r {
		case '-', ' ', '\t', '\r', '\n':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		b.WriteRune(r)
	}
	return b.String()
}
