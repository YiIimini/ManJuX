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

# 版本号:release 注入(MANJUX_VERSION)> git tag > short hash > dev
$ver = $env:MANJUX_VERSION
if (-not $ver) {
  try {
    $ErrorActionPreference = "Continue"
    # --tags:本仓的 tag 是轻量 tag(git cat-file -t v0.34.1 = commit),
    # 不带 --tags 时 git describe 是否认它们取决于 git 版本,显式带上才稳。
    $ver = (& git describe --tags --always 2>$null | Select-Object -First 1)
    $ErrorActionPreference = "Stop"
  } catch { $ErrorActionPreference = "Stop" }
}
if (-not $ver) { $ver = "dev" }
# git describe 本身带 v 前缀;防双 v(vv1.5.0)
if ($ver -notmatch '^v') { $ver = "v$ver" }
Write-Host "==> ManJuX 构建版本: $ver" -ForegroundColor Cyan

# 版本的**基线**部分:去掉 git describe 在 tag 之后追加的 "-N-gHASH" 后缀。
# 三个"同源"文件只写基线,exe 内显示仍用完整 describe 串(便于区分"这是 tag 之后
# 第几个提交的构建")。此前直接拿完整串写文件:仓库一旦领先于 tag,独立跑一次
# build.ps1 就会把 0.34.1-11-g4e18872 这类字符串写进 wails.json / package.json /
# version.go 并把工作区弄脏(release.ps1 因为显式注入 MANJUX_VERSION 恰好躲开了,
# 所以这个坑只在"按文档单独跑 build.ps1"时出现)。
$verBase = ($ver -split '-')[0]
$verNum = $verBase.TrimStart('v')

# 版本号单一真源 = git tag。构建前把同一版本同步进 wails.json、
# frontend/package.json 与 internal/version/version.go —— 三处同源
# (version.go 的注释即如此约定)。否则 exe 文件属性与安装包名会永远停在旧值。
# 此前只同步前两处,version.go 靠 -ldflags 注入时才正确,go run / 未注入场景
# 会读到漂移的旧值(实测漂到 v0.33.20 而 tag 已 v0.34.1)——根因是"三处同源"
# 只落地了两处,这里补齐第三处,不再依赖人工记得改。
# 用 UTF8Encoding($false) 写入,规避 Windows PowerShell 5.1 的 BOM 问题
# (package.json 带 BOM 会让 pnpm 解析失败)。
foreach ($vf in @("$root/wails.json", "$root/frontend/package.json")) {
  if (-not (Test-Path $vf)) { continue }
  $raw = [IO.File]::ReadAllText($vf, [Text.Encoding]::UTF8)
  $new = $raw -replace '("(?:product)?version":\s*")[^"]*(")', ('${1}' + $verNum + '${2}')
  if ($new -ne $raw) {
    [IO.File]::WriteAllText($vf, $new, (New-Object Text.UTF8Encoding($false)))
    Write-Host "    -> 版本已同步: $vf = $verNum" -ForegroundColor DarkGray
  }
}
# version.go 用带 v 前缀的写法(与 tag 一致),故单独一条规则
$vgFile = "$root/internal/version/version.go"
if (Test-Path $vgFile) {
  $raw = [IO.File]::ReadAllText($vgFile, [Text.Encoding]::UTF8)
  $new = $raw -replace '(?m)^(var Version = ")[^"]*(")', ('${1}' + $verBase + '${2}')
  if ($new -ne $raw) {
    [IO.File]::WriteAllText($vgFile, $new, (New-Object Text.UTF8Encoding($false)))
    Write-Host "    -> 版本已同步: $vgFile = $verBase" -ForegroundColor DarkGray
  }
}

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
# 工具链定位与 check.ps1 同一口径:本机 go 与 wails 都是**便携安装、不在系统 PATH**
# (go 在 D:\Ai\Tools\go、wails 在 GOPATH\bin),这里先把它们挂进本次进程的 PATH。
# wails build 内部要调 `go`,所以只找到 wails.exe 还不够 —— go 必须也在 PATH 里,
# 否则报 "unable to find compiler: go"。换机器可用 MANJUX_GOROOT / MANJUX_GOPATH 覆盖。
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

$wails = if ($env:GOPATH) { Join-Path $env:GOPATH "bin/wails.exe" } else { "" }
if (-not $wails -or -not (Test-Path $wails)) {
  $wails = (Get-Command wails -ErrorAction SilentlyContinue).Source
}
if (-not $wails) { throw "未找到 wails CLI,请先: go install github.com/wailsapp/wails/v2/cmd/wails@latest" }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  throw "PATH 里找不到 go:wails build 需要它(可设 MANJUX_GOROOT 指向 go 的根目录)"
}
Write-Host "==> Wails 桌面构建" -ForegroundColor Cyan
& $wails build -s -ldflags "-X github.com/YiIimini/ManJuX/internal/version.Version=$ver"
if ($LASTEXITCODE -ne 0) { throw "Wails 构建失败" }

# 3) 交付副本(项目根,双击即用)
Copy-Item "$root/build/bin/ManJuX.exe" "$root/ManJuX.exe" -Force
Write-Host "==> 完成: $root/ManJuX.exe" -ForegroundColor Green
Get-Item "$root/ManJuX.exe" | Select-Object Name, @{n='SizeMB';e={[math]::Round($_.Length/1MB,1)}}, LastWriteTime
