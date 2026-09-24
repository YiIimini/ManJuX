# ManJuX 发布脚本:体检门禁 → 构建 → 提交 → 打 tag → 推送 GitHub(私有仓)
# 版本更新自动推送:每次发布自动 commit + tag + push
# 用法: powershell -ExecutionPolicy Bypass -File scripts\release.ps1 [-Version v1.0.1] [-Note "说明"] [-DryRun]
#
# -DryRun 只跑到"门禁通过"就停:不起构建、不提交、不打 tag、不推送 ——
# 用于发布前单独复核门禁这一段(审计 P1-4:旧实现的门禁指向仓外脚本、
# 本机不存在就**静默跳过**,而且判据是"输出里有没有 FAIL 字样",python 报错也算过;
# 所以"这一段到底跑没跑"必须能被单独验证)。
param(
  [string]$Version = "",
  [string]$Note = "",
  [switch]$DryRun
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# 1) 版本号:显式指定 > 自动补丁号递增(基线取**版本真源 version.go**,不是 git tag)
# 为什么不用 tag:tag 只在本机打过 tag 之后才存在,而"当前版本"这件事的真源一直是
# internal/version/version.go(三处同源由 cmd/versync 维护)。用 tag 反推会让
# "改了版本还没打 tag"的仓库算出错误的下一版(见审计 P0-4 同一类根因)。
$ErrorActionPreference = "Continue"
$srcVer = (& go run ./cmd/versync -root $root -check 2>$null | Out-String)
$ErrorActionPreference = "Stop"
$curVer = ""
if (Test-Path "$root/internal/version/version.go") {
  $m0 = [regex]::Match((Get-Content "$root/internal/version/version.go" -Raw), 'var Version = "([^"]*)"')
  if ($m0.Success) { $curVer = $m0.Groups[1].Value.TrimStart("v") }
}
if (-not $Version) {
  $m = [regex]::Match($curVer, '^(\d+)\.(\d+)\.(\d+)$')
  if ($m.Success) {
    $Version = "v{0}.{1}.{2}" -f [int]$m.Groups[1].Value, [int]$m.Groups[2].Value, ([int]$m.Groups[3].Value + 1)
  } elseif ($srcVer -match "三处版本同源:(v[\d.]+)") {
    $base = $Matches[1].TrimStart("v")
    $m2 = [regex]::Match($base, '^(\d+)\.(\d+)\.(\d+)$')
    if ($m2.Success) {
      $Version = "v{0}.{1}.{2}" -f [int]$m2.Groups[1].Value, [int]$m2.Groups[2].Value, ([int]$m2.Groups[3].Value + 1)
    } else { $Version = "v1.0.1" }
  } else { $Version = "v1.0.1" }
}
Write-Host "==> 发布版本:$Version(当前 $curVer)" -ForegroundColor Cyan

# 1.5) 交付体检门禁:**随仓入库**的 check.ps1(六道门)+ 版本同源校验。
# 旧实现指向仓外绝对路径 C:/Mi/Ai/WorkBench/tools/deliver_check.py —— 本机不存在,
# 于是整段门禁**静默跳过**;判据还是"输出里有没有 FAIL 字样",python 报错也会判 PASS。
# 现在:门禁是本仓脚本,判据是**退出码**,不存在就 throw(审计 P1-4)。
$gate = Join-Path $PSScriptRoot "check.ps1"
if (-not (Test-Path $gate)) { throw "找不到门禁脚本 scripts/check.ps1(发布必须过门禁)" }
$ErrorActionPreference = "Continue"
& powershell -NoProfile -ExecutionPolicy Bypass -File $gate
$gateCode = $LASTEXITCODE
$verOut = (& go run ./cmd/versync -root $root -check 2>&1 | Out-String)
$verCode = $LASTEXITCODE
$ErrorActionPreference = "Stop"
if ($verCode -ne 0) {
  Write-Host $verOut -ForegroundColor Red
  throw "版本三处不同源:修复后重跑(禁止带病发布)"
}
if ($gateCode -ne 0) {
  throw "check.ps1 门禁未通过(退出码 $gateCode):修复后重跑,禁止带病交付"
}
Write-Host "==> 门禁通过(check.ps1 六道门 + 版本同源)" -ForegroundColor Green

if ($DryRun) {
  Write-Host "==> 预演结束:门禁已通过,未构建/未提交/未打 tag/未推送" -ForegroundColor Yellow
  exit 0
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
  # 用 -F 读消息文件,而不是 -m $msg:多行中文 note 经 PowerShell 命令行传参会被
  # 拆成多个参数,git 把多余的行当成 pathspec 报 "did not match any file(s)",
  # **提交随之失败而脚本继续打 tag** —— 2026-09-12 实测把 v0.34.1 的 tag 打到了
  # 上一个提交上(已修正)。消息文件写成无 BOM 的 UTF-8,避免中文提交信息变乱码。
  $msgFile = Join-Path ([IO.Path]::GetTempPath()) ("manjux-release-msg-" + [Guid]::NewGuid().ToString("N") + ".txt")
  [IO.File]::WriteAllText($msgFile, $msg, (New-Object Text.UTF8Encoding($false)))
  git commit -F $msgFile
  if ($LASTEXITCODE -ne 0) { throw "提交失败(release 中止,未打 tag)" }
  Remove-Item $msgFile -ErrorAction SilentlyContinue
  # 校验:提交必须真的发生(HEAD 变化),否则打 tag 会指向旧提交
  $headNow = (& git rev-parse HEAD)
  if (-not $headNow) { throw "无法读取 HEAD" }
}

# 4) tag + 推送
$ErrorActionPreference = "Continue"
$tagExists = (& git tag -l $Version 2>$null)
$ErrorActionPreference = "Stop"
if ($tagExists) { throw "tag $Version 已存在:请换版本号或先删除该 tag(git tag -d $Version)" }
git tag $Version
$ErrorActionPreference = "Continue"
$remote = (& git remote get-url origin 2>$null | Select-Object -First 1)
$ErrorActionPreference = "Stop"
if (-not $remote) {
  git remote add origin https://github.com/YiIimini/ManJuX.git
}
# 推送:常规 → 失败则禁用代理直连重试(本机代理常挂,实测 SSL handshake 失败)
# 注意不能写成 `git push ... | tail`:管道的退出码是 tail 的(0),回退分支永远不会执行
# —— 2026-09-12 实测就因此"看起来推送了、其实没推"。
git push -u origin main --tags
if ($LASTEXITCODE -ne 0) {
  Write-Host "==> 常规推送失败,尝试禁用代理直连重试…" -ForegroundColor Yellow
  git -c http.proxy= -c https.proxy= push -u origin main --tags
  if ($LASTEXITCODE -ne 0) { throw "推送失败(请检查网络/代理后手动 git push)" }
}
Write-Host "==> 已发布 $Version 并推送 GitHub" -ForegroundColor Green
