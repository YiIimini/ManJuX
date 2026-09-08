# ManJuX 发布脚本:体检门禁 → 构建 → 提交 → 打 tag → 推送 GitHub(私有仓)
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

# 1.5) 交付体检门禁:FAIL 立即中止,禁止带病发布
$check = "C:/Mi/Ai/WorkBench/tools/deliver_check.py"
if (Test-Path $check) {
  $out = (& python $check $root 2>&1 | Out-String)
  if ($out -match "FAIL") {
    Write-Host $out -ForegroundColor Red
    throw "deliver_check FAIL:修复后重跑,禁止带病交付"
  }
  Write-Host "==> deliver_check PASS" -ForegroundColor Green
}

# 2) 构建(注入本次发布版本号,避免 git describe 带旧 tag 后缀)
$env:MANJUX_VERSION = $Version
try {
  & "$PSScriptRoot\build.ps1"
  if ($LASTEXITCODE -ne 0) { throw "构建失败" }
} finally {
  Remove-Item Env:MANJUX_VERSION -ErrorAction SilentlyContinue
}

# 3) 提交(前端构建产物 webdist 入库,便于无 Node 环境直接构建)
git add -A
$hasChange = (& git status --porcelain)
if ($hasChange) {
  $msg = if ($Note) { "release: $Version $Note" } else { "release: $Version" }
  git commit -m $msg
}

# 4) tag + 推送
git tag $Version
$ErrorActionPreference = "Continue"
$remote = (& git remote get-url origin 2>$null | Select-Object -First 1)
$ErrorActionPreference = "Stop"
if (-not $remote) {
  git remote add origin https://github.com/YiIimini/ManJuX.git
}
git push -u origin main --tags
if ($LASTEXITCODE -ne 0) {
  Write-Host "==> 常规推送失败,尝试禁用代理直连重试…" -ForegroundColor Yellow
  git -c http.proxy= -c https.proxy= push -u origin main --tags
  if ($LASTEXITCODE -ne 0) { throw "推送失败(请检查网络/代理后手动 git push)" }
}
Write-Host "==> 已发布 $Version 并推送 GitHub" -ForegroundColor Green
