package instances

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
)

// secretManifest 是带密钥与代理字段的 agent 插件（interval 45s、timeout 7s）。
const secretManifest = `id: withsecret
version: %VER%
api_version: 1
name: WithSecret
kind: source
runtime: exec
runs_on: [agent]
interval: 45s
timeout: 7s
config_schema:
  - {key: host, type: string, title: Host, required: true}
  - {key: token, type: secret, title: Token}
  - {key: via, type: proxy, title: Proxy}
outputs:
  - {key: temp, type: number, title: Temp}
`

func secretManifestV(t *testing.T, ver string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(strings.ReplaceAll(secretManifest, "%VER%", ver)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// recNotify 记录实例变化通知。
type recNotify struct {
	mu  sync.Mutex
	ids []string
}

func (r *recNotify) add(id string) {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
}

func (r *recNotify) count(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, x := range r.ids {
		if x == id {
			n++
		}
	}
	return n
}

func specByID(specs []RemoteSpec, id string) (RemoteSpec, bool) {
	for _, s := range specs {
		if s.ID == id {
			return s, true
		}
	}
	return RemoteSpec{}, false
}

func TestRemoteSpecs组装可运行实例(t *testing.T) {
	mf := newMachineFx(t)
	mf.machines.put(devServer, secretManifestV(t, "1.0.0"))
	px, _ := proxy.Parse("http://u:pw@127.0.0.1:8080")
	mf.px.known["p1"] = px

	a, err := mf.createOn(devServer, "withsecret", "a", map[string]any{"host": "h1", "token": "s3cret", "via": "p1"})
	must(t, err)
	b, err := mf.svc.Create(bg, mkInput(devServer, "agentonly", "b", map[string]any{"host": "h2"}, 60))
	must(t, err)
	paused, err := mf.createOn(devServer, "agentonly", "paused", map[string]any{"host": "h3"})
	must(t, err)
	_, err = mf.svc.Pause(bg, paused.ID)
	must(t, err)
	other, err := mf.createOn(devVPS, "agentonly", "other", map[string]any{"host": "h4"})
	must(t, err)
	hubInst := mf.create("probe", "hub", probeCfg("x"))

	specs, err := mf.svc.RemoteSpecs(bg, devServer)
	must(t, err)
	if len(specs) != 2 {
		t.Fatalf("应只有 2 个可运行实例，得到 %d", len(specs))
	}
	for _, id := range []string{paused.ID, other.ID, hubInst.ID} {
		if _, ok := specByID(specs, id); ok {
			t.Fatalf("实例 %s 不应出现在 srv1 的下发里", id)
		}
	}
	sa, ok := specByID(specs, a.ID)
	if !ok {
		t.Fatal("缺少实例 a")
	}
	if sa.PluginID != "withsecret" || sa.Config["host"] != "h1" || sa.Secrets["token"] != "s3cret" {
		t.Fatalf("实例 a 的配置/密钥不对: plugin=%s cfg=%v secret_keys=%d", sa.PluginID, sa.Config, len(sa.Secrets))
	}
	if _, leaked := sa.Config["token"]; leaked {
		t.Fatal("密钥不应出现在普通配置里")
	}
	if sa.ProxyURL != px.URL() {
		t.Fatal("代理 URL 应为解析后的完整 URL")
	}
	if sa.Interval != 45*time.Second || sa.Timeout != 7*time.Second {
		t.Fatalf("间隔/超时 = %v/%v", sa.Interval, sa.Timeout)
	}
	if sa.ConfigHash == "" {
		t.Fatal("ConfigHash 不应为空")
	}
	var rowHash string
	must(t, mf.db.QueryRow(`SELECT config_hash FROM plugin_instances WHERE id = ?`, a.ID).Scan(&rowHash))
	if sa.ConfigHash == rowHash {
		t.Fatal("下发 hash 应与库里的内容摘要不同（含明文配置、间隔、超时、代理）")
	}
	if sa.ContentHash != rowHash {
		t.Fatal("ContentHash 应为组装时实例行的 config_hash")
	}
	sb, _ := specByID(specs, b.ID)
	if sb.Interval != 60*time.Second || sb.ProxyURL != "" || len(sb.Secrets) != 0 {
		t.Fatalf("实例 b = %+v", sb)
	}

	// 再取一次：同样输入得到同样结果
	specs2, err := mf.svc.RemoteSpecs(bg, devServer)
	must(t, err)
	if len(specs2) != 2 {
		t.Fatalf("同样输入应得到同样结果，得到 %d", len(specs2))
	}
	sa2, _ := specByID(specs2, a.ID)
	if sa2.ConfigHash != sa.ConfigHash {
		t.Fatal("同样输入的下发 hash 应稳定")
	}
}

func mkInput(machine, plugin, name string, cfg map[string]any, interval int) model.InstanceInput {
	return model.InstanceInput{PluginID: plugin, Name: name, Config: cfg, RunsOn: machine, IntervalSeconds: interval}
}

func TestRemoteHash随配置版本代理变化(t *testing.T) {
	mf := newMachineFx(t)
	mf.machines.put(devServer, secretManifestV(t, "1.0.0"))
	px1, _ := proxy.Parse("http://127.0.0.1:8080")
	mf.px.known["p1"] = px1
	a, err := mf.createOn(devServer, "withsecret", "a", map[string]any{"host": "h1", "token": "s", "via": "p1"})
	must(t, err)
	hashOf := func() string {
		t.Helper()
		specs, err := mf.svc.RemoteSpecs(bg, devServer)
		must(t, err)
		s, ok := specByID(specs, a.ID)
		if !ok {
			t.Fatal("实例应在下发里")
		}
		return s.ConfigHash
	}
	h0 := hashOf()

	// 设备 manifest 版本变化
	mf.machines.put(devServer, secretManifestV(t, "1.1.0"))
	h1 := hashOf()
	if h1 == h0 {
		t.Fatal("设备插件版本变化应改变下发 hash")
	}
	// 代理内容变化（同一 id）
	px2, _ := proxy.Parse("http://127.0.0.1:9090")
	mf.px.mu.Lock()
	mf.px.known["p1"] = px2
	mf.px.mu.Unlock()
	h2 := hashOf()
	if h2 == h1 {
		t.Fatal("代理 URL 变化应改变下发 hash")
	}
	// 只改密钥
	_, err = mf.svc.Update(bg, a.ID, mkInput("", "", "a", map[string]any{"host": "h1", "token": "t2", "via": "p1"}, 0))
	must(t, err)
	h3 := hashOf()
	if h3 == h2 {
		t.Fatal("密钥变化应改变下发 hash")
	}
	// 只改名称不改变运行内容
	_, err = mf.svc.Update(bg, a.ID, mkInput("", "", "a2", map[string]any{"host": "h1", "via": "p1"}, 0))
	must(t, err)
	if hashOf() != h3 {
		t.Fatal("只改名称不应改变下发 hash")
	}
	// 间隔覆盖
	_, err = mf.svc.Update(bg, a.ID, mkInput("", "", "a2", map[string]any{"host": "h1", "via": "p1"}, 90))
	must(t, err)
	if hashOf() == h3 {
		t.Fatal("间隔变化应改变下发 hash")
	}
}

func TestRemoteSpecs代理已删除不下发且只记一次失败(t *testing.T) {
	mf := newMachineFx(t)
	px, _ := proxy.Parse("http://127.0.0.1:8080")
	mf.px.known["p1"] = px
	a, err := mf.createOn(devServer, "agentonly", "a", map[string]any{"host": "h1", "via": "p1"})
	must(t, err)
	mf.px.mu.Lock()
	delete(mf.px.known, "p1")
	mf.px.mu.Unlock()

	for range 3 {
		specs, err := mf.svc.RemoteSpecs(bg, devServer)
		must(t, err)
		if _, ok := specByID(specs, a.ID); ok {
			t.Fatal("代理已删除的实例不应下发")
		}
	}
	v, err := mf.svc.View(bg, a.ID)
	must(t, err)
	if v.Failures != 1 || !strings.Contains(v.LastError, "代理已删除") {
		t.Fatalf("应只记一次代理已删除的失败: failures=%d err=%q", v.Failures, v.LastError)
	}
	// 代理恢复后再删除，重新记一次
	mf.px.mu.Lock()
	mf.px.known["p1"] = px
	mf.px.mu.Unlock()
	specs, err := mf.svc.RemoteSpecs(bg, devServer)
	must(t, err)
	if _, ok := specByID(specs, a.ID); !ok {
		t.Fatal("代理恢复后应下发")
	}
	mf.px.mu.Lock()
	delete(mf.px.known, "p1")
	mf.px.mu.Unlock()
	_, err = mf.svc.RemoteSpecs(bg, devServer)
	must(t, err)
	v, err = mf.svc.View(bg, a.ID)
	must(t, err)
	if v.Failures != 2 {
		t.Fatalf("代理再次被删除应再记一次失败: failures=%d", v.Failures)
	}
}

func TestSetAssignIssue写入实例问题(t *testing.T) {
	mf := newMachineFx(t)
	rec := &recNotify{}
	mf.svc.OnChange(rec.add)
	a, err := mf.createOn(devServer, "agentonly", "a", map[string]any{"host": "h1"})
	must(t, err)
	before := rec.count(a.ID)

	mf.svc.SetAssignIssue(devServer, a.ID, "设备上没有该插件：missing")
	v, err := mf.svc.View(bg, a.ID)
	must(t, err)
	if v.DisplayState != "broken" || v.Issue != "设备上没有该插件：missing" {
		t.Fatalf("应显示为引用失效并带原因: state=%s issue=%q", v.DisplayState, v.Issue)
	}
	if rec.count(a.ID) != before+1 {
		t.Fatal("写入问题应通知实例变化")
	}
	// 同样的问题重复写入不再通知
	mf.svc.SetAssignIssue(devServer, a.ID, "设备上没有该插件：missing")
	if rec.count(a.ID) != before+1 {
		t.Fatal("问题未变化不应重复通知")
	}
	d, err := mf.svc.Get(bg, a.ID)
	must(t, err)
	if d.Issue != "设备上没有该插件：missing" {
		t.Fatalf("实例详情应带回执问题: %q", d.Issue)
	}

	// 其他设备的回执问题不影响本实例（例如实例已改到别的设备）
	mf.svc.SetAssignIssue(devVPS, a.ID, "别的设备")
	v, _ = mf.svc.View(bg, a.ID)
	if v.Issue != "设备上没有该插件：missing" {
		t.Fatalf("运行位置不符的问题不应覆盖: %q", v.Issue)
	}
	mf.svc.SetAssignIssue(devServer, a.ID, "设备上没有该插件：missing")

	// 暂停的实例不展示回执问题（它已不在下发里）
	_, err = mf.svc.Pause(bg, a.ID)
	must(t, err)
	v, _ = mf.svc.View(bg, a.ID)
	if v.Issue != "" || v.DisplayState == "broken" {
		t.Fatalf("暂停实例不应展示回执问题: state=%s issue=%q", v.DisplayState, v.Issue)
	}
	_, err = mf.svc.Resume(bg, a.ID)
	must(t, err)

	// 清除
	mf.svc.SetAssignIssue(devServer, a.ID, "")
	v, _ = mf.svc.View(bg, a.ID)
	if v.Issue != "" || v.DisplayState == "broken" {
		t.Fatalf("清除后不应再有问题: state=%s issue=%q", v.DisplayState, v.Issue)
	}

	// 删除实例后清理
	mf.svc.SetAssignIssue(devServer, a.ID, "x")
	_, err = mf.svc.Delete(bg, a.ID)
	must(t, err)
	mf.svc.mu.Lock()
	_, left := mf.svc.assignIssues[a.ID]
	mf.svc.mu.Unlock()
	if left {
		t.Fatal("删除实例应清掉回执问题")
	}
}
