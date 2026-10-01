package httpx

// 全项目 API 错误码清单（唯一维护位置）。新增 code 时必须同步到前端 i18n。
//
// 错误响应体固定为 {"error":{"code":"<域>.<原因>","details":{...}}}，
// details 始终存在，无附加信息时为 {}。
//
//	code                     含义                                  HTTP 状态
//	request.invalid_json     请求体不是合法 JSON                   400
//	validation.failed        校验失败，details.fields 为字段错误    400
//	auth.required            需要登录                              401
//	auth.forbidden           无权限                                403
//	auth.invalid_password    密码错误，details.remaining           401
//	auth.locked              已锁定，details.retry_after_seconds   429
//	setup.required           尚未完成首次设置                      409
//	setup.already_done       已完成首次设置                        409
//	setup.invalid_code       设置码错误，details.remaining         401
//	origin.mismatch          Origin 校验失败                       403
//	not_found                资源不存在                            404
//	backup.exists            同一秒内已有同原因的备份              409
//	plugin.not_found         插件不存在                            404
//	plugin.invalid_manifest  manifest 不合法，details.problems 带行号  400（保留码：本期无路由返回，
//	                         插件目录的加载问题经 GET /api/plugins 的 errors[] 呈现）
//	proxy.not_found          代理不存在                            404
//	proxy.in_use             代理被实例引用，details.instances     409
//	instance.not_found       实例不存在                            404
//	instance.in_use          实例被 screen 引用，details.screens    409
//	run.timeout              采集超时（或上游请求超时），details.message 可选  504
//	run.failed               采集失败（或上游请求失败），details.message 可选  502
//	run.busy                 该实例正在运行，等待至多插件超时仍未轮到       409
//	internal                 内部错误                              500
const (
	CodeInvalidJSON           = "request.invalid_json"
	CodeValidationFail        = "validation.failed"
	CodeAuthRequired          = "auth.required"
	CodeAuthForbidden         = "auth.forbidden"
	CodeInvalidPassword       = "auth.invalid_password"
	CodeAuthLocked            = "auth.locked"
	CodeSetupRequired         = "setup.required"
	CodeSetupDone             = "setup.already_done"
	CodeInvalidSetup          = "setup.invalid_code"
	CodeOriginMismatch        = "origin.mismatch"
	CodeNotFound              = "not_found"
	CodeBackupExists          = "backup.exists"
	CodePluginNotFound        = "plugin.not_found"
	CodePluginInvalidManifest = "plugin.invalid_manifest"
	CodeProxyNotFound         = "proxy.not_found"
	CodeProxyInUse            = "proxy.in_use"
	CodeInstanceNotFound      = "instance.not_found"
	CodeInstanceInUse         = "instance.in_use"
	CodeRunTimeout            = "run.timeout"
	CodeRunFailed             = "run.failed"
	CodeRunBusy               = "run.busy"
	CodeInternal              = "internal"
)
