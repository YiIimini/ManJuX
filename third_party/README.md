# third_party —— 本地维护的第三方补丁

本目录存放**必须带本地改动、不能直接用上游**的依赖。目前只有一个。

## go-webview2(`third_party/go-webview2`)

- 模块路径:`github.com/wailsapp/go-webview2`(即 Wails 官方那个 fork,不是 `jchv/go-webview2`)
- 接入方式:`go.mod` 末尾的 `replace github.com/wailsapp/go-webview2 => ./third_party/go-webview2`
  —— 因此 `go.mod` 里写的 `v1.0.22` 只是**名义版本**,真正参与编译的是本目录的代码。
- 基线:上游 `v1.0.22`(与 Wails `v2.16.0` 所要求的一致)。

### 本地补丁:只有一处

差集只有 `pkg/edge/chromium.go` 一个文件,改动只在 `Focus()`:

```go
func (e *Chromium) Focus() {
	err := e.controller.MoveFocus(COREWEBVIEW2_MOVE_FOCUS_REASON_PROGRAMMATIC)
	if err != nil {
		// ManJuX-LOCAL-PATCH(2026-09-09) —— 这行 ASCII 标记是补丁是否在位的判据,
		// scripts/check.ps1 会查它;**重新同步上游后请一并恢复**。
		// 原生文件对话框开闭期间 MoveFocus 会返回瞬时错误(焦点尚在对话框),
		// 上游把它当致命错误 os.Exit(1) 整进程退出 —— 两次实测崩溃栈完全一致
		// (onFocus→Focus→errorCallback)。焦点获取失败可以安全忽略:
		// webview 仍存活,下一次窗口事件会重试。
		log.Printf("[webview2] Focus 瞬时失败(已忽略,常见于原生对话框开闭): %v", err)
	}
}
```

那行 `ManJuX-LOCAL-PATCH` 标记是**刻意用纯 ASCII** 的:判据要能在任何编码/任何 PowerShell
版本下稳定匹配(实测 `Get-Content` 不带 `-Encoding` 会把这文件的 UTF-8 注释读成 GBK 乱码,
拿中文当判据会误报"补丁丢了")。

**为什么要改**:打开/关闭原生文件对话框期间,焦点还在对话框上,`MoveFocus` 会返回瞬时错误。
上游把它当致命错误处理,`errorCallback` 兜底是 `os.Exit(1)` —— **整个应用直接消失**。
2026-09-09 两次实测崩溃栈完全一致(`onFocus → Focus → errorCallback`),不是偶发。
焦点获取失败本身可以安全忽略:webview 仍存活,下一次窗口事件会重试。

**与"禁用 webview GPU 加速"是两件事**,别混为一谈:
另有一处渲染期闪退是 GPU 满载挤崩 webview 的 GPU 进程,那个的修法是
`main.go` 的 `windows.Options{WebviewGpuIsDisabled: true}`(见 `docs/架构.md` 的
「WebView2 稳定性」)。本补丁管的是**原生对话框**那条路径。

### 升级 wails 之后必须做的事

`go mod tidy` 不会碰本目录,所以升级本身是安全的;但当 Wails 要求的 go-webview2 版本变了
(或按 `third_party/go-webview2/README.md` 里写的 `task update` 重新同步上游)时,
**同步动作会把补丁覆盖掉,崩溃会以"莫名其妙整进程退出"的形式复发**。

自检:**`scripts/check.ps1` 的第 6 道闸门已在查这件事**(补丁丢了会直接让自检失败),
所以正常情况下不需要手工跑。想单独确认时:

```powershell
$f = 'third_party/go-webview2/pkg/edge/chromium.go'
$ascii = [Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($f))
if ($ascii -match 'ManJuX-LOCAL-PATCH') { 'PATCH-OK' } else { 'PATCH-LOST' }
```

(读字节再转 ASCII 是刻意的:纯 ASCII 片段不受编码影响,`Get-Content` 在这台中文 Windows 上
会把该文件的 UTF-8 注释读成 GBK 乱码。)

丢了就按上面的片段重新打一次,并在 `docs/COMPATIBILITY.md` 第八节记一条升级记录。
