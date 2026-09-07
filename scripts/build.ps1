# ManJuX 构建脚本
# 用法: powershell -ExecutionPolicy Bypass -File scripts\build.ps1
# 产出: ManJuX.exe(项目根,单文件、无控制台窗口)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$ver = $null
try {
  $ErrorActionPreference = "Continue"
  $ver = (& git describe --tags --always 2>$null | Select-Object -First 1)
  $ErrorActionPreference = "Stop"
} catch { $ErrorActionPreference = "Stop" }
if (-not $ver) { $ver = "dev" }
$ver = $ver.TrimStart("v")
Write-Host "==> ManJuX 构建版本: v$ver" -ForegroundColor Cyan

# 1) 前端(如源码存在且 node 可用)
if (Test-Path "$root\frontend\src") {
  Write-Host "==> 构建前端 (Vue3 + Vite + Element Plus)" -ForegroundColor Cyan
  Push-Location "$root\frontend"
  if (-not (Test-Path "node_modules")) { pnpm install }
  pnpm run build
  if ($LASTEXITCODE -ne 0) { Pop-Location; throw "前端构建失败" }
  Pop-Location
}

# 2) 后端单 exe
Write-Host "==> 构建 Go 后端 (-H windowsgui -s -w)" -ForegroundColor Cyan
$ldflags = "-s -w -X github.com/YiIimini/ManJuX/internal/version.Version=v$ver"
go build -ldflags $ldflags -trimpath -o "$root\ManJuX.exe" .\cmd\manjux
if ($LASTEXITCODE -ne 0) { throw "Go 构建失败" }

Write-Host "==> 完成: $root\ManJuX.exe" -ForegroundColor Green
Get-Item "$root\ManJuX.exe" | Select-Object Name, @{n='SizeMB';e={[math]::Round($_.Length/1MB,1)}}, LastWriteTime
