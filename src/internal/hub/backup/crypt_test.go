package backup

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

const testWorkFactor = 10

func TestEncryptDecryptRoundTrip(t *testing.T) {
	plain := make([]byte, 300_000)
	_, _ = rand.Read(plain)
	enc, err := EncryptReader(bytes.NewReader(plain), "correct horse battery", testWorkFactor)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := io.ReadAll(enc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, plain[:64]) {
		t.Fatal("密文中不应出现明文")
	}
	if !IsAgeHeader(cipher[:32]) {
		t.Fatal("密文应以 age 文件头开头")
	}
	dec, err := DecryptReader(bytes.NewReader(cipher), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("往返后内容不一致")
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	enc, err := EncryptReader(bytes.NewReader([]byte("hello")), "right-passphrase", testWorkFactor)
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := io.ReadAll(enc)
	_, err = DecryptReader(bytes.NewReader(cipher), "wrong-passphrase")
	if !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("err = %v，期望 ErrWrongPassphrase", err)
	}
}

func TestDecryptNotAge(t *testing.T) {
	_, err := DecryptReader(bytes.NewReader([]byte("\x1f\x8b not age")), "x")
	if err == nil || errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("非 age 数据应返回其他错误，得到 %v", err)
	}
}

// 仅头部合法的文件声明了过高的 scrypt 强度：读取头部时就被拒，不会真的去算 scrypt（否则树莓派 OOM）。
func TestDecryptRejectsExcessiveWorkFactor(t *testing.T) {
	salt := base64.RawStdEncoding.EncodeToString(make([]byte, 16))
	body := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	mac := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	file := "age-encryption.org/v1\n-> scrypt " + salt + " 21\n" + body + "\n--- " + mac + "\n"
	_, err := DecryptReader(strings.NewReader(file), "x")
	if err == nil || errors.Is(err, ErrWrongPassphrase) || !strings.Contains(err.Error(), "work factor") {
		t.Fatalf("过高的 work factor 应被拒绝：%v", err)
	}
}

func TestEncryptEmptyPassphraseAndBadWorkFactor(t *testing.T) {
	if _, err := EncryptReader(bytes.NewReader(nil), "", testWorkFactor); err == nil {
		t.Fatal("空口令应报错")
	}
	for _, wf := range []int{-1, 31} {
		if _, err := EncryptReader(bytes.NewReader(nil), "pass", wf); err == nil {
			t.Fatalf("work factor %d 应报错", wf)
		}
	}
	if _, err := EncryptReader(bytes.NewReader(nil), "pass", 0); err != nil {
		t.Fatalf("0 表示默认强度: %v", err)
	}
}

func TestIsAgeHeader(t *testing.T) {
	if !IsAgeHeader([]byte("age-encryption.org/v1\nxxxx")) {
		t.Fatal("应识别 age 文件头")
	}
	if IsAgeHeader([]byte("age-encryption.org/v")) || IsAgeHeader([]byte{0x1f, 0x8b, 8}) || IsAgeHeader(nil) {
		t.Fatal("不应误判")
	}
}

// countingSource 生成 total 字节随机数据，并记录单次请求的最大缓冲与已读出的总量。
type countingSource struct {
	left   int64
	read   int64
	maxBuf int
}

func (c *countingSource) Read(p []byte) (int, error) {
	if len(p) > c.maxBuf {
		c.maxBuf = len(p)
	}
	if c.left == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, _ := rand.Read(p)
	c.left -= int64(n)
	c.read += int64(n)
	return n, nil
}

// 流式加密：消费端用小缓冲拉取时，源端已读出但尚未被消费的字节数必须有界，与总大小无关。
func TestEncryptReaderStreams(t *testing.T) {
	const total = 50 << 20
	src := &countingSource{left: total}
	enc, err := EncryptReader(src, "stream-passphrase", testWorkFactor)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	var out, maxOutstanding int64
	for {
		n, rerr := enc.Read(buf)
		out += int64(n)
		if o := src.read - out; o > maxOutstanding {
			maxOutstanding = o
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			t.Fatal(rerr)
		}
	}
	if out < total {
		t.Fatalf("输出 %d 小于输入 %d", out, total)
	}
	if limit := int64(1 << 20); maxOutstanding > limit {
		t.Fatalf("在途数据 %d 字节超过 %d，疑似整体读入内存", maxOutstanding, limit)
	}
	if src.maxBuf > 1<<20 {
		t.Fatalf("单次向源端请求 %d 字节，过大", src.maxBuf)
	}
}

func TestRemoteNameHelpers(t *testing.T) {
	local := "pimon-backup-20260930-101500-daily.tar.gz"
	remote, ok := RemoteName(local)
	if !ok || remote != local+".age" {
		t.Fatalf("RemoteName = %q %v", remote, ok)
	}
	if _, ok := RemoteName("other.tar.gz"); ok {
		t.Fatal("非备份命名不应通过")
	}
	tm, ok := ParseRemoteName(remote)
	if !ok || tm.Format("20060102-150405") != "20260930-101500" {
		t.Fatalf("ParseRemoteName = %v %v", tm, ok)
	}
	for _, bad := range []string{local, ".pimon-upload-" + remote + ".part", "pimon-backup-x-daily.tar.gz.age", "notes.txt", "pimon-backup-20260930-101500-weekly.tar.gz.age"} {
		if _, ok := ParseRemoteName(bad); ok {
			t.Fatalf("%q 不应匹配", bad)
		}
	}
}
