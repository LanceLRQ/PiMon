package auth

import "github.com/LanceLRQ/PiMon/src/internal/ratelimit"

// 限流 key 前缀，后面拼客户端 IP。
const (
	KeyLogin  = "login:"
	KeySetup  = "setup:"
	KeyScreen = "screen:"
)

// Limiter 是 ratelimit.Limiter 的别名，限流器实现已移到中立包 internal/ratelimit，
// 供 agent 通道入口防护共用；保留别名让现有调用方不改。
type Limiter = ratelimit.Limiter

// NewLimiter 创建限流器：连续失败 max 次后锁定 lock。
var NewLimiter = ratelimit.NewLimiter
