package remote

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

const (
	// MaxUploads 是保留的上传记录条数，超出的旧记录在写入新记录时清理。
	MaxUploads = 90
	// MinPassphraseLen 是备份口令的最短字符数（按 rune 计）。
	MinPassphraseLen = 12
	// DefaultKeep 是远端保留份数的默认值。
	DefaultKeep = 30
	// MaxKeep 是远端保留份数的上限。
	MaxKeep = 365
	// DefaultSMBPort 是 SMB 的默认端口。
	DefaultSMBPort = 445
)

// Config 是解密后的完整配置，只在 hub 内部使用，不得返回给前端或写入日志。
type Config struct {
	Enabled bool
	Type    string

	SMBHost, SMBShare, SMBDir, SMBUsername, SMBPassword, SMBDomain string
	SMBPort                                                        int

	WebDAVURL, WebDAVDir, WebDAVUsername, WebDAVPassword string

	Passphrase string
	Keep       int
}

// SMB 返回按当前配置构造 SMB 目标所需的参数。
func (c Config) SMB() SMBConfig {
	return SMBConfig{Host: c.SMBHost, Port: c.SMBPort, Share: c.SMBShare, Dir: c.SMBDir,
		Username: c.SMBUsername, Password: c.SMBPassword, Domain: c.SMBDomain}
}

// WebDAV 返回按当前配置构造 WebDAV 目标所需的参数。
func (c Config) WebDAV() WebDAVConfig {
	return WebDAVConfig{URL: c.WebDAVURL, Dir: c.WebDAVDir, Username: c.WebDAVUsername, Password: c.WebDAVPassword}
}

// Repo 保存远端备份配置与上传记录。密码与备份口令经 secret.Box 加密入库。
type Repo struct {
	db  *store.DB
	box *secret.Box
	clk clock.Clock
}

// NewRepo 构造仓库。
func NewRepo(db *store.DB, box *secret.Box, clk clock.Clock) *Repo {
	return &Repo{db: db, box: box, clk: clk}
}

func defaultConfig() Config {
	return Config{Type: model.BackupRemoteSMB, SMBPort: DefaultSMBPort, Keep: DefaultKeep}
}

const configCols = `enabled, type, smb_host, smb_port, smb_share, smb_dir, smb_username, smb_password_enc, smb_domain,
 webdav_url, webdav_dir, webdav_username, webdav_password_enc, passphrase_enc, keep, updated_at`

// load 读取并解密配置；没有保存过时返回默认值与零时间。
func (r *Repo) load(ctx context.Context) (Config, time.Time, error) {
	var (
		c                        = defaultConfig()
		enabled                  int
		smbEnc, davEnc, phraseEn string
		updated                  string
	)
	err := r.db.QueryRowContext(ctx, `SELECT `+configCols+` FROM backup_remote WHERE id = 1`).Scan(
		&enabled, &c.Type, &c.SMBHost, &c.SMBPort, &c.SMBShare, &c.SMBDir, &c.SMBUsername, &smbEnc, &c.SMBDomain,
		&c.WebDAVURL, &c.WebDAVDir, &c.WebDAVUsername, &davEnc, &phraseEn, &c.Keep, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return c, time.Time{}, nil
	}
	if err != nil {
		return Config{}, time.Time{}, fmt.Errorf("读取远端备份配置: %w", err)
	}
	c.Enabled = enabled == 1
	for _, f := range []struct {
		enc  string
		dst  *string
		name string
	}{{smbEnc, &c.SMBPassword, "SMB 密码"}, {davEnc, &c.WebDAVPassword, "WebDAV 密码"}, {phraseEn, &c.Passphrase, "备份口令"}} {
		if f.enc == "" {
			continue
		}
		if *f.dst, err = r.box.Decrypt(f.enc); err != nil {
			return Config{}, time.Time{}, fmt.Errorf("解密%s: %w", f.name, err)
		}
	}
	at, err := store.ParseTime(updated)
	if err != nil {
		return Config{}, time.Time{}, fmt.Errorf("远端备份配置时间: %w", err)
	}
	return c, at, nil
}

func masked(c Config, updated time.Time) model.BackupRemote {
	out := model.BackupRemote{
		Enabled: c.Enabled, Type: c.Type,
		SMBHost: c.SMBHost, SMBPort: c.SMBPort, SMBShare: c.SMBShare, SMBDir: c.SMBDir,
		SMBUsername: c.SMBUsername, SMBPassword: model.BackupSecretState{Set: c.SMBPassword != ""}, SMBDomain: c.SMBDomain,
		WebDAVURL: c.WebDAVURL, WebDAVDir: c.WebDAVDir, WebDAVUsername: c.WebDAVUsername,
		WebDAVPassword: model.BackupSecretState{Set: c.WebDAVPassword != ""},
		Passphrase:     model.BackupSecretState{Set: c.Passphrase != ""},
		Keep:           c.Keep,
	}
	if !updated.IsZero() {
		u := updated
		out.UpdatedAt = &u
	}
	return out
}

