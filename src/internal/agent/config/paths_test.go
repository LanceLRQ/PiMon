package config

import "testing"

func env(goos string, euid int, user bool) Env {
	return Env{GOOS: goos, EUID: euid, Home: "/home/u", User: user, Getenv: func(string) string { return "" }}
}

func TestResolve系统模式(t *testing.T) {
	p, err := Resolve(env("linux", 0, false))
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Bin: "/usr/local/bin/pimon-agent", Config: "/etc/pimon-agent/agent.json",
		DataDir: "/var/lib/pimon-agent", PluginDir: "/var/lib/pimon-agent/plugins",
		DB: "/var/lib/pimon-agent/agent.db", CertFile: "/var/lib/pimon-agent/agent.crt",
		KeyFile: "/var/lib/pimon-agent/agent.key", CtlSock: "/run/pimon-agent/ctl.sock", System: true,
	}
	if p != want {
		t.Fatalf("got %+v", p)
	}
}

func TestResolve用户模式(t *testing.T) {
	for name, e := range map[string]Env{"darwin": env("darwin", 501, false), "linux--user": env("linux", 1000, true)} {
		p, err := Resolve(e)
		if err != nil {
			t.Fatal(name, err)
		}
		want := Paths{
			Bin: "/home/u/.pimon/bin/pimon-agent", Config: "/home/u/.pimon/agent.json",
			DataDir: "/home/u/.pimon", PluginDir: "/home/u/.pimon/plugins", DB: "/home/u/.pimon/agent.db",
			CertFile: "/home/u/.pimon/agent.crt", KeyFile: "/home/u/.pimon/agent.key",
			CtlSock: "/home/u/.pimon/agent.sock", LogFile: "/home/u/.pimon/agent.log", System: false,
		}
		if p != want {
			t.Fatalf("%s: got %+v", name, p)
		}
	}
}

func TestResolve用户模式缺Home报错(t *testing.T) {
	e := env("darwin", 501, false)
	e.Home = ""
	if _, err := Resolve(e); err == nil {
		t.Fatal("应报错")
	}
}

func TestUserMode判定(t *testing.T) {
	if !env("darwin", 0, false).UserMode() || !env("linux", 0, true).UserMode() || env("linux", 0, false).UserMode() {
		t.Fatal("判定错误")
	}
}
