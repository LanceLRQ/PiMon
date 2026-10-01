package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func TestSystemRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	anon := e.newClient()
	for _, p := range []string{"/api/system", "/api/system/logs"} {
		resp, data := e.do(anon, "GET", p, nil)
		e.expectError(resp, data, http.StatusUnauthorized, httpx.CodeAuthRequired)
	}
}

func TestSystemInfoShape(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	e.clk.Advance(3 * time.Minute)
	resp, data := e.do(admin, "GET", "/api/system", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d: %s", resp.StatusCode, data)
	}
	var info model.SystemInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "v-test" || info.UptimeSeconds != 180 || info.Plugins.Total == 0 || info.Plugins.PluginDir == "" {
		t.Fatalf("info = %+v", info)
	}
	// 平台无进程 IO 统计时 disk_writes 与 used_bytes 要如实为 null，而不是 0。
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	if v, ok := raw["disk_writes"]; !ok || v != nil {
		t.Fatalf("disk_writes 应为 null: %v", raw["disk_writes"])
	}
	if raw["data_dir"].(map[string]any)["used_bytes"] == nil {
		t.Fatal("数据目录存在时 used_bytes 不应为 null")
	}
}

func TestSystemLogsFilterAndValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	lg := slog.New(e.ring.Handler(slog.LevelDebug))
	lg.Info("hello")
	lg.Warn("careful", "password", "p@ss")
	lg.Error("boom")

	get := func(q string) model.LogList {
		resp, data := e.do(admin, "GET", "/api/system/logs"+q, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", q, resp.StatusCode, data)
		}
		var l model.LogList
		if err := json.Unmarshal(data, &l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	if l := get("?level=warn"); len(l.Entries) != 2 || l.Entries[0].Message != "careful" || l.Capacity != 5 {
		t.Fatalf("warn 筛选: %+v", l)
	}
	if l := get("?level=error&limit=1"); len(l.Entries) != 1 || l.Entries[0].Message != "boom" {
		t.Fatalf("error 筛选: %+v", l)
	}
	_, data := e.do(admin, "GET", "/api/system/logs?level=warn", nil)
	if string(data) == "" || strings.Contains(string(data), "p@ss") {
		t.Fatalf("响应不应含敏感值: %s", data)
	}
	for _, q := range []string{"?level=trace", "?limit=0", "?limit=x"} {
		resp, body := e.do(admin, "GET", "/api/system/logs"+q, nil)
		e.expectError(resp, body, http.StatusBadRequest, httpx.CodeValidationFail)
	}
}
