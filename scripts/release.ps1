# ManJuX 发布脚本:构建 → 提交 → 打 tag → 推送 GitHub(私有仓)
# 版本更新自动推送:每次发布自动 commit + tag + push
# 用法: powershell -ExecutionPolicy Bypass -File scripts\release.ps1 [-Version v1.0.1] [-Note "说明"]
param(
  [string]$Version = "",
  [string]$Note = ""
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# 1) 版本号:显式指定 > 自动补丁号递增
$ErrorActionPreference = "Continue"
$last = (& git describe --tags --abbrev=0 2>$null | Select-Object -First 1)
$ErrorActionPreference = "Stop"
if (-not $Version) {
  if ($last) {
    $m = [regex]::Match($last.TrimStart("v"), '^(\d+)\.(\d+)\.(\d+)$')
    if ($m.Success) {
      $Version = "v{0}.{1}.{2}" -f [int]$m.Groups[1].Value, [int]$m.Groups[2].Value, ([int]$m.Groups[3].Value + 1)
    } else { $Version = "v1.0.1" }
  } else { $Version = "v1.0.0" }
}

# 2) 构建
& "$PSScriptRoot\build.ps1"
if ($LASTEXITCODE -ne 0) { throw "构建失败" }

# 3) 提交(前端构建产物 webdist 入库,便于无 Node 环境直接构建)
git add -A
$hasChange = (& git status --porcelain)
if ($hasChange) {
  $msg = if ($Note) { "release: $Version $Note" } else { "release: $Version" }
  git commit -m $msg
}

# 4) tag + 推送
git tag $Version
$remote = (& git remote get-url origin 2>$null | Select-Object -First 1)
if (-not $remote) {
  git remote add origin https://github.com/YiIimini/ManJuX.git
}
git push -u origin main --tags
if ($LASTEXITCODE -ne 0) { throw "推送失败" }
Write-Host "==> 已发布 $Version 并推送 GitHub" -ForegroundColor Green
