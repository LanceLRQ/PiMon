package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/netaddr"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// DeviceViewer 提供带在线状态、连接错误与时钟偏移的设备视图（devices.Viewer）；设备不存在返回 devices.ErrNotFound。
type DeviceViewer interface {
	List(ctx context.Context) ([]model.Device, error)
	View(ctx context.Context, id string) (model.Device, error)
}

// DeviceSessions 查询设备当前会话的结束信号（agentsrv.Server）；没有会话时 ok 为假。
type DeviceSessions interface {
	SessionDone(deviceID string) (done <-chan struct{}, ok bool)
}

// AgentChannel 是 agent 通道的接入信息。Addr 为实际监听地址（host:port），通道未启用时为空。
type AgentChannel struct {
	Addr        string
	Fingerprint string
	HubVersion  string
	// Platforms 是中枢内嵌的 agent 平台，没有内嵌时为空。
	Platforms []string
}

// port 返回监听端口，通道未启用或地址不合法时为空串。
func (c AgentChannel) port() string {
	_, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return ""
	}
	return port
}

// revokeWait 是吊销时等待会话断开的上限（真实时间）。会话由 Close 带断开原因尽快关闭，
// 超时只会在对端卡死时出现，此时设备行已删除，不应让请求挂住。
var revokeWait = 10 * time.Second

// maxDeviceNameRunes 与设备仓库的名称上限一致。
const maxDeviceNameRunes = 64

func (s *server) registerDeviceManagement(mux *http.ServeMux) {
	if s.Devices != nil && s.DeviceViews != nil {
		mux.HandleFunc("GET /api/devices", s.admin(s.listDevices))
		mux.HandleFunc("GET /api/devices/{id}", s.admin(s.getDevice))
		mux.HandleFunc("PUT /api/devices/{id}", s.admin(s.updateDevice))
		mux.HandleFunc("DELETE /api/devices/{id}", s.admin(s.revokeDevice))
		mux.HandleFunc("GET /api/devices/{id}/impact", s.admin(s.deviceImpact))
		mux.HandleFunc("POST /api/devices/{id}/repair", s.admin(s.repairDevice))
		if s.AgentInfo != nil {
			mux.HandleFunc("POST /api/devices/pairing", s.admin(s.createPairing))
		}
	}
	if s.AgentInfo != nil {
		mux.HandleFunc("GET /api/agent/info", s.admin(s.agentInfo))
	}
}

// writeDeviceLookupError 把设备仓库错误映射为响应：不存在 404，校验失败 400，其余 500。
func writeDeviceLookupError(w http.ResponseWriter, r *http.Request, err error) {
	var fe model.FieldErrors
	switch {
	case errors.Is(err, devices.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeDeviceNotFound, nil)
	case errors.As(err, &fe):
		httpx.WriteValidationFailed(w, fe)
	default:
		internalError(w, r, err)
	}
}

func (s *server) listDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.DeviceViews.List(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// getDevice 返回设备详情。中枢本机不是设备行（id 为 hub 时同样 404），其信息见 /api/agent/info 与 /api/system。
func (s *server) getDevice(w http.ResponseWriter, r *http.Request) {
	dev, err := s.DeviceViews.View(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDeviceLookupError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, dev)
}

// writeConflictingInstances 写出「违规实例」的 proxy.location_mismatch，details.instances 列出实例。
func writeConflictingInstances(w http.ResponseWriter, list []model.InstanceRef) {
	httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeProxyLocationMismatch, map[string]any{"instances": list})
}

// updateDevice 更新名称、角色、允许离线、维护模式；拨出设备另可改地址（规范化后入库）与代理。
// 改动经设备仓库的变更回调扇出到拨出管理器（按新地址与代理重连）、实例展示与 WS，这里不重复通知。
func (s *server) updateDevice(w http.ResponseWriter, r *http.Request) {
	var in model.DeviceInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	ctx, id := r.Context(), r.PathValue("id")
	cur, err := s.Devices.Get(ctx, id)
	if err != nil {
		writeDeviceLookupError(w, r, err)
		return
	}
	if cur.Direction == model.DeviceDialOut {
		fe := s.checkDialOutFields(ctx, &in)
		if len(fe) > 0 {
			httpx.WriteValidationFailed(w, fe)
			return
		}
	}
	// VPS 上的实例只能用 any 代理：改角色前检查，否则规则会被绕过。
	if in.Role == model.DeviceRoleVPS && cur.Role != model.DeviceRoleVPS && s.Instances != nil {
		conflicts, err := s.Instances.RoleProxyConflicts(ctx, id, in.Role)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if len(conflicts) > 0 {
			writeConflictingInstances(w, conflicts)
			return
		}
	}
	if _, err := s.Devices.Update(ctx, id, in); err != nil {
		writeDeviceLookupError(w, r, err)
		return
	}
	dev, err := s.DeviceViews.View(ctx, id)
	if err != nil {
		writeDeviceLookupError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, dev)
}

