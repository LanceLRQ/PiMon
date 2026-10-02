// Package mdns 提供最小的 mDNS 单次查询：向本地链路组播 A/AAAA 问题，在时间窗口内收集应答。
// 只做查询，不发布服务也不持续监听；不处理多网卡选择，使用系统默认路由。
package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const (
	mdnsPort      = 5353
	defaultWindow = time.Second
	cacheFlushBit = dnsmessage.Class(1 << 15)
	maxPacketSize = 9000
)

var (
	// ErrEmptyName 表示查询名为空。
	ErrEmptyName = errors.New("mDNS 查询名不能为空")
	// ErrNotLocalName 表示查询名不是以 .local 结尾的多播域名。
	ErrNotLocalName = errors.New("mDNS 只能查询以 .local 结尾的名称")
	// ErrNoAnswer 表示窗口内没有收到任何匹配的 A/AAAA 应答。
	// 无应答时 Lookup 返回 nil 切片与该错误，上层作为兜底解析失败处理即可。
	ErrNoAnswer = errors.New("mDNS 窗口内无应答")
)

var (
	groupV4 = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: mdnsPort}
	groupV6 = &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: mdnsPort}
)

// PacketConn 是 net.PacketConn 的子集，便于注入测试用的内存端点。
type PacketConn interface {
	WriteTo(p []byte, addr net.Addr) (int, error)
	ReadFrom(p []byte) (int, net.Addr, error)
	Close() error
}

// Options 是查询选项，零值可用。
type Options struct {
	// Clock 为空时用真实时钟。
	Clock clock.Clock
	// Window 是收集应答的时间窗口，<=0 时取 1 秒。
	Window time.Duration
	// Conn 创建一个新的端点，查询结束时由 Lookup 关闭；为空时用系统 UDP 套接字。
	Conn func() (PacketConn, error)
}

// Lookup 对 name（形如 pimon.local）发送 QU 查询，返回窗口内收到的去重后的 A/AAAA 地址。
// 向 IPv4 组播发送失败视为错误；IPv6 组播发送尽力而为，失败被忽略。
// ctx 取消返回 ctx.Err()；无应答返回 ErrNoAnswer。
func Lookup(ctx context.Context, name string, opts Options) ([]netip.Addr, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" {
		return nil, ErrEmptyName
	}
	if !strings.HasSuffix(strings.ToLower(name), ".local") || len(name) == len(".local") {
		return nil, ErrNotLocalName
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	window := opts.Window
	if window <= 0 {
		window = defaultWindow
	}
	newConn := opts.Conn
	if newConn == nil {
		newConn = defaultConn
	}

	query, err := buildQuery(name)
	if err != nil {
		return nil, fmt.Errorf("构造 mDNS 查询失败: %w", err)
	}
	conn, err := newConn()
	if err != nil {
		return nil, fmt.Errorf("创建 mDNS 端点失败: %w", err)
	}
	if _, err := conn.WriteTo(query, groupV4); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("发送 mDNS 查询失败: %w", err)
	}
	_, _ = conn.WriteTo(query, groupV6)

	c := &collector{name: name}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, maxPacketSize)
		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			c.add(buf[:n])
		}
	}()

	var ctxErr error
	select {
	case <-clk.After(window):
	case <-ctx.Done():
		ctxErr = ctx.Err()
	}
	_ = conn.Close()
	<-done

	if ctxErr != nil {
		return nil, ctxErr
	}
	addrs := c.result()
	if len(addrs) == 0 {
		return nil, ErrNoAnswer
	}
	return addrs, nil
}

// buildQuery 构造 A 与 AAAA 两个问题的单报文查询，ID 为 0，问题类别置 QU 位请求单播应答。
func buildQuery(name string) ([]byte, error) {
	n, err := dnsmessage.NewName(name + ".")
	if err != nil {
		return nil, err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	for _, t := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
		q := dnsmessage.Question{Name: n, Type: t, Class: dnsmessage.ClassINET | cacheFlushBit}
		if err := b.Question(q); err != nil {
			return nil, err
		}
	}
	return b.Finish()
}

type collector struct {
	name string

	mu    sync.Mutex
	seen  map[netip.Addr]struct{}
	addrs []netip.Addr
}

func (c *collector) result() []netip.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]netip.Addr(nil), c.addrs...)
}

func (c *collector) push(a netip.Addr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[netip.Addr]struct{}{}
	}
	if _, ok := c.seen[a]; ok {
		return
	}
	c.seen[a] = struct{}{}
	c.addrs = append(c.addrs, a)
}

// add 解析一个报文，只采信应答报文中与查询名匹配、类别为 IN 的 A/AAAA 记录；畸形报文静默丢弃。
func (c *collector) add(pkt []byte) {
	var p dnsmessage.Parser
	h, err := p.Start(pkt)
	if err != nil || !h.Response {
		return
	}
	if p.SkipAllQuestions() != nil {
		return
	}
	for {
		rh, err := p.AnswerHeader()
		if err != nil {
			break
		}
		if !c.take(&p, rh, p.SkipAnswer) {
			break
		}
	}
	if p.SkipAllAuthorities() != nil {
		return
	}
	for {
		rh, err := p.AdditionalHeader()
		if err != nil {
			break
		}
		if !c.take(&p, rh, p.SkipAdditional) {
			break
		}
	}
}

// take 处理刚读到头部的一条记录：匹配则取地址，否则跳过。返回 false 表示报文已无法继续解析。
func (c *collector) take(p *dnsmessage.Parser, rh dnsmessage.ResourceHeader, skip func() error) bool {
	match := rh.Class&^cacheFlushBit == dnsmessage.ClassINET &&
		strings.EqualFold(strings.TrimSuffix(rh.Name.String(), "."), c.name)
	switch {
	case match && rh.Type == dnsmessage.TypeA:
		r, err := p.AResource()
		if err != nil {
			return false
		}
		c.push(netip.AddrFrom4(r.A))
	case match && rh.Type == dnsmessage.TypeAAAA:
		r, err := p.AAAAResource()
		if err != nil {
			return false
		}
		c.push(netip.AddrFrom16(r.AAAA))
	default:
		if skip() != nil {
			return false
		}
	}
	return true
}
