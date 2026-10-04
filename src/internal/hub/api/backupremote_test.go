package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/webdav"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/internal/hub/backup/remote"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const (
	davUser       = "pimon"
	davPass       = "dav-secret-Pa55"
	davPassphrase = "correct horse battery staple"
)

// newDavServer 起一个带 BasicAuth 的 x/net/webdav 服务，返回服务与其根目录。
func newDavServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	h := &webdav.Handler{Prefix: "/dav", FileSystem: webdav.Dir(root), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != davUser || p != davPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, root
}

func davInput(davURL string) map[string]any {
	return map[string]any{
		"enabled": true, "type": "webdav", "webdav_url": davURL + "/dav", "webdav_dir": "pimon/backups",
		"webdav_username": davUser, "webdav_password": davPass, "passphrase": davPassphrase, "keep": 5,
	}
}

// captureLogs 把默认 slog 输出接到缓冲区，测试结束时恢复。
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(lockedWriter{&mu, &buf}, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func assertNoDavSecrets(t *testing.T, where string, data []byte) {
	t.Helper()
	for _, s := range []string{davPass, davPassphrase, "wrong-" + davPass} {
		if bytes.Contains(data, []byte(s)) {
			t.Fatalf("%s 含有密钥 %q：%s", where, s, data)
		}
	}
}

func TestBackupRemoteRoutesRequireAdminAndOrigin(t *testing.T) {
	e := newEnv(t)
	anon := e.newClient()
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/backups/remote"}, {"PUT", "/api/backups/remote"}, {"POST", "/api/backups/remote/test"},
		{"POST", "/api/backups/remote/upload"}, {"GET", "/api/backups/remote/uploads"}, {"DELETE", "/api/backups/remote"},
	} {
		resp, data := e.do(anon, c.method, c.path, map[string]any{})
		e.expectError(resp, data, http.StatusUnauthorized, httpx.CodeAuthRequired)
	}
	admin := e.setup()
	for _, p := range []string{"/api/backups/remote/test", "/api/backups/remote/upload"} {
		resp, data := e.do(admin, "POST", p, map[string]any{}, withHeader("Origin", "https://evil.example"))
		e.expectError(resp, data, http.StatusForbidden, httpx.CodeOriginMismatch)
	}
	resp, data := e.do(admin, "PUT", "/api/backups/remote", map[string]any{}, withHeader("Origin", "https://evil.example"))
	e.expectError(resp, data, http.StatusForbidden, httpx.CodeOriginMismatch)
	resp, data = e.do(admin, "DELETE", "/api/backups/remote", nil, withHeader("Origin", "https://evil.example"))
	e.expectError(resp, data, http.StatusForbidden, httpx.CodeOriginMismatch)
}

func TestBackupRemoteUploadErrors(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	resp, data := e.do(admin, "POST", "/api/backups/remote/upload", nil)
	e.expectError(resp, data, http.StatusConflict, httpx.CodeBackupRemoteDisabled)

	dav, _ := newDavServer(t)
	resp, data = e.do(admin, "PUT", "/api/backups/remote", davInput(dav.URL))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	resp, data = e.do(admin, "POST", "/api/backups/remote/upload", nil)
	e.expectError(resp, data, http.StatusConflict, httpx.CodeBackupNoLocal)
}

func TestBackupRemoteValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	in := davInput("http://example.com")
	for _, path := range []string{"/api/backups/remote", "/api/backups/remote/test"} {
		method := "PUT"
		if strings.HasSuffix(path, "/test") {
			method = "POST"
		}
		resp, data := e.do(admin, method, path, in)
		er := e.expectError(resp, data, http.StatusBadRequest, httpx.CodeValidationFail)
		fields, _ := er.Error.Details["fields"].(map[string]any)
		if fields["webdav_url"] != model.FieldInvalid {
			t.Fatalf("%s fields = %v", path, fields)
		}
		assertNoDavSecrets(t, path+" 响应", data)
	}
	resp, data := e.do(admin, "GET", "/api/backups/remote/uploads?limit=x", nil)
	e.expectError(resp, data, http.StatusBadRequest, httpx.CodeValidationFail)
}

