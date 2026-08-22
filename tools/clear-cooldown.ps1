# Clear ALL account cooldowns in grok2api — bulk version of the admin UI button.
# Usage:
#   powershell -ExecutionPolicy Bypass -File tools\clear-cooldown.ps1
#   powershell -File tools\clear-cooldown.ps1 -BaseUrl http://127.0.0.1:8000 -Password <admin-pass>
# Password: -Password param, or it will prompt securely.
# Lesson learned 2026-08-22: ALWAYS scan every page — the pool grew past our
# page size mid-incident and 13 cooling accounts hid on page 2.

param(
    [string]$BaseUrl = "http://127.0.0.1:8000",
    [string]$Password = ""
)

$ErrorActionPreference = "Stop"

if (-not $Password) {
    $sec = Read-Host "Admin password" -AsSecureString
    $Password = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec))
}

# 1. Login
$loginBody = @{ username = "admin"; password = $Password } | ConvertTo-Json
try {
    $login = Invoke-RestMethod -Uri "$BaseUrl/api/admin/v1/auth/login" -Method Post -ContentType "application/json" -Body $loginBody -TimeoutSec 10
} catch {
    Write-Host "Login gagal: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
$token = $login.data.tokens.accessToken
if (-not $token) { Write-Host "Login gagal: tiada token dalam respon" -ForegroundColor Red; exit 1 }
$headers = @{ Authorization = "Bearer $token" }
Write-Host "Login OK" -ForegroundColor Green

# 2. Safety check: is traffic still flowing? Clearing while a client
#    hammer-retries just re-burns accounts (incident 2026-08-22).
$audits = Invoke-RestMethod -Uri "$BaseUrl/api/admin/v1/request-audits?page=1&pageSize=5" -Headers $headers -TimeoutSec 10
$now = Get-Date
$recent = @($audits.data.items | Where-Object { ($now - [datetime]$_.createdAt).TotalMinutes -lt 2 })
if ($recent.Count -gt 0) {
    $fails = @($recent | Where-Object { $_.statusCode -ge 500 })
    if ($fails.Count -gt 0) {
        Write-Host "AMARAN: ada $($fails.Count) request gagal dalam 2 minit terakhir." -ForegroundColor Yellow
        Write-Host "Client mungkin masih retry. Stop client dulu, baru clear — kalau tidak cooldown akan diisi semula." -ForegroundColor Yellow
        $confirm = Read-Host "Teruskan jugak? (y/N)"
        if ($confirm -notmatch '^[Yy]') { Write-Host "Dibatalkan."; exit 0 }
    }
}

# 3. Scan EVERY page (do not assume one page is enough)
$page = 1
$all = @()
$total = 0
while ($true) {
    $r = Invoke-RestMethod -Uri "$BaseUrl/api/admin/v1/accounts?page=$page&pageSize=200" -Headers $headers -TimeoutSec 15
    $all += $r.data.items
    $total = $r.data.total
    if ($all.Count -ge $total) { break }
    $page++
}
Write-Host "Pool: $total akaun (scanned $($all.Count))"

# 4. Find accounts with cooldown (active OR stale — clearing stale cleans the display)
$now = Get-Date
$cooldown = @($all | Where-Object { $_.cooldownUntil })
if ($cooldown.Count -eq 0) {
    Write-Host "Tiada cooldown. Pool dah bersih." -ForegroundColor Green
    exit 0
}
$active = @($cooldown | Where-Object { ([datetime]$_.cooldownUntil) -gt $now })
$stale = $cooldown.Count - $active.Count
Write-Host "Cooldown ditemui: $($cooldown.Count) ($($active.Count) aktif, $stale stale/lupus)"

# 5. Clear each
$ok = 0; $fail = 0
foreach ($a in $cooldown) {
    try {
        Invoke-RestMethod -Uri "$BaseUrl/api/admin/v1/accounts/$($a.id)/clear-cooldown" -Method POST -Headers $headers -TimeoutSec 10 | Out-Null
        $ok++
    } catch {
        $fail++
        Write-Host "  GAGAL id=$($a.id): $($_.Exception.Message)" -ForegroundColor Red
    }
}
Write-Host "Clear berjaya: $ok / $($cooldown.Count) (gagal: $fail)"

# 6. Verify with the summary endpoint (the one the dashboard uses)
Start-Sleep -Seconds 3
$summary = Invoke-RestMethod -Uri "$BaseUrl/api/admin/v1/accounts/summary" -Headers $headers -TimeoutSec 10
Write-Host ""
Write-Host "=== RESULT ===" -ForegroundColor Cyan
Write-Host "Available : $($summary.data.available) / $($summary.data.total)"
Write-Host "Recovering: $($summary.data.recovering) (cooldown: $($summary.data.recovery.cooldown), probing: $($summary.data.recovery.probing), waitingReset: $($summary.data.recovery.waitingReset))"
if ($summary.data.recovering -eq 0) {
    Write-Host "BERSIH — semua akaun available." -ForegroundColor Green
    exit 0
} else {
    Write-Host "Masih ada recovering — tengok senarai di atas untuk yang gagal." -ForegroundColor Yellow
    exit 1
}
