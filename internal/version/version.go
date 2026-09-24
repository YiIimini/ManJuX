// Package version 提供 ManJuX 的版本信息。
// 版本号遵循语义化:主版本.次版本.修订号。
package version

import "runtime"

// AppName 应用名称。
const AppName = "ManJuX"

// Version 当前版本。构建时由 scripts/build.ps1 按 git tag 通过
// -ldflags "-X .../version.Version=vX.Y.Z" 注入;这里的默认值仅用于
// go run / 未注入场景,应与最新 tag 保持一致(构建脚本同时会把它同步进
// wails.json 与 frontend/package.json,三处同源)。
var Version = "v0.37.1"

// BuildInfo 返回完整版本描述。
func BuildInfo() string {
	return AppName + " " + Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}
