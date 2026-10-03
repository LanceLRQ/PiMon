// Package config 是 agent 的本地配置：连接方式、地址、指纹、凭据与运行路径约定。
// 不含任何插件配置或密钥；文件权限 0600，读取时校验权限与属主。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// 连接模式。
const (
	// ModeDialIn：agent 主动连接 hub（局域网）。
	ModeDialIn = "dial_in"
	// ModeDialOut：agent 监听，由 hub 主动连接（公网 VPS）。
	ModeDialOut = "dial_out"
)

// Credential 是拨入模式下 hub 签发的长期凭据原文；格式化与日志输出一律脱敏，只有 JSON 编码保留原文。
type Credential string

const masked = "***"

func (Credential) String() string               { return masked }
func (Credential) GoString() string             { return masked }
func (Credential) LogValue() slog.Value         { return slog.StringValue(masked) }
func (c Credential) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(masked)) }

// Config 是 agent.json 的内容。
type Config struct {
	Mode string `json:"mode"`

	// 拨入模式
	HubURL      string     `json:"hub_url,omitempty"`
	HubPin      string     `json:"hub_pin,omitempty"`
	Credential  Credential `json:"credential,omitempty"`
	HubHostname string     `json:"hub_hostname,omitempty"`

	// 拨出模式
	Listen             string `json:"listen,omitempty"`
	CredentialHash     string `json:"credential_hash,omitempty"`
	PrevCredentialHash string `json:"prev_credential_hash,omitempty"`

	Name string `json:"name,omitempty"`
	Role string `json:"role,omitempty"`
}

// Validate 检查模式取值。
func (c Config) Validate() error {
	if c.Mode != ModeDialIn && c.Mode != ModeDialOut {
		return fmt.Errorf("配置中的 mode %q 无效（应为 %s 或 %s）", c.Mode, ModeDialIn, ModeDialOut)
	}
	return nil
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8] + "…"
	}
	return h
}

// String 隐去凭据，凭据哈希只显示前 8 位。
func (c Config) String() string {
	return fmt.Sprintf("Config{mode:%s hub_url:%s hub_pin:%s credential:%s hub_hostname:%s listen:%s credential_hash:%s prev_credential_hash:%s name:%s role:%s}",
		c.Mode, c.HubURL, c.HubPin, masked, c.HubHostname, c.Listen, shortHash(c.CredentialHash), shortHash(c.PrevCredentialHash), c.Name, c.Role)
}

// GoString 与 String 一致。
func (c Config) GoString() string { return c.String() }

// Format 让 %v %+v %#v 都走脱敏输出。
func (c Config) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(c.String())) }

// LogValue 供 slog 使用。
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("mode", c.Mode), slog.String("hub_url", c.HubURL), slog.String("hub_pin", c.HubPin),
		slog.String("credential", masked), slog.String("hub_hostname", c.HubHostname),
		slog.String("listen", c.Listen), slog.String("credential_hash", shortHash(c.CredentialHash)),
		slog.String("prev_credential_hash", shortHash(c.PrevCredentialHash)),
		slog.String("name", c.Name), slog.String("role", c.Role))
}

// Load 读取并校验配置。权限含属组或其他用户位（mode&0o077 != 0）、
// 或属主既不是 euid 也不是 root 时拒绝。
func Load(path string, euid int) (Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取配置: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Config{}, fmt.Errorf("配置文件 %s 权限过宽（%04o），请执行: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	if uid, ok := ownerUID(info); ok && int(uid) != euid && uid != 0 {
		return Config{}, fmt.Errorf("配置文件 %s 的属主（uid %d）既不是当前用户（uid %d）也不是 root", path, uid, euid)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取配置: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Marshal 校验并编码配置为 agent.json 的文件内容（缩进 JSON，末尾换行）；install 以指定属主写入时使用。
func Marshal(c Config) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("编码配置: %w", err)
	}
	return append(data, '\n'), nil
}

// Save 以 0600 原子写入配置（同目录临时文件 + rename）。
func Save(path string, c Config) error {
	data, err := Marshal(c)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data, -1, -1)
}

// WriteFileAtomic 以 0600 原子写文件；uid/gid 均为 -1 时不改属主。
func WriteFileAtomic(path string, data []byte, uid, gid int) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if uid >= 0 || gid >= 0 {
		if err = f.Chown(uid, gid); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ErrNoHome 表示用户模式下无法确定家目录。
var ErrNoHome = errors.New("无法确定用户家目录")
