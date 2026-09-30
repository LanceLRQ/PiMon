package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

// ScreenTokens 管理屏幕令牌：明文写在文件里供 kiosk 读取，库中只存 sha256。
type ScreenTokens struct {
	db   *store.DB
	clk  clock.Clock
	path string
}

// NewScreenTokens 创建屏幕令牌服务，path 为令牌文件路径。
func NewScreenTokens(db *store.DB, clk clock.Clock, path string) *ScreenTokens {
	return &ScreenTokens{db: db, clk: clk, path: path}
}

// storedHash 返回库中的令牌哈希；无记录时 ok=false。
func (s *ScreenTokens) storedHash(ctx context.Context) (string, bool, error) {
	var h string
	err := s.db.QueryRowContext(ctx, `SELECT token_hash FROM screen_tokens WHERE id = 1`).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return h, err == nil, err
}

// EnsureExists 保证库记录与令牌文件一致。
// 两者都在且哈希相符时不做任何事；库无记录、文件缺失、或二者不一致时都重新生成，
// 因为库里只有哈希，无法从哈希恢复明文，重新生成是唯一能让两边重新对齐的办法。
func (s *ScreenTokens) EnsureExists(ctx context.Context) error {
	hash, ok, err := s.storedHash(ctx)
	if err != nil {
		return err
	}
	if ok {
		if b, err := os.ReadFile(s.path); err == nil && hashToken(strings.TrimSpace(string(b))) == hash {
			return nil
		}
	}
	return s.Rotate(ctx)
}

// Rotate 生成新令牌。顺序为：原子写文件 → 更新库 → 删除所有屏幕会话，
// 这样写文件失败时旧令牌与旧会话都保持有效。
func (s *ScreenTokens) Rotate(ctx context.Context) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.path, []byte(token+"\n"), 0o640); err != nil {
		return fmt.Errorf("写入屏幕令牌文件: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO screen_tokens (id, token_hash, created_at) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET token_hash = excluded.token_hash, created_at = excluded.created_at`,
		hashToken(token), store.FormatTime(s.clk.Now())); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE kind = ?`, string(KindScreen)); err != nil {
		return err
	}
	return tx.Commit()
}

// Verify 去掉首尾空白后与库中哈希常量时间比较；无记录返回 false。
func (s *ScreenTokens) Verify(ctx context.Context, token string) (bool, error) {
	hash, ok, err := s.storedHash(ctx)
	if err != nil || !ok {
		return false, err
	}
	got := hashToken(strings.TrimSpace(token))
	return subtle.ConstantTimeCompare([]byte(got), []byte(hash)) == 1, nil
}

// writeFileAtomic 先写同目录临时文件再 rename，避免读到半截内容。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func() { _ = os.Remove(tmp) }
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
