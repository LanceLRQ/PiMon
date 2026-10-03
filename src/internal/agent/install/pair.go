package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// configure 写入配置；拨入且尚未与该 hub 配对时执行一次性配对，凭据经 Save 落盘。
// 配对在写服务定义、启用服务之前进行：失败时不启用服务，也不留下单元或 plist。
func (r *run) configure(ctx context.Context) error {
	c := r.baseConfig()
	if r.mode == config.ModeDialOut {
		return r.writeConfig(c)
	}
	if !r.needPair {
		detail := "已与该 hub 配对"
		if r.o.Code != "" {
			detail += "，忽略 --code；如需重新配对加 --re-pair"
		}
		r.rep.Skipped("配对", detail)
		return r.writeConfig(c)
	}
	if r.Pair == nil || r.Hello == nil {
		return errors.New("缺少配对实现（Pair 或 Hello 未设置）")
	}
	saved := false
	_, err := r.Pair(ctx, link.PairRequest{
		HubURL: r.o.Hub, Pin: r.pin, Code: r.o.Code, Name: r.o.Name,
		Hello: r.Hello, Clock: r.Clock,
		Save: func(ok agent.PairOK) error {
			pc := c
			pc.Credential, pc.HubHostname = config.Credential(ok.Credential), ok.ListenerInfo.Hostname
			if err := r.writeConfig(pc); err != nil {
				return err
			}
			saved = true
			return nil
		},
	})
	var incompat *link.IncompatibleError
	switch {
	case err == nil:
		r.rep.Done("配对", "hub 已接受本设备")
		return nil
	case saved && errors.Is(err, link.ErrHelloTimeout):
		// 凭据已签发并落盘：尽快启动服务，让守护进程用凭据连上去确认 hub 的占位。
		r.rep.Warn("配对", "凭据已保存，但 hub 未在 10 秒内回应 hello；服务启动后会用凭据重新连接")
		return nil
	case saved && errors.As(err, &incompat):
		return fmt.Errorf("%w；凭据已保存，hub 已记录本设备，请升级 agent 后重新运行 install（会沿用已保存的凭据）", err)
	case saved:
		if rerr := r.rollbackConfig(); rerr != nil {
			return fmt.Errorf("%w；撤销刚保存的凭据也失败了: %v", err, rerr)
		}
		return fmt.Errorf("%w；已撤销刚保存的凭据，请在网页重新生成配对码后重试（若网页上已出现该设备，请先吊销再重试）", err)
	}
	return fmt.Errorf("%w%s", err, pairHint(err))
}

// rollbackConfig 把配置恢复为安装前的内容（安装前没有配置则删除）。
func (r *run) rollbackConfig() error {
	path := r.t.paths.Config
	r.confChanged = false
	if err := r.requireRegular(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) && r.existingRaw == nil {
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if r.existingRaw == nil {
		return r.FS.Remove(path)
	}
	return r.FS.WriteFile(path, r.existingRaw, 0o600, r.confOwn)
}

// pairHint 按拨号阶段的错误类型给出排查提示（凭据未落盘）。错误文本不含配对码。
func pairHint(err error) string {
	var af *agentlink.AuthFailError
	var pm *certs.PinMismatchError
	var de *agentlink.DialError
	switch {
	case errors.As(err, &af) && (af.Reason == agent.ReasonBadCode || af.Reason == agent.ReasonCodeExpired):
		return "：配对码无效、已使用或已过期，请在网页重新生成配对码"
	case errors.As(err, &af) && af.Reason == agent.ReasonRateLimited:
		return "：失败次数过多已被暂时锁定，请 15 分钟后再试"
	case errors.As(err, &af):
		return "：hub 拒绝了配对"
	case errors.As(err, &pm):
		return "：hub 证书指纹与 --fp 不符，请核对网页上显示的指纹"
	case errors.As(err, &de):
		return "：无法连接 hub，请检查地址、端口与网络"
	}
	return ""
}