// Get 返回脱敏后的配置，密钥只带 set 标记。
func (r *Repo) Get(ctx context.Context) (model.BackupRemote, error) {
	c, at, err := r.load(ctx)
	if err != nil {
		return model.BackupRemote{}, err
	}
	return masked(c, at), nil
}

// Config 返回解密后的完整配置，仅供内部构造目标与加密使用。
func (r *Repo) Config(ctx context.Context) (Config, error) {
	c, _, err := r.load(ctx)
	return c, err
}

// Resolve 校验请求体并与已保存的配置合并（密钥留空用已保存的值），不落库。测试连接用它取得有效配置。
func (r *Repo) Resolve(ctx context.Context, in model.BackupRemoteInput) (Config, error) {
	old, _, err := r.load(ctx)
	if err != nil {
		return Config{}, err
	}
	return merge(old, in)
}

// Put 校验并保存配置。密钥留空保留原值；响应的 PassphraseChanged 表示本次改了已有的备份口令。
func (r *Repo) Put(ctx context.Context, in model.BackupRemoteInput) (model.BackupRemote, error) {
	old, _, err := r.load(ctx)
	if err != nil {
		return model.BackupRemote{}, err
	}
	c, err := merge(old, in)
	if err != nil {
		return model.BackupRemote{}, err
	}
	var smbEnc, davEnc, phraseEnc string
	for _, f := range []struct {
		plain string
		dst   *string
	}{{c.SMBPassword, &smbEnc}, {c.WebDAVPassword, &davEnc}, {c.Passphrase, &phraseEnc}} {
		if f.plain == "" {
			continue
		}
		if *f.dst, err = r.box.Encrypt(f.plain); err != nil {
			return model.BackupRemote{}, fmt.Errorf("加密远端备份密钥: %w", err)
		}
	}
	now := r.clk.Now()
	_, err = r.db.ExecContext(ctx, `INSERT INTO backup_remote (id, `+configCols+`)
 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
 ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled, type=excluded.type, smb_host=excluded.smb_host,
 smb_port=excluded.smb_port, smb_share=excluded.smb_share, smb_dir=excluded.smb_dir, smb_username=excluded.smb_username,
 smb_password_enc=excluded.smb_password_enc, smb_domain=excluded.smb_domain, webdav_url=excluded.webdav_url,
 webdav_dir=excluded.webdav_dir, webdav_username=excluded.webdav_username, webdav_password_enc=excluded.webdav_password_enc,
 passphrase_enc=excluded.passphrase_enc, keep=excluded.keep, updated_at=excluded.updated_at`,
		boolInt(c.Enabled), c.Type, c.SMBHost, c.SMBPort, c.SMBShare, c.SMBDir, c.SMBUsername, smbEnc, c.SMBDomain,
		c.WebDAVURL, c.WebDAVDir, c.WebDAVUsername, davEnc, phraseEnc, c.Keep, store.FormatTime(now))
	if err != nil {
		return model.BackupRemote{}, fmt.Errorf("保存远端备份配置: %w", err)
	}
	out := masked(c, now.UTC())
	out.PassphraseChanged = old.Passphrase != "" && c.Passphrase != old.Passphrase
	return out, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// merge 把请求体套到旧配置上：密钥留空保留旧值，端口 0 取默认；随后校验。
func merge(old Config, in model.BackupRemoteInput) (Config, error) {
	c := Config{
		Enabled: in.Enabled, Type: strings.TrimSpace(in.Type),
		SMBHost: strings.TrimSpace(in.SMBHost), SMBPort: in.SMBPort, SMBShare: strings.TrimSpace(in.SMBShare),
		SMBDir: strings.TrimSpace(in.SMBDir), SMBUsername: in.SMBUsername, SMBPassword: in.SMBPassword,
		SMBDomain: strings.TrimSpace(in.SMBDomain),
		WebDAVURL: strings.TrimSpace(in.WebDAVURL), WebDAVDir: strings.TrimSpace(in.WebDAVDir),
		WebDAVUsername: in.WebDAVUsername, WebDAVPassword: in.WebDAVPassword,
		Passphrase: in.Passphrase, Keep: in.Keep,
	}
	if c.SMBPassword == "" {
		c.SMBPassword = old.SMBPassword
	}
	if c.WebDAVPassword == "" {
		c.WebDAVPassword = old.WebDAVPassword
	}
	if c.Passphrase == "" {
		c.Passphrase = old.Passphrase
	}
	if c.SMBPort == 0 {
		c.SMBPort = DefaultSMBPort
	}
	if fe := validate(c, in.Passphrase); fe != nil {
		return Config{}, fe
	}
	return c, nil
}

// validate 校验合并后的配置。格式规则始终生效；必填规则只在启用时对当前类型的字段生效。
// submittedPassphrase 是本次提交的口令（可能为空），提交了就必须满足长度要求。
func validate(c Config, submittedPassphrase string) model.FieldErrors {
	fe := model.FieldErrors{}
	switch c.Type {
	case model.BackupRemoteSMB, model.BackupRemoteWebDAV:
	case "":
		if c.Enabled {
			fe["type"] = model.FieldRequired
		}
	default:
		fe["type"] = model.FieldInvalid
	}
	if c.Keep < 1 || c.Keep > MaxKeep {
		fe["keep"] = model.FieldOutOfRange
	}
	if submittedPassphrase != "" && utf8.RuneCountInString(submittedPassphrase) < MinPassphraseLen {
		fe["passphrase"] = model.FieldOutOfRange
	}
	if c.SMBPort < 1 || c.SMBPort > 65535 {
		fe["smb_port"] = model.FieldOutOfRange
	}
	if c.SMBHost != "" && strings.ContainsAny(c.SMBHost, " \t/\\@") {
		fe["smb_host"] = model.FieldInvalid
	}
	if strings.ContainsAny(c.SMBShare, "/\\") {
		fe["smb_share"] = model.FieldInvalid
	}
	if badDir(c.SMBDir) {
		fe["smb_dir"] = model.FieldInvalid
	}
	if badDir(c.WebDAVDir) {
		fe["webdav_dir"] = model.FieldInvalid
	}
	if c.WebDAVURL != "" {
		if _, err := url.Parse(c.WebDAVURL); err != nil || schema.CheckURL(c.WebDAVURL, false, false) != nil {
			fe["webdav_url"] = model.FieldInvalid
		}
	}
	if c.Enabled {
		require := func(key string, ok bool) {
			if !ok && fe[key] == "" {
				fe[key] = model.FieldRequired
			}
		}
		switch c.Type {
		case model.BackupRemoteSMB:
			require("smb_host", c.SMBHost != "")
			require("smb_share", c.SMBShare != "")
			require("smb_username", c.SMBUsername != "")
			require("smb_password", c.SMBPassword != "")
		case model.BackupRemoteWebDAV:
			require("webdav_url", c.WebDAVURL != "")
			require("webdav_username", c.WebDAVUsername != "")
			require("webdav_password", c.WebDAVPassword != "")
		}
		require("passphrase", c.Passphrase != "")
	}
	if len(fe) == 0 {
		return nil
	}
	return fe
}

// badDir 判断目录是否含 .. 路径段（目录用 / 或 \ 分隔）。
func badDir(dir string) bool {
	for _, seg := range strings.FieldsFunc(dir, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

// RecordUpload 写入一条上传记录并清理到最近 MaxUploads 条，返回带 ID 的记录。
func (r *Repo) RecordUpload(ctx context.Context, u model.BackupUpload) (model.BackupUpload, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO backup_uploads (name, target_type, started_at, finished_at, size, success, reason) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.Name, u.TargetType, store.FormatTime(u.StartedAt), store.FormatTime(u.FinishedAt), u.Size, boolInt(u.Success), u.Reason)
	if err != nil {
		return model.BackupUpload{}, fmt.Errorf("写入上传记录: %w", err)
	}
	if u.ID, err = res.LastInsertId(); err != nil {
		return model.BackupUpload{}, err
	}
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM backup_uploads WHERE id <= (SELECT id FROM backup_uploads ORDER BY id DESC LIMIT 1 OFFSET ?)`, MaxUploads); err != nil {
		return model.BackupUpload{}, fmt.Errorf("清理上传记录: %w", err)
	}
	u.StartedAt, u.FinishedAt = u.StartedAt.UTC(), u.FinishedAt.UTC()
	return u, nil
}

const uploadCols = `id, name, target_type, started_at, finished_at, size, success, reason`

type rowScanner interface{ Scan(dest ...any) error }

func scanUpload(s rowScanner) (model.BackupUpload, error) {
	var (
		u            model.BackupUpload
		ok           int
		start, finis string
	)
	if err := s.Scan(&u.ID, &u.Name, &u.TargetType, &start, &finis, &u.Size, &ok, &u.Reason); err != nil {
		return u, err
	}
	var err error
	if u.StartedAt, err = store.ParseTime(start); err != nil {
		return u, err
	}
	if u.FinishedAt, err = store.ParseTime(finis); err != nil {
		return u, err
	}
	u.Success = ok == 1
	return u, nil
}

// Uploads 返回最近的上传记录，新到旧；limit 小于 1 或大于 MaxUploads 时取 MaxUploads。
func (r *Repo) Uploads(ctx context.Context, limit int) ([]model.BackupUpload, error) {
	if limit < 1 || limit > MaxUploads {
		limit = MaxUploads
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+uploadCols+` FROM backup_uploads ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("读取上传记录: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []model.BackupUpload{}
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, fmt.Errorf("读取上传记录: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// LastUpload 返回最近一次上传记录；successOnly 为 true 时只看成功的。没有记录时返回 nil。
func (r *Repo) LastUpload(ctx context.Context, successOnly bool) (*model.BackupUpload, error) {
	q := `SELECT ` + uploadCols + ` FROM backup_uploads `
	if successOnly {
		q += `WHERE success = 1 `
	}
	u, err := scanUpload(r.db.QueryRowContext(ctx, q+`ORDER BY id DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取最近上传记录: %w", err)
	}
	return &u, nil
}
