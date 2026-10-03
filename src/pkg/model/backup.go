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
	// Reason 在失败时给出原因「<connect|auth|not_writable|io>：<消息>」（不含密码与口令）；
	// 成功时通常为空，上传成功但清理远端过期备份失败时注明清理失败。
	Reason string `json:"reason"`
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
