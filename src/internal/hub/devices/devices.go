// Package devices 是设备仓库：设备表、拨入配对码、凭据哈希与密文、重新配对时的引用迁移与变更回调。
//
// 凭据与设备 ID 的推导复用 pkg/protocol/agent；本包不写日志，凭据与配对码不会出现在返回值的 model 结构或错误文本中。
// 在线状态（presence）、连接错误与时钟偏差是运行时值，不在本包保存。
package devices

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/internal/paircode"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

const (
	// PairingCodeTTL 是拨入配对码的有效期。
	PairingCodeTTL = 15 * time.Minute
	// PlaceholderTTL 是已签发凭据但从未 hello 的拨入设备占位的保留时间。
	PlaceholderTTL = 15 * time.Minute
	// MaxActivePairingCodes 是同时有效的配对码上限，超出时淘汰最旧的。
	MaxActivePairingCodes = 20

	maxNameRunes = 64
)

var (
	// ErrNotFound 表示设备不存在（或不满足查询条件，如拨入认证命中了拨出设备）。
	ErrNotFound = errors.New("设备不存在")
	// ErrCodeNotFound 表示配对码不存在（错误、已使用或已被清理）。
	ErrCodeNotFound = errors.New("配对码不存在")
	// ErrCodeExpired 表示配对码已过期；过期的行在同一事务内删除。
	ErrCodeExpired = errors.New("配对码已过期")
	// ErrInvalidRole 表示设备角色不合法。
	ErrInvalidRole = errors.New("设备角色不合法")
)

// Config 是设备仓库的依赖。
type Config struct {
	DB    *store.DB
	Box   *secret.Box
	Clock clock.Clock
}

// Store 是设备仓库。
type Store struct {
	db  *store.DB
	box *secret.Box
	clk clock.Clock

	mu        sync.Mutex
	callbacks []func(id string, removed bool)
}

// New 创建设备仓库。
func New(c Config) *Store {
	return &Store{db: c.DB, box: c.Box, clk: c.Clock}
}

// OnChange 注册变更回调，可注册多个。设备新增、确认、更新、握手信息变化时以 removed=false 调用，
// 吊销与清理占位时以 removed=true 调用。回调在事务提交之后同步执行，其中不得调用会阻塞在本仓库上的长操作。
func (s *Store) OnChange(fn func(id string, removed bool)) {
	s.mu.Lock()
	s.callbacks = append(s.callbacks, fn)
	s.mu.Unlock()
}

func (s *Store) notify(id string, removed bool) {
	s.mu.Lock()
	cbs := slices.Clone(s.callbacks)
	s.mu.Unlock()
	for _, cb := range cbs {
		cb(id, removed)
	}
}

// PairingOptions 是生成配对码时预填的设备属性。
type PairingOptions struct {
	Name string
	Role string
	// AllowOffline 缺省时角色为 dev 则开，其余关。
	AllowOffline *bool
	// AutoHostMetrics 缺省为开。
	AutoHostMetrics *bool
	// RepairDeviceID 非空表示用于重新配对该设备。
	RepairDeviceID string
}

// PairingCode 是消耗配对码后得到的预填信息。
type PairingCode struct {
	Name            string
	Role            string
	AllowOffline    bool
	AutoHostMetrics bool
	RepairDeviceID  string
	ExpiresAt       time.Time
}

// PairedDevice 是配对完成时创建的拨入设备占位。
type PairedDevice struct {
	// CredentialHash 是监听方签发凭据的哈希（agent.CredentialHash）。
	CredentialHash string
	Code           PairingCode
	// PairName、Hostname 来自 agent.Pair，用于配对码没有预填名称时命名。
	PairName string
	Hostname string
}

// DialOutDevice 是拨出配对成功后创建的设备（中枢为拨号方，保存凭据原文的密文）。
type DialOutDevice struct {
	Name       string
	Role       string
	Address    string
	Pin        string
	ProxyID    string
	Credential string
	// AllowOffline 缺省时按角色决定（dev 默认开），规则与配对码一致。
	AllowOffline    *bool
	AutoHostMetrics bool
	// Replaces 非空表示重新配对该设备，旧设备在首个 hello 确认时被替换。
	Replaces string
}

