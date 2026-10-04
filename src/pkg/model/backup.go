package model

import (
	"strings"
	"time"
)

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

// 远端备份目标类型。
const (
	BackupRemoteSMB    = "smb"
	BackupRemoteWebDAV = "webdav"
)

// BackupSecretState 是密钥字段的回显形式：{"set": true}，不含明文。
type BackupSecretState struct {
	Set bool `json:"set"`
}

// BackupRemote 是远端备份目标配置（GET 与 PUT 的响应）。两种类型的字段分列保存，切换类型不丢另一类型已填的值。
type BackupRemote struct {
	Enabled bool `json:"enabled"`
	// Type 是 smb | webdav。
	Type string `json:"type"`

	SMBHost     string            `json:"smb_host"`
	SMBPort     int               `json:"smb_port"`
	SMBShare    string            `json:"smb_share"`
	SMBDir      string            `json:"smb_dir"`
	SMBUsername string            `json:"smb_username"`
	SMBPassword BackupSecretState `json:"smb_password"`
	SMBDomain   string            `json:"smb_domain"`

	WebDAVURL      string            `json:"webdav_url"`
	WebDAVDir      string            `json:"webdav_dir"`
	WebDAVUsername string            `json:"webdav_username"`
	WebDAVPassword BackupSecretState `json:"webdav_password"`

	// Passphrase 表示备份口令是否已设置；口令丢失将无法恢复已加密的备份。
	Passphrase BackupSecretState `json:"passphrase"`
	// Keep 是远端保留份数（1–365）。
	Keep int `json:"keep"`
	// UpdatedAt 为空表示从未保存过。
	UpdatedAt *time.Time `json:"updated_at"`
	// PassphraseChanged 仅在 PUT 响应中有意义：本次保存改了已有的备份口令，之后上传的备份用新口令加密。
	PassphraseChanged bool `json:"passphrase_changed"`
}

// BackupRemoteInput 是 PUT 与测试连接的请求体。密钥字段为明文字符串，留空表示保留已保存的值。
type BackupRemoteInput struct {
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`

	SMBHost     string `json:"smb_host"`
	SMBPort     int    `json:"smb_port"`
	SMBShare    string `json:"smb_share"`
	SMBDir      string `json:"smb_dir"`
	SMBUsername string `json:"smb_username"`
	SMBPassword string `json:"smb_password"`
	SMBDomain   string `json:"smb_domain"`

	WebDAVURL      string `json:"webdav_url"`
	WebDAVDir      string `json:"webdav_dir"`
	WebDAVUsername string `json:"webdav_username"`
	WebDAVPassword string `json:"webdav_password"`

	Passphrase string `json:"passphrase"`
	Keep       int    `json:"keep"`
}

// BackupUpload 是一次远端上传的记录。
type BackupUpload struct {
	ID int64 `json:"id"`
	// Name 是远端文件名（本地备份名加 .age）。
	Name string `json:"name"`
	// TargetType 是 smb | webdav。
	TargetType string    `json:"target_type"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// Size 是实际写到远端的密文字节数；失败时为已写入的字节数。
	Size    int64 `json:"size"`
	Success bool  `json:"success"`
	// Reason 是入库的原文「<机器码>:<说明>」（见 UploadReason* 常量），成功且无附注时为空；说明不含密码与口令。
	// 前端用 ReasonCode 翻译（i18n 键 backup.upload_reason.<code>），ReasonDetail 作为补充说明显示。
	Reason string `json:"reason"`
	// ReasonCode 是从 Reason 解析出的机器码；成功且无附注时为空。
	ReasonCode string `json:"reason_code"`
	// ReasonDetail 是从 Reason 解析出的说明（可为空）。
	ReasonDetail string `json:"reason_detail"`
}

// BackupUploadList 是 GET /api/backups/remote/uploads 的响应：最近的上传记录（新到旧）与「最近一次结果」。
// LastResult 与 hub-self 判定同口径：跳过因中枢关停被取消的记录，没有记录时为 null。
type BackupUploadList struct {
	Uploads    []BackupUpload `json:"uploads"`
	LastResult *BackupUpload  `json:"last_result" tstype:"BackupUpload | null,required"`
}

// 上传记录的原因机器码（两端契约，不可随意改名）。前 4 个与 backup.remote_failed 的 details.reason 相同。
const (
	UploadReasonConnect     = "connect"
	UploadReasonAuth        = "auth"
	UploadReasonNotWritable = "not_writable"
	UploadReasonIO          = "io"
	// UploadReasonCanceled 表示上传被取消（中枢关停）；它不算失败，hub-self 判定时跳过。
	UploadReasonCanceled = "canceled"
	// UploadReasonConfig 表示配置不合法，未能构造目标。
	UploadReasonConfig = "config"
	// UploadReasonNoLocal 表示本地备份缺失或不可读。
	UploadReasonNoLocal = "no_local"
	// UploadReasonLocalRead 表示上传途中读取本地备份文件出错（与远端读写失败区分）；同为 backup.remote_failed 的 details.reason。
	UploadReasonLocalRead = "local_read"
	// UploadReasonCleanupFailed 表示上传成功（Success 仍为 true），但清理远端过期备份失败。
	UploadReasonCleanupFailed = "cleanup_failed"
)

// FormatUploadReason 拼出入库的原因文本「<code>:<detail>」；code 为空时返回空串。
func FormatUploadReason(code, detail string) string {
	if code == "" {
		return ""
	}
	return code + ":" + detail
}

// ParseUploadReason 把原因文本拆成机器码与说明（按第一个半角冒号）。
func ParseUploadReason(reason string) (code, detail string) {
	code, detail, _ = strings.Cut(reason, ":")
	return code, detail
}

// FillReason 按 Reason 填充 ReasonCode 与 ReasonDetail。
func (u *BackupUpload) FillReason() {
	u.ReasonCode, u.ReasonDetail = ParseUploadReason(u.Reason)
}

// BackupRemoteTest 是测试连接成功时的响应；失败走错误码 backup.remote_failed。
type BackupRemoteTest struct {
	// Success 恒为 true（失败不返回本结构）。
	Success bool `json:"success"`
	// TargetType 是本次测试的目标类型 smb | webdav。
	TargetType string `json:"target_type"`
	// ElapsedMS 是连接、建目录、写入并删除测试文件的总耗时（毫秒）。
	ElapsedMS int64 `json:"elapsed_ms"`
}
