package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/agentsrv"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// fakePairer 是拨出配对服务的测试替身：记录请求，按预设返回设备或错误。
type fakePairer struct {
	mu   sync.Mutex
	reqs []model.DialOutRequest
	dev  model.Device
	err  error
}

func (f *fakePairer) Pair(_ context.Context, req model.DialOutRequest) (model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return f.dev, f.err
}

func (f *fakePairer) set(dev model.Device, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dev, f.err = dev, err
}

func (f *fakePairer) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func TestDialOut需要管理员(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do(e.newClient(), "POST", "/api/devices/dial-out", map[string]any{})
	e.expectError(resp, data, http.StatusUnauthorized, "auth.required")
	e.setup()
	resp, data = e.do(e.screenClient(), "POST", "/api/devices/dial-out", map[string]any{})
	e.expectError(resp, data, http.StatusForbidden, "auth.forbidden")
	if e.pairer.calls() != 0 {
		t.Fatal("未授权的请求不应进入配对")
	}
}

func TestDialOut成功返回设备(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	e.pairer.set(model.Device{ID: "abcd", Name: "vps1", Direction: model.DeviceDialOut, Presence: model.PresenceOnline}, nil)
	body := map[string]any{"address": "wss://vps.example.com", "fingerprint": "SHA256:x", "code": "C", "name": "vps1", "auto_host_metrics": false}
	resp, data := e.do(c, "POST", "/api/devices/dial-out", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("状态码 = %d，响应 %s", resp.StatusCode, data)
	}
	var dev model.Device
	must(t, json.Unmarshal(data, &dev))
	if dev.ID != "abcd" || dev.Presence != model.PresenceOnline {
		t.Fatalf("响应设备 = %+v", dev)
	}
	e.pairer.mu.Lock()
	got := e.pairer.reqs[0]
	e.pairer.mu.Unlock()
	if got.Address != "wss://vps.example.com" || got.Code != "C" || got.AutoHostMetrics == nil || *got.AutoHostMetrics {
		t.Fatalf("传给配对服务的请求 = %+v", got)
	}
}

func TestDialOut字段校验失败(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	e.pairer.set(model.Device{}, model.FieldErrors{"address": model.FieldInvalid, "code": model.FieldRequired})
	resp, data := e.do(c, "POST", "/api/devices/dial-out", map[string]any{"address": "http://x"})
	er := e.expectError(resp, data, http.StatusBadRequest, "validation.failed")
	fields, _ := er.Error.Details["fields"].(map[string]any)
	if fields["address"] != model.FieldInvalid || fields["code"] != model.FieldRequired {
		t.Fatalf("字段错误 = %v", er.Error.Details)
	}
	resp, data = e.do(c, "POST", "/api/devices/dial-out", nil, withHeader("Content-Type", "application/json"))
	e.expectError(resp, data, http.StatusBadRequest, "request.invalid_json")
}

func TestDialOut配对失败返回502与原因(t *testing.T) {
	e := newEnv(t)
	c := e.setup()

	e.pairer.set(model.Device{}, &agentsrv.PairingError{Reason: agentsrv.ReasonPinMismatch, Err: errors.New("内部细节")})
	resp, data := e.do(c, "POST", "/api/devices/dial-out", map[string]any{})
	er := e.expectError(resp, data, http.StatusBadGateway, "pairing.failed")
	if len(er.Error.Details) != 1 || er.Error.Details["reason"] != agentsrv.ReasonPinMismatch {
		t.Fatalf("details = %v", er.Error.Details)
	}

	e.pairer.set(model.Device{}, &agentsrv.PairingError{Reason: agentsrv.ReasonTimeout, DeviceID: "dev1"})
	resp, data = e.do(c, "POST", "/api/devices/dial-out", map[string]any{})
	er = e.expectError(resp, data, http.StatusBadGateway, "pairing.failed")
	if er.Error.Details["reason"] != agentsrv.ReasonTimeout || er.Error.Details["device_id"] != "dev1" {
		t.Fatalf("details = %v", er.Error.Details)
	}

	e.pairer.set(model.Device{}, &agentsrv.PairingError{
		Reason: agentsrv.ReasonIncompatible, DeviceID: "dev2", RequiredMin: 3, HubVersion: "0.2.0",
		Err: &agentsrv.IncompatibleError{Reason: agent.ReasonVersionTooOld, RequiredMin: 3},
	})
	resp, data = e.do(c, "POST", "/api/devices/dial-out", map[string]any{})
	er = e.expectError(resp, data, http.StatusBadGateway, "pairing.failed")
	d := er.Error.Details
	if d["reason"] != agentsrv.ReasonIncompatible || d["device_id"] != "dev2" || d["required_min"] != float64(3) || d["hub_version"] != "0.2.0" {
		t.Fatalf("details = %v", d)
	}
}

func TestDialOut关停与内部错误(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	e.pairer.set(model.Device{}, agentsrv.ErrDialOutClosed)
	resp, data := e.do(c, "POST", "/api/devices/dial-out", map[string]any{})
	e.expectError(resp, data, http.StatusServiceUnavailable, "server.shutting_down")

	e.pairer.set(model.Device{}, fmt.Errorf("拨出配对: %w", agentlink.ErrDialConfig))
	resp, data = e.do(c, "POST", "/api/devices/dial-out", map[string]any{})
	e.expectError(resp, data, http.StatusInternalServerError, "internal")
}