// HelloInfo 是 hello 被接受后写入的握手信息。
type HelloInfo struct {
	// Address 为空时不改（拨入设备传来源 IP；拨出设备的地址是配置值，不覆盖）。
	Address         string
	Hostname        string
	AgentVersion    string
	OS              string
	Arch            string
	ProtocolVersion int
	// Incompatible 非空表示协议不兼容；为空会清除先前记录的不兼容原因。
	Incompatible *model.DeviceIncompatible
}

// Detail 是不进入 model.Device 的内部属性。
type Detail struct {
	Direction       string
	Pin             string
	AutoHostMetrics bool
	Replaces        string
	// Confirmed 为 false 表示已签发凭据但从未 hello。
	Confirmed bool
}

// resolveAllowOffline 返回「允许离线」的最终值：显式给出者优先，缺省时角色为 dev 则开。
func resolveAllowOffline(role string, v *bool) bool {
	if v != nil {
		return *v
	}
	return role == model.DeviceRoleDev
}

// ValidRole 判断 r 是否是合法的设备角色。
func ValidRole(r string) bool { return validRole(r) }

func validRole(r string) bool {
	switch r {
	case model.DeviceRoleServer, model.DeviceRoleNAS, model.DeviceRoleDev, model.DeviceRoleVPS:
		return true
	}
	return false
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// HashPairingCode 返回用户输入的配对码规范化后的 sha256 十六进制，库中只存这个值（规则见 paircode.Hash）。
func HashPairingCode(code string) string { return paircode.Hash(code) }

// NewPairingCode 生成一次性拨入配对码，返回展示形式（8 组 × 4 位）与过期时间。
// 同时有效的配对码超过上限时删除最旧的。
func (s *Store) NewPairingCode(ctx context.Context, o PairingOptions) (string, time.Time, error) {
	role := o.Role
	if role == "" {
		role = model.DeviceRoleServer
	}
	if !validRole(role) {
		return "", time.Time{}, fmt.Errorf("角色 %q 不合法", role)
	}
	allow := resolveAllowOffline(role, o.AllowOffline)
	auto := true
	if o.AutoHostMetrics != nil {
		auto = *o.AutoHostMetrics
	}
	display, err := paircode.New()
	if err != nil {
		return "", time.Time{}, err
	}

	now := s.clk.Now()
	exp := now.Add(PairingCodeTTL)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteExpiredCodes(ctx, tx, now); err != nil {
		return "", time.Time{}, err
	}
	// 为新码留出位置：淘汰最旧的，使插入后总数不超过上限。
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pairing_codes WHERE code_hash IN (
		   SELECT code_hash FROM pairing_codes ORDER BY rowid ASC
		   LIMIT MAX(0, (SELECT COUNT(*) FROM pairing_codes) - ?))`, MaxActivePairingCodes-1); err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pairing_codes (code_hash, expires_at, name, role, allow_offline, auto_host_metrics, repair_device_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		HashPairingCode(display), store.FormatTime(exp), strings.TrimSpace(o.Name), role,
		boolInt(allow), boolInt(auto), o.RepairDeviceID, store.FormatTime(now)); err != nil {
		return "", time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, err
	}
	return display, exp, nil
}

// deleteExpiredCodes 删除已过期的配对码（expires_at 为变长小数的 RFC3339Nano，须在 Go 中比较）。
func deleteExpiredCodes(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT code_hash, expires_at FROM pairing_codes`)
	if err != nil {
		return err
	}
	var expired []string
	for rows.Next() {
		var h, e string
		if err := rows.Scan(&h, &e); err != nil {
			_ = rows.Close()
			return err
		}
		if t, err := store.ParseTime(e); err != nil || !now.Before(t) {
			expired = append(expired, h)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, h := range expired {
		if _, err := tx.ExecContext(ctx, `DELETE FROM pairing_codes WHERE code_hash = ?`, h); err != nil {
			return err
		}
	}
	return nil
}

// ConsumePairingCode 按配对码哈希（HashPairingCode）取出并删除配对码，一次性。
// 不存在返回 ErrCodeNotFound，已过期返回 ErrCodeExpired（过期行同样删除）。
func (s *Store) ConsumePairingCode(ctx context.Context, codeHash string) (PairingCode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PairingCode{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var (
		pc           PairingCode
		exp          string
		allow, autoM int
	)
	err = tx.QueryRowContext(ctx,
		`SELECT expires_at, name, role, allow_offline, auto_host_metrics, repair_device_id FROM pairing_codes WHERE code_hash = ?`,
		codeHash).Scan(&exp, &pc.Name, &pc.Role, &allow, &autoM, &pc.RepairDeviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingCode{}, ErrCodeNotFound
	}
	if err != nil {
		return PairingCode{}, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM pairing_codes WHERE code_hash = ?`, codeHash)
	if err != nil {
		return PairingCode{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return PairingCode{}, ErrCodeNotFound
	}
	if err := tx.Commit(); err != nil {
		return PairingCode{}, err
	}
	t, err := store.ParseTime(exp)
	if err != nil || !s.clk.Now().Before(t) {
		return PairingCode{}, ErrCodeExpired
	}
	pc.ExpiresAt = t
	pc.AllowOffline = allow == 1
	pc.AutoHostMetrics = autoM == 1
	return pc, nil
}

// CredentialHash 按设备 ID 取凭据哈希与连接方向。拨出设备的哈希为空串。设备不存在返回 ErrNotFound。
func (s *Store) CredentialHash(ctx context.Context, id string) (hash, direction string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT credential_hash, direction FROM devices WHERE id = ?`, id).Scan(&hash, &direction)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return hash, direction, err
}

// VerifyCredential 校验拨入凭据：由凭据推导设备 ID，要求设备存在且为拨入，
// 并以常量时间比较完整哈希。通过返回设备 ID，否则一律返回 ErrNotFound。
func (s *Store) VerifyCredential(ctx context.Context, credential string) (string, error) {
	id := agent.DeviceID(credential)
	hash, dir, err := s.CredentialHash(ctx, id)
	if err != nil {
		return "", err
	}
	if dir != model.DeviceDialIn || subtle.ConstantTimeCompare([]byte(hash), []byte(agent.CredentialHash(credential))) != 1 {
		return "", ErrNotFound
	}
	return id, nil
}

// uniqueName 在 tx 内返回不与现有设备重名的名称，重名时依次追加 " (2)"、" (3)"……
func uniqueName(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	for n := 1; ; n++ {
		cand := base
		if n > 1 {
			cand = base + " (" + strconv.Itoa(n) + ")"
		}
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM devices WHERE name = ?`, cand).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return cand, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func clipName(name string) string {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxNameRunes {
		name = string([]rune(name)[:maxNameRunes])
	}
	return name
}

// CreatePaired 在配对完成时插入拨入设备占位（confirmed_at 为空）。名称取配对码预填、
// agent.Pair.Name、主机名、设备 ID 中第一个非空者，重名追加序号。重新配对时旧设备与引用保持原样，
// 直到首个 hello 被接受后由 Confirm 迁移。
func (s *Store) CreatePaired(ctx context.Context, p PairedDevice) error {
	id := agent.DeviceIDFromHash(p.CredentialHash)
	role := p.Code.Role
	if !validRole(role) {
		return ErrInvalidRole
	}
	base := ""
	for _, c := range []string{p.Code.Name, p.PairName, p.Hostname, id} {
		if base = clipName(c); base != "" {
			break
		}
	}
	now := store.FormatTime(s.clk.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	name, err := uniqueName(ctx, tx, base)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO devices (id, name, role, direction, hostname, allow_offline, credential_hash, auto_host_metrics, replaces, created_at, updated_at)
		 VALUES (?, ?, ?, 'dial_in', ?, ?, ?, ?, ?, ?, ?)`,
		id, name, role, strings.TrimSpace(p.Hostname), boolInt(p.Code.AllowOffline), p.CredentialHash,
		boolInt(p.Code.AutoHostMetrics), p.Code.RepairDeviceID, now, now)
	if err != nil {
		return fmt.Errorf("创建设备: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notify(id, false)
	return nil
}

// CreateDialOut 创建拨出设备（占位，confirmed_at 为空，首个 hello 时 Confirm）。
// 凭据用 secret.Box 加密保存，设备 ID 由凭据推导。名称重名追加序号。
func (s *Store) CreateDialOut(ctx context.Context, d DialOutDevice) (model.Device, error) {
	if d.Credential == "" {
		return model.Device{}, errors.New("缺少凭据")
	}
	role := d.Role
	if role == "" {
		role = model.DeviceRoleVPS
	}
	if !validRole(role) {
		return model.Device{}, ErrInvalidRole
	}
	id := agent.DeviceID(d.Credential)
	enc, err := s.box.Encrypt(d.Credential)
	if err != nil {
		return model.Device{}, fmt.Errorf("加密凭据: %w", err)
	}
	base := clipName(d.Name)
	if base == "" {
		base = id
	}
	now := store.FormatTime(s.clk.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Device{}, err
	}
	defer func() { _ = tx.Rollback() }()
	name, err := uniqueName(ctx, tx, base)
	if err != nil {
		return model.Device{}, err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO devices (id, name, role, direction, address, proxy_id, allow_offline, credential_enc, pin, auto_host_metrics, replaces, created_at, updated_at)
		 VALUES (?, ?, ?, 'dial_out', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, role, d.Address, d.ProxyID, boolInt(resolveAllowOffline(role, d.AllowOffline)), enc, d.Pin, boolInt(d.AutoHostMetrics), d.Replaces, now, now)
	if err != nil {
		return model.Device{}, fmt.Errorf("创建设备: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Device{}, err
	}
	s.notify(id, false)
	return s.Get(ctx, id)
}

// Credential 返回拨出设备的凭据原文（解密）。设备不存在或不是拨出设备返回 ErrNotFound。
// 凭据只供中枢拨号使用，不得经任何 API 返回或写入日志。
func (s *Store) Credential(ctx context.Context, id string) (string, error) {
	var enc string
	err := s.db.QueryRowContext(ctx, `SELECT credential_enc FROM devices WHERE id = ? AND direction = 'dial_out'`, id).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && enc == "") {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	plain, err := s.box.Decrypt(enc)
	if err != nil {
		return "", fmt.Errorf("解密设备凭据: %w", err)
	}
	return plain, nil
}

const selectCols = `id, name, role, direction, address, hostname, proxy_id, allow_offline, maintenance,
	agent_version, os, arch, protocol_version, incompatible_reason, required_min, last_seen_at, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scan(r rowScanner) (model.Device, error) {
	var (
		d                    model.Device
		allow, maint         int
		reason               string
		reqMin               int
		lastSeen             sql.NullString
		created, updatedText string
	)
	if err := r.Scan(&d.ID, &d.Name, &d.Role, &d.Direction, &d.Address, &d.Hostname, &d.ProxyID, &allow, &maint,
		&d.AgentVersion, &d.OS, &d.Arch, &d.ProtocolVersion, &reason, &reqMin, &lastSeen, &created, &updatedText); err != nil {
		return model.Device{}, err
	}
	d.AllowOffline = allow == 1
	d.Maintenance = maint == 1
	if reason != "" {
		d.Incompatible = &model.DeviceIncompatible{Reason: reason, RequiredMin: reqMin}
	}
	if lastSeen.Valid {
		if t, err := store.ParseTime(lastSeen.String); err == nil {
			d.LastSeenAt = &t
		}
	}
	d.CreatedAt, _ = store.ParseTime(created)
	d.UpdatedAt, _ = store.ParseTime(updatedText)
	return d, nil
}

// Get 读取单个设备；不存在返回 ErrNotFound。Presence 等运行时字段为空。
func (s *Store) Get(ctx context.Context, id string) (model.Device, error) {
	d, err := scan(s.db.QueryRowContext(ctx, `SELECT `+selectCols+` FROM devices WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Device{}, ErrNotFound
	}
	return d, err
}

// List 按创建时间升序返回全部设备（不含中枢本机）。
func (s *Store) List(ctx context.Context) ([]model.Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+selectCols+` FROM devices ORDER BY rowid ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.Device{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Detail 读取不进入 model.Device 的内部属性；设备不存在返回 ErrNotFound。
func (s *Store) Detail(ctx context.Context, id string) (Detail, error) {
	var (
		d    Detail
		auto int
		conf sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT direction, pin, auto_host_metrics, replaces, confirmed_at FROM devices WHERE id = ?`, id).
		Scan(&d.Direction, &d.Pin, &auto, &d.Replaces, &conf)
	if errors.Is(err, sql.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	d.AutoHostMetrics = auto == 1
	d.Confirmed = conf.Valid
	return d, nil
}

// Update 更新名称、角色、允许离线、维护模式；拨出设备另可改地址与代理，拨入设备忽略这两项。
// 校验失败返回 model.FieldErrors（含重名）。
func (s *Store) Update(ctx context.Context, id string, in model.DeviceInput) (model.Device, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return model.Device{}, err
	}
	name := strings.TrimSpace(in.Name)
	fe := model.FieldErrors{}
	switch {
	case name == "":
		fe["name"] = model.FieldRequired
	case utf8.RuneCountInString(name) > maxNameRunes:
		fe["name"] = model.FieldOutOfRange
	}
	if !validRole(in.Role) {
		fe["role"] = model.FieldInvalid
	}
	if len(fe) > 0 {
		return model.Device{}, fe
	}
	address, proxyID := cur.Address, cur.ProxyID
	if cur.Direction == model.DeviceDialOut {
		if in.Address != nil {
			address = *in.Address
		}
		if in.ProxyID != nil {
			proxyID = *in.ProxyID
		}
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE devices SET name = ?, role = ?, allow_offline = ?, maintenance = ?, address = ?, proxy_id = ?, updated_at = ? WHERE id = ?`,
		name, in.Role, boolInt(in.AllowOffline), boolInt(in.Maintenance), address, proxyID, store.FormatTime(s.clk.Now()), id)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: devices.name") {
		return model.Device{}, model.FieldErrors{"name": model.FieldDuplicate}
	}
	if err != nil {
		return model.Device{}, err
	}
	s.notify(id, false)
	return s.Get(ctx, id)
}

// UpdateHello 写入 hello 被接受后的握手信息并刷新最后在线时间，触发变更回调。
func (s *Store) UpdateHello(ctx context.Context, id string, h HelloInfo) error {
	reason, reqMin := "", 0
	if h.Incompatible != nil {
		reason, reqMin = h.Incompatible.Reason, h.Incompatible.RequiredMin
	}
	now := store.FormatTime(s.clk.Now())
	res, err := s.db.ExecContext(ctx,
		`UPDATE devices SET
		   address = CASE WHEN ? <> '' THEN ? ELSE address END,
		   hostname = CASE WHEN ? <> '' THEN ? ELSE hostname END,
		   agent_version = ?, os = ?, arch = ?, protocol_version = ?, incompatible_reason = ?, required_min = ?,
		   last_seen_at = ?, updated_at = ?
		 WHERE id = ?`,
		h.Address, h.Address, h.Hostname, h.Hostname, h.AgentVersion, h.OS, h.Arch, h.ProtocolVersion, reason, reqMin, now, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.notify(id, false)
	return nil
}

// TouchLastSeen 记录最后在线时间（如会话断开时），不触发变更回调。
func (s *Store) TouchLastSeen(ctx context.Context, id string, t time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET last_seen_at = ? WHERE id = ?`, store.FormatTime(t), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TakeAutoHostMetrics 原子地读取并清除「首次上线自动创建 host-metrics 实例」标记，只会返回一次 true。
func (s *Store) TakeAutoHostMetrics(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET auto_host_metrics = 0 WHERE id = ? AND auto_host_metrics = 1`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Confirm 在设备的首个 hello 被接受时确认占位：写入 confirmed_at；若是重新配对（replaces 非空），
// 在同一事务内把旧设备的实例 machine 与 manifest 行迁移到新 ID、继承旧设备的维护模式、删除旧设备行并清空 replaces。
// 返回被替换的旧设备 ID（无则为空串），供上层通知实例重新下发、关闭旧会话。
// 已确认的设备重复调用是空操作；设备不存在返回 ErrNotFound。
func (s *Store) Confirm(ctx context.Context, newID string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var (
		replaces, name string
		conf           sql.NullString
	)
	err = tx.QueryRowContext(ctx, `SELECT replaces, name, confirmed_at FROM devices WHERE id = ?`, newID).Scan(&replaces, &name, &conf)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if conf.Valid {
		return "", nil
	}
	now := store.FormatTime(s.clk.Now())
	oldID := ""
	oldMaintenance := 0
	if replaces != "" && replaces != newID {
		var oldName string
		err := tx.QueryRowContext(ctx, `SELECT name, maintenance FROM devices WHERE id = ?`, replaces).Scan(&oldName, &oldMaintenance)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// 旧设备已不存在，无需迁移。
		case err != nil:
			return "", err
		default:
			oldID = replaces
			for _, st := range []struct {
				sql  string
				args []any
			}{
				{`UPDATE plugin_instances SET machine = ? WHERE machine = ?`, []any{newID, oldID}},
				{`DELETE FROM manifests WHERE machine = ?`, []any{newID}},
				{`UPDATE manifests SET machine = ? WHERE machine = ?`, []any{newID, oldID}},
				{`DELETE FROM devices WHERE id = ?`, []any{oldID}},
			} {
				if _, err := tx.ExecContext(ctx, st.sql, st.args...); err != nil {
					return "", fmt.Errorf("迁移重新配对引用: %w", err)
				}
			}
			// 占位期间因旧名称占用而追加了序号的，确认后恢复旧名称。
			if isNumberedVariant(name, oldName) {
				name = oldName
			}
		}
	}
	// 重新配对时实例已随旧设备迁入，不再自动创建 host-metrics 实例。
	autoOff := boolInt(oldID != "")
	// 维护模式不在配对码里预填，重新配对时在迁移处继承旧设备的值。
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET name = ?, replaces = '', confirmed_at = ?, updated_at = ?,
		auto_host_metrics = CASE WHEN ? = 1 THEN 0 ELSE auto_host_metrics END,
		maintenance = CASE WHEN ? = 1 THEN ? ELSE maintenance END WHERE id = ?`,
		name, now, now, autoOff, autoOff, oldMaintenance, newID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.notify(newID, false)
	if oldID != "" {
		s.notify(oldID, true)
	}
	return oldID, nil
}

// isNumberedVariant 判断 name 是否形如「base (N)」。
func isNumberedVariant(name, base string) bool {
	rest, ok := strings.CutPrefix(name, base+" (")
	if !ok || !strings.HasSuffix(rest, ")") {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(rest, ")"))
	return err == nil && n >= 2
}

// Delete 吊销设备：同一事务内删除设备行、该机器的 manifest 行、绑定它的配对码，
// 以及以它为重新配对目标且尚未确认的占位设备；实例行保留（引用失效）。随后触发变更回调。
func (s *Store) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM devices WHERE id = ?`, id).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	removed := []string{id}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM devices WHERE replaces = ? AND confirmed_at IS NULL`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			_ = rows.Close()
			return err
		}
		removed = append(removed, pid)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, rid := range removed {
		for _, q := range []string{`DELETE FROM devices WHERE id = ?`, `DELETE FROM manifests WHERE machine = ?`} {
			if _, err := tx.ExecContext(ctx, q, rid); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pairing_codes WHERE repair_device_id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, rid := range removed {
		s.notify(rid, true)
	}
	return nil
}

// Cleanup 删除已过期的配对码，以及创建超过 PlaceholderTTL 仍未确认的拨入设备占位（旧设备不受影响）。
// 拨出设备由中枢主动拨号、凭据只存在中枢，未确认时不清理，需用户手动删除。
// 已记录不兼容原因的拨入占位（重新配对遇不兼容的 agent，hello 未确认）也不清理：agent 已持凭据，
// 升级后首个兼容的 hello 才接替旧设备；用户可手动删除它，吊销旧设备时一并删除。
// 由 Server 定期（建议每分钟）调用。
func (s *Store) Cleanup(ctx context.Context) error {
	now := s.clk.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteExpiredCodes(ctx, tx, now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, created_at FROM devices WHERE confirmed_at IS NULL AND direction = 'dial_in' AND incompatible_reason = ''`)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var id, created string
		if err := rows.Scan(&id, &created); err != nil {
			_ = rows.Close()
			return err
		}
		if t, err := store.ParseTime(created); err == nil && !now.Before(t.Add(PlaceholderTTL)) {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, id := range stale {
		if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, id := range stale {
		s.notify(id, true)
	}
	return nil
}
