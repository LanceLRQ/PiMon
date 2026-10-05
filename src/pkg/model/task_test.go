package model

import (
	"errors"
	"strings"
	"testing"
)

func validTaskEvent() TaskEvent {
	return TaskEvent{
		Tool: TaskToolClaude, Session: "s1", Turn: "t1", State: TaskStateRunning,
		Project: "pimon", Source: TaskSourceHook,
	}
}

func TestTaskEvent_Normalize合法事件通过(t *testing.T) {
	e := validTaskEvent()
	e.Parent = "p1"
	e.Title = "标题"
	e.Detail = "rate_limit"
	e.AgentType = "general-purpose"
	if err := e.Normalize(); err != nil {
		t.Fatalf("应通过: %v", err)
	}
}

func TestTaskEvent_Normalize拒绝非法字段(t *testing.T) {
	cases := map[string]struct {
		mut   func(*TaskEvent)
		field string
		code  string
	}{
		"工具非法":         {func(e *TaskEvent) { e.Tool = "gpt" }, "tool", FieldInvalid},
		"工具为空":         {func(e *TaskEvent) { e.Tool = "" }, "tool", FieldRequired},
		"状态非法":         {func(e *TaskEvent) { e.State = "done" }, "state", FieldInvalid},
		"状态为空":         {func(e *TaskEvent) { e.State = "" }, "state", FieldRequired},
		"来源非法":         {func(e *TaskEvent) { e.Source = "x" }, "source", FieldInvalid},
		"session为空":    {func(e *TaskEvent) { e.Session = "" }, "session", FieldRequired},
		"session过长":    {func(e *TaskEvent) { e.Session = strings.Repeat("a", 257) }, "session", FieldOutOfRange},
		"parent过长":     {func(e *TaskEvent) { e.Parent = strings.Repeat("a", 257) }, "parent", FieldOutOfRange},
		"running缺turn": {func(e *TaskEvent) { e.Turn = "" }, "turn", FieldRequired},
		"turn过长":       {func(e *TaskEvent) { e.Turn = strings.Repeat("a", 257) }, "turn", FieldOutOfRange},
		"detail非法":     {func(e *TaskEvent) { e.Detail = "Has Space" }, "detail", FieldPattern},
		"detail过长":     {func(e *TaskEvent) { e.Detail = strings.Repeat("a", 33) }, "detail", FieldPattern},
		"agent_type非法": {func(e *TaskEvent) { e.AgentType = "a b" }, "agent_type", FieldPattern},
		"agent_type过长": {func(e *TaskEvent) { e.AgentType = strings.Repeat("a", 65) }, "agent_type", FieldOutOfRange},
	}
	for name, c := range cases {
		e := validTaskEvent()
		c.mut(&e)
		err := e.Normalize()
		var fe FieldErrors
		if !errors.As(err, &fe) || fe[c.field] != c.code {
			t.Errorf("%s: 期望 %s=%s，得到 %v", name, c.field, c.code, err)
		}
	}
}

func TestTaskEvent_Normalize多个错误一并返回(t *testing.T) {
	e := TaskEvent{}
	var fe FieldErrors
	if err := e.Normalize(); !errors.As(err, &fe) || len(fe) < 4 {
		t.Fatalf("应返回多个字段错误: %v", err)
	}
}

func TestTaskEvent_idle与ended允许空turn(t *testing.T) {
	for _, s := range []string{TaskStateIdle, TaskEventEnded} {
		e := validTaskEvent()
		e.State, e.Turn = s, ""
		if err := e.Normalize(); err != nil {
			t.Errorf("%s 空 turn 应通过: %v", s, err)
		}
	}
}

func TestTaskEvent_八种状态与ended均合法(t *testing.T) {
	for _, s := range []string{
		TaskStateRunning, TaskStateWaitingApproval, TaskStateWaitingInput, TaskStateCompleted,
		TaskStateInterrupted, TaskStateFailed, TaskStateIdle, TaskStateUnknown, TaskEventEnded,
	} {
		e := validTaskEvent()
		e.State = s
		if err := e.Normalize(); err != nil {
			t.Errorf("%s 应合法: %v", s, err)
		}
	}
}

func TestTaskEvent_Normalize按字符截断并去控制字符(t *testing.T) {
	e := validTaskEvent()
	e.Project = strings.Repeat("项", 250)
	e.Title = "a\x00b\nc\td" + strings.Repeat("é", 300)
	if err := e.Normalize(); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(e.Project)); n != 200 {
		t.Errorf("project 应截到 200 字符，得到 %d", n)
	}
	if !strings.HasPrefix(e.Title, "abcd") || len([]rune(e.Title)) != 200 {
		t.Errorf("title 应去控制字符并截断: %q (%d)", e.Title[:10], len([]rune(e.Title)))
	}
}

func TestTaskEvent_Normalize幂等(t *testing.T) {
	e := validTaskEvent()
	e.Title = strings.Repeat("x", 300)
	_ = e.Normalize()
	first := e
	if err := e.Normalize(); err != nil || e != first {
		t.Fatalf("重复调用应无变化: %v", err)
	}
}
