package remote

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"regexp"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// Reason 是远端失败的原因分类，对应错误码 backup.remote_failed 的 details.reason，也是前端翻译的依据。
type Reason string

// 失败原因取值（两端契约，不可随意改名）。
const (
	// ReasonConnect 网络不可达、拨号超时、TLS 或证书校验失败。
	ReasonConnect Reason = "connect"
	// ReasonAuth 用户名或密码被拒绝。
	ReasonAuth Reason = "auth"
	// ReasonNotWritable 目录不存在且无法创建，或没有写入、改名、删除权限。
	ReasonNotWritable Reason = "not_writable"
	// ReasonIO 其余读写失败，包括无进展超时。
	ReasonIO Reason = "io"
	// ReasonLocalRead 上传途中读取本地备份文件出错（打开失败仍记 no_local）。
	ReasonLocalRead Reason = "local_read"
)

// 时间限制。连接超时由各目标实现在拨号时使用；上传没有总时长限制，只有无进展超时。
const (
	ConnectTimeout = 15 * time.Second
	StallTimeout   = 5 * time.Minute
)

// Error 是远端操作的分类错误。Message 面向用户，不含密码或带凭据的地址；
// Cause 保留底层错误供 errors.Is/As，但其文本不会出现在 Error() 里。
type Error struct {
	Reason  Reason
	Message string
	Cause   error
}

func (e *Error) Error() string {
	return fmt.Sprintf("远端备份失败（%s）：%s", e.Reason, e.Message)
}

// Unwrap 返回底层错误。
func (e *Error) Unwrap() error { return e.Cause }

// NewError 构造分类错误；message 会先清除其中带凭据的地址片段。
func NewError(reason Reason, message string, cause error) *Error {
	return &Error{Reason: reason, Message: scrub(message), Cause: cause}
}

var credentialPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]*@`)

// scrub 把 scheme://user:pass@host 里的凭据部分替换为 ***。
func scrub(s string) string { return credentialPattern.ReplaceAllString(s, "${1}***@") }

// Classify 把任意错误归入 *Error：已是 *Error 的原样返回；网络错误归 connect；
// 权限错误归 not_writable；其余归 io。调用方（取消）的 context 错误不应传入。
func Classify(err error) *Error {
	if err == nil {
		return nil
	}
	var re *Error
	if errors.As(err, &re) {
		return re
	}
	var le *LocalError
	if errors.As(err, &le) {
		// 本地文件的权限错误不是远端「不可写」。
		return NewError(ReasonIO, err.Error(), err)
	}
	var ne net.Error
	switch {
	case errors.As(err, &ne):
		return NewError(ReasonConnect, err.Error(), err)
	case errors.Is(err, fs.ErrPermission):
		return NewError(ReasonNotWritable, err.Error(), err)
	default:
		return NewError(ReasonIO, err.Error(), err)
	}
}

// ConfigError 是构造目标时的配置校验错误（地址、端口、目录等），对应 API 的 validation.failed 字段错误，
// 而不是远端失败。Field 是配置字段键（与 model.BackupRemoteInput 的 JSON 键一致），Code 是 model.Field* 取值。
type ConfigError struct {
	Field   string
	Code    string
	Message string
}

func (e *ConfigError) Error() string { return e.Message }

// FieldErrors 把配置错误转成字段错误。
func (e *ConfigError) FieldErrors() model.FieldErrors { return model.FieldErrors{e.Field: e.Code} }

func configErr(field, code, msg string) *ConfigError {
	return &ConfigError{Field: field, Code: code, Message: msg}
}

// LocalError 是读取本地备份时的错误（文件缺失、不可读），与远端错误分开，由调用方映射为「没有可上传的本地备份」。
type LocalError struct{ Err error }

func (e *LocalError) Error() string { return e.Err.Error() }

// Unwrap 返回底层错误。
func (e *LocalError) Unwrap() error { return e.Err }
