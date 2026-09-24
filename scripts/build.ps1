# ManJuX 构建脚本(Wails v2 桌面应用)
# 用法: powershell -ExecutionPolicy Bypass -File scripts/build.ps1
# 产出: build/bin/ManJuX.exe(单文件桌面应用,原生窗口)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# 版本号:release 注入(MANJUX_VERSION)> git tag > short hash > dev
$ver = $env:MANJUX_VERSION
if (-not $ver) {
  try {
    $ErrorActionPreference = "Continue"
    $ver = (& git describe --tags --always 2>$null | Select-Object -First 1)
    $ErrorActionPreference = "Stop"
  } catch { $ErrorActionPreference = "Stop" }
}
if (-not $ver) { $ver = "dev" }
# git describe 本身带 v 前缀;防双 v(vv1.5.0)
if ($ver -notmatch '^v') { $ver = "v$ver" }
Write-Host "==> ManJuX 构建版本: $ver" -ForegroundColor Cyan

# 版本号单一真源 = git tag。构建前把同一版本同步进 wails.json、
# frontend/package.json 与 internal/version/version.go —— 三处同源
# (version.go 的注释即如此约定)。否则 exe 文件属性与安装包名会永远停在旧值。
# 此前只同步前两处,version.go 靠 -ldflags 注入时才正确,go run / 未注入场景
# 会读到漂移的旧值(实测漂到 v0.33.20 而 tag 已 v0.34.1)——根因是"三处同源"
# 只落地了两处,这里补齐第三处,不再依赖人工记得改。
# 用 UTF8Encoding($false) 写入,规避 Windows PowerShell 5.1 的 BOM 问题
# (package.json 带 BOM 会让 pnpm 解析失败)。
$verNum = $ver.TrimStart('v')
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
  $new = $raw -replace '(?m)^(var Version = ")[^"]*(")', ('${1}' + $ver + '${2}')
  if ($new -ne $raw) {
    [IO.File]::WriteAllText($vgFile, $new, (New-Object Text.UTF8Encoding($false)))
    Write-Host "    -> 版本已同步: $vgFile = $ver" -ForegroundColor DarkGray
  }
}

# 0) 静默清理运行中的实例(exe 被锁会导致复制失败;构建过程零弹窗零打扰)
Get-Process ManJuX -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 600

# 1) 前端(Vue3 + Vite + Element Plus + Tailwind)
if (Test-Path "$root/frontend/src") {
  Write-Host "==> 构建前端" -ForegroundColor Cyan
  Push-Location "$root/frontend"
  if (-not (Test-Path "node_modules")) { pnpm install }
  pnpm run build
  if ($LASTEXITCODE -ne 0) { Pop-Location; throw "前端构建失败" }
  Pop-Location
}

# 2) Wails 桌面构建(-s 跳过 wails 自带前端钩子,前端已构建)
$wails = Join-Path $env:GOPATH "bin/wails.exe"
if (-not (Test-Path $wails)) { throw "未找到 wails CLI,请先: go install github.com/wailsapp/wails/v2/cmd/wails@latest" }
Write-Host "==> Wails 桌面构建" -ForegroundColor Cyan
& $wails build -s -ldflags "-X github.com/YiIimini/ManJuX/internal/version.Version=$ver"
if ($LASTEXITCODE -ne 0) { throw "Wails 构建失败" }

# 3) 交付副本(项目根,双击即用)
Copy-Item "$root/build/bin/ManJuX.exe" "$root/ManJuX.exe" -Force
Write-Host "==> 完成: $root/ManJuX.exe" -ForegroundColor Green
Get-Item "$root/ManJuX.exe" | Select-Object Name, @{n='SizeMB';e={[math]::Round($_.Length/1MB,1)}}, LastWriteTime
