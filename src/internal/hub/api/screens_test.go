package api

import (
	"net/http"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func apiGenericValue(id string, col int, instanceID string) model.LayoutWidget {
	return model.LayoutWidget{
		ID: id, Source: model.WidgetSourceGeneric, Template: "value",
		Size: model.WidgetSize{Cols: 1, Rows: 1}, Col: col,
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{{InstanceID: instanceID, Item: "temp"}}},
	}
}

func apiLayout(ws ...model.LayoutWidget) model.Layout {
	return model.Layout{
		Grid:    model.Grid{Cols: 6, Rows: 4},
		Screens: []model.LayoutScreen{{ID: "index", Name: "首页", InRotation: true, Widgets: ws}},
	}
}

func putLayout(e *env, c *http.Client, base int, l model.Layout) (*http.Response, []byte) {
	return e.do(c, "PUT", "/api/screens", model.LayoutSaveRequest{BaseVersion: base, Layout: l})
}

func TestScreensRoutesRequireAdmin(t *testing.T) {
	e := newEnv(t)
	anon := e.newClient()
	routes := []struct{ m, p string }{
		{"GET", "/api/screens"}, {"PUT", "/api/screens"}, {"POST", "/api/screens/rollback"},
		{"GET", "/api/screens/versions"}, {"GET", "/api/screens/versions/1"},
		{"GET", "/api/screens/catalog"}, {"GET", "/api/screens/resolved"},
	}
	for _, rt := range routes {
		resp, data := e.do(anon, rt.m, rt.p, map[string]any{})
		e.expectError(resp, data, http.StatusUnauthorized, "auth.required")
	}
	// 屏幕会话不是管理员
	sc := e.newClient()
	if resp, _ := e.do(sc, "GET", "/screen/auth?token="+e.screenToken(), nil); resp.StatusCode != http.StatusFound {
		t.Fatalf("screen/auth = %d", resp.StatusCode)
	}
	for _, rt := range routes {
		resp, data := e.do(sc, rt.m, rt.p, map[string]any{})
		e.expectError(resp, data, http.StatusForbidden, "auth.forbidden")
	}
}

