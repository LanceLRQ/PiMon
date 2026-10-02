package screens

import (
	"errors"
	"fmt"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// ErrVersionNotFound 表示请求的布局版本不存在（含已被淘汰的）。
var ErrVersionNotFound = errors.New("布局版本不存在")

// InvalidError 表示布局不合法，Problems 列出全部问题（几何、尺寸与结构）。
type InvalidError struct {
	Problems []model.LayoutProblem
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("布局不合法: %d 个问题", len(e.Problems))
}

// ConflictError 表示 base_version 与服务端当前版本不一致，Latest 是服务端的最新版本号。
type ConflictError struct {
	Latest int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("布局版本冲突: 最新版本为 %d", e.Latest)
}
