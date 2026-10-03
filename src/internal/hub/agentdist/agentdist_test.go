package agentdist

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

func gz(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

// fixture 构造含两个平台的分发目录，返回各平台原始字节。
func fixture(t *testing.T, ver string) (fstest.MapFS, map[string][]byte) {
	t.Helper()
	raws := map[string][]byte{
		"linux-amd64":  []byte("linux amd64 binary"),
		"darwin-arm64": bytes.Repeat([]byte("mac"), 1000),
	}
	fsys := fstest.MapFS{
		".gitkeep": {},
		"VERSION":  {Data: []byte(ver + "\n")},
	}
	sums := ""
	for _, p := range []string{"linux-amd64", "darwin-arm64"} {
		fsys["pimon-agent-"+p+".gz"] = &fstest.MapFile{Data: gz(t, raws[p])}
		sums += sum(raws[p]) + "  pimon-agent-" + p + "\n"
	}
	fsys["SHA256SUMS"] = &fstest.MapFile{Data: []byte(sums)}
	return fsys, raws
}

func TestPlatformsSortedAndOpenReturnsRawBytes(t *testing.T) {
	setVersion(t, "v9.9.9")
	fsys, raws := fixture(t, "v9.9.9")
	d := NewFS(fsys)
	if got, want := d.Platforms(), []string{"darwin-arm64", "linux-amd64"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Platforms = %v，期望 %v", got, want)
	}
	for p, raw := range raws {
		rc, size, err := d.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, raw) {
			t.Errorf("%s 解压结果与原始字节不一致", p)
		}
		if size != int64(len(raw)) {
			t.Errorf("%s size = %d，期望 %d", p, size, len(raw))
		}
		hex, ok := d.Checksum(p)
		if !ok || hex != sum(raw) {
			t.Errorf("%s 校验和不匹配：%q %v", p, hex, ok)
		}
	}
	if d.Version() != "v9.9.9" {
		t.Errorf("Version = %q", d.Version())
	}
	if len(d.Checksums()) != 2 {
		t.Errorf("Checksums = %v", d.Checksums())
	}
	wantSums := sum(raws["darwin-arm64"]) + "  pimon-agent-darwin-arm64\n" + sum(raws["linux-amd64"]) + "  pimon-agent-linux-amd64\n"
	if string(d.SumsFile()) != wantSums {
		t.Errorf("SumsFile = %q，期望 %q", d.SumsFile(), wantSums)
	}
}

func TestUnknownPlatformUnavailable(t *testing.T) {
	setVersion(t, "v1")
	fsys, _ := fixture(t, "v1")
	d := NewFS(fsys)
	if _, _, err := d.Open("linux-arm64"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v，期望 ErrUnavailable", err)
	}
	if _, ok := d.Checksum("linux-arm64"); ok {
		t.Fatal("未知平台不应有校验和")
	}
}

func TestVersionMismatchHidesEverything(t *testing.T) {
	setVersion(t, "v2")
	fsys, _ := fixture(t, "v1")
	d := NewFS(fsys)
	if len(d.Platforms()) != 0 || len(d.Checksums()) != 0 || len(d.SumsFile()) != 0 {
		t.Fatalf("版本不一致应视为空：%v", d.Platforms())
	}
	if _, _, err := d.Open("linux-amd64"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyDist(t *testing.T) {
	d := NewFS(fstest.MapFS{".gitkeep": {}})
	if len(d.Platforms()) != 0 {
		t.Fatal("只有 .gitkeep 时 Platforms 应为空")
	}
	if _, _, err := d.Open("linux-amd64"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	// 内嵌目录在未构建 agent 时同样可用。
	_ = Embedded()
}

func TestSumsWithoutBinarySkipsPlatform(t *testing.T) {
	setVersion(t, "v1")
	fsys, _ := fixture(t, "v1")
	delete(fsys, "pimon-agent-darwin-arm64.gz")
	d := NewFS(fsys)
	if got := d.Platforms(); !reflect.DeepEqual(got, []string{"linux-amd64"}) {
		t.Fatalf("Platforms = %v", got)
	}
}
