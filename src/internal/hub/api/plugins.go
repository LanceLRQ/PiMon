package api

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"path/filepath"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// lookupTimeout 是一次 lookup 候选查询的上限。
const lookupTimeout = 15 * time.Second

func (s *server) registerPlugins(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/plugins", s.admin(s.listPlugins))
	mux.HandleFunc("POST /api/plugins/rescan", s.admin(s.rescanPlugins))
	mux.HandleFunc("POST /api/plugins/{id}/lookup/{key}", s.admin(s.lookupPlugin))
}

// requestLang 取 ?lang=zh|en；缺省或非法时用全局设置的语言。
func (s *server) requestLang(r *http.Request) string {
	if l := r.URL.Query().Get("lang"); l == "zh" || l == "en" {
		return l
	}
	return s.Settings.Get().Language
}

func (s *server) listPlugins(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, pluginList(s.Plugins.Snapshot(), s.requestLang(r)))
}

func (s *server) rescanPlugins(w http.ResponseWriter, r *http.Request) {
	snap, err := s.Plugins.Scan(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, pluginList(snap, s.requestLang(r)))
}

func (s *server) lookupPlugin(w http.ResponseWriter, r *http.Request) {
	id, key := r.PathValue("id"), r.PathValue("key")
	p, ok := s.Plugins.Get(id)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodePluginNotFound, nil)
		return
	}
	var req model.PluginLookupRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	invalid := func() {
		httpx.WriteValidationFailed(w, model.FieldErrors{key: model.FieldInvalid})
	}
	if !hasLookupField(p.Manifest, key) {
		invalid()
		return
	}
	lk, ok := p.Source.(runtime.Lookuper)
	if !ok {
		invalid()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lookupTimeout)
	defer cancel()
	cands, err := runtime.SafeLookup(ctx, lk, key, req.Query, s.requestLang(r))
	if err != nil {
		writeUpstreamError(w, r, err)
		return
	}
	out := model.PluginLookupResponse{Candidates: make([]model.PluginCandidate, 0, len(cands))}
	for _, c := range cands {
		out.Candidates = append(out.Candidates, model.PluginCandidate{Value: c.Value, Label: c.Label})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// writeUpstreamError 把插件访问上游失败映射为 504（超时）或 502（其它失败）。
// 上游错误文字可能带地址，不回给前端，只记日志。
func writeUpstreamError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("插件请求上游失败", "method", r.Method, "path", r.URL.Path, "err", err)
	if errors.Is(err, runtime.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		httpx.WriteError(w, http.StatusGatewayTimeout, httpx.CodeRunTimeout, nil)
		return
	}
	httpx.WriteError(w, http.StatusBadGateway, httpx.CodeRunFailed, nil)
}

func hasLookupField(m *manifest.Manifest, key string) bool {
	for _, f := range m.ConfigSchema {
		if f.Key == key {
			return f.Type == schema.TypeLookup
		}
	}
	return false
}

func pluginList(snap plugins.Snapshot, lang string) model.PluginList {
	out := model.PluginList{
		Plugins:   make([]model.PluginInfo, 0, len(snap.Plugins)),
		Errors:    []model.PluginLoadIssue{},
		Conflicts: []model.PluginLoadIssue{},
	}
	for _, p := range snap.Plugins {
		out.Plugins = append(out.Plugins, pluginInfo(p, lang))
	}
	for _, is := range snap.Issues {
		mi := model.PluginLoadIssue{Dir: filepath.Base(is.Dir), ID: is.ID, Kind: string(is.Kind), Message: is.Message}
		for _, pr := range is.Problems {
			mi.Problems = append(mi.Problems, model.PluginProblem{Line: pr.Line, Path: pr.Path, Message: pr.Message})
		}
		if is.Kind == plugins.IssueConflict {
			out.Conflicts = append(out.Conflicts, mi)
		} else {
			out.Errors = append(out.Errors, mi)
		}
	}
	return out
}

func pluginInfo(p plugins.Plugin, lang string) model.PluginInfo {
	m := p.Manifest
	info := model.PluginInfo{
		ID: m.ID, Version: m.Version, Name: m.Name.Get(lang),
		Kind: string(m.Kind), Runtime: string(m.Runtime), Origin: string(p.Origin),
		RunsOn:          append([]string{}, m.RunsOn...),
		IntervalSeconds: int(m.Interval.Seconds()),
		// 0 表示插件未声明 min_interval（只受全局 5 秒下限约束）。
		MinIntervalSeconds: int(math.Ceil(m.MinInterval.Seconds())),
		TimeoutSeconds:     int(m.Timeout.Seconds()),
		ConfigSchema:       fieldsInfo(m.ConfigSchema, lang),
		Outputs:            make([]model.PluginOutput, 0, len(m.Outputs)),
		Widgets:            make([]model.PluginWidget, 0, len(m.Widgets)),
	}
	for _, o := range m.Outputs {
		info.Outputs = append(info.Outputs, model.PluginOutput{Key: o.Key, Type: o.Type, Title: o.Title.Get(lang)})
	}
	for _, w := range m.Widgets {
		mw := model.PluginWidget{ID: w.ID, Name: w.Name.Get(lang), Sizes: make([]model.PluginWidgetSize, 0, len(w.Sizes))}
		for _, sz := range w.Sizes {
			ms := model.PluginWidgetSize{Size: sz.Size, Cols: sz.Cols, Rows: sz.Rows, Template: sz.Template,
				Bind: make([]model.PluginBinding, 0, len(sz.Bind))}
			for _, b := range sz.Bind {
				mb := model.PluginBinding{Slot: b.Slot, List: b.List, Refs: make([]model.PluginItemRef, 0, len(b.Refs))}
				for _, ref := range b.Refs {
					mb.Refs = append(mb.Refs, model.PluginItemRef{Item: ref.Item, Field: ref.Field})
				}
				ms.Bind = append(ms.Bind, mb)
			}
			mw.Sizes = append(mw.Sizes, ms)
		}
		info.Widgets = append(info.Widgets, mw)
	}
	return info
}

func fieldsInfo(fs []schema.Field, lang string) []model.PluginField {
	out := make([]model.PluginField, 0, len(fs))
	for _, f := range fs {
		mf := model.PluginField{
			Key: f.Key, Type: string(f.Type), Title: f.Title.Get(lang), Help: f.Help.Get(lang),
			Required: f.Required, Default: f.Default, Min: f.Min, Max: f.Max, Pattern: f.Pattern,
			AllowQuery: f.AllowQuery, AllowPublicHTTP: f.AllowPublicHTTP, FollowRedirects: f.FollowRedirects,
			SecretValues: f.SecretValues,
		}
		for _, c := range f.VisibleWhen {
			mf.VisibleWhen = append(mf.VisibleWhen, model.PluginCondition{Key: c.Key, Values: c.Values})
		}
		for _, o := range f.Options {
			mf.Options = append(mf.Options, model.PluginOption{Value: o.Value, Title: o.Title.Get(lang)})
		}
		if len(f.Fields) > 0 {
			mf.Fields = fieldsInfo(f.Fields, lang)
		}
		out = append(out, mf)
	}
	return out
}
