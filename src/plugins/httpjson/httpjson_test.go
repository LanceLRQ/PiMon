package httpjson

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

func src(t *testing.T) runtime.Source {
	t.Helper()
	s, ok := runtime.Builtin("http-json")
	if !ok {
		t.Fatal("http-json 应在 init 中注册")
	}
	return s
}

const validBody = `{"status":"warning","summary":"磁盘偏高","items":[{"key":"disk.root","type":"gauge","value":91,"unit":"%"}]}`

func TestManifestFields(t *testing.T) {
	types := map[string]schema.Type{}
	for _, f := range src(t).Manifest().ConfigSchema {
		types[f.Key] = f.Type
	}
	if types["url"] != schema.TypeURL || types["headers"] != schema.TypeKV || types["proxy"] != schema.TypeProxy {
		t.Fatalf("配置字段不符: %v", types)
	}
	if len(src(t).Manifest().Outputs) != 0 || len(src(t).Manifest().Widgets) != 0 {
		t.Fatal("http-json 不声明 outputs 与 widgets")
	}
}

func TestCollectValidReportWithHeaders(t *testing.T) {
	var gotAuth, gotPlain string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPlain = r.Header.Get("X-Plain")
		_, _ = w.Write([]byte(validBody))
	}))
	defer srv.Close()
	rep, err := src(t).Collect(context.Background(), runtime.Input{
		Config:  map[string]any{"url": srv.URL, "headers": map[string]any{"Authorization": nil, "X-Plain": "p"}},
		Secrets: map[string]string{"headers.Authorization": "Bearer abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusWarning || rep.Find("disk.root") == nil {
		t.Fatalf("报告解析错误: %+v", rep)
	}
	if gotAuth != "Bearer abc" || gotPlain != "p" {
		t.Fatalf("请求头未带上: %q %q", gotAuth, gotPlain)
	}
}

func TestCollectInvalidJSONAndInvalidReport(t *testing.T) {
	for name, body := range map[string]string{
		"非 JSON":   "<html>oops</html>",
		"缺 status": `{"items":[]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := src(t).Collect(context.Background(), runtime.Input{Config: map[string]any{"url": srv.URL}})
		srv.Close()
		if err == nil {
			t.Errorf("%s 应返回错误", name)
		}
	}
}

func TestCollectNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(validBody))
	}))
	defer srv.Close()
	_, err := src(t).Collect(context.Background(), runtime.Input{Config: map[string]any{"url": srv.URL}})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("非 2xx 应报错并带状态码: %v", err)
	}
}

func TestCollectBodyTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", maxBody+1)))
	}))
	defer srv.Close()
	_, err := src(t).Collect(context.Background(), runtime.Input{Config: map[string]any{"url": srv.URL}})
	if err == nil || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("过大响应应报错: %v", err)
	}
}

func TestCollectTimeoutViaContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := src(t).Collect(ctx, runtime.Input{Config: map[string]any{"url": srv.URL}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应为 ctx 超时: %v", err)
	}
}

func TestErrorDoesNotLeakQueryOrHeaders(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/api?token=query-secret"
	srv.Close()
	_, err := src(t).Collect(context.Background(), runtime.Input{
		Config:  map[string]any{"url": url, "headers": map[string]any{"Authorization": nil}},
		Secrets: map[string]string{"headers.Authorization": "header-secret"},
	})
	if err == nil {
		t.Fatal("连接失败应报错")
	}
	if strings.Contains(err.Error(), "query-secret") || strings.Contains(err.Error(), "header-secret") {
		t.Fatalf("错误不得含密钥: %v", err)
	}
}

func TestCollectMissingURL(t *testing.T) {
	if _, err := src(t).Collect(context.Background(), runtime.Input{}); err == nil {
		t.Fatal("缺少 url 应报错")
	}
}
