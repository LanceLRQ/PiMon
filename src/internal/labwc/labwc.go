// Package labwc 提供 labwc 的 autostart、environment 文件的纯文本处理函数。
// 所有函数只处理字符串，不读写文件；备份与落盘由调用方负责。
// kiosk 的息屏检查与 install 的息屏处理共用这里的判定，保证两处口径一致。
package labwc

import (
	"path"
	"strings"
)

// splitLines 把内容按行切开，每个元素保留自己的行尾（\n、\r\n 或无）。
func splitLines(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// body 去掉行尾的 \r\n。
func body(line string) string { return strings.TrimRight(line, "\r\n") }

// newline 返回内容使用的换行符：出现过 CRLF 就用 CRLF。
func newline(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// withTerminator 保证末行以换行结束（用于追加新行前）。
func withTerminator(content string) string {
	if content == "" || strings.HasSuffix(content, "\n") {
		return content
	}
	return content + newline(content)
}

// IsComment 判断一行是否是注释行（去掉前导空白后以 # 开头）。
func IsComment(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#")
}

// IsSwayidleLine 判断一行是否启用了 swayidle：非注释，且某个不带引号的词的文件名是 swayidle。
// 引号里的内容和行内尾注释不参与判断。
func IsSwayidleLine(line string) bool {
	for _, w := range commandWords(body(line)) {
		if path.Base(w) == "swayidle" {
			return true
		}
	}
	return false
}

// commandWords 切出一行里不含引号的词；遇到词首的 # 即停止（行内注释）。
// 含引号的词整体丢弃，避免把参数内容当成命令。
func commandWords(line string) []string {
	var (
		words   []string
		cur     strings.Builder
		tainted bool
		quote   rune
		inWord  bool
	)
	flush := func() {
		if inWord && !tainted && cur.Len() > 0 {
			words = append(words, cur.String())
		}
		cur.Reset()
		tainted, inWord = false, false
	}
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			escaped = false
			tainted = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\\':
			escaped, inWord, tainted = true, true, true
		case r == '\'' || r == '"':
			quote, inWord, tainted = r, true, true
		case r == '#' && !inWord:
			flush()
			return words
		case strings.ContainsRune(" \t;&|(){}", r):
			flush()
		default:
			inWord = true
			cur.WriteRune(r)
		}
	}
	flush()
	return words
}

// HasSwayidle 判断 autostart 内容里是否有启用的 swayidle 行。
func HasSwayidle(content string) bool {
	for _, l := range splitLines(content) {
		if IsSwayidleLine(l) {
			return true
		}
	}
	return false
}

// RemoveSwayidle 删掉所有启用的 swayidle 行，其余内容（含注释行、空行、行尾风格）原样保留；
// 第二个返回值表示是否有改动。
func RemoveSwayidle(content string) (string, bool) {
	var b strings.Builder
	changed := false
	for _, l := range splitLines(content) {
		if IsSwayidleLine(l) {
			changed = true
			continue
		}
		b.WriteString(l)
	}
	if !changed {
		return content, false
	}
	return b.String(), true
}

// EnsureLine 保证 autostart 里有 line 这一启用行：已有（忽略首尾空白、注释行不算）则不动，
// 否则追加到末尾。第二个返回值表示是否有改动。
func EnsureLine(content, line string) (string, bool) {
	want := strings.TrimSpace(line)
	for _, l := range splitLines(content) {
		if !IsComment(l) && strings.TrimSpace(body(l)) == want {
			return content, false
		}
	}
	return withTerminator(content) + want + newline(content), true
}

// SetEnv 设置 environment 文件里的 KEY=VALUE：整行替换同名键（所有出现处），
// 其他行原样保留；键不存在则追加。第二个返回值表示是否有改动。
func SetEnv(content, key, value string) (string, bool) {
	entry := key + "=" + value
	var b strings.Builder
	found, changed := false, false
	for _, l := range splitLines(content) {
		if k, ok := envKey(l); ok && k == key {
			found = true
			text := body(l)
			if text != entry {
				changed = true
			}
			b.WriteString(entry + l[len(text):])
			continue
		}
		b.WriteString(l)
	}
	if found {
		return b.String(), changed
	}
	return withTerminator(content) + entry + newline(content), true
}

// envKey 取一行的键名；注释行与不含 = 的行返回 false。
func envKey(line string) (string, bool) {
	text := strings.TrimSpace(body(line))
	if text == "" || strings.HasPrefix(text, "#") {
		return "", false
	}
	k, _, ok := strings.Cut(text, "=")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(k), true
}
