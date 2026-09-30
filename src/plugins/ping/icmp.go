package ping

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// icmpPinger 用 "udp4" 网络的非特权 ICMP 套接字发包；内核会改写回显 ID，
// 所以只按序号匹配应答。
type icmpPinger struct{}

// NewICMPPinger 返回真实的 ICMP 探测器。
func NewICMPPinger() Pinger { return icmpPinger{} }

func (icmpPinger) Ping(ctx context.Context, host string, count int, timeout time.Duration) (Result, error) {
	ip, err := resolveIPv4(ctx, host)
	if err != nil {
		return Result{}, ErrResolve
	}
	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		if isPermission(err) {
			return Result{}, ErrPermission
		}
		return Result{}, err
	}
	defer func() { _ = conn.Close() }()
	// ctx 结束时关闭套接字，打断阻塞中的读。
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	res := Result{}
	buf := make([]byte, 1500)
	dst := &net.UDPAddr{IP: ip}
	for seq := 1; seq <= count; seq++ {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: seq, Data: []byte("pimon")}}
		wb, err := msg.Marshal(nil)
		if err != nil {
			return res, err
		}
		start := time.Now()
		if _, err := conn.WriteTo(wb, dst); err != nil {
			if isPermission(err) {
				return res, ErrPermission
			}
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			// 发送失败（如网络不可达）按丢包处理。
			res.Sent++
			continue
		}
		res.Sent++
		if rtt, ok := waitReply(conn, buf, seq, start, timeout); ok {
			res.RTTs = append(res.RTTs, rtt)
		}
	}
	return res, nil
}

// waitReply 读取直到收到序号匹配的回显应答或到达截止时间。
func waitReply(conn *icmp.PacketConn, buf []byte, seq int, start time.Time, timeout time.Duration) (time.Duration, bool) {
	deadline := start.Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return 0, false
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return 0, false
		}
		m, err := icmp.ParseMessage(1, buf[:n])
		if err != nil || m.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		if echo, ok := m.Body.(*icmp.Echo); ok && echo.Seq == seq {
			return time.Since(start), true
		}
	}
}

func resolveIPv4(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
		return nil, errors.New("仅支持 IPv4")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("解析失败")
	}
	return ips[0], nil
}

func isPermission(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}
