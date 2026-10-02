package kiosk

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

var errTokenEmpty = errors.New("屏幕令牌文件为空")

// readToken 读取屏幕令牌文件并去掉首尾空白；不存在、读失败或内容为空都返回错误。
func readToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取屏幕令牌: %w", err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", errTokenEmpty
	}
	return tok, nil
}

func tokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}
