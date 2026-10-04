package manifest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// FromJSON 解码 agent 上报的 manifest（Manifest 的 JSON 序列化形式）并校验：id、version 格式，
// api_version 为 SupportedAPIVersion，kind、runtime 取值，runs_on 只含 hub/agent 且包含 agent，
// interval 不低于 MinAllowedInterval，timeout 为正，min_interval 不大于 interval；
// 并预编译 config_schema（含 object_list 子字段）里的全部 pattern，使返回值可被多协程并发校验。
// 失败时返回 *Error（Problems 的 Path 指出字段，Line 为 0）。
func FromJSON(raw []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, &Error{Problems: []Problem{{Message: fmt.Sprintf("不是合法的 manifest JSON: %v", err)}}}
	}
	var probs []Problem
	add := func(path, format string, args ...any) {
		probs = append(probs, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if !idPattern.MatchString(m.ID) {
		add("id", "id %q 不合法", clip(m.ID))
	}
	if !versionPattern.MatchString(m.Version) {
		add("version", "version %q 不是 x.y.z 形式", clip(m.Version))
	}
	if m.APIVersion != SupportedAPIVersion {
		add("api_version", "不支持 api_version %d，当前支持 %d", m.APIVersion, SupportedAPIVersion)
	}
	if m.Kind != KindSource && m.Kind != KindNotifier {
		add("kind", "kind %q 不合法", clip(string(m.Kind)))
	}
	if m.Runtime != RuntimeBuiltin && m.Runtime != RuntimeExec {
		add("runtime", "runtime %q 不合法", clip(string(m.Runtime)))
	}
	hasAgent := false
	for _, r := range m.RunsOn {
		switch r {
		case RunsOnAgent:
			hasAgent = true
		case RunsOnHub:
		default:
			add("runs_on", "runs_on 取值 %q 不合法", clip(r))
		}
	}
	if !hasAgent {
		add("runs_on", "runs_on 必须包含 agent")
	}
	if m.Interval < MinAllowedInterval {
		add("interval", "interval（%s）不能低于全局下限 %s", m.Interval, MinAllowedInterval)
	}
	if m.Timeout <= 0 {
		add("timeout", "timeout 必须为正")
	}
	if m.MinInterval < 0 || m.MinInterval > m.Interval {
		add("min_interval", "min_interval（%s）必须在 0 到 interval（%s）之间", m.MinInterval, m.Interval)
	}
	if err := schema.Compile(m.ConfigSchema); err != nil {
		add("config_schema", "%v", err)
	}
	if len(probs) > 0 {
		return nil, &Error{Problems: probs}
	}
	return &m, nil
}

// clip 截断写进错误文本的对端字符串（64 字节，保持 UTF-8 合法），避免超长值进入日志。
func clip(s string) string {
	const limit = 64
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "") + "…"
}
