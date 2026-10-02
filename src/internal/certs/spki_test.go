package certs

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSPKI_同一密钥重签证书指纹不变(t *testing.T) {
	c, k := paths(t)
	first, err := LoadOrCreate(c, k, t0, Options{Env: testEnv()})
	if err != nil {
		t.Fatal(err)
	}
	want, err := SPKIOf(first)
	if err != nil {
		t.Fatal(err)
	}
	// 用同一私钥重新签一张证书，序列号与有效期都不同。
	resigned := resign(t, first, t0.Add(48*time.Hour))
	got, err := SPKIOf(resigned)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("同一密钥重签后 SPKI 应不变")
	}
	if string(first.Certificate[0]) == string(resigned.Certificate[0]) {
		t.Fatal("重签后的证书 DER 应与原证书不同")
	}
}

func TestSPKI_叶子为空时从DER解析(t *testing.T) {
	c, k := paths(t)
	cert, err := LoadOrCreate(c, k, t0, Options{Env: testEnv()})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := SPKIOf(cert)
	cert.Leaf = nil
	got, err := SPKIOf(cert)
	if err != nil || got != want {
		t.Fatalf("Leaf 为空也应能计算: %v", err)
	}
	if _, err := SPKIOf(tls.Certificate{}); err == nil {
		t.Fatal("空证书应报错")
	}
}

func TestSPKI_文本格式与解析(t *testing.T) {
	c, k := paths(t)
	cert, _ := LoadOrCreate(c, k, t0, Options{Env: testEnv()})
	spki, _ := SPKIOf(cert)
	s := spki.String()
	if !strings.HasPrefix(s, "SHA256:") || len(s) != len("SHA256:")+43 || strings.Contains(s, "=") {
		t.Fatalf("格式不对: %q", s)
	}
	hexForm := strings.ToUpper(hexOf(spki))
	colon := colonHex(hexOf(spki))
	cases := map[string]string{
		"标准":       s,
		"前缀小写":     "sha256:" + s[len("SHA256:"):],
		"十六进制":     hexOf(spki),
		"十六进制大写":   hexForm,
		"冒号分隔":     colon,
		"首尾空白":     "  \t" + s + "\n",
		"冒号分隔带空白":  " " + strings.ToUpper(colon) + " ",
		"带前缀的十六进制": "SHA256:" + hexOf(spki),
	}
	for name, in := range cases {
		got, err := ParseSPKI(in)
		if err != nil {
			t.Errorf("%s: 解析失败: %v", name, err)
			continue
		}
		if got != spki {
			t.Errorf("%s: 解析结果不一致", name)
		}
	}
	for _, bad := range []string{"", "SHA256:", "SHA256:abc", "zz", hexOf(spki)[:62], hexOf(spki) + "00",
		strings.Repeat("g", 64), "SHA256:" + strings.Repeat("!", 43), "MD5:" + s[len("SHA256:"):]} {
		if _, err := ParseSPKI(bad); err == nil {
			t.Errorf("非法文本应报错: %q", bad)
		}
	}
}

func TestPinVerifier_匹配通过_不同拒绝(t *testing.T) {
	c, k := paths(t)
	cert, _ := LoadOrCreate(c, k, t0, Options{Env: testEnv()})
	want, _ := SPKIOf(cert)
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert.Leaf}}
	if err := PinVerifier(want)(state); err != nil {
		t.Fatalf("匹配应通过: %v", err)
	}
	other := want
	other[0] ^= 0xff
	err := PinVerifier(other)(state)
	var pe *PinMismatchError
	if !errors.As(err, &pe) {
		t.Fatalf("应返回 PinMismatchError: %v", err)
	}
	if pe.Want != other || pe.Got != want {
		t.Fatal("PinMismatchError 的 Want/Got 不对")
	}
	if err := PinVerifier(want)(tls.ConnectionState{}); err == nil {
		t.Fatal("没有对端证书应报错")
	}
}

func TestPinVerifier_过期证书仍通过(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	cert, err := create(dir+"/a.crt", dir+"/a.key", old, Options{CommonName: "PiMon Test", Env: testEnv()})
	if err != nil {
		t.Fatal(err)
	}
	if time.Now().Before(cert.Leaf.NotAfter) {
		t.Fatal("测试前提：证书应已过期")
	}
	want, _ := SPKIOf(cert)
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert.Leaf}}
	if err := PinVerifier(want)(state); err != nil {
		t.Fatalf("过期证书应通过钉扎: %v", err)
	}
}

func TestLoadOrCreate_默认CN与自定义CN(t *testing.T) {
	c, k := paths(t)
	cert, err := LoadOrCreate(c, k, t0, Options{Env: testEnv()})
	if err != nil {
		t.Fatal(err)
	}
	if cn := cert.Leaf.Subject.CommonName; cn != "PiMon" {
		t.Errorf("默认 CN = %q", cn)
	}
	c2, k2 := paths(t)
	cert2, _ := LoadOrCreate(c2, k2, t0, Options{CommonName: "PiMon Agent", Env: testEnv()})
	if cn := cert2.Leaf.Subject.CommonName; cn != "PiMon Agent" {
		t.Errorf("自定义 CN = %q", cn)
	}
}
