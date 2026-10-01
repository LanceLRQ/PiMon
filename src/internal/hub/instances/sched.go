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

// Resync 按库里的实例与当前插件重新对齐调度：插件出现、消失、升级，或代理内容变化后调用。
// 调度器尚未启动或 ctx 已结束时什么也不做。
func (s *Service) Resync(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	rows, err := s.listRows(ctx)
	if err != nil {
		s.log.Error("重新排程时读取实例失败", "err", err)
		return
	}
	alive := make(map[string]bool, len(rows))
	for _, r := range rows {
		alive[r.ID] = true
		s.ensureState(r.ID, r.ConfigHash)
		s.syncRow(ctx, r)
		s.notify(r.ID)
	}
	s.mu.Lock()
	var stale []string
	for id := range s.tasks {
		if !alive[id] {
			stale = append(stale, id)
		}
	}
	for id := range s.states {
		if !alive[id] {
			delete(s.states, id)
		}
	}
	s.mu.Unlock()
	for _, id := range stale {
		s.dropTask(id)
		s.notify(id)
	}
}

// resyncOne 重新读取单个实例并对齐其调度；实例已被删除时什么也不做。
func (s *Service) resyncOne(ctx context.Context, id string) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if r, err := s.getRow(ctx, id); err == nil {
		s.syncRow(ctx, r)
		s.notify(id)
	}
}

// syncRow 让调度器里该实例的任务与其当前配置一致：
// 暂停、配置不完整、插件消失、代理不可用的不调度；其余 Upsert（ConfigHash 不变则不动）。
// 引用的代理已被删除的照常排程，但每次运行直接记为失败，不退回直连（Ruling 49）。
func (s *Service) syncRow(ctx context.Context, r row) {
	s.trackHash(r.ID, r.ConfigHash)
	s.mu.Lock()
	sched, streams := s.sched, s.streams
	s.mu.Unlock()
	if sched == nil {
		return
	}
	s.setSyncIssue(r.ID, nil)
	if r.Paused {
		s.dropTask(r.ID)
		return
	}
	res, iss := s.resolve(r)
	if iss != nil {
		s.dropTask(r.ID)
		return
	}
	pr, proxyFail, err := s.proxyForRun(ctx, r)
	if err != nil {
		s.setSyncIssue(r.ID, &issue{kind: issueBroken, msg: err.Error()})
		s.dropTask(r.ID)
		return
	}
	src := sourceFor(res.plugin)
	interval := effectiveInterval(r, res.plugin.Manifest)
	proxyTag := pr.URL()
	if proxyFail != nil {
		proxyTag = "!removed"
	}
	hash := runtimeHash(r.ConfigHash, res, interval, proxyTag)
	in := s.baseInput(res, pr)
	_, isStream := src.(runtime.Streamer)
	content := r.ConfigHash
	if isStream && proxyFail != nil {
		// Streamer 没有「每次运行」，代理已删除时不启动，只记一次失败。
		s.dropTask(r.ID)
		s.applyResult(r.ID, content, nil, proxyFail)
		return
	}

	s.mu.Lock()
	old, had := s.tasks[r.ID]
	s.mu.Unlock()
	if had && old.stream != isStream {
		s.dropTask(r.ID)
	}
	if st, ok := src.(runtime.Streamer); ok {
		streams.Upsert(runtime.StreamTask{ID: r.ID, ConfigHash: hash, Streamer: st, Input: in})
	} else {
		id := r.ID
		sched.Upsert(runtime.Task{
			ID: id, Interval: interval, ConfigHash: hash,
			Run: func(ctx context.Context) (*report.Report, error) {
				return s.runLocked(ctx, id, content, proxyFail, src, in)
			},
		})
	}
	s.mu.Lock()
	s.tasks[r.ID] = taskInfo{hash: hash, content: content, stream: isStream}
	s.mu.Unlock()
}

