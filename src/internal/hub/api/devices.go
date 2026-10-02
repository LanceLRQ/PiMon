package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/LanceLRQ/PiMon/src/internal/hub/agentsrv"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// DialOutPairer 是拨出配对服务（agentsrv.DialOut）：成功返回设备；请求不合法返回 model.FieldErrors；
// 配对失败返回 *agentsrv.PairingError。
type DialOutPairer interface {
	Pair(ctx context.Context, req model.DialOutRequest) (model.Device, error)
}

func (s *server) registerDevices(mux *http.ServeMux) {
	if s.DialOut != nil {
		mux.HandleFunc("POST /api/devices/dial-out", s.admin(s.dialOut))
	}
}

// dialOut 同步执行拨出配对（最长 agentsrv.PairTimeout），成功回 201 与设备。
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
	default:
		internalError(w, r, err)
	}
}
