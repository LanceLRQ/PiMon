// Package version 保存构建版本号，发布构建通过 ldflags 注入。
package version

// Version 默认为 dev，构建时由 -ldflags "-X .../pkg/version.Version=..." 覆盖。
var Version = "dev"
