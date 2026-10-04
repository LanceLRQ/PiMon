package mdns

import (
	"net"
	"sync"
)

type packet struct {
	data []byte
	from net.Addr
}

// systemConn 是默认端点：必有 IPv4 套接字，IPv6 套接字尽力而为。
// 两个套接字使用临时端口，组播查询的单播应答回到各自的源端口。
type systemConn struct {
	v4, v6 *net.UDPConn

	recv     chan packet
	closed   chan struct{}
	closeOne sync.Once
}

func defaultConn() (PacketConn, error) {
	v4, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, err
	}
	c := &systemConn{v4: v4, recv: make(chan packet), closed: make(chan struct{})}
	if v6, err := net.ListenUDP("udp6", nil); err == nil {
		c.v6 = v6
	}
	for _, u := range []*net.UDPConn{c.v4, c.v6} {
		if u != nil {
			go c.pump(u)
		}
	}
	return c, nil
}

func (c *systemConn) pump(u *net.UDPConn) {
	buf := make([]byte, maxPacketSize)
	for {
		n, from, err := u.ReadFrom(buf)
		if err != nil {
			return
		}
		select {
		case c.recv <- packet{append([]byte(nil), buf[:n]...), from}:
		case <-c.closed:
			return
		}
	}
}

func (c *systemConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if ua, ok := addr.(*net.UDPAddr); ok && ua.IP.To4() == nil {
		if c.v6 == nil {
			return 0, net.ErrClosed
		}
		return c.v6.WriteTo(p, addr)
	}
	return c.v4.WriteTo(p, addr)
}

func (c *systemConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case pk := <-c.recv:
		return copy(p, pk.data), pk.from, nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *systemConn) Close() error {
	c.closeOne.Do(func() {
		close(c.closed)
		_ = c.v4.Close()
		if c.v6 != nil {
			_ = c.v6.Close()
		}
	})
	return nil
}
