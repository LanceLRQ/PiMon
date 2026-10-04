// Package mdns 提供最小的 mDNS 单次查询：向本地链路组播 A/AAAA 问题，在时间窗口内收集应答。
// 只做查询，不发布服务也不持续监听；不处理多网卡选择，使用系统默认路由。
//
// 应答过滤：只采信来源端口为 5353、来源 IP 与本机某个网卡在同一子网的报文，
// 并丢弃链路本地（fe80::/10、169.254/16）与带 zone 的地址（拨号方无法可靠使用）。
// 已知限制：RFC 6762 建议的「来源 IP TTL 为 255」校验需要 golang.org/x/net/ipv4 的控制消息，
// 成本较高，未实现；同子网 + 端口 5353 的校验已挡住大多数跨网段伪造。
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
	// LocalPrefixes 返回本机各网卡地址所在的子网前缀，用于校验应答来源；为空时读取系统网卡
	// （net.InterfaceAddrs）。每次 Lookup 调用一次，出错时 Lookup 返回该错误。
	LocalPrefixes func() ([]netip.Prefix, error)
}

// Lookup 对 name（形如 pimon.local）发送 QU 查询，返回窗口内收到的去重后的 A/AAAA 地址。
// 窗口固定走满（收到应答也不提前返回），窗口过半时重发一次查询以应对首包丢失。
// 首次向 IPv4 组播发送失败视为错误；IPv6 组播发送与重发都尽力而为，失败被忽略。
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
	localPrefixes := opts.LocalPrefixes
	if localPrefixes == nil {
		localPrefixes = systemPrefixes
	}
	prefixes, err := localPrefixes()
	if err != nil {
		return nil, fmt.Errorf("读取本机网卡地址失败: %w", err)
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
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if fromLocalResponder(from, prefixes) {
				c.add(buf[:n])
			}
		}
	}()

	// 截止时刻按绝对时间算：后半窗口的计时从重发后剩余的时长开始，时钟被一次推进过整个窗口时也能按时结束。
	deadline := clk.Now().Add(window)
	ctxErr := waitOrDone(ctx, clk.After(window/2))
	if ctxErr == nil {
		_, _ = conn.WriteTo(query, groupV4)
		_, _ = conn.WriteTo(query, groupV6)
		ctxErr = waitOrDone(ctx, clk.After(deadline.Sub(clk.Now())))
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

// waitOrDone 等到 timer 触发返回 nil，或 ctx 结束返回 ctx.Err()。
func waitOrDone(ctx context.Context, timer <-chan time.Time) error {
	select {
	case <-timer:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// systemPrefixes 读取系统网卡地址并转换为子网前缀。
func systemPrefixes() ([]netip.Prefix, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	return prefixesFromAddrs(addrs), nil
}

// prefixesFromAddrs 把网卡地址（*net.IPNet）转换为规范化的子网前缀：IPv4 映射地址还原为 IPv4，
// 16 字节掩码按 IPv4 位数折算；无法解析的条目跳过。
func prefixesFromAddrs(addrs []net.Addr) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(addrs))
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipn.IP)
		if !ok {
			continue
		}
		ones, bits := ipn.Mask.Size()
		if bits == 0 {
			continue
		}
		ip = ip.Unmap()
		if ip.Is4() && bits == 128 {
			ones -= 96
		}
		p, err := ip.Prefix(ones)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// fromLocalResponder 校验应答来源：必须是 UDP 端口 5353，且来源 IP（去 zone、还原 IPv4 映射）
// 落在本机某个网卡的子网内。
func fromLocalResponder(from net.Addr, prefixes []netip.Prefix) bool {
	ua, ok := from.(*net.UDPAddr)
	if !ok || ua == nil || ua.Port != mdnsPort {
		return false
	}
	ip, ok := netip.AddrFromSlice(ua.IP)
	if !ok {
		return false
	}
	ip = ip.Unmap()
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// usableAddr 报告应答中的地址能否交给拨号方：丢弃链路本地与带 zone 的地址。
func usableAddr(a netip.Addr) bool {
	return !a.IsLinkLocalUnicast() && a.Zone() == ""
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
	if !usableAddr(a) {
		return
	}
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
