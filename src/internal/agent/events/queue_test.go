package events

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

func newQueue(t *testing.T, opts ...Option) (*Queue, *clock.Fake, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.db")
	clk := clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	st, err := store.Open(path, store.WithClock(clk))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, clk, opts...), clk, path
}

func ev(id string) Event {
	return Event{ID: id, Kind: "other", Data: json.RawMessage(`{"x":1}`), At: time.UnixMilli(5000)}
}

func ids(es []Entry) string {
	var s []string
	for _, e := range es {
		s = append(s, e.ID)
	}
	return strings.Join(s, ",")
}

func TestEnqueue分配seq且同id忽略(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t)
	s1, err := q.Enqueue(ctx, ev("a"))
	if err != nil || s1 != 1 {
		t.Fatal(s1, err)
	}
	if s, err := q.Enqueue(ctx, ev("a")); err != nil || s != 0 {
		t.Fatalf("重复入队 s=%d err=%v", s, err)
	}
	if s, _ := q.Enqueue(ctx, ev("b")); s != 2 {
		t.Fatalf("s=%d", s)
	}
}

func TestEnqueue校验(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t)
	bad := []Event{
		{Kind: "other", Data: json.RawMessage(`{}`), At: time.UnixMilli(1)},
		{ID: "a", Data: json.RawMessage(`{}`), At: time.UnixMilli(1)},
		{ID: "a", Kind: "other", Data: json.RawMessage(`{`), At: time.UnixMilli(1)},
		{ID: "a", Kind: "other", Data: json.RawMessage(`{}`)},
		{ID: strings.Repeat("x", 129), Kind: "other", Data: json.RawMessage(`{}`), At: time.UnixMilli(1)},
		{ID: "a", Kind: "task", Data: json.RawMessage(`{"tool":"nope"}`), At: time.UnixMilli(1)},
	}
	for i, e := range bad {
		if _, err := q.Enqueue(ctx, e); err == nil {
			t.Fatalf("case %d 应被拒绝", i)
		}
	}
	if st, _ := q.Stats(ctx); st.Pending != 0 || st.LastSeq != 0 {
		t.Fatalf("非法事件不应入队/占 seq: %+v", st)
	}
}

func TestEnqueue任务事件规范化后入队(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t)
	long := strings.Repeat("字", 300)
	data := fmt.Sprintf(`{"tool":"codex","session":"s1","turn":"t1","state":"running","project":%q,"source":"hook"}`, long)
	if _, err := q.Enqueue(ctx, Event{ID: "t1", Kind: "task", Data: json.RawMessage(data), At: time.UnixMilli(1)}); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Pending(ctx, 0, 10)
	var te struct{ Project string }
	_ = json.Unmarshal(got[0].Data, &te)
	if n := len([]rune(te.Project)); n != 200 {
		t.Fatalf("project 应被裁剪到 200 字符，实际 %d", n)
	}
}

func TestEnqueue超限淘汰并计数(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t, WithMaxEntries(3))
	for i := 1; i <= 5; i++ {
		if _, err := q.Enqueue(ctx, ev(fmt.Sprint("e", i))); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := q.Pending(ctx, 0, 10)
	if ids(got) != "e3,e4,e5" {
		t.Fatalf("got %s", ids(got))
	}
	if d, err := q.Dropped(ctx); err != nil || d != 2 {
		t.Fatalf("dropped=%d err=%v", d, err)
	}
}

func TestEnqueue七天过期与Prune(t *testing.T) {
	ctx := context.Background()
	q, clk, _ := newQueue(t)
	_, _ = q.Enqueue(ctx, ev("old"))
	clk.Advance(7*24*time.Hour + time.Minute)
	_, _ = q.Enqueue(ctx, ev("new"))
	got, _ := q.Pending(ctx, 0, 10)
	if ids(got) != "new" {
		t.Fatalf("got %s", ids(got))
	}
	clk.Advance(8 * 24 * time.Hour)
	if err := q.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := q.Stats(ctx); st.Pending != 0 || st.Dropped != 2 {
		t.Fatalf("%+v", st)
	}
}

func TestSeq与丢弃计数重启不归零(t *testing.T) {
	ctx := context.Background()
	q, clk, path := newQueue(t, WithMaxEntries(2))
	for i := 1; i <= 3; i++ {
		_, _ = q.Enqueue(ctx, ev(fmt.Sprint("e", i)))
	}
	_ = q.st.Close()
	st, err := store.Open(path, store.WithClock(clk))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	q2 := New(st, clk, WithMaxEntries(2))
	if s, _ := q2.Enqueue(ctx, ev("e4")); s != 4 {
		t.Fatalf("seq=%d", s)
	}
	if d, _ := q2.Dropped(ctx); d != 2 {
		t.Fatalf("dropped=%d", d)
	}
}

