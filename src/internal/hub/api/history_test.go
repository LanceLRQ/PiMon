package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func historyPath(id string, kv ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	return "/api/instances/" + id + "/history?" + q.Encode()
}

func TestInstanceHistoryRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do(e.newClient(), "GET", historyPath("x", "item", "temp", "range", "1h"), nil)
	e.expectError(resp, data, http.StatusUnauthorized, "auth.required")
}

func TestInstanceHistoryQuery(t *testing.T) {
	e, admin := newInstEnv(t)
	d := createInst(t, e, admin, "a")

	// 运行一次（采集到 temp=20），写盘后按 1h 查询走原始档
	if resp, data := e.do(admin, "POST", "/api/instances/"+d.ID+"/run", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("run = %d %s", resp.StatusCode, data)
	}
	if err := e.deps.History.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	resp, data := e.do(admin, "GET", historyPath(d.ID, "item", "temp", "range", "1h"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET history = %d %s", resp.StatusCode, data)
	}
	res := decode[model.HistoryResult](t, data)
	if res.Tier != "raw" || res.Field != "value" || res.InstanceID != d.ID || len(res.Points) != 1 || res.Points[0].Avg != 20 {
		t.Fatalf("结果 = %+v", res)
	}

	// 删除实例后历史随之级联删除
	other := createInst(t, e, admin, "b")
	if resp, data := e.do(admin, "POST", "/api/instances/"+other.ID+"/run", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("run = %d %s", resp.StatusCode, data)
	}
	if err := e.deps.History.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM history_raw WHERE instance_id = ?`, other.ID).Scan(&before); err != nil || before != 1 {
		t.Fatalf("删除前 raw = %d %v", before, err)
	}
	if resp, data := e.do(admin, "DELETE", "/api/instances/"+other.ID, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE = %d %s", resp.StatusCode, data)
	}
	var after int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM history_raw WHERE instance_id = ?`, other.ID).Scan(&after); err != nil || after != 0 {
		t.Fatalf("删除后 raw = %d %v", after, err)
	}

	// 大范围选 1h 档，空结果是 [] 而不是 null
	e.clk.Advance(time.Minute)
	resp, data = e.do(admin, "GET", historyPath(d.ID, "item", "temp", "field", "value", "range", "365d"), nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"tier":"1h"`) || !strings.Contains(string(data), `"points":[]`) {
		t.Fatalf("365d = %d %s", resp.StatusCode, data)
	}

	// 校验失败与实例不存在
	for _, c := range []struct{ name, path string }{
		{"range 缺失", historyPath(d.ID, "item", "temp")},
		{"range 非法", historyPath(d.ID, "item", "temp", "range", "abc")},
		{"超出 1h 档保留期", historyPath(d.ID, "item", "temp", "range", "400d")},
		{"item 缺失", historyPath(d.ID, "range", "1h")},
		{"field 非法", historyPath(d.ID, "item", "temp", "field", "unit", "range", "1h")},
	} {
		resp, data := e.do(admin, "GET", c.path, nil)
		e.expectError(resp, data, http.StatusBadRequest, "validation.failed")
	}
	resp, data = e.do(admin, "GET", historyPath("nope", "item", "temp", "range", "1h"), nil)
	e.expectError(resp, data, http.StatusNotFound, "instance.not_found")
}