func TestBackupRemoteTestFailureHidesSecrets(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	logs := captureLogs(t)
	dav, _ := newDavServer(t)

	in := davInput(dav.URL)
	in["webdav_password"] = "wrong-" + davPass
	resp, data := e.do(admin, "POST", "/api/backups/remote/test", in)
	er := e.expectError(resp, data, http.StatusBadGateway, httpx.CodeBackupRemoteFailed)
	if er.Error.Details["reason"] != "auth" || er.Error.Details["message"] == "" {
		t.Fatalf("details = %v", er.Error.Details)
	}
	assertNoDavSecrets(t, "认证失败响应", data)

	// 连接失败：关掉的端口。
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	resp, data = e.do(admin, "POST", "/api/backups/remote/test", davInput(deadURL))
	er = e.expectError(resp, data, http.StatusBadGateway, httpx.CodeBackupRemoteFailed)
	if er.Error.Details["reason"] != "connect" {
		t.Fatalf("details = %v", er.Error.Details)
	}
	assertNoDavSecrets(t, "连接失败响应", data)

	// 已保存配置后上传失败：记录与日志都不含密钥。
	if resp, data := e.do(admin, "POST", "/api/backups", nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建本地备份 = %d %s", resp.StatusCode, data)
	}
	if resp, data := e.do(admin, "PUT", "/api/backups/remote", in); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	resp, data = e.do(admin, "POST", "/api/backups/remote/upload", nil)
	er = e.expectError(resp, data, http.StatusBadGateway, httpx.CodeBackupRemoteFailed)
	if er.Error.Details["reason"] != "auth" {
		t.Fatalf("details = %v", er.Error.Details)
	}
	assertNoDavSecrets(t, "上传失败响应", data)
	_, data = e.do(admin, "GET", "/api/backups/remote/uploads", nil)
	var list model.BackupUploadList
	if err := json.Unmarshal(data, &list); err != nil || len(list.Uploads) != 1 || list.Uploads[0].Success || list.Uploads[0].ReasonCode != model.UploadReasonAuth {
		t.Fatalf("记录 = %s", data)
	}
	if list.LastResult == nil || list.LastResult.ID != list.Uploads[0].ID {
		t.Fatalf("last_result = %s", data)
	}
	assertNoDavSecrets(t, "上传记录", data)
	if !strings.Contains(logs(), "远端备份上传失败") {
		t.Fatalf("应写失败日志：%s", logs())
	}
	assertNoDavSecrets(t, "日志", []byte(logs()))
}

// 完成判定 3：经 API 配置 WebDAV 目标并立即上传，从 WebDAV 下载密文，用口令解密后得到原 tar.gz，且能被恢复流程识别。
func TestBackupRemoteEndToEndWebDAV(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	dav, _ := newDavServer(t)

	resp, data := e.do(admin, "POST", "/api/backups", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建本地备份 = %d %s", resp.StatusCode, data)
	}
	var local model.BackupInfo
	_ = json.Unmarshal(data, &local)

	resp, data = e.do(admin, "PUT", "/api/backups/remote", davInput(dav.URL))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	assertNoDavSecrets(t, "PUT 响应", data)
	var cfg model.BackupRemote
	_ = json.Unmarshal(data, &cfg)
	if !cfg.Enabled || !cfg.WebDAVPassword.Set || !cfg.Passphrase.Set || cfg.PassphraseChanged {
		t.Fatalf("PUT 响应 = %s", data)
	}

	// 测试连接：密钥留空用已保存的值。
	in := davInput(dav.URL)
	in["webdav_password"], in["passphrase"] = "", ""
	resp, data = e.do(admin, "POST", "/api/backups/remote/test", in)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("测试连接 = %d %s", resp.StatusCode, data)
	}
	var tr model.BackupRemoteTest
	if err := json.Unmarshal(data, &tr); err != nil || !tr.Success || tr.TargetType != "webdav" {
		t.Fatalf("测试连接响应 = %s", data)
	}

	resp, data = e.do(admin, "POST", "/api/backups/remote/upload", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("立即上传 = %d %s", resp.StatusCode, data)
	}
	var rec model.BackupUpload
	_ = json.Unmarshal(data, &rec)
	if !rec.Success || rec.Name != local.Name+".age" || rec.TargetType != "webdav" || rec.Size <= 0 || rec.ID == 0 {
		t.Fatalf("上传记录 = %s", data)
	}
	_, data = e.do(admin, "GET", "/api/backups/remote/uploads", nil)
	var list model.BackupUploadList
	if err := json.Unmarshal(data, &list); err != nil || len(list.Uploads) != 1 || list.Uploads[0].ID != rec.ID ||
		list.LastResult == nil || list.LastResult.ID != rec.ID {
		t.Fatalf("上传记录列表 = %s", data)
	}

	// 从 WebDAV 下载密文。
	req, _ := http.NewRequest(http.MethodGet, dav.URL+"/dav/pimon/backups/"+rec.Name, nil)
	req.SetBasicAuth(davUser, davPass)
	dl, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if dl.StatusCode != http.StatusOK || int64(len(cipher)) != rec.Size {
		t.Fatalf("下载 = %d，%d 字节，记录 %d", dl.StatusCode, len(cipher), rec.Size)
	}
	if !backup.IsAgeHeader(cipher) {
		t.Fatal("远端文件应为 age 格式")
	}
	plainR, err := backup.DecryptReader(bytes.NewReader(cipher), davPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(plainR)
	f, _, err := e.deps.Backups.Open(local.Name)
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := io.ReadAll(f)
	_ = f.Close()
	if !bytes.Equal(plain, orig) {
		t.Fatal("解密结果与本地 tar.gz 不一致")
	}

	// 解密出的 tar.gz 能被恢复流程识别（完整恢复由 E3 负责）。
	dir := t.TempDir()
	archive := filepath.Join(dir, local.Name)
	if err := os.WriteFile(archive, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath, keyPath := filepath.Join(dir, "pimon.db"), filepath.Join(dir, "secret.key")
	if err := backup.Restore(archive, dbPath, keyPath); err != nil {
		t.Fatalf("恢复流程应能识别解密后的备份：%v", err)
	}
	wantKey, _ := os.ReadFile(filepath.Join(e.deps.DataDir, "secret.key"))
	if gotKey, _ := os.ReadFile(keyPath); !bytes.Equal(gotKey, wantKey) {
		t.Fatal("恢复出的密钥与原密钥不一致")
	}

	// 改口令：响应提示 passphrase_changed，之后上传用新口令。
	in = davInput(dav.URL)
	in["webdav_password"], in["passphrase"] = "", "another passphrase 2026"
	resp, data = e.do(admin, "PUT", "/api/backups/remote", in)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("改口令 = %d %s", resp.StatusCode, data)
	}
	_ = json.Unmarshal(data, &cfg)
	if !cfg.PassphraseChanged {
		t.Fatalf("改口令后 passphrase_changed 应为 true：%s", data)
	}
}

