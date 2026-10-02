package api

import (
	"context"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/screenstate"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func nightSchedule() model.Schedule {
	return model.Schedule{Periods: []model.SchedulePeriod{
		{Start: "07:00", End: "23:00", Theme: model.ThemeAmbient},
		{Start: "23:00", End: "07:00", Theme: model.ThemeOff},
	}}
}

// screenClient 返回已换取屏幕会话的客户端。
func (e *env) screenClient() *http.Client {
	e.t.Helper()
	sc := e.newClient()
	if resp, _ := e.do(sc, "GET", "/screen/auth?token="+e.screenToken(), nil); resp.StatusCode != http.StatusFound {
		e.t.Fatalf("screen/auth = %d", resp.StatusCode)
	}
	return sc
}

func TestScreenCtlRoutesRequireAdmin(t *testing.T) {
	e := newEnv(t)
	anon := e.newClient()
	routes := []struct{ m, p string }{
		{"GET", "/api/schedule"}, {"PUT", "/api/schedule"}, {"GET", "/api/screen/status"},
		{"POST", "/api/screen/control"}, {"GET", "/api/screen/ops"},
	}
	for _, rt := range routes {
		resp, data := e.do(anon, rt.m, rt.p, map[string]any{})
		e.expectError(resp, data, http.StatusUnauthorized, "auth.required")
	}
	sc := e.screenClient()
	for _, rt := range routes {
		resp, data := e.do(sc, rt.m, rt.p, map[string]any{})
		e.expectError(resp, data, http.StatusForbidden, "auth.forbidden")
	}
}

