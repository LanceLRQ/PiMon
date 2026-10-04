package remote

import (
	"context"
	"io"
)

// Entry 是远端目录中的一项。
type Entry struct {
	Name  string
	Size  int64
	IsDir bool
}

// Target 是已绑定到某个远端目录的存储目标，由 WebDAV、SMB 各自实现。
// 所有方法的错误应为 *Error（或可经 Classify 归类），文本不得含密码或带凭据的地址；
// name 一律是目录内的单个文件名，不含路径分隔符。
type Target interface {
	// EnsureDir 确保备份目录存在，不存在时逐级创建；已存在不是错误。
	EnsureDir(ctx context.Context) error
	// Put 把 r 的全部内容写成目录内的 name 文件，r 的长度事先未知。
	Put(ctx context.Context, name string, r io.Reader) error
	// Rename 把 from 改名为 to；to 已存在时覆盖。
	Rename(ctx context.Context, from, to string) error
	// List 列出目录内的直接子项（不递归）。
	List(ctx context.Context) ([]Entry, error)
	// Remove 删除目录内的文件。
	Remove(ctx context.Context, name string) error
	// Close 释放连接。
	Close() error
}
