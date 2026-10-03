package netaddr

import (
	"net"
	"slices"
	"testing"
)

func TestCandidateURLs(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("169.254.3.4"),
		net.ParseIP("fe80::1"),
		net.ParseIP("192.168.1.20"),
		net.ParseIP("192.168.1.20"),
		net.ParseIP("10.0.0.5"),
		net.ParseIP("172.16.0.9"),
		net.ParseIP("172.16.0.10"),
	}
	got := CandidateURLs("", "http", "31415", ips)
	want := []string{"http://192.168.1.20:31415", "http://10.0.0.5:31415", "http://172.16.0.9:31415"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 配置了对外访问地址时排第一，去掉末尾斜杠
	got = CandidateURLs("https://pimon.home/", "http", "31415", ips)
	if got[0] != "https://pimon.home" || len(got) != 3 || got[1] != "http://192.168.1.20:31415" {
		t.Fatalf("access_url 优先: %v", got)
	}
	// 没有任何可用地址
	if got := CandidateURLs("", "http", "31415", []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("169.254.1.1")}); len(got) != 0 {
		t.Fatalf("回环与链路本地应被排除: %v", got)
	}
	// https 与无端口
	if got := CandidateURLs("", "https", "", []net.IP{net.ParseIP("192.168.1.2")}); !slices.Equal(got, []string{"https://192.168.1.2"}) {
		t.Fatalf("无端口: %v", got)
	}
}

func TestAgentURLs(t *testing.T) {
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.1.20"), net.ParseIP("fe80::1"), net.ParseIP("10.0.0.5")}
	got := AgentURLs("", "31418", ips)
	if want := []string{"wss://192.168.1.20:31418", "wss://10.0.0.5:31418"}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 对外访问地址的主机名排第一（只取主机名，换成 agent 通道端口），与本机 IP 去重、总数有上限
	got = AgentURLs("https://pimon.home:8443/x", "31418", ips)
	if want := []string{"wss://pimon.home:31418", "wss://192.168.1.20:31418", "wss://10.0.0.5:31418"}; !slices.Equal(got, want) {
		t.Fatalf("access_url 主机名: got %v want %v", got, want)
	}
	got = AgentURLs("http://192.168.1.20:31415", "31418", ips)
	if want := []string{"wss://192.168.1.20:31418", "wss://10.0.0.5:31418"}; !slices.Equal(got, want) {
		t.Fatalf("access_url 为 IP 时去重: got %v want %v", got, want)
	}
	// 无法解析的访问地址被忽略；IPv6 主机名要加方括号
	if got := AgentURLs("::bad", "31418", nil); len(got) != 0 {
		t.Fatalf("非法访问地址应忽略: %v", got)
	}
	if got := AgentURLs("http://[2001:db8::1]:31415", "31418", nil); !slices.Equal(got, []string{"wss://[2001:db8::1]:31418"}) {
		t.Fatalf("IPv6: %v", got)
	}
	if got := AgentURLs("", "31418", []net.IP{net.ParseIP("127.0.0.1")}); len(got) != 0 {
		t.Fatalf("无可用地址: %v", got)
	}
}
