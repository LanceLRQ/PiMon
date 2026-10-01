package instances

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// 名称与刷新间隔的取值范围。
const (
	maxNameRunes       = 100
	minIntervalSeconds = 5
	maxIntervalSeconds = 86400
)

func mergeErrs(dst, src model.FieldErrors) {
	for k, v := range src {
		if _, dup := dst[k]; !dup {
			dst[k] = v
		}
	}
}

func validateName(name string, errs model.FieldErrors) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		errs["name"] = model.FieldRequired
	case utf8.RuneCountInString(name) > maxNameRunes:
		errs["name"] = model.FieldOutOfRange
	}
	return name
}

// validateInterval 校验刷新间隔：0 表示用插件默认值；非 0 须在全局范围内，
// 且不低于插件 manifest 声明的 min_interval（minInterval 为 0 表示未声明）。
func validateInterval(sec int, minInterval time.Duration, errs model.FieldErrors) {
	floor := minIntervalSeconds
	if m := int(math.Ceil(minInterval.Seconds())); m > floor {
		floor = m
	}
	if sec != 0 && (sec < floor || sec > maxIntervalSeconds) {
		errs["interval_seconds"] = model.FieldOutOfRange
	}
}

// prepareConfig 按 schema 校验请求里的配置：密钥缺省、留空、回显标记时取 existing 的原值，
// 然后补默认值、丢弃不可见字段，最后拆成普通配置与密钥。错误按配置路径给出。
func (s *Service) prepareConfig(ctx context.Context, fields []schema.Field, incoming, existing map[string]any, errs model.FieldErrors) (plain, secrets map[string]any, err error) {
	if incoming == nil {
		incoming = map[string]any{}
	}
	merged, kerrs := schema.KeepSecrets(fields, incoming, existing)
	mergeErrs(errs, kerrs)
	cfg, perrs := schema.Prepare(fields, merged)
	mergeErrs(errs, perrs)
	if len(errs) > 0 {
		return nil, nil, nil
	}
	if id := proxyOf(fields, cfg); id != "" && s.proxies != nil {
		if _, rerr := s.proxies.Resolve(ctx, id); rerr != nil {
			if !isProxyNotFound(rerr) {
				return nil, nil, rerr
			}
			errs[proxyKey(fields)] = model.FieldInvalid
			return nil, nil, nil
		}
	}
	plain, secrets = schema.Split(fields, cfg)
	return plain, secrets, nil
}

