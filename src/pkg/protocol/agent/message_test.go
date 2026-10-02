package agent

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

var testTime = time.Date(2026, 10, 2, 8, 30, 0, 0, time.UTC)

func sampleOutcomeOK() RunOutcome {
	return RunOutcome{
		Report:      &report.Report{Status: "ok", Summary: "fine", CollectedAt: 1700000000000, DurationMs: 12},
		DurationMs:  12,
		CollectedAt: testTime,
	}
}

func sampleOutcomeErr() RunOutcome {
	return RunOutcome{Error: "超时", ErrorKind: ErrorKindTimeout, DurationMs: 5000, CollectedAt: testTime}
}

func allSamples() []Message {
	manifest := PluginManifest{Origin: "builtin", Manifest: json.RawMessage(`{"id":"host.cpu"}`)}
	return []Message{
		&Auth{Credential: "cred", Role: RoleAgent},
		&AuthOK{DeviceID: "dev1"},
		&AuthFail{Reason: ReasonBadCredential},
		&Pair{Code: "ABCD", Role: RoleAgent, Name: "nas", Hostname: "nas.local", OS: "linux", Arch: "arm64"},
		&PairOK{Credential: "cred", DeviceID: "dev1", ListenerInfo: ListenerInfo{Hostname: "pimon", Version: "0.1.0"}},
		&Hello{ProtocolVersion: 1, MinCompatibleVersion: 1, AgentVersion: "0.1.0", OS: "linux", Arch: "amd64", Hostname: "h", Plugins: []PluginManifest{manifest}},
		&HelloOK{HubVersion: "0.2.0", ServerTime: testTime},
		&Incompatible{Reason: ReasonVersionTooOld, RequiredMin: 2, HubVersion: "0.2.0"},
		&PluginsChanged{Plugins: []PluginManifest{manifest}},
		&Assign{Version: 3, Instances: []AssignedInstance{{
			ID: "i1", PluginID: "host.cpu", Config: map[string]any{"a": "b"},
			Secrets: map[string]string{"token": "s3cret"}, ProxyURL: "http://u:pw@127.0.0.1:8080",
			IntervalMs: 5000, TimeoutMs: 3000, ConfigHash: "abc",
		}}},
		&AssignResult{Version: 3, Results: []AssignResultItem{{InstanceID: "i1", Status: AssignStarted, Message: "ok"}}},
		&Report{InstanceID: "i1", Seq: 1, RunOutcome: sampleOutcomeOK()},
		&Report{InstanceID: "i1", Seq: 2, RunOutcome: sampleOutcomeErr()},
		&Event{ID: "e1", Kind: "task", Data: json.RawMessage(`{"x":1}`), At: testTime},
		&Ack{IDs: []string{"e1"}},
		&RunNow{RequestID: "r1", InstanceID: "i1"},
		&RunResult{RequestID: "r1", InstanceID: "i1", RunOutcome: sampleOutcomeOK()},
		&LogsGet{RequestID: "r2", Limit: 100, MinLevel: "warn"},
		&Logs{RequestID: "r2", Entries: []model.LogEntry{{Time: testTime, Level: "info", Message: "m", Attrs: "a=b"}}},
		&UpgradeInfoGet{RequestID: "r3"},
		&UpgradeInfo{RequestID: "r3", HubVersion: "0.2.0", WebPort: 31415, Agents: map[string]string{"linux-arm64": "deadbeef"}},
		&Ping{SentAt: testTime},
		&Pong{Echo: testTime, SentAt: testTime.Add(time.Second)},
		&Bye{Reason: ReasonShutdown},
	}
}