func TestLayoutSaveReadRollbackFlow(t *testing.T) {
	e, admin := newInstEnv(t)
	inst := createInst(t, e, admin, "a")

	// 尚无版本
	resp, data := e.do(admin, "GET", "/api/screens", nil)
	st := decode[model.LayoutState](t, data)
	if resp.StatusCode != http.StatusOK || st.Version != 0 || len(st.Layout.Screens) != 1 {
		t.Fatalf("初始 = %d %s", resp.StatusCode, data)
	}

	resp, data = putLayout(e, admin, 0, apiLayout(apiGenericValue("w1", 0, inst.ID)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	v1 := decode[model.LayoutState](t, data)
	if v1.Version != 1 || v1.Source != "edit" || len(v1.Broken) != 0 {
		t.Fatalf("v1 = %+v", v1)
	}
	resp, data = putLayout(e, admin, 1, apiLayout(apiGenericValue("w1", 0, inst.ID), apiGenericValue("w2", 1, inst.ID)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT v2 = %d %s", resp.StatusCode, data)
	}

	// 版本列表与单版本
	resp, data = e.do(admin, "GET", "/api/screens/versions", nil)
	vs := decode[[]model.LayoutVersionInfo](t, data)
	if resp.StatusCode != http.StatusOK || len(vs) != 2 || vs[0].Version != 2 || vs[0].Summary.WidgetsAdded != 1 {
		t.Fatalf("versions = %d %s", resp.StatusCode, data)
	}
	resp, data = e.do(admin, "GET", "/api/screens/versions/1", nil)
	got := decode[model.LayoutState](t, data)
	if resp.StatusCode != http.StatusOK || got.Version != 1 || len(got.Layout.Screens[0].Widgets) != 1 {
		t.Fatalf("version 1 = %d %s", resp.StatusCode, data)
	}
	for _, p := range []string{"/api/screens/versions/99", "/api/screens/versions/abc"} {
		resp, data = e.do(admin, "GET", p, nil)
		e.expectError(resp, data, http.StatusNotFound, "not_found")
	}

	// 回滚生成新版本
	resp, data = e.do(admin, "POST", "/api/screens/rollback", model.LayoutRollbackRequest{Version: 1})
	rb := decode[model.LayoutState](t, data)
	if resp.StatusCode != http.StatusOK || rb.Version != 3 || rb.Source != "rollback" || len(rb.Layout.Screens[0].Widgets) != 1 {
		t.Fatalf("rollback = %d %s", resp.StatusCode, data)
	}
	resp, data = e.do(admin, "POST", "/api/screens/rollback", model.LayoutRollbackRequest{Version: 99})
	e.expectError(resp, data, http.StatusNotFound, "not_found")
}

func TestLayoutConflictAndInvalidResponses(t *testing.T) {
	e, admin := newInstEnv(t)
	inst := createInst(t, e, admin, "a")
	if resp, data := putLayout(e, admin, 0, apiLayout()); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}

	resp, data := putLayout(e, admin, 0, apiLayout())
	er := e.expectError(resp, data, http.StatusConflict, "layout.conflict")
	if er.Error.Details["latest_version"] != float64(1) {
		t.Fatalf("details = %v", er.Error.Details)
	}

	overlap := apiLayout(apiGenericValue("a", 0, inst.ID), apiGenericValue("b", 0, inst.ID))
	resp, data = putLayout(e, admin, 1, overlap)
	er = e.expectError(resp, data, http.StatusBadRequest, "layout.invalid")
	problems, _ := er.Error.Details["problems"].([]any)
	first, _ := problems[0].(map[string]any)
	if len(problems) != 1 || first["code"] != "overlap" || first["widget"] != "b" || first["with"] != "a" || first["screen"] != "index" {
		t.Fatalf("problems = %v", problems)
	}

	resp, data = e.do(admin, "PUT", "/api/screens", map[string]any{"base_version": "x"})
	e.expectError(resp, data, http.StatusBadRequest, "request.invalid_json")
}

func TestInstanceDeleteWithScreenReferences(t *testing.T) {
	e, admin := newInstEnv(t)
	inst := createInst(t, e, admin, "a")
	if resp, data := putLayout(e, admin, 0, apiLayout(apiGenericValue("w1", 0, inst.ID))); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}

	resp, data := e.do(admin, "DELETE", "/api/instances/"+inst.ID, nil)
	er := e.expectError(resp, data, http.StatusConflict, "instance.in_use")
	screensList, _ := er.Error.Details["screens"].([]any)
	if len(screensList) != 1 || screensList[0].(map[string]any)["id"] != "index" {
		t.Fatalf("details = %v", er.Error.Details)
	}

	resp, data = e.do(admin, "DELETE", "/api/instances/"+inst.ID+"?confirm=1", nil)
	res := decode[model.InstanceDeleteResult](t, data)
	if resp.StatusCode != http.StatusOK || len(res.AffectedScreens) != 1 || res.AffectedScreens[0].Name != "首页" {
		t.Fatalf("confirm 删除 = %d %s", resp.StatusCode, data)
	}

	// 删除后小组件变成引用失效，解析布局标为 broken
	_, data = e.do(admin, "GET", "/api/screens", nil)
	st := decode[model.LayoutState](t, data)
	if len(st.Broken) != 1 || st.Broken[0].Widget != "w1" || st.Broken[0].Code != model.LayoutBrokenInstanceMissing {
		t.Fatalf("broken = %+v", st.Broken)
	}
	_, data = e.do(admin, "GET", "/api/screens/resolved?lang=zh", nil)
	rl := decode[model.ResolvedLayout](t, data)
	if len(rl.Screens) != 1 || rl.Screens[0].Widgets[0].DisplayState != "broken" {
		t.Fatalf("resolved = %s", data)
	}
}

func TestWidgetCatalogEndpoint(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	resp, data := e.do(admin, "GET", "/api/screens/catalog", nil)
	c := decode[model.WidgetCatalog](t, data)
	if resp.StatusCode != http.StatusOK || len(c.Generic) == 0 || len(c.Aggregate) == 0 {
		t.Fatalf("catalog = %d %s", resp.StatusCode, data)
	}
}
