package instances

import (
	"context"
	"errors"
	"fmt"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

var (
	// ErrDeviceOffline 表示远程实例所在设备不在线，无法经通道执行（测试运行、拉日志）。
	ErrDeviceOffline = errors.New("设备不在线")
	// ErrDeviceIncompatible 表示设备 agent 与中枢协议不兼容；具体原因见 *DeviceIncompatibleError。
	ErrDeviceIncompatible = errors.New("设备 agent 版本不兼容")
)

// DeviceIncompatibleError 是设备不兼容的详情，可用 errors.Is(err, ErrDeviceIncompatible) 判断。
type DeviceIncompatibleError struct {
	// Reason 取 agent.ReasonVersionTooOld / ReasonVersionTooNew。
	Reason string
	// RequiredMin 是要求对端至少具备的协议版本。
	RequiredMin int
	// HubVersion 是中枢软件版本。
	HubVersion string
}

func (e *DeviceIncompatibleError) Error() string {
	return fmt.Sprintf("设备 agent 版本不兼容：%s（要求至少 %d）", e.Reason, e.RequiredMin)
}

// Is 让 errors.Is(err, ErrDeviceIncompatible) 成立。
func (e *DeviceIncompatibleError) Is(target error) bool { return target == ErrDeviceIncompatible }

// notRecordedError 标记不应写入实例状态的远程运行错误（设备没有给出运行结果），文字与原错误相同。
type notRecordedError struct{ err error }

func (e *notRecordedError) Error() string { return e.err.Error() }
func (e *notRecordedError) Unwrap() error { return e.err }

// NotRecorded 包装 Remote.RunNow 返回的错误，表示设备没有给出运行结果（如中枢等待应答超时、
// 实例尚未下发到设备），Run 原样返回而不计入失败。errors.Is/As 仍能识别 err 本身。
func NotRecorded(err error) error {
	if err == nil {
		return nil
	}
	return &notRecordedError{err: err}
}

// Recordable 判断远程运行的错误是否代表设备给出的运行结果（应写入当前状态）：
// 只有超时与失败两类、且未被 NotRecorded 标记的才算；离线、不兼容、忙、调用方取消都不算。
func Recordable(err error) bool {
	var nr *notRecordedError
	if errors.As(err, &nr) {
		return false
	}
	return errors.Is(err, runtime.ErrTimeout) || errors.Is(err, runtime.ErrFailed)
}

// ApplyRemoteResult 把设备上报的一次运行结果写入当前状态，与本机采集走同一条路径
// （当前值、历史、失败计数、通知订阅者）。contentHash 是结果对应的实例行 config_hash
// （下发时随 RemoteSpec.ContentHash 记录）：与当前不符说明结果对应旧配置或旧运行位置，整份丢弃。
// 报告的插件私有 state 一律清空（agent 实例的 state 只存在 agent 本地）。实例不存在时忽略。
func (s *Service) ApplyRemoteResult(id, contentHash string, rep *report.Report, err error) {
	if rep != nil {
		c := *rep
		c.State = ""
		rep = &c
	}
	s.applyResult(id, contentHash, rep, err)
}

// runRemote 是远程实例的「保存并测试」：经 Remote.RunNow 在设备上运行一次。
// 不拿本机运行锁（设备忙时 agent 返回 busy）；设备给出的成功与失败结果按运行开始时的
// config_hash 入库，离线、不兼容、忙、无结果与调用方取消原样返回、不入库。不触发重新下发。
func (s *Service) runRemote(ctx context.Context, r row) (model.InstanceRunResult, error) {
	if s.remote == nil {
		return model.InstanceRunResult{}, fmt.Errorf("%w: 远程下发器未接入 / remote dispatcher unavailable", runtime.ErrFailed)
	}
	res, iss := s.resolve(r)
	if iss != nil {
		return model.InstanceRunResult{}, fmt.Errorf("%w: %s", runtime.ErrFailed, iss.msg)
	}
	_, proxyFail, err := s.proxyForRun(ctx, r)
	if err != nil {
		return model.InstanceRunResult{}, fmt.Errorf("%w: %s", runtime.ErrFailed, err)
	}
	if proxyFail != nil {
		// 代理已被删除：与本机一致，不运行，直接记一次失败。
		s.applyResult(r.ID, r.ConfigHash, nil, proxyFail)
		return model.InstanceRunResult{}, proxyFail
	}
	timeout := res.plugin.Manifest.Timeout
	if timeout <= 0 {
		timeout = manifest.DefaultTimeout
	}
	rep, err := s.remote.RunNow(ctx, r.Machine, r.ID, timeout)
	if err == nil && rep == nil {
		err = fmt.Errorf("%w: 设备未返回报告 / device returned no report", runtime.ErrFailed)
	}
	if err != nil {
		if Recordable(err) {
			s.ApplyRemoteResult(r.ID, r.ConfigHash, nil, err)
		}
		return model.InstanceRunResult{}, err
	}
	s.ApplyRemoteResult(r.ID, r.ConfigHash, rep, nil)
	return model.InstanceRunResult{Instance: s.toInstance(r), Report: publicReport(rep)}, nil
}
