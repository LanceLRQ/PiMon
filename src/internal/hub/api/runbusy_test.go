package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
)

// 同一实例正在运行、等不到运行锁时，保存并测试回 409 run.busy。
func TestRunBusyMapsTo409(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/instances/x/run", nil)
	writeInstanceError(rec, req, fmt.Errorf("等待运行锁: %w", instances.ErrRunBusy))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"run.busy"`) {
		t.Fatalf("run.busy 映射错误: %d %s", rec.Code, rec.Body.String())
	}
}
