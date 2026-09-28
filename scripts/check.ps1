# ManJuX 提交前自检 —— 一条命令跑完全部检查。
#
# 为什么需要它:go build / go vet / gofmt / go test / 前端构建 / 第三方补丁在位
# 是七道各自独立的闸门,平时靠人记得逐个跑;漏跑任何一道,问题都会等到界面上才暴露
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

# ---------- 5. 竞态检测 (go test -race) ----------
# 为什么加这一道（2026-09-28）：pipeline/server 都是并发跑的（协调器、任务分配、资产库共享状态），
# 而数据竞争**不会**在普通 go test 里暴露 —— 它需要 -race（要 cgo，即本机得有 C 工具链）。
# 没装工具链时**如实跳过**（不假装验过），装了就跑。实测两个并发最重的包合计约 19 秒，够便宜。
Step "竞态检测 (go test -race)"
$raceCC = $null
foreach ($cand in @("D:\Ai\Tools\mingw64\bin", $env:MANJUX_MINGW)) {
  if ($cand -and (Test-Path (Join-Path $cand "gcc.exe"))) { $raceCC = $cand; break }
}
if (-not $raceCC) {
  $g = Get-Command gcc -ErrorAction SilentlyContinue
  if ($g) { $raceCC = Split-Path -Parent $g.Source }
}
if (-not $raceCC) {
  Ok "跳过：本机没有 C 工具链（-race 需要 cgo；装 mingw 后自动启用）"
} else {
  $env:CGO_ENABLED = "1"
  $env:PATH = "$raceCC;$env:PATH"
  $raceOut = & go test -race ./internal/pipeline/ ./internal/server/ 2>&1
  if ($LASTEXITCODE -ne 0) {
    Bad "竞态检测失败（有数据竞争或测试失败）:"
    $raceOut | Where-Object { $_ -match "DATA RACE|WARNING|FAIL|^---" } | Select-Object -First 20 | ForEach-Object { Write-Host "        $_" }
  } else {
    Ok "无数据竞争（pipeline + server，gcc: $raceCC）"
  }
}

# ---------- 6. 前端构建 ----------
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

# ---------- 7. 第三方本地补丁在位 ----------
# third_party/go-webview2 是**打了补丁的 fork**(go.mod 里有 replace 指向它)。它自己的
# README 写着一条 `task update` 会重新同步上游、**覆盖掉补丁** —— 而补丁丢了的症状是
# "应用莫名其妙整进程退出"(上游把原生文件对话框开闭期的瞬时焦点错误当致命错误
# os.Exit(1)),既不报错也不留栈,极难查。这种事不该靠人记得跑自检,放进闸门里。
Step "第三方补丁在位"
$patchFile = Join-Path $root "third_party\go-webview2\pkg\edge\chromium.go"
if (-not (Test-Path $patchFile)) {
  Bad "找不到 third_party\go-webview2\pkg\edge\chromium.go:go.mod 的 replace 指向了一个空目录"
} else {
  # 读字节再转 ASCII:纯 ASCII 判据不受编码影响。直接用 Get-Content 会在这台中文
  # Windows 上把该文件的 UTF-8 注释读成 GBK 乱码,拿中文当判据必然误报。
  $ascii = [Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($patchFile))
  if ($ascii -notmatch 'ManJuX-LOCAL-PATCH') {
    Bad "go-webview2 的 Focus() 补丁已丢失 —— 原生文件对话框开闭时会让整个应用静默退出。修法见 third_party/README.md"
  } else {
    Ok "go-webview2 本地补丁在位"
  }
}

# ---------- 8. 脚本文本编码（BOM 在位）----------
# 为什么要有这一道（2026-09-28 用户点破：这个错反复犯，说明我一直在修症状没加守卫）：
# 本机的 `powershell` 是 Windows PowerShell **5.1**，它对**没有 BOM** 的 .ps1 按 ANSI(GBK) 解析；
# 而 pwsh 7 的 `Set-Content -Encoding UTF8` 写出的是**不带 BOM** 的 UTF-8 ⇒ 中文注释全变乱码、
# 脚本语法直接崩（实测 1 秒即挂）。这类"用会重写编码的工具改文件"的坑靠人记得没用，放进闸门。
# 判据：含非 ASCII 字节的脚本文件**必须**有 UTF-8 BOM（纯 ASCII 的文件不需要，也不该被强加）。
Step "脚本文本编码 (BOM)"
$encBad = @()
foreach ($f in (Get-ChildItem (Join-Path $root "scripts") -File -Include *.ps1, *.cmd, *.bat -Recurse -ErrorAction SilentlyContinue)) {
  $bytes = [IO.File]::ReadAllBytes($f.FullName)
  $hasBom = ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)
  $nonAscii = $false
  foreach ($b in $bytes) { if ($b -gt 0x7F) { $nonAscii = $true; break } }
  if ($nonAscii -and -not $hasBom) { $encBad += $f.Name }
}
if ($encBad.Count -gt 0) {
  Bad ("这些脚本含中文却没有 UTF-8 BOM，Windows PowerShell 5.1 会按 ANSI 解析而崩: " + ($encBad -join ", "))
  Write-Host "       修法: 用 edit/write 工具改（它们保留原编码），不要用 Set-Content 写回" -ForegroundColor DarkGray
} else {
  Ok "脚本编码正确（含中文的都有 BOM）"
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