// checkDialOutFields 校验并规范化拨出设备可改的地址与代理（原地改写 in.Address），返回字段错误。
func (s *server) checkDialOutFields(ctx context.Context, in *model.DeviceInput) model.FieldErrors {
	fe := model.FieldErrors{}
	if in.Address != nil {
		addr := strings.TrimSpace(*in.Address)
		if addr == "" {
			fe["address"] = model.FieldRequired
		} else if u, err := agentlink.NormalizeDialURL(addr); err != nil {
			fe["address"] = model.FieldInvalid
		} else {
			in.Address = &u
		}
	}
	if in.ProxyID != nil && s.Proxies != nil {
		if _, err := s.Proxies.Resolve(ctx, *in.ProxyID); errors.Is(err, proxies.ErrNotFound) {
			fe["proxy_id"] = model.FieldInvalid
		}
	}
	return fe
}

// revokeDevice 吊销设备：删除设备行，经变更回调断开会话并清除在线状态、插件缓存、停止拨出重连。
// 先取会话的结束信号，删除后等它关闭，保证返回时会话已断开。
func (s *server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	var done <-chan struct{}
	var hasSession bool
	if s.DeviceSessions != nil {
		done, hasSession = s.DeviceSessions.SessionDone(id)
	}
	if err := s.Devices.Delete(ctx, id); err != nil {
		writeDeviceLookupError(w, r, err)
		return
	}
	if hasSession {
		timeout := time.NewTimer(revokeWait)
		defer timeout.Stop()
		select {
		case <-done:
		case <-timeout.C:
			slog.Warn("吊销设备后会话迟迟未断开，不再等待", "device_id", id)
		case <-ctx.Done():
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// deviceImpact 列出吊销或重新配对前受影响的实例、引用这些实例的 screen（按 ID 去重）与告警规则（M4 前为空）。
func (s *server) deviceImpact(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	if _, err := s.Devices.Get(ctx, id); err != nil {
		writeDeviceLookupError(w, r, err)
		return
	}
	imp := model.DeviceImpact{Instances: []model.InstanceRef{}, Screens: []model.ScreenRef{}, Rules: []model.RuleRef{}}
	insts, err := s.Instances.ListByMachine(ctx, id)
	if err != nil {
		internalError(w, r, err)
		return
	}
	seen := map[string]bool{}
	for _, in := range insts {
		imp.Instances = append(imp.Instances, model.InstanceRef{ID: in.ID, Name: in.Name})
		refs, err := s.Instances.AffectedScreens(ctx, in.ID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		for _, sc := range refs {
			if !seen[sc.ID] {
				seen[sc.ID] = true
				imp.Screens = append(imp.Screens, sc)
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, imp)
}

// createPairing 生成拨入配对码，返回地址列表、指纹与安装命令。
func (s *server) createPairing(w http.ResponseWriter, r *http.Request) {
	var in model.PairingRequest
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			httpx.WriteInvalidJSON(w)
			return
		}
	}
	in.Name = strings.TrimSpace(in.Name)
	fe := model.FieldErrors{}
	if utf8.RuneCountInString(in.Name) > maxDeviceNameRunes {
		fe["name"] = model.FieldOutOfRange
	}
	if in.Role != "" && !devices.ValidRole(in.Role) {
		fe["role"] = model.FieldInvalid
	}
	if len(fe) > 0 {
		httpx.WriteValidationFailed(w, fe)
		return
	}
	s.issuePairingCode(w, r, devices.PairingOptions{
		Name: in.Name, Role: in.Role, AllowOffline: in.AllowOffline, AutoHostMetrics: in.AutoHostMetrics,
	})
}

// issuePairingCode 按预填选项生成配对码并写出结果（201）；agent 通道未启用返回 409 agent.disabled。
func (s *server) issuePairingCode(w http.ResponseWriter, r *http.Request, opts devices.PairingOptions) {
	ch := s.AgentInfo()
	if ch.port() == "" {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeAgentDisabled, nil)
		return
	}
	code, exp, err := s.Devices.NewPairingCode(r.Context(), opts)
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, s.pairingResult(r, ch, code, exp))
}

// pairingResult 组装配对结果：agent 通道地址（对外访问地址的主机名优先，其后是本机局域网 IPv4，
// 都没有时退回请求里的主机名）、中枢指纹、一行安装命令与分步命令。
func (s *server) pairingResult(r *http.Request, ch AgentChannel, code string, exp time.Time) model.PairingResult {
	urls := netaddr.AgentURLs(s.Settings.Get().AccessURL, ch.port(), s.localIPs())
	if len(urls) == 0 {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		urls = []string{"wss://" + net.JoinHostPort(host, ch.port())}
	}
	web := "http://" + r.Host
	if cand := s.setupURLs(r); len(cand) > 0 {
		web = cand[0]
	}
	hub := urls[0]
	install := fmt.Sprintf("--hub %s --fp %s --code %s", hub, ch.Fingerprint, code)
	return model.PairingResult{
		Code: code, ExpiresAt: exp, URLs: urls, Fingerprint: ch.Fingerprint,
		Command: fmt.Sprintf("curl -fsSL %s/agent/install.sh | sh -s -- %s", web, install),
		Steps: []string{
			`PLATFORM=$(uname -s | tr 'A-Z' 'a-z')-$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')`,
			fmt.Sprintf(`curl -fsSLo pimon-agent %s/agent/download/$PLATFORM && chmod +x pimon-agent`, web),
			fmt.Sprintf(`echo "$(curl -fsSL %s/agent/checksums | awk -v f="pimon-agent-$PLATFORM" '$2==f{print $1}')  pimon-agent" | sha256sum -c -`, web),
			"sudo ./pimon-agent install " + install,
		},
	}
}

// repairDevice 重新配对：拨入设备生成绑定该设备的新配对码（预填旧设备的名称、角色、允许离线）；
// 拨出设备按请求里的新地址、指纹与配对码重新配对，缺省的字段沿用旧设备。
// 新设备首次 hello 被接受时，旧设备的实例、manifest 与维护模式迁到新设备。
func (s *server) repairDevice(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	dev, err := s.Devices.Get(ctx, id)
	if err != nil {
		writeDeviceLookupError(w, r, err)
		return
	}
	if dev.Direction == model.DeviceDialIn {
		if s.AgentInfo == nil {
			httpx.WriteError(w, http.StatusConflict, httpx.CodeAgentDisabled, nil)
			return
		}
		noAuto := false
		allow := dev.AllowOffline
		s.issuePairingCode(w, r, devices.PairingOptions{
			Name: dev.Name, Role: dev.Role, AllowOffline: &allow, AutoHostMetrics: &noAuto, RepairDeviceID: id,
		})
		return
	}
	var req model.DialOutRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	det, err := s.Devices.Detail(ctx, id)
	if err != nil {
		writeDeviceLookupError(w, r, err)
		return
	}
	prefillRepair(&req, dev, det)
	if s.DialOut == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeShuttingDown, nil)
		return
	}
	paired, err := s.DialOut.Repair(ctx, id, req)
	s.writePairResult(w, r, paired, err)
}

