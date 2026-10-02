package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/LanceLRQ/PiMon/src/internal/hub/agentsrv"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// DialOutPairer 是拨出配对服务（agentsrv.DialOut）：成功返回设备；请求不合法返回 model.FieldErrors；
// 配对失败返回 *agentsrv.PairingError。
type DialOutPairer interface {
	Pair(ctx context.Context, req model.DialOutRequest) (model.Device, error)
}

// DeviceLogger 经 agent 通道拉取设备日志（agentsrv.Dispatcher）：limit ≤ agentsrv.MaxLogLimit，minLevel 为空不过滤；
// 设备不在线返回 instances.ErrDeviceOffline，不兼容返回 *instances.DeviceIncompatibleError，
// 等待超时返回 runtime.ErrTimeout。
type DeviceLogger interface {
	Logs(ctx context.Context, deviceID string, limit int, minLevel string) ([]model.LogEntry, error)
}

func (s *server) registerDevices(mux *http.ServeMux) {
	if s.DialOut != nil {
		mux.HandleFunc("POST /api/devices/dial-out", s.admin(s.dialOut))
	}
	if s.DeviceLogs != nil {
		mux.HandleFunc("GET /api/devices/{id}/logs", s.admin(s.getDeviceLogs))
	}
}

// getDeviceLogs 经通道拉取设备最近的日志（?limit= 缺省 200、上限 2000；?level= 同中枢日志接口）。
func (s *server) getDeviceLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fe := model.FieldErrors{}
	level := q.Get("level")
	if _, ok := parseLogLevel(level); !ok {
		fe["level"] = model.FieldInvalid
	}
	limit := defaultLogLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fe["limit"] = model.FieldInvalid
		} else {
			limit = min(n, agentsrv.MaxLogLimit)
		}
	}
	if len(fe) > 0 {
		httpx.WriteValidationFailed(w, fe)
		return
	}
	id := r.PathValue("id")
	if s.Devices == nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeDeviceNotFound, nil)
		return
	}
	if _, err := s.Devices.Get(r.Context(), id); err != nil {
		if errors.Is(err, devices.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, httpx.CodeDeviceNotFound, nil)
			return
		}
		internalError(w, r, err)
		return
	}
	entries, err := s.DeviceLogs.Logs(r.Context(), id, limit, level)
	if err != nil {
		if !writeDeviceError(w, err) {
			internalError(w, r, err)
		}
		return
	}
	if entries == nil {
		entries = []model.LogEntry{}
	}
	httpx.WriteJSON(w, http.StatusOK, model.LogList{Entries: entries, Capacity: agentsrv.MaxLogLimit})
}

// writeDeviceError 写出经通道执行时的设备与上游错误（离线、不兼容、超时、失败），已处理返回真。
func writeDeviceError(w http.ResponseWriter, err error) bool {
	var ie *instances.DeviceIncompatibleError
	switch {
	case errors.As(err, &ie):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeDeviceIncompatible,
			map[string]any{"reason": ie.Reason, "required_min": ie.RequiredMin, "hub_version": ie.HubVersion})
	case errors.Is(err, instances.ErrDeviceOffline):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeDeviceOffline, nil)
	case errors.Is(err, runtime.ErrTimeout):
		httpx.WriteError(w, http.StatusGatewayTimeout, httpx.CodeRunTimeout, map[string]any{"message": err.Error()})
	case errors.Is(err, runtime.ErrFailed):
		httpx.WriteError(w, http.StatusBadGateway, httpx.CodeRunFailed, map[string]any{"message": err.Error()})
	default:
		return false
	}
	return true
}

// dialOut 同步执行拨出配对（最长 agentsrv.PairTimeout），成功回 201 与设备。
// 客户端断开不中止配对（由 Pair 自己忽略 ctx 的取消），凭据照常入库。
func (s *server) dialOut(w http.ResponseWriter, r *http.Request) {
	var in model.DialOutRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	dev, err := s.DialOut.Pair(r.Context(), in)
	var fe model.FieldErrors
	var pe *agentsrv.PairingError
	switch {
	case err == nil:
		httpx.WriteJSON(w, http.StatusCreated, dev)
	case errors.As(err, &fe):
		httpx.WriteValidationFailed(w, fe)
	case errors.As(err, &pe):
		details := map[string]any{"reason": pe.Reason}
		if pe.DeviceID != "" {
			details["device_id"] = pe.DeviceID
		}
		if pe.Reason == agentsrv.ReasonIncompatible {
			details["required_min"] = pe.RequiredMin
			details["hub_version"] = pe.HubVersion
		}
		httpx.WriteError(w, http.StatusBadGateway, httpx.CodePairingFailed, details)
	case errors.Is(err, agentsrv.ErrDialOutClosed):
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeShuttingDown, nil)
	default:
		internalError(w, r, err)
	}
}
