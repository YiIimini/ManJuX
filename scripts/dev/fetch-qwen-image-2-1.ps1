# fetch-qwen-image-2-1.ps1 -- download the Qwen-Image-2.1 ComfyUI split files.
#
# Why this route: huggingface.co is unreachable from this machine (every request
# fails with TypeError), so weights come from the ModelScope mirror of the
# Comfy-Org repo. Direct resolve URLs support HTTP Range, so curl -C - resumes.
#
# Files (int8_convrot build: ~16 GB total instead of ~30 GB for bf16):
#   diffusion_models/qwen_image_2.1_int8_convrot.safetensors
#   text_encoders/qwen3vl_8b_int8_convrot.safetensors
#   vae/qwen_image_2.1_vae_bf16.safetensors
#
# Usage:  powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev\fetch-qwen-image-2-1.ps1
# Progress is appended to $env:TEMP\vis\qwen21.log; the exact expected byte size of
# every file is read back from the ModelScope API, so a truncated file is detected.

param(
  [string]$ModelsRoot = 'D:\Ai\ComfyUI\shared\models',
  [string]$Repo       = 'Comfy-Org/Qwen-Image-2.1',
  [string]$Revision   = 'master'
)

$ErrorActionPreference = 'Continue'
$ProgressPreference    = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$logDir = Join-Path $env:TEMP 'vis'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$log = Join-Path $logDir 'qwen21.log'

function Log([string]$m) {
  $line = "{0} {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $m
  Add-Content -Path $log -Value $line -Encoding utf8
  Write-Output $line
}

$wanted = @(
  'diffusion_models/qwen_image_2.1_int8_convrot.safetensors',
  'text_encoders/qwen3vl_8b_int8_convrot.safetensors',
  'vae/qwen_image_2.1_vae_bf16.safetensors'
)

Log "=== fetch $Repo (revision $Revision) ==="

$api = "https://modelscope.cn/api/v1/models/$Repo/repo/files?Revision=$Revision&Recursive=true"
try {
  $listing = (Invoke-WebRequest -Uri $api -UseBasicParsing -TimeoutSec 60).Content | ConvertFrom-Json
} catch {
  Log "FATAL: cannot read the file listing: $($_.Exception.Message)"
  exit 2
}
$sizeByPath = @{}
foreach ($f in $listing.Data.Files) { $sizeByPath[$f.Path] = [int64]$f.Size }

$failed = 0
foreach ($rel in $wanted) {
  $dest = Join-Path $ModelsRoot ($rel -replace '/', '\')
  $dir  = Split-Path -Parent $dest
  New-Item -ItemType Directory -Force -Path $dir | Out-Null

  $want = 0
  if ($sizeByPath.ContainsKey($rel)) { $want = $sizeByPath[$rel] } else { Log "WARN: $rel is not listed upstream" }

  $have = 0
  if (Test-Path $dest) { $have = (Get-Item $dest).Length }
  if ($want -gt 0 -and $have -eq $want) { Log "SKIP $rel (complete, $have bytes)"; continue }

  $url = "https://modelscope.cn/models/$Repo/resolve/$Revision/$rel"
  Log "GET  $rel  (have $have / want $want) -> $dest"

  # -C - resumes a partial file; --retry survives the CDN's occasional 5xx.
  & curl.exe -sS -L -C - --retry 8 --retry-delay 5 --retry-all-errors `
      --connect-timeout 30 --max-time 21600 -o "$dest" "$url" 2>&1 | ForEach-Object { Log "     curl: $_" }

  $now = 0
  if (Test-Path $dest) { $now = (Get-Item $dest).Length }
  if ($want -gt 0 -and $now -ne $want) {
    Log "FAIL $rel : got $now bytes, expected $want"
    $failed++
  } else {
    Log "OK   $rel ($now bytes)"
  }
}

if ($failed -eq 0) { Log "ALL FILES COMPLETE" } else { Log "FINISHED WITH $failed FAILURE(S)" }
exit $failed