// Create 创建实例。配置不合法返回 model.FieldErrors，插件不存在返回 ErrPluginNotFound。
func (s *Service) Create(ctx context.Context, in model.InstanceInput) (model.InstanceDetail, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	p, ok := s.reg.Get(in.PluginID)
	if !ok {
		return model.InstanceDetail{}, ErrPluginNotFound
	}
	if p.Manifest.Kind != manifest.KindSource {
		// 通知渠道插件不是数据源，不能建监控实例。
		return model.InstanceDetail{}, model.FieldErrors{"plugin_id": model.FieldInvalid}
	}
	errs := model.FieldErrors{}
	name := validateName(in.Name, errs)
	validateInterval(in.IntervalSeconds, p.Manifest.MinInterval, errs)
	fields := p.Manifest.ConfigSchema
	plain, secrets, err := s.prepareConfig(ctx, fields, in.Config, map[string]any{}, errs)
	if err != nil {
		return model.InstanceDetail{}, err
	}
	if len(errs) > 0 {
		return model.InstanceDetail{}, errs
	}
	enc, err := s.encodeSecrets(secrets)
	if err != nil {
		return model.InstanceDetail{}, err
	}
	id, err := newID()
	if err != nil {
		return model.InstanceDetail{}, err
	}
	now := s.clk.Now()
	r := row{
		ID: id, PluginID: p.ID, Name: name, Config: plain, SecretsEnc: enc,
		IntervalSeconds: in.IntervalSeconds, ProxyID: proxyOf(fields, plain),
		ConfigHash: contentHash(p.ID, plain, enc), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.insertRow(ctx, r); err != nil {
		return model.InstanceDetail{}, err
	}
	s.ensureState(id, r.ConfigHash)
	s.syncRow(ctx, r)
	s.notify(id)
	return s.toDetail(r), nil
}

// Update 更新名称、配置与刷新间隔（暂停状态不变）。密钥字段留空表示保留原值。
// 内容（插件、配置、密钥）变化后旧的当前状态被清空；只改名称或间隔则保留。
func (s *Service) Update(ctx context.Context, id string, in model.InstanceInput) (model.InstanceDetail, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.getRow(ctx, id)
	if err != nil {
		return model.InstanceDetail{}, err
	}
	if in.PluginID != "" && in.PluginID != r.PluginID {
		return model.InstanceDetail{}, model.FieldErrors{"plugin_id": model.FieldInvalid}
	}
	p, existing, iss := s.loadFull(r)
	// 配置损坏或密钥无法解密时没有可保留的旧值：请求体就是完整的新配置。
	refill := iss != nil && iss.refill
	if iss != nil && !refill {
		if _, ok := s.reg.Get(r.PluginID); !ok {
			return model.InstanceDetail{}, ErrPluginNotFound
		}
		return model.InstanceDetail{}, errors.New(iss.msg)
	}
	if refill {
		existing = map[string]any{}
	}
	errs := model.FieldErrors{}
	name := validateName(in.Name, errs)
	validateInterval(in.IntervalSeconds, p.Manifest.MinInterval, errs)
	fields := p.Manifest.ConfigSchema
	plain, secrets, err := s.prepareConfig(ctx, fields, in.Config, existing, errs)
	if err != nil {
		return model.InstanceDetail{}, err
	}
	if len(errs) > 0 {
		return model.InstanceDetail{}, errs
	}
	old := map[string]any{}
	if !refill {
		if old, err = s.decodeSecrets(r.SecretsEnc); err != nil {
			return model.InstanceDetail{}, err
		}
	}
	enc := r.SecretsEnc
	if refill {
		if enc, err = s.encodeSecrets(secrets); err != nil {
			return model.InstanceDetail{}, err
		}
	} else if len(old)+len(secrets) > 0 && !reflect.DeepEqual(old, secrets) {
		if enc, err = s.encodeSecrets(secrets); err != nil {
			return model.InstanceDetail{}, err
		}
	}
	hash := contentHash(r.PluginID, plain, enc)
	changed := refill || hash != r.ConfigHash
	r.Name, r.Config, r.SecretsEnc = name, plain, enc
	r.IntervalSeconds, r.ProxyID, r.ConfigHash = in.IntervalSeconds, proxyOf(fields, plain), hash
	r.UpdatedAt = s.clk.Now()
	if err := s.updateRow(ctx, r); err != nil {
		return model.InstanceDetail{}, err
	}
	// 新配置已完整写回，损坏标记随之清除。
	r.Corrupt = ""
	if changed {
		s.resetState(id, hash)
	}
	s.syncRow(ctx, r)
	s.notify(id)
	return s.toDetail(r), nil
}

// Copy 复制实例：配置原样复制，密钥置空，需要用户重新填写。
func (s *Service) Copy(ctx context.Context, id, name string) (model.InstanceDetail, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	src, err := s.getRow(ctx, id)
	if err != nil {
		return model.InstanceDetail{}, err
	}
	if p, ok := s.reg.Get(src.PluginID); ok && p.Manifest.Kind != manifest.KindSource {
		return model.InstanceDetail{}, model.FieldErrors{"plugin_id": model.FieldInvalid}
	}
	errs := model.FieldErrors{}
	name = validateName(name, errs)
	if len(errs) > 0 {
		return model.InstanceDetail{}, errs
	}
	raw, err := json.Marshal(src.Config)
	if err != nil {
		return model.InstanceDetail{}, err
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return model.InstanceDetail{}, err
	}
	newIDStr, err := newID()
	if err != nil {
		return model.InstanceDetail{}, err
	}
	now := s.clk.Now()
	r := row{
		ID: newIDStr, PluginID: src.PluginID, Name: name, Config: cfg,
		IntervalSeconds: src.IntervalSeconds, ProxyID: src.ProxyID,
		ConfigHash: contentHash(src.PluginID, cfg, ""), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.insertRow(ctx, r); err != nil {
		return model.InstanceDetail{}, err
	}
	s.ensureState(r.ID, r.ConfigHash)
	s.syncRow(ctx, r)
	s.notify(r.ID)
	return s.toDetail(r), nil
}

// AffectedScreens 返回当前布局里引用该实例的 screen，没有引用时为空列表。
// 实例不存在返回 ErrNotFound。
func (s *Service) AffectedScreens(ctx context.Context, id string) ([]model.ScreenRef, error) {
	if _, err := s.getRow(ctx, id); err != nil {
		return nil, err
	}
	return s.screensUsing(ctx, id)
}

func (s *Service) screensUsing(ctx context.Context, id string) ([]model.ScreenRef, error) {
	if s.screens == nil {
		return []model.ScreenRef{}, nil
	}
	return s.screens.ScreensUsing(ctx, id)
}

// Delete 删除实例：移出调度、丢弃当前状态；状态行与历史随外键级联删除。
// 返回值里的 AffectedScreens 是删除时引用它的 screen（这些 screen 上的小组件随后显示引用失效）。
func (s *Service) Delete(ctx context.Context, id string) (model.InstanceDeleteResult, error) {
	affected, err := s.screensUsing(ctx, id)
	if err != nil {
		return model.InstanceDeleteResult{}, err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM plugin_instances WHERE id = ?`, id)
	if err != nil {
		return model.InstanceDeleteResult{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.InstanceDeleteResult{}, ErrNotFound
	}
	s.dropTask(id)
	s.dropState(id)
	s.setSyncIssue(id, nil)
	s.notify(id)
	return model.InstanceDeleteResult{AffectedScreens: affected}, nil
}

// Pause 暂停实例：移出调度器，当前状态与配置保留。
func (s *Service) Pause(ctx context.Context, id string) (model.Instance, error) {
	return s.setPaused(ctx, id, true)
}

// Resume 恢复实例：按当前配置重新加入调度器。
func (s *Service) Resume(ctx context.Context, id string) (model.Instance, error) {
	return s.setPaused(ctx, id, false)
}

func (s *Service) setPaused(ctx context.Context, id string, paused bool) (model.Instance, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err := s.getRow(ctx, id)
	if err != nil {
		return model.Instance{}, err
	}
	if r.Paused != paused {
		r.Paused = paused
		r.UpdatedAt = s.clk.Now()
		if err := s.updateRow(ctx, r); err != nil {
			return model.Instance{}, err
		}
	}
	s.syncRow(ctx, r)
	s.notify(r.ID)
	return s.toInstance(r), nil
}

// Get 返回实例详情（配置已脱敏）。
func (s *Service) Get(ctx context.Context, id string) (model.InstanceDetail, error) {
	r, err := s.getRow(ctx, id)
	if err != nil {
		return model.InstanceDetail{}, err
	}
	return s.toDetail(r), nil
}

// List 按名称排序返回全部实例。
func (s *Service) List(ctx context.Context) ([]model.Instance, error) {
	rows, err := s.listRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Instance, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.toInstance(r))
	}
	return out, nil
}

func isProxyNotFound(err error) bool { return errors.Is(err, proxies.ErrNotFound) }
