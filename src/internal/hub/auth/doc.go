// Package auth 提供认证原语：密码哈希、失败限流、设置码、管理员、会话与屏幕令牌。
// 本包只负责存取与校验，不涉及 HTTP；所有时间取自注入的 clock.Clock。
package auth
