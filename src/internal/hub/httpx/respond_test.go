package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const jsonCT = "application/json; charset=utf-8"

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusCreated, map[string]int{"a": 1})
	if w.Code != 201 || w.Header().Get("Content-Type") != jsonCT {
		t.Errorf("code=%d ct=%q", w.Code, w.Header().Get("Content-Type"))
	}
	if strings.TrimSpace(w.Body.String()) != `{"a":1}` {
		t.Errorf("body=%q", w.Body.String())
	}
}

func TestWriteErrorShape(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusNotFound, CodeNotFound, nil)
	if w.Code != 404 || w.Header().Get("Content-Type") != jsonCT {
		t.Errorf("code=%d ct=%q", w.Code, w.Header().Get("Content-Type"))
	}
	if strings.TrimSpace(w.Body.String()) != `{"error":{"code":"not_found","details":{}}}` {
		t.Errorf("空 details 应输出 {}: %q", w.Body.String())
	}

	w = httptest.NewRecorder()
	WriteError(w, 401, CodeInvalidPassword, map[string]any{"remaining": 3})
	if strings.TrimSpace(w.Body.String()) != `{"error":{"code":"auth.invalid_password","details":{"remaining":3}}}` {
		t.Errorf("body=%q", w.Body.String())
	}
}

func TestWriteValidationFailed(t *testing.T) {
	w := httptest.NewRecorder()
	WriteValidationFailed(w, model.FieldErrors{"a.b": "required"})
	if w.Code != 400 {
		t.Errorf("code=%d", w.Code)
	}
	var got struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Fields map[string]string `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Code != "validation.failed" || got.Error.Details.Fields["a.b"] != "required" {
		t.Errorf("got %+v", got)
	}
}

func TestWriteLocked(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		d     time.Duration
		want  string
		until string
	}{
		{15 * time.Minute, "900", "2026-01-01T12:15:00Z"},
		{1500 * time.Millisecond, "2", "2026-01-01T12:00:02Z"},
		{1 * time.Millisecond, "1", "2026-01-01T12:00:01Z"},
		{0, "1", "2026-01-01T12:00:01Z"},
		{-time.Second, "1", "2026-01-01T12:00:01Z"},
	}
	for _, tc := range tests {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/login", nil)
		r.RemoteAddr = "192.168.1.35:5555"
		WithRequestInfo(func() []netip.Prefix { return nil })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			WriteLocked(w, r, now, tc.d)
		})).ServeHTTP(w, r)
		if w.Code != 429 || w.Header().Get("Retry-After") != tc.want {
			t.Errorf("%v: code=%d Retry-After=%q", tc.d, w.Code, w.Header().Get("Retry-After"))
		}
		want := `{"error":{"code":"auth.locked","details":{"client_ip":"192.168.1.35","locked_until":"` + tc.until + `","retry_after_seconds":` + tc.want + `}}}`
		if strings.TrimSpace(w.Body.String()) != want {
			t.Errorf("%v: body=%q", tc.d, w.Body.String())
		}
	}
}

func TestDecodeJSON(t *testing.T) {
	type dst struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{"合法", `{"name":"x"}`, true},
		{"合法带尾随空白", "{\"name\":\"x\"}\n  ", true},
		{"未知字段", `{"name":"x","extra":1}`, false},
		{"尾随多余 JSON", `{"name":"x"}{"name":"y"}`, false},
		{"尾随垃圾", `{"name":"x"} x`, false},
		{"语法错误", `{"name":`, false},
		{"空体", ``, false},
		{"类型错误", `{"name":1}`, false},
		{"超过 1 MiB", `{"name":"` + strings.Repeat("a", 1<<20) + `"}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			var d dst
			err := DecodeJSON(httptest.NewRecorder(), r, &d)
			if tc.ok {
				if err != nil || d.Name != "x" {
					t.Errorf("err=%v d=%+v", err, d)
				}
				return
			}
			if !errors.Is(err, ErrInvalidJSON) {
				t.Errorf("err=%v, 应可用 errors.Is(ErrInvalidJSON) 识别", err)
			}
		})
	}
}

func TestWriteInvalidJSON(t *testing.T) {
	w := httptest.NewRecorder()
	WriteInvalidJSON(w)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"request.invalid_json"`) {
		t.Errorf("code=%d body=%s", w.Code, w.Body.String())
	}
}
