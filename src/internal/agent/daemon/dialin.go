package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// 状态里的停止原因文案。
const (
	msgRevokedDialIn  = "已被吊销，需要重新配对（pimon-agent install --code …）"
	msgRevokedDialOut = "已被吊销，需要在本机执行 pimon-agent pair 重新配对"
	msgBadCredential  = "凭据无效（可能已吊销或 hub 数据被重置），需要重新配对"
	msgNoCredential   = "配置中没有凭据，需要配对（pimon-agent install --code …）"
	msgUnpaired       = "尚未与 hub 配对，请在本机执行 pimon-agent pair"
)

// runDialIn 用凭据模式的重连循环连接 hub（钉扎 SPKI，直拨失败时按 hub 主机名查 mDNS），直到 ctx 结束
// 或重连循环判定停止。返回 true 表示已停止（状态已记录），false 表示 ctx 结束。
func (d *Daemon) runDialIn(ctx context.Context, conf config.Config) bool {
	pin, err := certs.ParseSPKI(conf.HubPin)
	if conf.HubURL == "" || err != nil {
		d.setStopped(StateConfigError, "配置错误：缺少 hub 地址或 hub 指纹无效")
		return true
	}
	if conf.Credential == "" {
		d.setStopped(StateConfigError, msgNoCredential)
		return true
	}
	name := link.MDNSName(conf.HubHostname)
	rc := agentlink.NewReconnector(agentlink.ReconnectorConfig{
		Dialer: agentlink.DialerConfig{
			URL: conf.HubURL, Pin: pin, Role: agent.RoleAgent, Credential: string(conf.Credential),
			Fallback: func(ctx context.Context) ([]netip.Addr, error) { return d.cfg.MDNS(ctx, name) },
			Clock:    d.clk, Log: d.log, Handler: d.handlerFor,
		},
		// Policy 与 OnSession 在重连协程里同步调用：只记录状态、发 hello，不调用 Stop。
		Policy: func(err error, reason *agentlink.CloseReason) agentlink.Decision {
			if err != nil {
				d.setLastErr(describeDialErr(err))
			}
			return agentlink.DefaultPolicy(err, reason)
		},
		OnSession: d.onSession,
		Clock:     d.clk, Rand: d.cfg.Rand, Log: d.log,
	})
	err = rc.Run(ctx)
	var se *agentlink.StopError
	if !errors.As(err, &se) {
		return false
	}
	d.dialInStopped(se, string(conf.Credential))
	return true
}

// dialInStopped 处理重连循环的停止：对端 bye{revoked}（或 auth_fail{revoked}）清空凭据并落盘；
// auth_fail{bad_credential} 只显示状态、不删凭据；配置错误显示原因。
func (d *Daemon) dialInStopped(se *agentlink.StopError, cred string) {
	var af *agentlink.AuthFailError
	if !errors.As(se.Err, &af) {
		af = &agentlink.AuthFailError{}
	}
	revoked := se.Close != nil && se.Close.Kind == agentlink.ClosePeerBye && se.Close.Bye == agent.ReasonRevoked
	switch {
	case revoked || af.Reason == agent.ReasonRevoked:
		d.log.Warn("agent 已被 hub 吊销，停止重连并清除凭据")
		err := d.updateConfig(func(c *config.Config) bool {
			if c.Mode != config.ModeDialIn || string(c.Credential) != cred {
				return false
			}
			c.Credential = ""
			return true
		})
		if err != nil {
			d.log.Error("清除已吊销的凭据失败", "err", err)
		}
		d.setStopped(StateRevoked, msgRevokedDialIn) // 凭据清除之后才显示，状态为已吊销时磁盘上已无凭据
	case af.Reason == agent.ReasonBadCredential:
		d.setStopped(StateBadCredential, msgBadCredential)
		d.log.Warn("hub 拒绝了凭据，停止重连", "reason", af.Reason)
	case errors.Is(se.Err, agentlink.ErrDialConfig):
		d.setStopped(StateConfigError, "配置错误："+se.Err.Error())
		d.log.Error("拨号配置不合法，停止重连", "err", se.Err)
	default:
		d.setStopped(StateIdle, se.Error())
		d.log.Warn("agent 通道停止重连", "err", se)
	}
}

// describeDialErr 把拨号失败转成状态里的最近错误（不含凭据）。
func describeDialErr(err error) string {
	var pin *certs.PinMismatchError
	var af *agentlink.AuthFailError
	var de *agentlink.DialError
	switch {
	case errors.As(err, &pin):
		return "hub 证书指纹与钉扎不符（hub 证书可能已重建，需要 pimon-agent repin）"
	case errors.As(err, &af):
		return "hub 拒绝认证：" + af.Reason
	case errors.As(err, &de):
		return "连不上 hub：" + de.Error()
	}
	return fmt.Sprintf("连接 hub 失败：%v", err)
}
