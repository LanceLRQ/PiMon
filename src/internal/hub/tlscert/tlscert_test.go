package tlscert

import (
	"crypto/ecdsa"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func testEnv() Env {
	return Env{
		Hostname: func() (string, error) { return "pimon-box", nil },
		InterfaceAddrs: func() ([]net.Addr, error) {
			return []net.Addr{
				&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)},
				&net.IPAddr{IP: net.ParseIP("fe80::1")},
			}, nil
		},
	}
}

func paths(t *testing.T) (string, string) {
	dir := t.TempDir()
	return filepath.Join(dir, "hub.crt"), filepath.Join(dir, "hub.key")
}

func TestCreateThenLoadReturnsSameCert(t *testing.T) {
	c, k := paths(t)
	first, err := LoadOrCreate(c, k, t0, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(c, k, t0.Add(time.Hour), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(first) != Fingerprint(second) {
		t.Fatal("再次读取应得到同一张证书")
	}
	if len(Fingerprint(first)) != 64 {
		t.Fatalf("指纹应为 64 位十六进制: %q", Fingerprint(first))
	}
}

func TestCertContents(t *testing.T) {
	c, k := paths(t)
	cert, err := LoadOrCreate(c, k, t0, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	x, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"pimon-box", "pimon-box.local", "localhost"} {
		if !slices.Contains(x.DNSNames, n) {
			t.Errorf("SAN 缺少 DNS %s: %v", n, x.DNSNames)
		}
	}
	has := func(s string) bool {
		return slices.ContainsFunc(x.IPAddresses, func(ip net.IP) bool { return ip.Equal(net.ParseIP(s)) })
	}
	for _, s := range []string{"127.0.0.1", "::1", "192.168.1.20", "fe80::1"} {
		if !has(s) {
			t.Errorf("SAN 缺少 IP %s: %v", s, x.IPAddresses)
		}
	}
	if !x.NotBefore.Equal(t0) || !x.NotAfter.Equal(t0.AddDate(10, 0, 0)) {
		t.Errorf("有效期不对: %v ~ %v", x.NotBefore, x.NotAfter)
	}
	if _, ok := x.PublicKey.(*ecdsa.PublicKey); !ok {
		t.Errorf("应为 ECDSA 公钥: %T", x.PublicKey)
	}
	if err := x.VerifyHostname("localhost"); err != nil {
		t.Error(err)
	}
}

func TestFilePermissions(t *testing.T) {
	c, k := paths(t)
	if _, err := LoadOrCreate(c, k, t0, testEnv()); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{c: 0o644, k: 0o600} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s 权限 = %o, 期望 %o", p, st.Mode().Perm(), want)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(c))
	if len(entries) != 2 {
		t.Errorf("不应残留临时文件: %v", entries)
	}
}

func TestDefaultEnvWorks(t *testing.T) {
	c, k := paths(t)
	if _, err := LoadOrCreate(c, k, t0, Env{}); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptFilesError(t *testing.T) {
	c, k := paths(t)
	_ = os.WriteFile(c, []byte("junk"), 0o644)
	_ = os.WriteFile(k, []byte("junk"), 0o600)
	if _, err := LoadOrCreate(c, k, t0, testEnv()); err == nil {
		t.Fatal("损坏的证书应报错而不是被覆盖")
	}
}
