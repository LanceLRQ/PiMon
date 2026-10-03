package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	goruntime "runtime"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

// 守护进程状态（Status.State）。
const (
	// StateRunning：连接循环在运行（拨入在连接或重连，拨出在监听）；是否已连上看 Connected。
	StateRunning = "running"
	// StateRevoked：已被 hub 吊销，需要重新配对。
	StateRevoked = "revoked"
	// StateBadCredential：hub 拒绝凭据（auth_fail{bad_credential}），凭据保留，需要重新配对。
	StateBadCredential = "bad_credential"
	// StateConfigError：配置缺失或不合法，等修正后 reload。
	StateConfigError = "config_error"
	// StateIncompatible：hub 判定协议版本不兼容，按最大退避等待升级。
	StateIncompatible = "incompatible"
	// StateIdle：尚未运行，或因其他原因停止。
	StateIdle = "idle"
)

// UpgradeInfoTimeout 是向 hub 请求升级信息的超时（走会话的 Clock）。
const UpgradeInfoTimeout = 10 * time.Second

// Incompatible、Status 是控制套接字协议里的类型（定义在 ctl，这里是别名，供守护进程与测试直接使用）。
type (
	Incompatible = ctl.Incompatible
	Status       = ctl.Status
)

// Status 返回状态快照。
func (d *Daemon) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := Status{
		Mode: d.conf.Mode, LastError: d.lastErr, State: d.state, Connected: d.cur != nil, Since: d.since,
		Version: version.Version, ProtocolVersion: agent.ProtocolVersion, MinCompatibleVersion: agent.MinCompatibleVersion,
	}
	switch d.conf.Mode {
	case config.ModeDialIn:
		st.HubURL, st.Pin = d.conf.HubURL, d.conf.HubPin
	case config.ModeDialOut:
		st.Listen = d.conf.Listen
		if d.listen != nil {
			st.Listen = d.listen.addr
		}
	}
	if d.incompat != nil {
		inc := *d.incompat
		st.Incompatible = &inc
	}
	if d.cur != nil {
		st.Instances = d.cur.instances
	}
	if w := d.window; w != nil && d.clk.Now().Before(w.expires) {
		st.PairWindowExpiresAt = w.expires
	}
	return st
}

// Repin 在拨入模式下把 hub 指纹更新为 pin（hub 证书重建后），落盘后按新配置重连。
func (d *Daemon) Repin(ctx context.Context, pin certs.SPKI) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if pin == (certs.SPKI{}) {
		return errors.New("指纹不能为空")
	}
	// 模式以磁盘配置为准（updateConfig 内检查）：守护进程刚启动、连接循环尚未读配置时也能 repin。
	notDialIn := false
	err := d.updateConfig(func(c *config.Config) bool {
		if c.Mode != config.ModeDialIn {
			notDialIn = true
			return false
		}
		c.HubPin = pin.String()
		return true
	})
	switch {
	case err != nil:
		return err
	case notDialIn:
		return ErrNotDialIn
	}
	d.log.Info("已更新 hub 指纹，按新指纹重连", "pin", pin.String())
	d.requestRestart()
	return nil
}

// Reload 重新扫描插件目录（有就绪会话时随之发 plugins_changed），重新读取配置：
// 配置与当前不同、或连接已停止（吊销、凭据失效、配置错误）时按新配置重建连接；配置不合法时返回错误，
// 当前连接不受影响。
func (d *Daemon) Reload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.reg.Scan()
	c, err := config.Load(d.cfg.Paths.Config, euid())
	if err != nil {
		d.mu.Lock()
		idle := d.runCancel == nil
		d.mu.Unlock()
		if idle {
			d.setLastErr("配置错误：" + err.Error())
		}
		return fmt.Errorf("重新读取配置: %w", err)
	}
	d.mu.Lock()
	changed := c != d.conf
	idle := d.runCancel == nil
	d.mu.Unlock()
	if changed || idle {
		d.log.Info("重新加载配置，重建连接", "changed", changed)
		d.requestRestart()
	}
	return nil
}

// UpgradeInfo 是 hub 经 agent 通道告知的升级信息（ctl 协议类型的别名，C7 用它下载并校验）。
type UpgradeInfo = ctl.UpgradeInfo

// UpgradeInfo 经当前会话向 hub 请求升级信息；没有就绪会话返回 ErrNotConnected。
func (d *Daemon) UpgradeInfo(ctx context.Context) (UpgradeInfo, error) {
	d.mu.Lock()
	c := d.cur
	d.mu.Unlock()
	if c == nil {
		return UpgradeInfo{}, ErrNotConnected
	}
	resp, err := c.s.Request(ctx, &agent.UpgradeInfoGet{}, UpgradeInfoTimeout)
	if err != nil {
		return UpgradeInfo{}, fmt.Errorf("请求升级信息: %w", err)
	}
	ui, ok := resp.(*agent.UpgradeInfo)
	if !ok {
		return UpgradeInfo{}, fmt.Errorf("hub 的升级信息应答类型不符：%s", resp.MsgType())
	}
	host := c.s.Peer().RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return UpgradeInfo{
		HubVersion: ui.HubVersion, WebPort: ui.WebPort,
		SHA256: ui.Agents[goruntime.GOOS+"-"+goruntime.GOARCH], HubHost: host,
	}, nil
}