// runLocked 是定时采集的一次运行：取实例运行锁，读上次报告与私有 state，采集并在持锁期间写入状态，
// 使下一次运行（定时或手动）一定看到本次写入的 state。被取消（停止、重排、删除）时不写状态。
func (s *Service) runLocked(ctx context.Context, id, content string, proxyFail error, src runtime.Source, in runtime.Input) (*report.Report, error) {
	release, err := s.acquireRun(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	if proxyFail != nil {
		s.applyResult(id, content, nil, proxyFail)
		return nil, proxyFail
	}
	in.Last, in.State = s.lastFor(id)
	rep, err := runtime.CollectWithTimeout(ctx, src, in)
	if err == nil || errors.Is(err, runtime.ErrTimeout) || errors.Is(err, runtime.ErrFailed) {
		s.applyResult(id, content, rep, err)
	}
	return rep, err
}

// dropTask 把实例从调度器或 Streamer 管理器里移除。
func (s *Service) dropTask(id string) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	delete(s.tasks, id)
	sched, streams := s.sched, s.streams
	s.mu.Unlock()
	if !ok {
		return
	}
	if t.stream {
		streams.Remove(id)
	} else {
		sched.Remove(id)
	}
}

func (s *Service) setSyncIssue(id string, iss *issue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if iss == nil {
		delete(s.syncIssues, id)
	} else {
		s.syncIssues[id] = iss
	}
}

func (s *Service) isScheduled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tasks[id]
	return ok
}

func (s *Service) scheduleHash(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasks[id].hash
}

// Run 是「保存并测试」：同步运行一次并返回报告，不经调度器，超时取 manifest 的 timeout。
// 与定时采集共用实例运行锁（Ruling 48）：先等锁，至多等该插件的超时（受 ctx 约束），
// 等不到返回 ErrRunBusy。结果与定时采集一样写入当前状态（失败保留旧值）；
// 运行期间配置内容被改掉时结果对应旧配置，不写入。
// 失败返回的错误可用 errors.Is 判断 runtime.ErrTimeout / runtime.ErrFailed，文字已脱敏；
// 实例无法运行（插件消失、配置不完整、代理不可用或已删除）归为 runtime.ErrFailed；
// 调用方取消时错误原样返回，不计入状态。
func (s *Service) Run(ctx context.Context, id string) (model.InstanceRunResult, error) {
	r, err := s.getRow(ctx, id)
	if err != nil {
		return model.InstanceRunResult{}, err
	}
	res, iss := s.resolve(r)
	if iss != nil {
		return model.InstanceRunResult{}, fmt.Errorf("%w: %s", runtime.ErrFailed, iss.msg)
	}
	pr, proxyFail, err := s.proxyForRun(ctx, r)
	if err != nil {
		return model.InstanceRunResult{}, fmt.Errorf("%w: %s", runtime.ErrFailed, err)
	}
	src := sourceFor(res.plugin)
	wait := src.Manifest().Timeout
	if wait <= 0 {
		wait = manifest.DefaultTimeout
	}
	release, err := s.acquireRunWithin(ctx, id, wait)
	if err != nil {
		return model.InstanceRunResult{}, err
	}
	var (
		rep    *report.Report
		runErr = proxyFail
	)
	if runErr == nil {
		in := s.baseInput(res, pr)
		in.Last, in.State = s.lastFor(id)
		rep, runErr = runtime.CollectWithTimeout(ctx, src, in)
		if runErr != nil && !errors.Is(runErr, runtime.ErrTimeout) && !errors.Is(runErr, runtime.ErrFailed) {
			release()
			return model.InstanceRunResult{}, runErr // 调用方取消
		}
	}
	s.applyResult(id, r.ConfigHash, rep, runErr)
	release()
	s.resyncOne(ctx, id)
	if runErr != nil {
		return model.InstanceRunResult{}, runErr
	}
	inst := s.toInstance(r)
	return model.InstanceRunResult{Instance: inst, Report: publicReport(rep)}, nil
}
