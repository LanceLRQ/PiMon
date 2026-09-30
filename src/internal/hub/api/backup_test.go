package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func TestBackupsRequireAdmin(t *testing.T) {
	e := newEnv(t)
	anon := e.newClient()
	resp, data := e.do(anon, "GET", "/api/backups", nil)
	e.expectError(resp, data, http.StatusUnauthorized, httpx.CodeAuthRequired)
	resp, data = e.do(anon, "POST", "/api/backups", nil)
	e.expectError(resp, data, http.StatusUnauthorized, httpx.CodeAuthRequired)
	resp, data = e.do(anon, "GET", "/api/backups/pimon-backup-20260101-000000-manual.tar.gz", nil)
	e.expectError(resp, data, http.StatusUnauthorized, httpx.CodeAuthRequired)
}

func TestBackupsPostChecksOrigin(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	resp, data := e.do(admin, "POST", "/api/backups", nil, withHeader("Origin", "https://evil.example"))
	e.expectError(resp, data, http.StatusForbidden, httpx.CodeOriginMismatch)
}

func TestBackupsFlow(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()

	resp, data := e.do(admin, "GET", "/api/backups", nil)
	if resp.StatusCode != http.StatusOK || string(data) != "[]\n" && string(data) != "[]" {
		t.Fatalf("空列表应为 []: %d %q", resp.StatusCode, data)
	}

	resp, data = e.do(admin, "POST", "/api/backups", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST = %d %s", resp.StatusCode, data)
	}
	var info model.BackupInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info.Reason != "manual" || info.Size <= 0 || info.Name != "pimon-backup-20260101-000000-manual.tar.gz" {
		t.Fatalf("info = %+v", info)
	}
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	for _, k := range []string{"name", "reason", "created_at", "size"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("缺少字段 %s", k)
		}
	}

	_, data = e.do(admin, "GET", "/api/backups", nil)
	var list []model.BackupInfo
	if err := json.Unmarshal(data, &list); err != nil || len(list) != 1 || list[0].Name != info.Name {
		t.Fatalf("list = %s err = %v", data, err)
	}

	resp, data = e.do(admin, "GET", "/api/backups/"+info.Name, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("下载 = %d %s", resp.StatusCode, data)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="`+info.Name+`"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if int64(len(data)) != info.Size {
		t.Errorf("下载体积 %d != %d", len(data), info.Size)
	}
}

func TestBackupDownloadNotFound(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	for _, name := range []string{"pimon-backup-20260101-000000-manual.tar.gz", "..%2Fsecret.key", "note.txt"} {
		resp, data := e.do(admin, "GET", "/api/backups/"+name, nil)
		e.expectError(resp, data, http.StatusNotFound, httpx.CodeNotFound)
	}
}