func TestScheduleGetPut(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()

	resp, data := e.do(admin, "GET", "/api/schedule", nil)
	def := decode[model.Schedule](t, data)
	if resp.StatusCode != http.StatusOK || len(def.Periods) != 1 || def.Periods[0].Theme != model.ThemeAmbient {
		t.Fatalf("默认计划 = %d %s", resp.StatusCode, data)
	}

	resp, data = e.do(admin, "PUT", "/api/schedule", nightSchedule())
	if resp.StatusCode != http.StatusOK || len(decode[model.Schedule](t, data).Periods) != 2 {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	_, data = e.do(admin, "GET", "/api/schedule", nil)
	if got := decode[model.Schedule](t, data); len(got.Periods) != 2 || got.Periods[0].Start != "07:00" {
		t.Fatalf("GET = %s", data)
	}
}

func TestSchedulePutInvalid(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	overlap := model.Schedule{Periods: []model.SchedulePeriod{
		{Start: "07:00", End: "12:00", Theme: model.ThemeAmbient},
		{Start: "11:00", End: "07:00", Theme: model.ThemeOff},
	}}
	resp, data := e.do(admin, "PUT", "/api/schedule", overlap)
	er := e.expectError(resp, data, http.StatusBadRequest, "schedule.invalid")
	probs, _ := er.Error.Details["problems"].([]any)
	if len(probs) != 1 {
		t.Fatalf("details = %v", er.Error.Details)
	}
	p, _ := probs[0].(map[string]any)
	if p["kind"] != "overlap" || p["from"] != "11:00" || p["to"] != "12:00" {
		t.Fatalf("problem = %v", p)
	}
	gap := model.Schedule{Periods: []model.SchedulePeriod{{Start: "08:00", End: "20:00", Theme: model.ThemeAmbient}}}
	resp, data = e.do(admin, "PUT", "/api/schedule", gap)
	e.expectError(resp, data, http.StatusBadRequest, "schedule.invalid")
	badTheme := model.Schedule{Periods: []model.SchedulePeriod{{Start: "00:00", End: "00:00", Theme: "neon"}}}
	resp, data = e.do(admin, "PUT", "/api/schedule", badTheme)
	e.expectError(resp, data, http.StatusBadRequest, "schedule.invalid")
	resp, data = e.do(admin, "PUT", "/api/schedule", nil)
	e.expectError(resp, data, http.StatusBadRequest, "request.invalid_json")
}

func TestScreenStatusRecommendsGridFromAdoptedViewport(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	e.deps.ScreenState.ReportViewport(model.Viewport{W: 1280, H: 720, DPR: 1})
	e.deps.ScreenState.SetScreenOnline(true)
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.clk.Advance(screenstate.ViewportStableFor)
		_, data := e.do(admin, "GET", "/api/screen/status", nil)
		st := decode[model.ScreenStatus](t, data)
		if st.Viewport != nil {
			if st.RecommendedGrid == nil || *st.RecommendedGrid != (model.Grid{Cols: 10, Rows: 6}) || !st.Online {
				t.Fatalf("status = %s", data)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("viewport 应在稳定 2 秒后被采信")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestScreenControlFlow(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	// 测试时钟为 UTC 00:00 = 上海 08:00。
	if resp, data := e.do(admin, "PUT", "/api/schedule", nightSchedule()); resp.StatusCode != 200 {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	_, data := e.do(admin, "GET", "/api/screen/status", nil)
	st := decode[model.ScreenStatus](t, data)
	if st.State.Mode != "on" || st.State.ThemeID != "ambient" || st.State.NextChange == nil || st.Viewport != nil {
		t.Fatalf("status = %s", data)
	}

	resp, data := e.do(admin, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "off"})
	cr := decode[model.ScreenControlResponse](t, data)
	if resp.StatusCode != 200 || cr.State.Mode != "off" || cr.State.Reason != "remote_off" || cr.Op.Action != "off" || cr.Op.ClientIP == "" {
		t.Fatalf("control off = %d %s", resp.StatusCode, data)
	}
	_, data = e.do(admin, "GET", "/api/screen/status", nil)
	if decode[model.ScreenStatus](t, data).State.Mode != "off" {
		t.Fatalf("status = %s", data)
	}

	resp, data = e.do(admin, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "wake", Minutes: 15})
	cr = decode[model.ScreenControlResponse](t, data)
	if resp.StatusCode != 200 || cr.State.Mode != "on" || cr.State.Until == nil {
		t.Fatalf("control wake = %d %s", resp.StatusCode, data)
	}
	if resp, data = e.do(admin, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "refresh"}); resp.StatusCode != 200 {
		t.Fatalf("refresh = %d %s", resp.StatusCode, data)
	}
	if resp, data = e.do(admin, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "switch", ScreenID: "index"}); resp.StatusCode != 200 {
		t.Fatalf("switch = %d %s", resp.StatusCode, data)
	}

	_, data = e.do(admin, "GET", "/api/screen/ops?limit=3", nil)
	ops := decode[[]model.ScreenOp](t, data)
	if len(ops) != 3 || ops[0].Action != "switch" || ops[1].Action != "refresh" || ops[2].Action != "wake" {
		t.Fatalf("ops = %s", data)
	}
	if ops[0].Params["screen_id"] != "index" || ops[0].Delivered {
		t.Fatalf("switch 记录 = %+v", ops[0])
	}
}

func TestScreenControlValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	cases := []struct {
		name  string
		req   model.ScreenControlRequest
		field string
		code  string
	}{
		{"未知动作", model.ScreenControlRequest{Action: "explode"}, "action", "invalid"},
		{"switch 缺 screen_id", model.ScreenControlRequest{Action: "switch"}, "screen_id", "required"},
		{"switch 不存在的 screen", model.ScreenControlRequest{Action: "switch", ScreenID: "nope"}, "screen_id", "invalid"},
		{"wake 分钟过大", model.ScreenControlRequest{Action: "wake", Minutes: 5000}, "minutes", "out_of_range"},
	}
	for _, c := range cases {
		resp, data := e.do(admin, "POST", "/api/screen/control", c.req)
		er := e.expectError(resp, data, http.StatusBadRequest, "validation.failed")
		fields, _ := er.Error.Details["fields"].(map[string]any)
		if fields[c.field] != c.code {
			t.Errorf("%s: details = %v", c.name, er.Error.Details)
		}
	}
	_, data := e.do(admin, "GET", "/api/screen/ops", nil)
	if ops := decode[[]model.ScreenOp](t, data); len(ops) != 0 {
		t.Fatalf("校验失败不应写记录: %s", data)
	}
	resp, data := e.do(admin, "POST", "/api/screen/control", nil)
	e.expectError(resp, data, http.StatusBadRequest, "request.invalid_json")
}

