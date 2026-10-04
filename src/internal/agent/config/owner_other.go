//go:build !unix

package config

import "io/fs"

// ownerUID 在非 unix 平台上无法取得属主。
func ownerUID(fs.FileInfo) (uint32, bool) { return 0, false }
