# Wait for the Qwen-Image-Edit multi-view weights to finish downloading, then VERIFY
# completeness against the remote Content-Length (exit code alone cannot prove that),
# resume once if short, and write a clear result log.
#
# ASCII-only on purpose: Windows PowerShell 5.1 decodes .ps1 as ANSI unless it has a
# UTF-8 BOM, so non-ASCII string literals can break parsing (hit once, fixed here).

$ErrorActionPreference = "Continue"
$ProgressPreference = "SilentlyContinue"
$proxy = "http://127.0.0.1:7897"
$logDir = Join-Path $env:TEMP "vis"
if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Path $logDir -Force | Out-Null }
$log = Join-Path $logDir "qwen-done.log"
function Say($m) {
  $line = "[{0}] {1}" -f (Get-Date -Format "HH:mm:ss"), $m
  Add-Content -Path $log -Value $line -Encoding UTF8
  Write-Host $line
}

$items = @(
  @{ name = "qwen_image_edit_2511_fp8mixed.safetensors"; dir = "diffusion_models"
     url = "https://huggingface.co/Comfy-Org/Qwen-Image-Edit_ComfyUI/resolve/main/split_files/diffusion_models/qwen_image_edit_2511_fp8mixed.safetensors" },
  @{ name = "qwen_2.5_vl_7b_fp8_scaled.safetensors"; dir = "text_encoders"
     url = "https://huggingface.co/Comfy-Org/HunyuanVideo_1.5_repackaged/resolve/main/split_files/text_encoders/qwen_2.5_vl_7b_fp8_scaled.safetensors" },
  @{ name = "Qwen-Image-Edit-2511-Lightning-4steps-V1.0-bf16.safetensors"; dir = "loras"
     url = "https://huggingface.co/lightx2v/Qwen-Image-Edit-2511-Lightning/resolve/main/Qwen-Image-Edit-2511-Lightning-4steps-V1.0-bf16.safetensors" },
  @{ name = "qwen-image-edit-2511-multiple-angles-lora.safetensors"; dir = "loras"
     url = "https://huggingface.co/0k-t0/Qwen-Image-Edit-2511-Multiple-Angles-LoRA/resolve/main/qwen-image-edit-2511-multiple-angles-lora.safetensors" }
)
$base = "D:\Ai\ComfyUI\shared\models"

function LocalSize($it) {
  $p = Join-Path (Join-Path $base $it.dir) $it.name
  if (Test-Path $p) { return (Get-Item $p).Length }
  return 0
}
function RemoteSize($url) {
  $len = 0
  $out = & curl.exe -sIL -x $proxy --max-time 60 $url 2>$null
  foreach ($line in $out) { if ($line -match "^(?i)content-length:\s*(\d+)") { $len = [int64]$Matches[1] } }
  return $len
}

Say ("monitor started; waiting for downloads to settle (proxy {0})" -f $proxy)

# 1) Wait until: no curl process AND sizes unchanged over 3 consecutive checks.
#    (Process visibility is not assumed: the size-stability part alone is enough.)
$unchanged = 0
$last = @{}
$waited = 0
while ($true) {
  $cur = @{}
  foreach ($it in $items) { $cur[$it.name] = (LocalSize $it) }
  $same = $true
  foreach ($k in $cur.Keys) { if ($last[$k] -ne $cur[$k]) { $same = $false } }
  $curlN = (Get-Process curl -ErrorAction SilentlyContinue | Measure-Object).Count
  if ($same -and $curlN -eq 0) { $unchanged++ } else { $unchanged = 0 }
  $last = $cur
  # also stop when everything already matches remote size (fast path)
  if ($unchanged -ge 3) { break }
  $waited += 60
  if ($waited % 600 -eq 0) {
    $s = ($items | ForEach-Object { "{0}={1:N0}MB" -f $_.name.Split(".")[0], ((LocalSize $_) / 1MB) }) -join " "
    Say ("still downloading: {0} (curl={1})" -f $s, $curlN)
  }
  if ($waited -gt 21600) { Say "waited 6h, giving up for now"; break }
  Start-Sleep -Seconds 60
}
Say ("downloads settled; verifying against remote sizes")

# 2) Verify + resume once if short.
foreach ($it in $items) {
  $local = LocalSize $it
  $remote = RemoteSize $it.url
  $rs = if ($remote) { "{0:N0} MB" -f ($remote / 1MB) } else { "unknown" }
  Say ("  {0}: local {1:N0} MB / remote {2}" -f $it.name, ($local / 1MB), $rs)
  if ($remote -and $local -lt $remote) {
    Say ("  -> short by {0:N0} MB, resuming" -f (($remote - $local) / 1MB))
    $p = Join-Path (Join-Path $base $it.dir) $it.name
    $proc = Start-Process -FilePath "curl.exe" -PassThru -WindowStyle Hidden -ArgumentList @(
      "-L", "-C", "-", "--retry", "5", "--retry-delay", "3", "-sS", "-x", $proxy, "-o", $p, $it.url)
    $proc.WaitForExit()
    Say ("  -> after resume {0:N0} MB" -f ((LocalSize $it) / 1MB))
  }
}

# 3) Final verdict.
$ok = 0; $bad = 0
foreach ($it in $items) {
  $local = LocalSize $it
  $remote = RemoteSize $it.url
  if ($remote -and $local -ge $remote) { $ok++ }
  elseif ($remote -and $local -lt $remote) { $bad++ }
  elseif ($local -gt 0) { $ok++ }   # remote size unavailable but file exists
  else { $bad++ }
}
Say ("RESULT: {0}/{1} complete{2}" -f $ok, $items.Count,
     $(if ($bad -gt 0) { " - $bad incomplete, ask the assistant to retry" } else { " - multi-view image-edit path is ready" }))
