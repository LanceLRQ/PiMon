package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// 协议版本与帧大小上限。版本号与软件版本独立递增。
const (
	ProtocolVersion      = 1
	MinCompatibleVersion = 1

	// MaxPreAuthFrame 是认证完成前的单帧上限。
	MaxPreAuthFrame = 4 * 1024
	// MaxFrame 是认证后的单帧上限（assign 可能较大）。
	MaxFrame = 8 * 1024 * 1024
)

// 握手角色（auth/pair 中的 role）。
const (
	RoleHub   = "hub"
	RoleAgent = "agent"
)

// Type 是消息的顶层 type 取值。
type Type string

// 消息类型。
const (
	TypeAuth           Type = "auth"
	TypeAuthOK         Type = "auth_ok"
	TypeAuthFail       Type = "auth_fail"
	TypePair           Type = "pair"
	TypePairOK         Type = "pair_ok"
	TypeHello          Type = "hello"
	TypeHelloOK        Type = "hello_ok"
	TypeIncompatible   Type = "incompatible"
	TypePluginsChanged Type = "plugins_changed"
	TypeAssign         Type = "assign"
	TypeAssignResult   Type = "assign_result"
	TypeReport         Type = "report"
	TypeEvent          Type = "event"
	TypeAck            Type = "ack"
	TypeRunNow         Type = "run_now"
	TypeRunResult      Type = "run_result"
	TypeLogsGet        Type = "logs_get"
	TypeLogs           Type = "logs"
	TypeUpgradeInfoGet Type = "upgrade_info_get"
	TypeUpgradeInfo    Type = "upgrade_info"
	TypePing           Type = "ping"
	TypePong           Type = "pong"
	TypeBye            Type = "bye"
)

// 错误原因码（auth_fail、bye、incompatible 的 reason）。
const (
	ReasonBadCredential = "bad_credential"
	ReasonBadCode       = "bad_code"
	ReasonCodeExpired   = "code_expired"
	ReasonRateLimited   = "rate_limited"
	ReasonRevoked       = "revoked"
	// ReasonVersionTooOld 表示 agent 的协议版本低于 hub 的最低兼容版本（需升级 agent）。
	ReasonVersionTooOld = "version_too_old"
	// ReasonVersionTooNew 表示 hub 的协议版本低于 agent 的最低兼容版本（需升级 hub）。
	ReasonVersionTooNew = "version_too_new"
	// ReasonReplaced 表示同一设备的新连接顶替了旧连接。
	ReasonReplaced = "replaced"
	ReasonShutdown = "shutdown"
)

// ErrUnknownType 表示收到未知的消息 type；会话层应记录并忽略，不断开连接。
var ErrUnknownType = errors.New("未知的消息类型")

// InvalidMessageError 表示已知类型（或帧本身）的字段不合法；会话层应断开连接。
type InvalidMessageError struct {
	// Type 是消息类型，帧不是 JSON 或缺 type 时为空。
	Type Type
	// Field 是出错字段的路径，无法定位时为空。
	Field  string
	Reason string
	Err    error
}

func (e *InvalidMessageError) Error() string {
	s := "消息不合法"
	if e.Type != "" {
		s += "（" + string(e.Type) + "）"
	}
	if e.Field != "" {
		s += "：字段 " + e.Field
	}
	if e.Reason != "" {
		s += " " + e.Reason
	}
	if e.Err != nil {
		s += "：" + e.Err.Error()
	}
	return s
}

func (e *InvalidMessageError) Unwrap() error { return e.Err }

func invalid(t Type, field, reason string) error {
	return &InvalidMessageError{Type: t, Field: field, Reason: reason}
}

// Message 是通道上的一条消息；各实现用值接收者，编码时值与指针都可传入，解码得到指针。
type Message interface {
	MsgType() Type
	// Validate 检查字段合法性，失败返回 *InvalidMessageError。
	Validate() error
}

// Requester 由带 request_id 的消息实现，供会话层的请求/响应关联使用。
type Requester interface {
	GetRequestID() string
}

