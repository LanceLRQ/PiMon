//go:build !unix

package sudocmd

import "io/fs"

// 非 unix 平台取不到属主，一律按不安全处理。
func ownerUID(fs.FileInfo) (uint32, bool) { return 0, false }
