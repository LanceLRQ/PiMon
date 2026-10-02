package agent

import (
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
)

// 含敏感信息的消息实现 String、GoString 与 LogValue，格式化（%v %+v %#v）与 slog 输出都脱敏；
// JSON 编码不受影响（线上必须带原文）。

const (
	masked          = "***"
	maskPlaceholder = "MASKPLACEHOLDER"
)

func (m Auth) String() string {
	return fmt.Sprintf("Auth{credential:%s role:%s}", masked, m.Role)
}
func (m Auth) GoString() string { return m.String() }
func (m Auth) LogValue() slog.Value {
	return slog.GroupValue(slog.String("credential", masked), slog.String("role", m.Role))
}

func (m Pair) String() string {
	return fmt.Sprintf("Pair{code:%s role:%s name:%s hostname:%s os:%s arch:%s}", masked, m.Role, m.Name, m.Hostname, m.OS, m.Arch)
}
func (m Pair) GoString() string { return m.String() }
func (m Pair) LogValue() slog.Value {
	return slog.GroupValue(slog.String("pair_code", masked), slog.String("role", m.Role),
		slog.String("name", m.Name), slog.String("hostname", m.Hostname),
		slog.String("os", m.OS), slog.String("arch", m.Arch))
}

func (m PairOK) String() string {
	return fmt.Sprintf("PairOK{credential:%s device_id:%s listener_info:%+v}", masked, m.DeviceID, m.ListenerInfo)
}
func (m PairOK) GoString() string { return m.String() }
func (m PairOK) LogValue() slog.Value {
	return slog.GroupValue(slog.String("credential", masked), slog.String("device_id", m.DeviceID),
		slog.String("listener_hostname", m.ListenerInfo.Hostname), slog.String("listener_version", m.ListenerInfo.Version))
}

// redactProxyURL 隐藏代理 URL 中的密码；无法解析时整体隐藏。
func redactProxyURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return masked
	}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			// url.String 会转义 "*"，先写占位符再整体替换。
			u.User = url.UserPassword(u.User.Username(), maskPlaceholder)
		}
	}
	return strings.Replace(u.String(), maskPlaceholder, masked, 1)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// String 只输出配置键名与密钥键名（值一律不出现），代理 URL 的密码替换为 ***。
func (a AssignedInstance) String() string {
	return fmt.Sprintf("AssignedInstance{id:%s plugin_id:%s config_keys:%v secret_keys:%v proxy_url:%s interval_ms:%d timeout_ms:%d config_hash:%s}",
		a.ID, a.PluginID, sortedKeys(a.Config), sortedKeys(a.Secrets), redactProxyURL(a.ProxyURL), a.IntervalMs, a.TimeoutMs, a.ConfigHash)
}
func (a AssignedInstance) GoString() string { return a.String() }
func (a AssignedInstance) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", a.ID), slog.String("plugin_id", a.PluginID),
		slog.Any("config_keys", sortedKeys(a.Config)), slog.Any("secret_keys", sortedKeys(a.Secrets)),
		slog.String("proxy_url", redactProxyURL(a.ProxyURL)),
		slog.Int64("interval_ms", a.IntervalMs), slog.Int64("timeout_ms", a.TimeoutMs),
		slog.String("config_hash", a.ConfigHash))
}

// LogValue 让 slog 输出 assign 时逐实例脱敏，而不是把结构体按 JSON 展开。
func (m Assign) LogValue() slog.Value {
	attrs := make([]slog.Attr, 0, len(m.Instances)+2)
	attrs = append(attrs, slog.Int64("version", m.Version), slog.Int("instance_count", len(m.Instances)))
	for i, in := range m.Instances {
		attrs = append(attrs, slog.Any(fmt.Sprintf("instance_%d", i), in))
	}
	return slog.GroupValue(attrs...)
}
