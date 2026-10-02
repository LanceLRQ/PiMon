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