var registry = map[Type]func() Message{
	TypeAuth:           func() Message { return new(Auth) },
	TypeAuthOK:         func() Message { return new(AuthOK) },
	TypeAuthFail:       func() Message { return new(AuthFail) },
	TypePair:           func() Message { return new(Pair) },
	TypePairOK:         func() Message { return new(PairOK) },
	TypeHello:          func() Message { return new(Hello) },
	TypeHelloOK:        func() Message { return new(HelloOK) },
	TypeIncompatible:   func() Message { return new(Incompatible) },
	TypePluginsChanged: func() Message { return new(PluginsChanged) },
	TypeAssign:         func() Message { return new(Assign) },
	TypeAssignResult:   func() Message { return new(AssignResult) },
	TypeReport:         func() Message { return new(Report) },
	TypeEvent:          func() Message { return new(Event) },
	TypeAck:            func() Message { return new(Ack) },
	TypeRunNow:         func() Message { return new(RunNow) },
	TypeRunResult:      func() Message { return new(RunResult) },
	TypeLogsGet:        func() Message { return new(LogsGet) },
	TypeLogs:           func() Message { return new(Logs) },
	TypeUpgradeInfoGet: func() Message { return new(UpgradeInfoGet) },
	TypeUpgradeInfo:    func() Message { return new(UpgradeInfo) },
	TypePing:           func() Message { return new(Ping) },
	TypePong:           func() Message { return new(Pong) },
	TypeBye:            func() Message { return new(Bye) },
}

// Encode 校验消息并编码为一帧 JSON，顶层写入 type 字段。
func Encode(m Message) ([]byte, error) {
	if m == nil {
		return nil, errors.New("消息为空")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("编码消息 %s: %w", m.MsgType(), err)
	}
	head := `{"type":"` + string(m.MsgType()) + `"`
	if len(body) <= 2 { // "{}"
		return []byte(head + "}"), nil
	}
	out := make([]byte, 0, len(head)+len(body))
	out = append(out, head...)
	out = append(out, ',')
	out = append(out, body[1:]...)
	return out, nil
}

// Decode 解码一帧：未知 type 返回 ErrUnknownType（可用 errors.Is 判断），
// 帧不是合法 JSON、缺 type 或字段不合法返回 *InvalidMessageError。
func Decode(data []byte) (Message, error) {
	var head struct {
		Type Type `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, &InvalidMessageError{Reason: "不是合法的 JSON 对象", Err: err}
	}
	if head.Type == "" {
		return nil, invalid("", "type", "缺失")
	}
	ctor, ok := registry[head.Type]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, string(head.Type))
	}
	m := ctor()
	if err := json.Unmarshal(data, m); err != nil {
		field := ""
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			field = ute.Field
		}
		return nil, &InvalidMessageError{Type: head.Type, Field: field, Reason: "字段类型不符", Err: err}
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// CheckCompat 由 hub 判断兼容性，reason 以 agent 为主语；兼容返回空串。
// agent 的协议版本低于 hub 的最低兼容版本返回 version_too_old（升级 agent），
// hub 的协议版本低于 agent 的最低兼容版本返回 version_too_new（升级 hub）；两者同时成立时以 too_old 为先。
// 返回 version_too_old 时 incompatible.required_min 应取 hubMin，version_too_new 时取 agentMin。
func CheckCompat(hubVer, hubMin, agentVer, agentMin int) string {
	if agentVer < hubMin {
		return ReasonVersionTooOld
	}
	if hubVer < agentMin {
		return ReasonVersionTooNew
	}
	return ""
}

// NewCredential 生成长期凭据：32 字节随机数的 base64url（无填充）。
func NewCredential() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成凭据: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// CredentialHash 返回凭据字符串字节的 SHA-256 十六进制，监听方只存此哈希。
func CredentialHash(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(sum[:])
}

// DeviceID 由凭据推导设备 ID（凭据哈希的前 16 个十六进制字符）。
func DeviceID(credential string) string { return DeviceIDFromHash(CredentialHash(credential)) }

// DeviceIDFromHash 由已存的凭据哈希推导设备 ID。
func DeviceIDFromHash(hash string) string {
	if len(hash) < 16 {
		return hash
	}
	return hash[:16]
}