func TestBackupUploadsEmptyListShape(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	_, data := e.do(admin, "GET", "/api/backups/remote/uploads", nil)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || string(raw["uploads"]) != "[]" || string(raw["last_result"]) != "null" {
		t.Fatalf("空列表应为 uploads=[] 与 last_result=null：%s", data)
	}
}

func TestWriteRemoteErrorShuttingDownIs503(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRemoteError(rec, httptest.NewRequest(http.MethodPost, "/api/backups/remote/upload", nil), remote.ErrShuttingDown)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	var er struct{ Error struct{ Code string } }
	if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || er.Error.Code != httpx.CodeShuttingDown {
		t.Fatalf("响应 = %s", rec.Body.String())
	}
}

func TestWriteRemoteErrorLocalReadIsRemoteFailedWithReason(t *testing.T) {
	rec := httptest.NewRecorder()
	err := remote.NewError(remote.ReasonLocalRead, "读取本地备份: boom", nil)
	writeRemoteError(rec, httptest.NewRequest(http.MethodPost, "/api/backups/remote/upload", nil), err)
	var er struct {
		Error struct {
			Code    string
			Details map[string]any
		}
	}
	if rec.Code != http.StatusBadGateway || json.Unmarshal(rec.Body.Bytes(), &er) != nil ||
		er.Error.Code != httpx.CodeBackupRemoteFailed || er.Error.Details["reason"] != "local_read" {
		t.Fatalf("响应 = %d %s", rec.Code, rec.Body.String())
	}
}

// 重置：配置与密文清空，之后读取为默认值、立即上传报未启用，日志与响应不含密钥。
func TestBackupRemoteReset(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	logs := captureLogs(t)
	dav, _ := newDavServer(t)
	if resp, data := e.do(admin, "PUT", "/api/backups/remote", davInput(dav.URL)); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	resp, data := e.do(admin, "DELETE", "/api/backups/remote", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", resp.StatusCode, data)
	}
	_, data = e.do(admin, "GET", "/api/backups/remote", nil)
	var cfg model.BackupRemote
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.Enabled || cfg.WebDAVURL != "" || cfg.WebDAVPassword.Set || cfg.Passphrase.Set {
		t.Fatalf("重置后配置 = %s", data)
	}
	resp, data = e.do(admin, "POST", "/api/backups/remote/upload", nil)
	e.expectError(resp, data, http.StatusConflict, httpx.CodeBackupRemoteDisabled)
	// 重复重置也成功。
	if resp, data := e.do(admin, "DELETE", "/api/backups/remote", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("重复 DELETE = %d %s", resp.StatusCode, data)
	}
	assertNoDavSecrets(t, "日志", []byte(logs()))
}
