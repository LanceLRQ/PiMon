package backup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"filippo.io/age"
)

// ageMagic 是 age 二进制格式的文件头第一行（含换行）。
var ageMagic = []byte("age-encryption.org/v1\n")

const maxScryptWorkFactor = 30

// maxDecryptWorkFactor 是解密时接受的 scrypt 强度上限（约 128 MiB × 2^(n-18) 的内存）。
const maxDecryptWorkFactor = 20

// ErrWrongPassphrase 表示备份口令不正确。
var ErrWrongPassphrase = errors.New("备份口令不正确")

// IsAgeHeader 判断 head（文件开头的若干字节）是否为 age 加密文件的头部。
func IsAgeHeader(head []byte) bool {
	return bytes.HasPrefix(head, ageMagic)
}

// EncryptReader 用口令（age scrypt 模式）流式加密 src，返回拉取式密文读取器，不会把 src 整体读入内存。
// workFactor 为 scrypt 的 log2(N)；0 表示 age 默认强度，测试可传较小值提速。
// 传入值只来自程序内部常量，非法值（负数或过大）返回错误而不是交给 age 触发 panic。
func EncryptReader(src io.Reader, passphrase string, workFactor int) (io.Reader, error) {
	if passphrase == "" {
		return nil, errors.New("备份口令不能为空")
	}
	if workFactor < 0 || workFactor > maxScryptWorkFactor {
		return nil, fmt.Errorf("scrypt 强度 %d 超出范围", workFactor)
	}
	rec, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("创建加密接收方: %w", err)
	}
	if workFactor > 0 {
		rec.SetWorkFactor(workFactor)
	}
	r, err := age.EncryptReader(src, rec)
	if err != nil {
		return nil, fmt.Errorf("初始化加密: %w", err)
	}
	return r, nil
}

// DecryptReader 用口令解密 age 流；口令错误时返回 ErrWrongPassphrase。
// 文件头在此处解析，因此口令错误会在读取任何明文之前报出。
func DecryptReader(src io.Reader, passphrase string) (io.Reader, error) {
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("创建解密身份: %w", err)
	}
	// 限制文件头可声明的强度，避免被篡改的高强度文件在树莓派上耗尽内存；上限不低于加密端默认强度 18。
	id.SetMaxWorkFactor(maxDecryptWorkFactor)
	r, err := age.Decrypt(src, id)
	if err != nil {
		if errors.Is(err, age.ErrIncorrectIdentity) {
			return nil, ErrWrongPassphrase
		}
		return nil, fmt.Errorf("解密备份: %w", err)
	}
	return r, nil
}

const remoteSuffix = ".age"

// RemoteName 把本地备份文件名转成远端加密文件名（加 .age 后缀）；name 不是本项目的备份命名时返回 false。
func RemoteName(name string) (string, bool) {
	if _, _, ok := parseName(name); !ok {
		return "", false
	}
	return name + remoteSuffix, true
}

// ParseRemoteName 判断远端文件名是否为本项目的加密备份命名，并解析出文件名里的时间。
func ParseRemoteName(name string) (time.Time, bool) {
	local, ok := strings.CutSuffix(name, remoteSuffix)
	if !ok {
		return time.Time{}, false
	}
	t, _, ok := parseName(local)
	return t, ok
}