// prefillRepair 用旧设备的属性补全重新配对请求里没给的字段。代理为空表示沿用旧设备的代理；要改回直连填 "direct"。
func prefillRepair(req *model.DialOutRequest, dev model.Device, det devices.Detail) {
	if strings.TrimSpace(req.Name) == "" {
		req.Name = dev.Name
	}
	if strings.TrimSpace(req.Role) == "" {
		req.Role = dev.Role
	}
	if req.AllowOffline == nil {
		allow := dev.AllowOffline
		req.AllowOffline = &allow
	}
	if strings.TrimSpace(req.ProxyID) == "" {
		req.ProxyID = dev.ProxyID
	}
	if strings.TrimSpace(req.Address) == "" {
		req.Address = dev.Address
	}
	if strings.TrimSpace(req.Fingerprint) == "" {
		req.Fingerprint = det.Pin
	}
	if req.AutoHostMetrics == nil {
		no := false
		req.AutoHostMetrics = &no
	}
}

// agentInfo 返回 agent 通道的接入信息。
func (s *server) agentInfo(w http.ResponseWriter, _ *http.Request) {
	ch := s.AgentInfo()
	port, _ := strconv.Atoi(ch.port())
	platforms := ch.Platforms
	if platforms == nil {
		platforms = []string{}
	}
	httpx.WriteJSON(w, http.StatusOK, model.AgentInfo{
		AgentPort: port, Fingerprint: ch.Fingerprint, HubVersion: ch.HubVersion,
		ProtocolVersion: agent.ProtocolVersion, MinCompatibleVersion: agent.MinCompatibleVersion, Platforms: platforms,
	})
}
