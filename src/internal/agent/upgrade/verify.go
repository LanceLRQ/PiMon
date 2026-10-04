package upgrade

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// tagPattern 是 Release tag 的格式（只接受正式版本 vX.Y.Z）。
var tagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// ValidTag 报告 tag 是否为合法的 Release tag（vX.Y.Z）。
func ValidTag(tag string) bool { return tagPattern.MatchString(tag) }

// normalizeSum 校验并规范化 SHA-256 十六进制（64 位，统一小写）。
func normalizeSum(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != sha256.Size*2 {
		return "", false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", false
	}
	return s, true
}

// fileSum 计算文件内容的 SHA-256 十六进制。
func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// lookupSum 在 SHA256SUMS 内容里按文件名列精确匹配 name（兼容二进制模式的 "*" 前缀），返回小写校验和。
func lookupSum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		s, ok := normalizeSum(fields[0])
		if !ok {
			return "", fmt.Errorf("SHA256SUMS 中 %s 的校验和格式不正确", name)
		}
		return s, nil
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("解析 SHA256SUMS: %w", err)
	}
	return "", fmt.Errorf("SHA256SUMS 中没有 %s（该 Release 可能不提供本平台）", name)
}
