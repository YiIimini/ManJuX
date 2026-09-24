# ManJuX 提交前自检 —— 一条命令跑完全部检查。
#
# 为什么需要它:go build / go vet / gofmt / go test / 前端构建 是五道各自独立的
# 闸门,平时靠人记得逐个跑;漏跑任何一道,问题都会等到界面上才暴露
# （典型:改了 frontend/src 但忘了 pnpm run build,webdist 是 go:embed 进去的,
#  不重建就等于界面改动没生效）。
#
# 用法:
#   powershell -ExecutionPolicy Bypass -File scripts\check.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\check.ps1 -SkipFrontend
#
# 退出码:0 = 全过;非 0 = 有失败项。
#
# 工具链:本机是**便携安装、不在系统 PATH**(Go 在 D:\Ai\Tools\go、wails 在
# GOPATH\bin),所以这里先把它们挂进本次进程的 PATH;换机器可用环境变量覆盖
# （MANJUX_GOROOT / MANJUX_GOPATH / MANJUX_PNPM）。

param(
  [switch]$SkipFrontend,
  [switch]$SkipTest
)

$ErrorActionPreference = "Continue"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$fails = @()
function Step($name) { Write-Host ""; Write-Host "==> $name" -ForegroundColor Cyan }
function Ok($msg)    { Write-Host "    [OK] $msg" -ForegroundColor Green }
function Bad($msg)   { Write-Host "    [FAIL] $msg" -ForegroundColor Red; $script:fails += $msg }

# ---------- 工具链定位 ----------
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

$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
  Write-Host "找不到 go。请设 MANJUX_GOROOT 或把 go 加入 PATH。" -ForegroundColor Red
  exit 1
}

# ---------- 1. gofmt ----------
Step "gofmt 格式"
$fmt = & gofmt -l internal main.go tee.go tee_test.go 2>&1 | Where-Object { $_ }
if ($fmt) {
  Bad ("gofmt 未格式化 " + ($fmt.Count) + " 个文件:")
  $fmt | Select-Object -First 10 | ForEach-Object { Write-Host "        $_" }
  Write-Host "       修法: gofmt -w internal main.go tee.go tee_test.go" -ForegroundColor DarkGray
} else {
  Ok "全部已格式化"
}

# ---------- 2. go vet ----------
Step "go vet 静态检查"
$vet = & go vet ./... 2>&1 | Where-Object { $_ }
if ($vet) {
  Bad "go vet 有告警:"
  $vet | Select-Object -First 15 | ForEach-Object { Write-Host "        $_" }
} else {
  Ok "无告警"
}

# ---------- 3. go build ----------
Step "go build 编译"
& go build ./... 2>&1 | Select-Object -First 15 | ForEach-Object { Write-Host "        $_" }
if ($LASTEXITCODE -ne 0) { Bad "go build 失败(退出码 $LASTEXITCODE)" } else { Ok "编译通过" }

# ---------- 4. go test ----------
if (-not $SkipTest) {
  Step "go test 测试"
  $out = & go test ./... 2>&1
  if ($LASTEXITCODE -ne 0) {
    Bad "go test 失败:"
    $out | Where-Object { $_ -match "FAIL|^---" } | Select-Object -First 15 | ForEach-Object { Write-Host "        $_" }
  } else {
    $okN = ($out | Where-Object { $_ -match "^ok\s" }).Count
    Ok "$okN 个包通过"
  }
} else {
  Step "go test 测试（按参数跳过）"
}

# ---------- 5. 前端构建 ----------
# 这一步不能省:webdist 是 go:embed 进二进制的,改了 frontend/src 不重建
# 就等于界面改动没生效 —— 而且 git 里会留下"新源码 + 旧产物"的不一致。
if (-not $SkipFrontend) {
  Step "前端构建 (vite)"
  $pnpm = if ($env:MANJUX_PNPM) { $env:MANJUX_PNPM } else { (Get-Command pnpm -ErrorAction SilentlyContinue).Source }
  if (-not $pnpm) {
    Bad "找不到 pnpm(可设 MANJUX_PNPM 指向 pnpm.cmd)"
  } else {
    Push-Location (Join-Path $root "frontend")
    if (-not (Test-Path "node_modules")) {
      Write-Host "    node_modules 不存在,先安装依赖..." -ForegroundColor DarkGray
      & $pnpm install 2>&1 | Select-Object -Last 3 | ForEach-Object { Write-Host "        $_" }
    }
    $fb = & $pnpm run build 2>&1
    if ($LASTEXITCODE -ne 0) {
      Bad "前端构建失败:"
      $fb | Select-Object -Last 15 | ForEach-Object { Write-Host "        $_" }
    } else {
      Ok "vite build 通过"
    }
    Pop-Location
  }
} else {
  Step "前端构建（按参数跳过）"
}

# ---------- 汇总 ----------
Write-Host ""
if ($fails.Count) {
  Write-Host ("自检未通过:" + $fails.Count + " 项") -ForegroundColor Red
  $fails | ForEach-Object { Write-Host ("   - " + $_) -ForegroundColor Red }
  exit 1
}
Write-Host "自检全过" -ForegroundColor Green
exit 0
