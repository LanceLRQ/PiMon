package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// fakeDeviceLogs 是设备日志拉取的替身：记录参数，按预设返回。
type fakeDeviceLogs struct {
	mu      sync.Mutex
	calls   []string
	entries []model.LogEntry
	err     error
}

func (f *fakeDeviceLogs) Logs(_ context.Context, id string, limit int, minLevel string) ([]model.LogEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s/%d/%s", id, limit, minLevel))
	return f.entries, f.err
}

func (f *fakeDeviceLogs) set(entries []model.LogEntry, err error) {
	f.mu.Lock()
	f.entries, f.err = entries, err
	f.mu.Unlock()
}

func (f *fakeDeviceLogs) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

func TestDeviceLogs需要管理员(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do(e.newClient(), "GET", "/api/devices/x/logs", nil)
	e.expectError(resp, data, http.StatusUnauthorized, "auth.required")
	e.setup()
	resp, data = e.do(e.screenClient(), "GET", "/api/devices/x/logs", nil)
	e.expectError(resp, data, http.StatusForbidden, "auth.forbidden")
}

func TestDeviceLogs拉取与参数(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	dev := e.addDevice("vps1", model.DeviceRoleVPS)
	at := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	e.devLogs.set([]model.LogEntry{{Time: at, Level: "warn", Message: "磁盘将满"}}, nil)

	resp, data := e.do(c, "GET", "/api/devices/"+dev.ID+"/logs?limit=50&level=warn", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 %s", resp.StatusCode, data)
	}
	var got model.LogList
	must(t, json.Unmarshal(data, &got))
	if len(got.Entries) != 1 || got.Entries[0].Message != "磁盘将满" || got.Capacity != 2000 {
		t.Fatalf("响应 = %+v", got)
	}
	if want := dev.ID + "/50/warn"; e.devLogs.last() != want {
		t.Fatalf("转发参数 = %q，应为 %q", e.devLogs.last(), want)
	}

	// 缺省：200 条、不过滤；超过上限截到 2000
	e.do(c, "GET", "/api/devices/"+dev.ID+"/logs", nil)
	if want := dev.ID + "/200/"; e.devLogs.last() != want {
		t.Fatalf("缺省参数 = %q", e.devLogs.last())
	}
	e.do(c, "GET", "/api/devices/"+dev.ID+"/logs?limit=99999&level=debug", nil)
	if want := dev.ID + "/2000/debug"; e.devLogs.last() != want {
		t.Fatalf("上限 = %q", e.devLogs.last())
	}

	// 空列表输出 []
	e.devLogs.set(nil, nil)
	_, data = e.do(c, "GET", "/api/devices/"+dev.ID+"/logs", nil)
	var raw map[string]json.RawMessage
	must(t, json.Unmarshal(data, &raw))
	if string(raw["entries"]) != "[]" {
		t.Fatalf("空日志应为 []，得到 %s", raw["entries"])
	}
}

func TestDeviceLogs错误映射(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	dev := e.addDevice("vps1", model.DeviceRoleVPS)
	path := "/api/devices/" + dev.ID + "/logs"

	resp, data := e.do(c, "GET", path+"?level=trace", nil)
	er := e.expectError(resp, data, http.StatusBadRequest, "validation.failed")
	if fields, _ := er.Error.Details["fields"].(map[string]any); fields["level"] == nil {
		t.Fatalf("应指出 level 字段: %v", er.Error.Details)
	}
	resp, data = e.do(c, "GET", path+"?limit=0", nil)
	e.expectError(resp, data, http.StatusBadRequest, "validation.failed")

	resp, data = e.do(c, "GET", "/api/devices/nope/logs", nil)
	e.expectError(resp, data, http.StatusNotFound, "device.not_found")

	e.devLogs.set(nil, fmt.Errorf("%w: x", instances.ErrDeviceOffline))
	resp, data = e.do(c, "GET", path, nil)
	e.expectError(resp, data, http.StatusConflict, "device.offline")

	e.devLogs.set(nil, &instances.DeviceIncompatibleError{Reason: "version_too_old", RequiredMin: 2, HubVersion: "0.3.0"})
	resp, data = e.do(c, "GET", path, nil)
	er = e.expectError(resp, data, http.StatusConflict, "device.incompatible")
	if er.Error.Details["reason"] != "version_too_old" || er.Error.Details["required_min"] != float64(2) || er.Error.Details["hub_version"] != "0.3.0" {
		t.Fatalf("details = %v", er.Error.Details)
	}

	e.devLogs.set(nil, instances.NotRecorded(fmt.Errorf("%w: 设备未应答", runtime.ErrTimeout)))
	resp, data = e.do(c, "GET", path, nil)
	e.expectError(resp, data, http.StatusGatewayTimeout, "run.timeout")

	e.devLogs.set(nil, errors.New("内部"))
	resp, data = e.do(c, "GET", path, nil)
	e.expectError(resp, data, http.StatusInternalServerError, "internal")
}

func TestWriteInstanceError设备错误码(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: 会话已断开", instances.ErrDeviceOffline), http.StatusConflict, "device.offline"},
		{&instances.DeviceIncompatibleError{Reason: "version_too_new", RequiredMin: 3, HubVersion: "0.2.0"}, http.StatusConflict, "device.incompatible"},
		{instances.NotRecorded(fmt.Errorf("%w: 尚未下发", runtime.ErrFailed)), http.StatusBadGateway, "run.failed"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		writeInstanceError(w, httptest.NewRequest("POST", "/api/instances/x/run", nil), c.err)
		if w.Code != c.status {
			t.Fatalf("%v: 状态码 = %d", c.err, w.Code)
		}
		er := parseErr(t, w.Body.Bytes())
		if er.Error.Code != c.code {
			t.Fatalf("%v: code = %q", c.err, er.Error.Code)
		}
		if c.code == "device.incompatible" &&
			(er.Error.Details["reason"] != "version_too_new" || er.Error.Details["required_min"] != float64(3) || er.Error.Details["hub_version"] != "0.2.0") {
			t.Fatalf("details = %v", er.Error.Details)
		}
	}
}
