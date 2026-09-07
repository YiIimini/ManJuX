// Package version 提供 ManJuX 的版本信息。
// 版本号遵循语义化:主版本.次版本.修订号。
package version

import "runtime"

// AppName 应用名称。
const AppName = "ManJuX"

// Version 当前版本。构建时可通过 -ldflags "-X .../version.Version=vX.Y.Z" 注入。
var Version = "v1.0.0"

// BuildInfo 返回完整版本描述。
func BuildInfo() string {
	return AppName + " " + Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}
