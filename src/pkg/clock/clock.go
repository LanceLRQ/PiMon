// Package clock 提供可注入的时钟，业务代码通过 Clock 取时间，测试用 Fake 推进时间而不 sleep。
package clock

import "time"

// Clock 是时间来源的抽象。
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Real 是基于系统时间的真实时钟。
type Real struct{}

// Now 返回当前系统时间。
func (Real) Now() time.Time { return time.Now() }

// After 等价于 time.After。
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }
