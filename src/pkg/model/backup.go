package model

import "time"

// BackupInfo 描述一份本地备份。
type BackupInfo struct {
	// Name 是备份文件名，同时是下载时的标识。
	Name string `json:"name"`
	// Reason 是备份原因：daily | manual | pre-upgrade。
	Reason string `json:"reason"`
	// CreatedAt 是备份时间（UTC，取自文件名）。
	CreatedAt time.Time `json:"created_at"`
	// Size 是文件大小，单位字节。
	Size int64 `json:"size"`
}
