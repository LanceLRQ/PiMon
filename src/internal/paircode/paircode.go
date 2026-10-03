// Package paircode 是一次性配对码的生成、规范化与哈希，hub（拨入配对码）与 agent（拨出配对窗口）共用。
// 只依赖标准库。
package paircode

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
)

// Bytes 是配对码的随机字节数：160 bit，编成 32 位 Crockford。
const Bytes = 20

// Crockford base32 字母表（不含 I L O U）。
var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// New 生成配对码的展示形式（8 组 × 4 位，以 - 分隔）。
func New() (string, error) {
	raw := make([]byte, Bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成配对码: %w", err)
	}
	plain := crockford.EncodeToString(raw)
	groups := make([]string, 0, len(plain)/4)
	for i := 0; i < len(plain); i += 4 {
		groups = append(groups, plain[i:i+4])
	}
	return strings.Join(groups, "-"), nil
}

// Normalize 转大写、去掉分隔符与空白，并按 Crockford 规则归一易混字符（O→0，I/L→1）。
func Normalize(in string) string {
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

// Hash 返回用户输入的配对码规范化后的 sha256 十六进制；只存这个值，不存原文。
func Hash(input string) string {
	sum := sha256.Sum256([]byte(Normalize(input)))
	return hex.EncodeToString(sum[:])
}