func TestScreenTokenResetRecordsOp(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	if resp, _ := e.do(admin, "POST", "/api/screen/token/reset", nil); resp.StatusCode != 204 {
		t.Fatalf("reset = %d", resp.StatusCode)
	}
	_, data := e.do(admin, "GET", "/api/screen/ops", nil)
	ops := decode[[]model.ScreenOp](t, data)
	if len(ops) != 1 || ops[0].Action != "token_reset" || ops[0].ClientIP == "" || !ops[0].Delivered {
		t.Fatalf("ops = %s", data)
	}
}

func TestSetupCodeReveal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sc := e.screenClient()

	// 没有有效设置码：404 not_found
	resp, data := e.do(sc, "GET", "/api/screen/setup-code", nil)
	e.expectError(resp, data, http.StatusNotFound, "not_found")

	code, exp, err := e.deps.SetupCodes.Generate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resp, data = e.do(sc, "GET", "/api/screen/setup-code", nil)
	got := decode[model.SetupCodeReveal](t, data)
	if resp.StatusCode != 200 || got.Code != code || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("reveal = %d %s", resp.StatusCode, data)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("明文设置码响应必须 no-store，得到 %q", cc)
	}

	// 未登录与管理员会话都不能取
	resp, data = e.do(e.newClient(), "GET", "/api/screen/setup-code", nil)
	e.expectError(resp, data, http.StatusUnauthorized, "auth.required")

	// 完成设置后：设置码已消费，屏幕会话取到 409 setup.already_done
	admin := e.newClient()
	if resp, data := e.do(admin, "POST", "/api/setup", map[string]any{"setup_code": code, "password": testPassword}); resp.StatusCode != 200 {
		t.Fatalf("setup = %d %s", resp.StatusCode, data)
	}
	resp, data = e.do(admin, "GET", "/api/screen/setup-code", nil)
	e.expectError(resp, data, http.StatusForbidden, "auth.forbidden")
	resp, data = e.do(sc, "GET", "/api/screen/setup-code", nil)
	e.expectError(resp, data, http.StatusConflict, "setup.already_done")
}

func TestSetupCodeRevealOldCodeNotDisplayable(t *testing.T) {
	e := newEnv(t)
	sc := e.screenClient()
	if _, _, err := e.deps.SetupCodes.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(context.Background(), `UPDATE setup_codes SET code_enc = ''`); err != nil {
		t.Fatal(err)
	}
	resp, data := e.do(sc, "GET", "/api/screen/setup-code", nil)
	e.expectError(resp, data, http.StatusNotFound, "not_found")
}

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
	got := candidateURLs("", "http", "31415", ips)
	want := []string{"http://192.168.1.20:31415", "http://10.0.0.5:31415", "http://172.16.0.9:31415"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 配置了对外访问地址时排第一，去掉末尾斜杠
	got = candidateURLs("https://pimon.home/", "http", "31415", ips)
	if got[0] != "https://pimon.home" || len(got) != 3 || got[1] != "http://192.168.1.20:31415" {
		t.Fatalf("access_url 优先: %v", got)
	}
	// 没有任何可用地址
	if got := candidateURLs("", "http", "31415", []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("169.254.1.1")}); len(got) != 0 {
		t.Fatalf("回环与链路本地应被排除: %v", got)
	}
	// https 与无端口
	if got := candidateURLs("", "https", "", []net.IP{net.ParseIP("192.168.1.2")}); !slices.Equal(got, []string{"https://192.168.1.2"}) {
		t.Fatalf("无端口: %v", got)
	}
}

func TestSetupCodeRevealURLs(t *testing.T) {
	e := newEnv(t)
	sc := e.screenClient()
	if _, _, err := e.deps.SetupCodes.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, data := e.do(sc, "GET", "/api/screen/setup-code", nil)
	got := decode[model.SetupCodeReveal](t, data)
	if !slices.Equal(got.URLs, []string{"http://192.168.7.8:41999"}) {
		t.Fatalf("urls = %v", got.URLs)
	}
	// 配置了访问地址：排第一
	cur := e.deps.Settings.Get()
	cur.AccessURL = "https://pimon.home"
	if err := e.deps.Settings.Update(context.Background(), cur); err != nil {
		t.Fatal(err)
	}
	_, data = e.do(sc, "GET", "/api/screen/setup-code", nil)
	got = decode[model.SetupCodeReveal](t, data)
	if !slices.Equal(got.URLs, []string{"https://pimon.home", "http://192.168.7.8:41999"}) {
		t.Fatalf("urls = %v", got.URLs)
	}
}
