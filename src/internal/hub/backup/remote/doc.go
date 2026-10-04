// Package remote 负责把加密后的备份上传到远端目标（WebDAV、SMB），并在远端保留最近 N 份。
//
// Target 是目标协议实现需要满足的接口；Uploader 负责加密、临时名上传、改名、残留与过期清理。
package remote
