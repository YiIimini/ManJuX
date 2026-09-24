# ManJuX 构建脚本(Wails v2 桌面应用)
# 用法: powershell -ExecutionPolicy Bypass -File scripts/build.ps1
# 产出: build/bin/ManJuX.exe(单文件桌面应用,原生窗口)+ 项目根 ManJuX.exe(双击即用)
#
# 工具链:本机 go / pnpm 都是便携安装、不在系统 PATH,脚本会自动挂进去;
# 换机器可用 MANJUX_GOROOT / MANJUX_GOPATH / MANJUX_PNPM 覆盖。
#
# 发布请用 scripts/release.ps1(它会带版本号调本脚本,再多做门禁/提交/tag/推送)。

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# 版本:release 注入(MANJUX_VERSION)> internal/version/version.go 现值。
# **不再从 git tag 反推** —— 旧写法(2026-09-24 审计 P0-4)在 HEAD 领先 tag 时会把
# 已经升级的三处版本文件改回旧 tag(实测 v0.37.2 → v0.34.4)并弄脏工作区;
# 当时靠"记得传 MANJUX_VERSION"躲开,等于把必然踩的坑留给人的记性。
# 现在三处同源由 cmd/versync 完成(可测的逻辑,见 internal/version/sync.go):
#   - 传了 MANJUX_VERSION:三处都写该版本;
#   - 没传:以 version.go 为真源纠正两个 JSON,本来就一致则一个字节都不写。
$ver = $env:MANJUX_VERSION
# exe 内显示用的串:带上 "tag 之后第几个提交" 便于区分构建,但**永不写进任何文件**。
$verDisplay = $ver
if (-not $verDisplay) {
  try {
    $ErrorActionPreference = "Continue"
    $verDisplay = (& git describe --tags --always 2>$null | Select-Object -First 1)
    $ErrorActionPreference = "Stop"
  } catch { $ErrorActionPreference = "Stop" }
}
if (-not $verDisplay) { $verDisplay = "dev" }
if ($verDisplay -notmatch '^v') { $verDisplay = "v$verDisplay" }
Write-Host "==> ManJuX 构建版本: $verDisplay" -ForegroundColor Cyan

# 工具链先在前面挂好(versync 是个 go 程序)
$goroot = if ($env:MANJUX_GOROOT) { $env:MANJUX_GOROOT } else { "D:\Ai\Tools\go" }
$gopath = if ($env:MANJUX_GOPATH) { $env:MANJUX_GOPATH } else { "D:\Ai\Tools\gopath" }
if (Test-Path $goroot) {
  $env:GOROOT = $goroot
  $env:PATH = (Join-Path $goroot "bin") + ";" + $env:PATH
}
if (Test-Path $gopath) {
  $env:GOPATH = $gopath
  $env:PATH = (Join-Path $gopath "bin") + ";" + $env:PATH
}
# 国内直连 proxy.golang.org 不通;已设则不覆盖
if (-not $env:GOPROXY) { $env:GOPROXY = "https://goproxy.cn,direct" }
if (-not $env:GOSUMDB) { $env:GOSUMDB = "sum.golang.google.cn" }

# 版本三处同源(wails.json / frontend/package.json / internal/version/version.go)。
# **同步这三处只是必要条件,不是充分条件**:exe 右键「属性 → 详细信息」里的字段由
# build/windows/info.json 渲染进 RT_VERSION 资源,而 wails 自带的模板把版本块写在
# 语言 "0000"(中性)下 —— Windows 的版本 API(VerQueryValue / 资源管理器)根本不认
# 这个语言键,于是资源明明在二进制里、属性面板却整片空白(2026-09-24 实测:字符串在
# exe 字节流里搜得到,FileVersionInfo 读出来全是空)。已把该文件的语言键改成 "0409"
# 并补上 FileVersion/product_version。**若日后用 wails 脚手架重新生成
# build/windows/info.json,记得把这两处改回来**,否则属性又会静默变空。
$versyncArgs = @("run", "./cmd/versync", "-root", $root)
if ($ver) { $versyncArgs += @("-set", $ver) }
& go @versyncArgs
if ($LASTEXITCODE -ne 0) { throw "版本同步失败(cmd/versync)" }

# 0) 静默清理运行中的实例(exe 被锁会导致复制失败;构建过程零弹窗零打扰)
Get-Process ManJuX -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 600

# 1) 前端(Vue3 + Vite + Element Plus + Tailwind)
# pnpm 定位:本机是便携工具链(DSH 自带的 pnpm,不在系统 PATH),与 check.ps1 同一口径,
# 可用 MANJUX_PNPM 覆盖。此前这里写死裸 `pnpm`,在没把它挂进 PATH 的机器上直接失败。
$pnpmCmd = if ($env:MANJUX_PNPM) { $env:MANJUX_PNPM } else { (Get-Command pnpm -ErrorAction SilentlyContinue).Source }
if (Test-Path "$root/frontend/src") {
  if (-not $pnpmCmd) { throw "找不到 pnpm(可设 MANJUX_PNPM 指向 pnpm.cmd)" }
  Write-Host "==> 构建前端" -ForegroundColor Cyan
  Push-Location "$root/frontend"
  if (-not (Test-Path "node_modules")) { & $pnpmCmd install }
  & $pnpmCmd run build
  if ($LASTEXITCODE -ne 0) { Pop-Location; throw "前端构建失败" }
  Pop-Location
}

# 2) Wails 桌面构建(-s 跳过 wails 自带前端钩子,前端已构建)
#
# 工具链已在版本同步前挂进 PATH(见上);wails build 内部要调 `go`,所以只找到
# wails.exe 还不够 —— go 必须也在 PATH 里,否则报 "unable to find compiler: go"。
$wails = if ($env:GOPATH) { Join-Path $env:GOPATH "bin/wails.exe" } else { "" }
if (-not $wails -or -not (Test-Path $wails)) {
  $wails = (Get-Command wails -ErrorAction SilentlyContinue).Source
}
if (-not $wails) { throw "未找到 wails CLI,请先: go install github.com/wailsapp/wails/v2/cmd/wails@latest" }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  throw "PATH 里找不到 go:wails build 需要它(可设 MANJUX_GOROOT 指向 go 的根目录)"
}
Write-Host "==> Wails 桌面构建" -ForegroundColor Cyan
# ldflags 注入的是**显示串**(可含 describe 后缀,便于区分构建),文件里存的是基线 —— 两者刻意不同:
# 前者给人看,后者给安装包名/文件属性用。
& $wails build -s -ldflags "-X github.com/YiIimini/ManJuX/internal/version.Version=$verDisplay"
if ($LASTEXITCODE -ne 0) { throw "Wails 构建失败" }

# 3) 交付副本(项目根,双击即用)
Copy-Item "$root/build/bin/ManJuX.exe" "$root/ManJuX.exe" -Force
Write-Host "==> 完成: $root/ManJuX.exe" -ForegroundColor Green
Get-Item "$root/ManJuX.exe" | Select-Object Name, @{n='SizeMB';e={[math]::Round($_.Length/1MB,1)}}, LastWriteTime