func TestEncodeDecode_每种消息往返一致(t *testing.T) {
	seen := map[Type]bool{}
	for _, m := range allSamples() {
		seen[m.MsgType()] = true
		data, err := Encode(m)
		if err != nil {
			t.Fatalf("%s 编码失败: %v", m.MsgType(), err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(data, &top); err != nil {
			t.Fatalf("%s 不是 JSON 对象: %v", m.MsgType(), err)
		}
		if string(top["type"]) != `"`+string(m.MsgType())+`"` {
			t.Fatalf("顶层 type 不对: %s", data)
		}
		got, err := Decode(data)
		if err != nil {
			t.Fatalf("%s 解码失败: %v (%s)", m.MsgType(), err, data)
		}
		if !reflect.DeepEqual(got, m) {
			t.Fatalf("%s 往返不一致:\n got %#v\nwant %#v", m.MsgType(), got, m)
		}
	}
	if len(seen) != 23 {
		t.Fatalf("消息类型数 = %d，应为 23", len(seen))
	}
}

func TestEncode_类型字符串与简报一致(t *testing.T) {
	want := map[Type]string{
		TypeAuth: "auth", TypeAuthOK: "auth_ok", TypeAuthFail: "auth_fail", TypePair: "pair", TypePairOK: "pair_ok",
		TypeHello: "hello", TypeHelloOK: "hello_ok", TypeIncompatible: "incompatible", TypePluginsChanged: "plugins_changed",
		TypeAssign: "assign", TypeAssignResult: "assign_result", TypeReport: "report", TypeEvent: "event", TypeAck: "ack",
		TypeRunNow: "run_now", TypeRunResult: "run_result", TypeLogsGet: "logs_get", TypeLogs: "logs",
		TypeUpgradeInfoGet: "upgrade_info_get", TypeUpgradeInfo: "upgrade_info", TypePing: "ping", TypePong: "pong", TypeBye: "bye",
	}
	for k, v := range want {
		if string(k) != v {
			t.Errorf("%q != %q", k, v)
		}
	}
}

func TestEncode_字段名固定(t *testing.T) {
	data, _ := Encode(&Hello{ProtocolVersion: 1, MinCompatibleVersion: 1, AgentVersion: "v", OS: "linux", Arch: "arm64", Hostname: "h"})
	for _, f := range []string{`"protocol_version":1`, `"min_compatible_version":1`, `"agent_version":"v"`, `"os":"linux"`, `"arch":"arm64"`, `"hostname":"h"`, `"plugins"`} {
		if !strings.Contains(string(data), f) {
			t.Errorf("hello 缺字段 %s: %s", f, data)
		}
	}
	data, _ = Encode(&Pong{Echo: testTime, SentAt: testTime})
	if !strings.Contains(string(data), `"echo"`) || !strings.Contains(string(data), `"sent_at"`) {
		t.Errorf("pong 字段: %s", data)
	}
}

func TestDecode_未知类型返回ErrUnknownType(t *testing.T) {
	_, err := Decode([]byte(`{"type":"nope","x":1}`))
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("err = %v，应为 ErrUnknownType", err)
	}
	var inv *InvalidMessageError
	if errors.As(err, &inv) {
		t.Fatalf("未知类型不应是 InvalidMessageError")
	}
}

func TestDecode_非法字段返回InvalidMessageError(t *testing.T) {
	cases := []struct {
		name, in, typ, field string
	}{
		{"auth 缺凭据", `{"type":"auth","role":"agent"}`, "auth", "credential"},
		{"auth 角色非法", `{"type":"auth","credential":"c","role":"x"}`, "auth", "role"},
		{"assign 版本为 0", `{"type":"assign","version":0,"instances":[]}`, "assign", "version"},
		{"assign 实例缺 id", `{"type":"assign","version":1,"instances":[{"plugin_id":"p"}]}`, "assign", "instances[0].id"},
		{"report seq 为 0", `{"type":"report","instance_id":"i","seq":0,"report":{"status":"ok"}}`, "report", "seq"},
		{"report 既无报告也无错误", `{"type":"report","instance_id":"i","seq":1}`, "report", "report"},
		{"report 带 state", `{"type":"report","instance_id":"i","seq":1,"report":{"status":"ok","state":"x"}}`, "report", "report.state"},
		{"report 错误类别非法", `{"type":"report","instance_id":"i","seq":1,"error":"e","error_kind":"zzz"}`, "report", "error_kind"},
		{"hello 最低版本大于自身", `{"type":"hello","protocol_version":1,"min_compatible_version":2,"agent_version":"v"}`, "hello", "min_compatible_version"},
		{"assign_result 状态非法", `{"type":"assign_result","version":1,"results":[{"instance_id":"i","status":"zzz"}]}`, "assign_result", "results[0].status"},
		{"run_now 缺 request_id", `{"type":"run_now","instance_id":"i"}`, "run_now", "request_id"},
		{"logs_get 级别非法", `{"type":"logs_get","request_id":"r","min_level":"zzz"}`, "logs_get", "min_level"},
		{"ping 缺时间", `{"type":"ping"}`, "ping", "sent_at"},
		{"字段类型错误", `{"type":"assign","version":"x"}`, "assign", ""},
		{"不是 JSON", `not json`, "", ""},
		{"缺 type", `{"a":1}`, "", "type"},
	}
	for _, c := range cases {
		_, err := Decode([]byte(c.in))
		var inv *InvalidMessageError
		if !errors.As(err, &inv) {
			t.Errorf("%s: err = %v，应为 *InvalidMessageError", c.name, err)
			continue
		}
		if errors.Is(err, ErrUnknownType) {
			t.Errorf("%s: 不应同时是 ErrUnknownType", c.name)
		}
		if string(inv.Type) != c.typ {
			t.Errorf("%s: Type = %q，want %q", c.name, inv.Type, c.typ)
		}
		if c.field != "" && inv.Field != c.field {
			t.Errorf("%s: Field = %q，want %q", c.name, inv.Field, c.field)
		}
	}
}

func TestEncode_校验失败拒绝编码(t *testing.T) {
	if _, err := Encode(&Auth{Role: RoleAgent}); err == nil {
		t.Fatal("非法消息不应能编码")
	}
	if _, err := Encode(nil); err == nil {
		t.Fatal("nil 消息应报错")
	}
}

func TestRequester_带请求号的消息(t *testing.T) {
	want := map[Type]bool{TypeRunNow: true, TypeRunResult: true, TypeLogsGet: true, TypeLogs: true, TypeUpgradeInfoGet: true, TypeUpgradeInfo: true}
	for _, m := range allSamples() {
		r, ok := m.(Requester)
		if ok != want[m.MsgType()] {
			t.Errorf("%s: Requester = %v", m.MsgType(), ok)
		}
		if ok && r.GetRequestID() == "" {
			t.Errorf("%s: 请求号为空", m.MsgType())
		}
	}
}

func TestRequestIDSetter_只有请求方消息实现(t *testing.T) {
	want := map[Type]bool{TypeRunNow: true, TypeLogsGet: true, TypeUpgradeInfoGet: true}
	for _, m := range allSamples() { // 样本均为指针
		s, ok := m.(RequestIDSetter)
		if ok != want[m.MsgType()] {
			t.Errorf("%s: RequestIDSetter = %v", m.MsgType(), ok)
		}
		if ok {
			s.SetRequestID("abc")
			if got := m.(Requester).GetRequestID(); got != "abc" {
				t.Errorf("%s: 设置后请求号 = %q", m.MsgType(), got)
			}
		}
	}
	// 值类型不实现（setter 用指针接收者），会话层据此要求传指针才能代为生成请求号。
	var v any = RunNow{}
	if _, ok := v.(RequestIDSetter); ok {
		t.Error("值类型 RunNow 不应实现 RequestIDSetter")
	}
}

func TestReport_无state字段进入JSON(t *testing.T) {
	data, _ := Encode(&Report{InstanceID: "i", Seq: 1, RunOutcome: sampleOutcomeOK()})
	if strings.Contains(string(data), `"state"`) {
		t.Fatalf("不应带 state: %s", data)
	}
}
