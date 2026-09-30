package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// MinPasswordLen 是管理员密码的最短长度。
const MinPasswordLen = 8

// ErrInvalidHash 表示密码哈希串不是合法的 argon2id PHC 格式。
var ErrInvalidHash = errors.New("auth: 密码哈希格式非法")

// Params 是 argon2id 参数。Memory 单位为 KiB。
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultParams 是生产参数：64 MiB 内存、3 轮、2 线程、32 字节密钥、16 字节盐。
var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 2, KeyLen: 32, SaltLen: 16}

// Hasher 生成密码哈希。校验时参数取自编码串本身，因此与 Hasher 的参数无关。
type Hasher struct {
	Params Params
}

// Hash 用随机盐计算 password 的哈希，返回 PHC 字符串。
func (h Hasher) Hash(password string) (string, error) {
	p := h.Params
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify 校验 password 是否匹配 encoded；密码不匹配返回 (false, nil)，编码非法返回错误。
func (Hasher) Verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// 形如 ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}
	var mem, t uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &threads); err != nil || mem == 0 || t == 0 || threads == 0 {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrInvalidHash
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
