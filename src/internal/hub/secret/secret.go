// Package secret 负责 secret.key 的生成与读取，以及基于它的 AES-GCM 加解密。
//
// 密文格式为 base64(nonce ‖ 密文)，每次加密使用随机 nonce。
// 不支持密钥轮换；恢复备份时密钥文件须与数据库一起还原。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// KeySize 是密钥文件的固定长度（AES-256）。
const KeySize = 32

// hookBeforePublish 仅供测试：在临时文件发布为正式文件之前调用，用于模拟并发启动。
var hookBeforePublish func()

// Box 持有 AES-GCM 加解密能力，可并发使用。
type Box struct {
	aead cipher.AEAD
}

// New 用 32 字节原始密钥构造 Box。
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("密钥长度必须为 %d 字节，实际 %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// LoadOrCreate 读取 path 处的密钥文件；不存在则生成随机密钥并以 0600 创建。
// 已有文件长度不是 32 字节时报错，且不会覆盖。
func LoadOrCreate(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		key, err = create(path)
	}
	if err != nil {
		return nil, fmt.Errorf("读取密钥文件 %s: %w", path, err)
	}
	return New(key)
}

// create 生成新密钥。先完整写入同目录临时文件，再用硬链接发布为正式文件：
// 链接是原子的且目标已存在时失败（等价于 O_EXCL），因此并发启动不会互相覆盖，
// 读取方也不会看到写了一半的文件。链接失败因文件已存在时，转为读取对方写好的密钥。
func create(path string) ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secret-*.tmp")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	if hookBeforePublish != nil {
		hookBeforePublish()
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return os.ReadFile(path)
		}
		return nil, err
	}
	return key, nil
}

// Encrypt 加密字符串，返回 base64(nonce ‖ 密文)。
func (b *Box) Encrypt(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt 解密 Encrypt 的输出；格式非法、被篡改或密钥不符时报错。
func (b *Box) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("密文不是合法 base64: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns+b.aead.Overhead() {
		return "", errors.New("密文过短")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", errors.New("解密失败")
	}
	return string(plain), nil
}
