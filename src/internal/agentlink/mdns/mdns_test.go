package mdns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

var testStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeConn 是内存假端点：写入被记录，读取从 inbox 取报文，Close 后读返回错误。
type fakeConn struct {
	mu       sync.Mutex
	writes   []fakeWrite
	failV6   bool
	failAll  bool
	inbox    chan []byte
	closed   chan struct{}
	closeOne sync.Once
}

type fakeWrite struct {
	data []byte
	addr net.Addr
}

func newFakeConn() *fakeConn {
	return &fakeConn{inbox: make(chan []byte), closed: make(chan struct{})}
}

func (f *fakeConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	ua, _ := addr.(*net.UDPAddr)
	if f.failAll || (f.failV6 && ua != nil && ua.IP.To4() == nil) {
		return 0, errors.New("发送失败")
	}
	f.mu.Lock()
	f.writes = append(f.writes, fakeWrite{append([]byte(nil), p...), addr})
	f.mu.Unlock()
	return len(p), nil
}

func (f *fakeConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case b := <-f.inbox:
		return copy(p, b), &net.UDPAddr{IP: net.IPv4(192, 168, 1, 9), Port: 5353}, nil
	case <-f.closed:
		return 0, nil, net.ErrClosed
	}
}

func (f *fakeConn) Close() error {
	f.closeOne.Do(func() { close(f.closed) })
	return nil
}

// deliver 把报文交给读协程；返回时读协程已取走报文。
func (f *fakeConn) deliver(t *testing.T, b []byte) {
	t.Helper()
	select {
	case f.inbox <- b:
	case <-time.After(2 * time.Second):
		t.Fatal("投递报文超时")
	}
}

func (f *fakeConn) isClosed() bool {
	select {
	case <-f.closed:
		return true
	default:
		return false
	}
}

type result struct {
	addrs []netip.Addr
	err   error
}

func startLookup(ctx context.Context, name string, fc *fakeConn, clk clock.Clock) <-chan result {
	ch := make(chan result, 1)
	go func() {
		a, err := Lookup(ctx, name, Options{Clock: clk, Conn: func() (PacketConn, error) { return fc, nil }})
		ch <- result{a, err}
	}()
	return ch
}

func waitWaiters(t *testing.T, f *clock.Fake) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for f.Waiters() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("等待窗口计时器超时")
		}
		time.Sleep(time.Millisecond)
	}
}

func finish(t *testing.T, f *clock.Fake, ch <-chan result) result {
	t.Helper()
	f.Advance(time.Second)
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("Lookup 未返回")
	}
	return result{}
}

type rr struct {
	name  string
	class dnsmessage.Class
	addr  netip.Addr
}