func TestClaim按seq升序且不重复取在途(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t)
	for _, id := range []string{"a", "b", "c"} {
		_, _ = q.Enqueue(ctx, ev(id))
	}
	owner := new(int)
	got, retry, err := q.Claim(ctx, owner)
	if err != nil || ids(got) != "a,b,c" || got[0].Seq != 1 || retry <= 0 {
		t.Fatalf("got %s retry %v err %v", ids(got), retry, err)
	}
	again, _, _ := q.Claim(ctx, owner)
	if len(again) != 0 {
		t.Fatalf("在途条目被重复取出: %s", ids(again))
	}
	_, _ = q.Enqueue(ctx, ev("d"))
	more, _, _ := q.Claim(ctx, owner)
	if ids(more) != "d" {
		t.Fatalf("got %s", ids(more))
	}
}

func TestClaim发送窗口与ack补发(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t, WithWindow(3))
	for i := 1; i <= 5; i++ {
		_, _ = q.Enqueue(ctx, ev(fmt.Sprint("e", i)))
	}
	owner := new(int)
	got, _, _ := q.Claim(ctx, owner)
	if ids(got) != "e1,e2,e3" {
		t.Fatalf("got %s", ids(got))
	}
	if again, _, _ := q.Claim(ctx, owner); len(again) != 0 {
		t.Fatalf("窗口已满仍取出 %s", ids(again))
	}
	if err := q.Ack(ctx, []string{"e1", "e2"}); err != nil {
		t.Fatal(err)
	}
	got, _, _ = q.Claim(ctx, owner)
	if ids(got) != "e4,e5" {
		t.Fatalf("got %s", ids(got))
	}
}

func TestClaim超时未确认重发(t *testing.T) {
	ctx := context.Background()
	q, clk, _ := newQueue(t, WithResendAfter(60*time.Second))
	_, _ = q.Enqueue(ctx, ev("a"))
	owner := new(int)
	_, retry, _ := q.Claim(ctx, owner)
	if retry != 60*time.Second {
		t.Fatalf("retry=%v", retry)
	}
	clk.Advance(30 * time.Second)
	got, retry, _ := q.Claim(ctx, owner)
	if len(got) != 0 || retry != 30*time.Second {
		t.Fatalf("got %s retry %v", ids(got), retry)
	}
	clk.Advance(30 * time.Second)
	got, _, _ = q.Claim(ctx, owner)
	if ids(got) != "a" {
		t.Fatalf("未重发: %s", ids(got))
	}
	// 重发后重新计时
	if again, _, _ := q.Claim(ctx, owner); len(again) != 0 {
		t.Fatal("重发后立即再取")
	}
}

func TestRelease后新会话补发在途条目(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t)
	_, _ = q.Enqueue(ctx, ev("a"))
	_, _ = q.Enqueue(ctx, ev("b"))
	o1, o2 := new(int), new(int)
	_, _, _ = q.Claim(ctx, o1)
	q.Release(o1)
	got, _, _ := q.Claim(ctx, o2)
	if ids(got) != "a,b" {
		t.Fatalf("got %s", ids(got))
	}
}

func TestAck幂等且删除(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t)
	_, _ = q.Enqueue(ctx, ev("a"))
	_, _ = q.Enqueue(ctx, ev("b"))
	owner := new(int)
	_, _, _ = q.Claim(ctx, owner)
	for i := 0; i < 2; i++ {
		if err := q.Ack(ctx, []string{"a", "不存在"}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := q.Pending(ctx, 0, 10)
	if ids(got) != "b" {
		t.Fatalf("got %s", ids(got))
	}
	if st, _ := q.Stats(ctx); st.Dropped != 0 {
		t.Fatalf("ack 不应计入丢弃: %+v", st)
	}
}

func TestSubscribe入队与ack都唤醒(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t)
	ch, cancel := q.Subscribe()
	defer cancel()
	select {
	case <-ch:
		t.Fatal("不应有初始唤醒")
	default:
	}
	_, _ = q.Enqueue(ctx, ev("a"))
	select {
	case <-ch:
	default:
		t.Fatal("入队未唤醒")
	}
	_ = q.Ack(ctx, []string{"a"})
	select {
	case <-ch:
	default:
		t.Fatal("ack 未唤醒")
	}
	// 重复入队不唤醒
	_, _ = q.Enqueue(ctx, ev("b"))
	<-ch
	_, _ = q.Enqueue(ctx, ev("b"))
	select {
	case <-ch:
		t.Fatal("重复入队不应唤醒")
	default:
	}
	cancel()
	_, _ = q.Enqueue(ctx, ev("c"))
	select {
	case <-ch:
		t.Fatal("取消订阅后仍被唤醒")
	default:
	}
}

func TestAck中间条目腾出窗口(t *testing.T) {
	ctx := context.Background()
	q, _, _ := newQueue(t, WithWindow(3))
	for i := 1; i <= 5; i++ {
		_, _ = q.Enqueue(ctx, ev(fmt.Sprint("e", i)))
	}
	owner := new(int)
	_, _, _ = q.Claim(ctx, owner)
	_ = q.Ack(ctx, []string{"e2"})
	got, _, _ := q.Claim(ctx, owner)
	if ids(got) != "e4" {
		t.Fatalf("got %s", ids(got))
	}
}
