package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

func openEvents(t *testing.T) (*Store, *clock.Fake, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.db")
	clk := clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	s, err := Open(path, WithClock(clk))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, clk, path
}

func qe(id string) QueuedEvent {
	return QueuedEvent{ID: id, Kind: "task", Data: json.RawMessage(`{"k":"` + id + `"}`), At: time.UnixMilli(1000)}
}

func TestAppendEvent分配单调seq并按序读取(t *testing.T) {
	ctx := context.Background()
	s, _, _ := openEvents(t)
	for i := 1; i <= 3; i++ {
		seq, ins, ev, err := s.AppendEvent(ctx, qe(fmt.Sprint("e", i)), 100, time.Hour)
		if err != nil || !ins || ev != 0 || seq != int64(i) {
			t.Fatalf("i=%d seq=%d ins=%v ev=%d err=%v", i, seq, ins, ev, err)
		}
	}
	got, err := s.PendingEvents(ctx, 1, 10)
	if err != nil || len(got) != 2 || got[0].Seq != 2 || got[1].ID != "e3" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if string(got[0].Data) != `{"k":"e2"}` || got[0].At.UnixMilli() != 1000 || got[0].Kind != "task" {
		t.Fatalf("字段不一致 %+v", got[0])
	}
	if lim, _ := s.PendingEvents(ctx, 0, 2); len(lim) != 2 {
		t.Fatalf("limit 未生效 %d", len(lim))
	}
}

func TestAppendEvent同id不占seq(t *testing.T) {
	ctx := context.Background()
	s, _, _ := openEvents(t)
	_, _, _, _ = s.AppendEvent(ctx, qe("a"), 100, time.Hour)
	seq, ins, _, err := s.AppendEvent(ctx, qe("a"), 100, time.Hour)
	if err != nil || ins || seq != 0 {
		t.Fatalf("seq=%d ins=%v err=%v", seq, ins, err)
	}
	seq, _, _, _ = s.AppendEvent(ctx, qe("b"), 100, time.Hour)
	if seq != 2 {
		t.Fatalf("重复入队占用了 seq: %d", seq)
	}
}

func TestAppendEvent超条数淘汰最旧并计数(t *testing.T) {
	ctx := context.Background()
	s, _, _ := openEvents(t)
	var total int64
	for i := 1; i <= 5; i++ {
		_, _, ev, err := s.AppendEvent(ctx, qe(fmt.Sprint("e", i)), 3, 0)
		if err != nil {
			t.Fatal(err)
		}
		total += ev
	}
	if total != 2 {
		t.Fatalf("淘汰 %d", total)
	}
	got, _ := s.PendingEvents(ctx, 0, 10)
	if len(got) != 3 || got[0].ID != "e3" || got[2].ID != "e5" {
		t.Fatalf("got %+v", got)
	}
	st, _ := s.EventStats(ctx)
	if st.Dropped != 2 || st.Pending != 3 || st.LastSeq != 5 {
		t.Fatalf("stats %+v", st)
	}
}

func TestAppendEvent按入队时间过期(t *testing.T) {
	ctx := context.Background()
	s, clk, _ := openEvents(t)
	const week = 7 * 24 * time.Hour
	// 事件时间很旧，但入队时间新：不应被淘汰
	old := qe("old")
	old.At = clk.Now().Add(-30 * 24 * time.Hour)
	_, _, _, _ = s.AppendEvent(ctx, old, 100, week)
	clk.Advance(week - time.Minute)
	_, _, ev, _ := s.AppendEvent(ctx, qe("mid"), 100, week)
	if ev != 0 {
		t.Fatalf("未到期被淘汰 %d", ev)
	}
	clk.Advance(2 * time.Minute) // old 入队已满 7 天
	_, _, ev, _ = s.AppendEvent(ctx, qe("new"), 100, week)
	if ev != 1 {
		t.Fatalf("ev=%d", ev)
	}
	got, _ := s.PendingEvents(ctx, 0, 10)
	if len(got) != 2 || got[0].ID != "mid" {
		t.Fatalf("got %+v", got)
	}
	clk.Advance(week + time.Minute)
	n, err := s.PruneEvents(ctx, 100, week)
	if err != nil || n != 2 {
		t.Fatalf("prune n=%d err=%v", n, err)
	}
	if st, _ := s.EventStats(ctx); st.Dropped != 3 || st.Pending != 0 {
		t.Fatalf("stats %+v", st)
	}
}

func TestDeleteEvents删除与幂等(t *testing.T) {
	ctx := context.Background()
	s, _, _ := openEvents(t)
	for _, id := range []string{"a", "b", "c"} {
		_, _, _, _ = s.AppendEvent(ctx, qe(id), 100, time.Hour)
	}
	n, err := s.DeleteEvents(ctx, []string{"a", "c", "zz"})
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, _ = s.DeleteEvents(ctx, []string{"a"})
	if n != 0 {
		t.Fatalf("重复删除 n=%d", n)
	}
	if n, err := s.DeleteEvents(ctx, nil); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if st, _ := s.EventStats(ctx); st.Pending != 1 || st.Dropped != 0 {
		t.Fatalf("确认删除不应计入丢弃: %+v", st)
	}
	// 大批量删除分块
	ids := make([]string, 1200)
	for i := range ids {
		ids[i] = fmt.Sprint("x", i)
		_, _, _, _ = s.AppendEvent(ctx, qe(ids[i]), 0, 0)
	}
	if n, err := s.DeleteEvents(ctx, ids); err != nil || n != 1200 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestEvents重启不归零且清空后seq不复用(t *testing.T) {
	ctx := context.Background()
	s, clk, path := openEvents(t)
	for i := 1; i <= 4; i++ {
		_, _, _, _ = s.AppendEvent(ctx, qe(fmt.Sprint("e", i)), 2, 0)
	}
	_, _ = s.DeleteEvents(ctx, []string{"e3", "e4"})
	_ = s.Close()
	s2, err := Open(path, WithClock(clk))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	seq, _, _, _ := s2.AppendEvent(ctx, qe("e5"), 2, 0)
	if seq != 5 {
		t.Fatalf("seq=%d", seq)
	}
	if st, _ := s2.EventStats(ctx); st.Dropped != 2 {
		t.Fatalf("dropped=%d", st.Dropped)
	}
}
