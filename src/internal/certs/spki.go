package certs

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const spkiPrefix = "SHA256:"

// SPKI 是证书公钥信息（SubjectPublicKeyInfo）的 SHA-256 摘要。
// 同一密钥重签证书时它保持不变。
type SPKI [32]byte

// String 返回 "SHA256:<无填充标准 base64>"，与 OpenSSH 指纹格式一致。
func (s SPKI) String() string {
	return spkiPrefix + base64.RawStdEncoding.EncodeToString(s[:])
}

// SPKIOf 计算证书叶子的 SPKI 指纹。
func SPKIOf(cert tls.Certificate) (SPKI, error) {
	leaf := cert.Leaf
	if leaf == nil {
		if len(cert.Certificate) == 0 {
			return SPKI{}, errors.New("证书为空")
		}
		var err error
		if leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return SPKI{}, fmt.Errorf("解析证书失败: %w", err)
		}
	}
	return spkiOfX509(leaf), nil
}

func spkiOfX509(c *x509.Certificate) SPKI {
	return sha256.Sum256(c.RawSubjectPublicKeyInfo)
}

// ParseSPKI 解析指纹文本。接受 "SHA256:<base64>"（前缀不区分大小写），
// 以及 64 位十六进制（不区分大小写，可含冒号分隔与首尾空白）。
func ParseSPKI(text string) (SPKI, error) {
	t := strings.TrimSpace(text)
	if len(t) >= len(spkiPrefix) && strings.EqualFold(t[:len(spkiPrefix)], spkiPrefix) {
		t = strings.TrimSpace(t[len(spkiPrefix):])
	}
	var out SPKI
	if t == "" {
		return out, errors.New("指纹为空")
	}
	// 十六进制（含冒号分隔）优先：base64 的 43 字符不可能误判为 64 位十六进制。
	if h := strings.ReplaceAll(t, ":", ""); len(h) == 2*len(out) {
		if _, err := hex.Decode(out[:], []byte(h)); err != nil {
			return SPKI{}, errors.New("指纹格式不对：不是有效的十六进制")
		}
		return out, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(t)
	if err != nil || len(raw) != len(out) {
		return SPKI{}, errors.New("指纹格式不对：应为 SHA256:<base64> 或 64 位十六进制")
	}
	copy(out[:], raw)
	return out, nil
}

// PinMismatchError 表示对端证书的 SPKI 与预期不符。
type PinMismatchError struct {
	Want, Got SPKI
}

func (e *PinMismatchError) Error() string {
	return fmt.Sprintf("证书指纹不匹配: 期望 %s, 实际 %s", e.Want, e.Got)
}

// PinVerifier 返回 tls.Config.VerifyConnection 回调：只比对对端叶子证书的
// SPKI，忽略有效期、主机名与证书链。使用时需同时设置 InsecureSkipVerify，
// 让标准校验让位给钉扎。
func PinVerifier(want SPKI) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("对端未提供证书")
		}
		got := spkiOfX509(cs.PeerCertificates[0])
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			return &PinMismatchError{Want: want, Got: got}
		}
		return nil
	}
}
