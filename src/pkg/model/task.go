package model

import (
	"regexp"
	"strings"
	"unicode"
)

// EventKindTask 是 agent 事件外壳里任务事件的 kind。
const EventKindTask = "task"

// 任务工具。
const (
	TaskToolCodex  = "codex"
	TaskToolClaude = "claude"
)

// 任务状态（库存状态）。
const (
	TaskStateRunning         = "running"
	TaskStateWaitingApproval = "waiting_approval"
	TaskStateWaitingInput    = "waiting_input"
	TaskStateCompleted       = "completed"
	TaskStateInterrupted     = "interrupted"
	TaskStateFailed          = "failed"
	TaskStateIdle            = "idle"
	TaskStateUnknown         = "unknown"
)

// TaskEventEnded 是 SessionEnd 对应的会话结束标记，只出现在事件里，Task.State 永不取该值。
const TaskEventEnded = "ended"

// TaskDisplayOffline 是设备离线时 hub 叠加的展示状态，不入库。
const TaskDisplayOffline = "offline"

// 任务事件来源。
const (
	TaskSourceHook = "hook"
	TaskSourceLog  = "log"
)

// 任务事件字段上限。
const (
	TaskIDMaxBytes        = 256
	TaskAgentTypeMaxBytes = 64
	TaskTextMaxRunes      = 200
)

// TaskEvent 是 event.data 的内容（kind="task"）。事件 id、at、seq 在协议外壳上。
type TaskEvent struct {
	Tool    string `json:"tool"`
	Session string `json:"session"`
	// Parent 非空表示子任务，值为父 session。
	Parent string `json:"parent,omitempty"`
	// Turn 在 state 为 idle 或 ended 时可空，其余必填；只校验长度，不规定格式。
	Turn  string `json:"turn,omitempty"`
	State string `json:"state"`
	// Project 与 Title 非空时总会合并到任务上；Title 只来自客户端会话标题，永不含提示词。
	Project string `json:"project,omitempty"`
	Title   string `json:"title,omitempty"`
	Source  string `json:"source"`
	// Detail 是短令牌（如 rate_limit、permission_prompt），不含自由文本。
	Detail string `json:"detail,omitempty"`
	// AgentType 是子 agent 类型的短令牌。
	AgentType string `json:"agent_type,omitempty"`
}

var (
	taskDetailPattern    = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	taskAgentTypePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]*$`)
)

// Normalize 裁剪 project/title（去控制字符、按字符截到 200）并校验其余字段。
// 不合法返回 FieldErrors。agent 入队前与 hub 入库前各调用一次。
func (e *TaskEvent) Normalize() error {
	e.Project = cleanTaskText(e.Project)
	e.Title = cleanTaskText(e.Title)

	fe := FieldErrors{}
	switch e.Tool {
	case TaskToolCodex, TaskToolClaude:
	case "":
		fe["tool"] = FieldRequired
	default:
		fe["tool"] = FieldInvalid
	}
	switch e.Source {
	case TaskSourceHook, TaskSourceLog:
	case "":
		fe["source"] = FieldRequired
	default:
		fe["source"] = FieldInvalid
	}
	switch e.State {
	case TaskStateRunning, TaskStateWaitingApproval, TaskStateWaitingInput, TaskStateCompleted,
		TaskStateInterrupted, TaskStateFailed, TaskStateIdle, TaskStateUnknown, TaskEventEnded:
	case "":
		fe["state"] = FieldRequired
	default:
		fe["state"] = FieldInvalid
	}
	if e.Session == "" {
		fe["session"] = FieldRequired
	} else if len(e.Session) > TaskIDMaxBytes {
		fe["session"] = FieldOutOfRange
	}
	if len(e.Parent) > TaskIDMaxBytes {
		fe["parent"] = FieldOutOfRange
	}
	if e.Turn == "" {
		if e.State != TaskStateIdle && e.State != TaskEventEnded && fe["state"] == "" {
			fe["turn"] = FieldRequired
		}
	} else if len(e.Turn) > TaskIDMaxBytes {
		fe["turn"] = FieldOutOfRange
	}
	if e.Detail != "" && !taskDetailPattern.MatchString(e.Detail) {
		fe["detail"] = FieldPattern
	}
	if len(e.AgentType) > TaskAgentTypeMaxBytes {
		fe["agent_type"] = FieldOutOfRange
	} else if !taskAgentTypePattern.MatchString(e.AgentType) {
		fe["agent_type"] = FieldPattern
	}
	if len(fe) > 0 {
		return fe
	}
	return nil
}

// cleanTaskText 去掉控制字符并按字符截断到 TaskTextMaxRunes。
func cleanTaskText(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		if n >= TaskTextMaxRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// Task 是对外的任务视图（API 与 WS）。时间均为 Unix 毫秒；任务键为 (device_id, tool, session)。
type Task struct {
	ID         string `json:"id"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Tool       string `json:"tool"`
	Session    string `json:"session"`
	Parent     string `json:"parent,omitempty"`
	AgentType  string `json:"agent_type,omitempty"`
	Project    string `json:"project"`
	Title      string `json:"title"`
	// State 是库存状态，永不为 ended。
	State string `json:"state"`
	// DisplayState 是叠加 offline、unknown 后的展示状态。
	DisplayState string `json:"display_state"`
	Detail       string `json:"detail,omitempty"`
	Turn         string `json:"turn"`
	StartedAt    int64  `json:"started_at"`
	// UpdatedAt 取 hub 接收时间。
	UpdatedAt      int64  `json:"updated_at"`
	EndedAt        *int64 `json:"ended_at" tstype:"number | null,required"`
	ChildrenTotal  int    `json:"children_total"`
	ChildrenActive int    `json:"children_active"`
	// Children 仅 API 详情返回。
	Children []Task `json:"children,omitempty"`
}