func buildResponse(t *testing.T, answers, additionals []rr, compress bool) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	if compress {
		b.EnableCompression()
	}
	if err := b.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	add := func(r rr, answer bool) {
		h := dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(r.name), Class: r.class, TTL: 120}
		var err error
		if r.addr.Is4() {
			h.Type = dnsmessage.TypeA
			if answer {
				err = b.AResource(h, dnsmessage.AResource{A: r.addr.As4()})
			}
		} else {
			h.Type = dnsmessage.TypeAAAA
			if answer {
				err = b.AAAAResource(h, dnsmessage.AAAAResource{AAAA: r.addr.As16()})
			}
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range answers {
		add(r, true)
	}
	if len(additionals) > 0 {
		if err := b.StartAdditionals(); err != nil {
			t.Fatal(err)
		}
		for _, r := range additionals {
			h := dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(r.name), Class: r.class, TTL: 120}
			var err error
			if r.addr.Is4() {
				h.Type = dnsmessage.TypeA
				err = b.AResource(h, dnsmessage.AResource{A: r.addr.As4()})
			} else {
				h.Type = dnsmessage.TypeAAAA
				err = b.AAAAResource(h, dnsmessage.AAAAResource{AAAA: r.addr.As16()})
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLookup_查询报文含AAAA两个问题且带QU位(t *testing.T) {
	fc := newFakeConn()
	clk := clock.NewFake(testStart)
	ch := startLookup(context.Background(), "pimon.local", fc, clk)
	waitWaiters(t, clk)
	r := finish(t, clk, ch)
	if !errors.Is(r.err, ErrNoAnswer) {
		t.Fatalf("err = %v", r.err)
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	var v4 *fakeWrite
	for i := range fc.writes {
		if ua := fc.writes[i].addr.(*net.UDPAddr); ua.IP.Equal(net.IPv4(224, 0, 0, 251)) && ua.Port == 5353 {
			v4 = &fc.writes[i]
		}
	}
	if v4 == nil {
		t.Fatal("未向 224.0.0.251:5353 发送")
	}
	var m dnsmessage.Message
	if err := m.Unpack(v4.data); err != nil {
		t.Fatal(err)
	}
	if m.ID != 0 || m.Response || len(m.Questions) != 2 {
		t.Fatalf("报文头或问题数不对: %+v", m)
	}
	types := map[dnsmessage.Type]bool{}
	for _, q := range m.Questions {
		if q.Name.String() != "pimon.local." {
			t.Fatalf("问题名 = %s", q.Name)
		}
		if q.Class != dnsmessage.ClassINET|1<<15 {
			t.Fatalf("缺 QU 位: %#x", uint16(q.Class))
		}
		types[q.Type] = true
	}
	if !types[dnsmessage.TypeA] || !types[dnsmessage.TypeAAAA] {
		t.Fatalf("问题类型 = %v", types)
	}
	var v6 bool
	for _, w := range fc.writes {
		if ua := w.addr.(*net.UDPAddr); ua.IP.Equal(net.ParseIP("ff02::fb")) && ua.Port == 5353 {
			v6 = true
		}
	}
	if !v6 {
		t.Fatal("未向 [ff02::fb]:5353 发送")
	}
}

func TestLookup_解析含名称压缩的应答并去重(t *testing.T) {
	fc := newFakeConn()
	clk := clock.NewFake(testStart)
	ch := startLookup(context.Background(), "pimon.local", fc, clk)
	waitWaiters(t, clk)
	a4 := netip.MustParseAddr("192.168.1.5")
	a6 := netip.MustParseAddr("fe80::1")
	fc.deliver(t, buildResponse(t, []rr{
		{"pimon.local.", dnsmessage.ClassINET, a4},
		{"pimon.local.", dnsmessage.ClassINET, a6},
	}, nil, true))
	fc.deliver(t, buildResponse(t, []rr{{"PIMON.local.", dnsmessage.ClassINET, a4}}, nil, false))
	r := finish(t, clk, ch)
	if r.err != nil || len(r.addrs) != 2 || r.addrs[0] != a4 || r.addrs[1] != a6 {
		t.Fatalf("结果 = %+v", r)
	}
	if !fc.isClosed() {
		t.Fatal("窗口结束后应关闭 conn")
	}
}

func TestLookup_cacheflush位被接受且Additionals也看(t *testing.T) {
	fc := newFakeConn()
	clk := clock.NewFake(testStart)
	ch := startLookup(context.Background(), "pimon.local.", fc, clk)
	waitWaiters(t, clk)
	a := netip.MustParseAddr("10.0.0.2")
	b := netip.MustParseAddr("10.0.0.3")
	fc.deliver(t, buildResponse(t,
		[]rr{{"pimon.local.", dnsmessage.ClassINET | 1<<15, a}},
		[]rr{{"pimon.local.", dnsmessage.ClassINET | 1<<15, b}}, false))
	r := finish(t, clk, ch)
	if r.err != nil || len(r.addrs) != 2 {
		t.Fatalf("结果 = %+v", r)
	}
}

func TestLookup_名称或class不符与畸形报文被忽略且不终止(t *testing.T) {
	fc := newFakeConn()
	clk := clock.NewFake(testStart)
	ch := startLookup(context.Background(), "pimon.local", fc, clk)
	waitWaiters(t, clk)
	bad := netip.MustParseAddr("10.9.9.9")
	good := netip.MustParseAddr("10.0.0.7")
	fc.deliver(t, buildResponse(t, []rr{{"other.local.", dnsmessage.ClassINET, bad}}, nil, false))
	fc.deliver(t, buildResponse(t, []rr{{"pimon.local.", dnsmessage.ClassCHAOS, bad}}, nil, false))
	fc.deliver(t, []byte{1, 2, 3})
	fc.deliver(t, nil)
	// 查询报文（非应答）不采信
	q := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("pimon.local."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	qb, _ := q.Pack()
	fc.deliver(t, qb)
	// 截断的应答
	full := buildResponse(t, []rr{{"pimon.local.", dnsmessage.ClassINET, bad}}, nil, false)
	fc.deliver(t, full[:len(full)-2])
	fc.deliver(t, buildResponse(t, []rr{{"pimon.local.", dnsmessage.ClassINET, good}}, nil, false))
	r := finish(t, clk, ch)
	if r.err != nil || len(r.addrs) != 1 || r.addrs[0] != good {
		t.Fatalf("结果 = %+v", r)
	}
}

func TestLookup_窗口内无应答返回ErrNoAnswer(t *testing.T) {
	fc := newFakeConn()
	clk := clock.NewFake(testStart)
	ch := startLookup(context.Background(), "pimon.local", fc, clk)
	waitWaiters(t, clk)
	clk.Advance(999 * time.Millisecond)
	select {
	case <-ch:
		t.Fatal("窗口未到不应返回")
	case <-time.After(20 * time.Millisecond):
	}
	r := finish(t, clk, ch)
	if !errors.Is(r.err, ErrNoAnswer) || len(r.addrs) != 0 {
		t.Fatalf("结果 = %+v", r)
	}
}

func TestLookup_ctx取消返回ctxErr并关闭conn(t *testing.T) {
	fc := newFakeConn()
	clk := clock.NewFake(testStart)
	ctx, cancel := context.WithCancel(context.Background())
	ch := startLookup(ctx, "pimon.local", fc, clk)
	waitWaiters(t, clk)
	cancel()
	select {
	case r := <-ch:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err = %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消后未返回")
	}
	if !fc.isClosed() {
		t.Fatal("应关闭 conn")
	}
}

func TestLookup_IPv6发送失败不影响IPv4结果(t *testing.T) {
	fc := newFakeConn()
	fc.failV6 = true
	clk := clock.NewFake(testStart)
	ch := startLookup(context.Background(), "pimon.local", fc, clk)
	waitWaiters(t, clk)
	a := netip.MustParseAddr("192.168.1.5")
	fc.deliver(t, buildResponse(t, []rr{{"pimon.local.", dnsmessage.ClassINET, a}}, nil, false))
	r := finish(t, clk, ch)
	if r.err != nil || len(r.addrs) != 1 || r.addrs[0] != a {
		t.Fatalf("结果 = %+v", r)
	}
}

func TestLookup_IPv4发送失败返回错误(t *testing.T) {
	fc := newFakeConn()
	fc.failAll = true
	_, err := Lookup(context.Background(), "pimon.local", Options{Clock: clock.NewFake(testStart), Conn: func() (PacketConn, error) { return fc, nil }})
	if err == nil || errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v", err)
	}
	if !fc.isClosed() {
		t.Fatal("应关闭 conn")
	}
}

func TestLookup_Conn创建失败返回错误(t *testing.T) {
	boom := errors.New("无网卡")
	_, err := Lookup(context.Background(), "pimon.local", Options{Conn: func() (PacketConn, error) { return nil, boom }})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestLookup_名称非法(t *testing.T) {
	for _, n := range []string{"", ".", "example.com", "local", ".local", "pimon.localx"} {
		_, err := Lookup(context.Background(), n, Options{Conn: func() (PacketConn, error) {
			t.Fatal("非法名称不应创建 conn")
			return nil, nil
		}})
		if !errors.Is(err, ErrEmptyName) && !errors.Is(err, ErrNotLocalName) {
			t.Fatalf("%q: err = %v", n, err)
		}
	}
	if _, err := Lookup(context.Background(), "", Options{}); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("空名称 err = %v", err)
	}
	if _, err := Lookup(context.Background(), "example.com", Options{}); !errors.Is(err, ErrNotLocalName) {
		t.Fatalf("非 local err = %v", err)
	}
}
