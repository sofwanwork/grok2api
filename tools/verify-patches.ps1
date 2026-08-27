# Verify local patches survived a merge/deploy.
# Checks: (1) patch code markers exist, (2) config drift vs example,
# (3) live /v1/models limits, (4) live persona injection.
# Usage:  powershell -File tools/verify-patches.ps1 [-BaseUrl http://127.0.0.1:8000] [-SkipLive] [-Key <g2a_...>]
param(
    [string]$BaseUrl = "http://127.0.0.1:8000",
    [switch]$SkipLive,
    [string]$Key = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$fail = 0
function Pass($msg) { Write-Host "  [PASS] $msg" -ForegroundColor Green }
function Fail($msg) { Write-Host "  [FAIL] $msg" -ForegroundColor Red; $script:fail++ }
function Warn($msg) { Write-Host "  [WARN] $msg" -ForegroundColor Yellow }

Write-Host "`n=== 1. Patch markers in code ===" -ForegroundColor Cyan
# (marker, file, description) — keep in sync with UPDATE.md patch table.
$patches = @(
    @{ f = "backend/internal/infra/provider/cli/normalize.go";      m = "detailed";           d = "Patch 1: reasoning summary detailed (xhigh)" },
    @{ f = "backend/internal/infra/provider/conversation/stream.go"; m = "DoomLoop|RepeatCount|repeatCount"; d = "Patch 2: doom-loop split counters"; regex = $true },
    @{ f = "backend/internal/application/gateway/prompt_cache.go"; m = "identity";         d = "Patch 3: soft-session identity" },
    @{ f = "backend/internal/infra/provider/conversation/stream.go"; m = "reasoning_opaque";  d = "Patch 8: reasoning_opaque replay" },
    @{ f = "backend/internal/transport/http/inference/handler.go";   m = "65536";             d = "Patch 4: max_output_tokens 65536" },
    @{ f = "backend/internal/infra/provider/cli/adapter.go";         m = "systemPromptWhenClientHasSystem|hasAnthropicSystemContent"; d = "Patch 6/9: persona gateway"; regex = $true },
    @{ f = "backend/internal/transport/http/inference/handler.go";   m = "grok2api-reasoning-evidence"; d = "Patch 10: reasoning evidence marker (thinking detection)" },
    @{ f = "backend/internal/application/gateway/tool_salvage.go";   m = "salvageToolCallStream"; d = "Patch 11: tool-call salvage" },
    @{ f = "backend/internal/infra/provider/conversation/chat_stream.go"; m = "trace withheld by upstream"; d = "Patch 12: thinking placeholder for withheld CoT" },
    @{ f = "backend/internal/application/gateway/quality_retry_scan.go"; m = "holdExpired";       d = "Patch 13: early release after hold deadline" },
    @{ f = "backend/internal/application/gateway/quality_retry_scan.go"; m = "firstEvidenceAt";   d = "Patch 14: honest upstream TTFT for held streams" },
    @{ f = "backend/internal/application/gateway/quality_retry_scan.go"; m = "startHoldKeepalive|HoldKeepaliveSink"; d = "Patch 15: hold keepalive (anti duplicate retry)"; regex = $true },
    @{ f = "backend/internal/transport/http/inference/handler.go"; m = "streamPreamble|writeCommittedStreamError"; d = "Patch 15 v2: early SSE head + keepalive sink"; regex = $true },
    @{ f = "backend/internal/application/gateway/quality_retry.go"; m = "QualitySilentThinking|classifySilentThinking"; d = "Patch 16: silent-thinking retry (thought-but-empty answer)"; regex = $true },
    @{ f = "backend/internal/application/gateway/selector.go"; m = "NoteThinking|thinkingScore"; d = "Patch 17: soft thinking-score ordering"; regex = $true },
    @{ f = "backend/internal/application/gateway/selector.go"; m = "SeedThinkingScores|thinkingScorePenaltySeed"; d = "Patch 18: persist thinking score seeding"; regex = $true },
    @{ f = "backend/internal/application/gateway/selector_plan.go"; m = "benakAvoid"; d = "Patch 19: preemptive benak avoidance"; regex = $false },
    @{ f = "backend/internal/application/gateway/quality_retry.go"; m = "errQualityHeaderBudget|qualityHeaderBudget"; d = "Patch 20: early header abort (instrument-first)"; regex = $true },
    @{ f = "backend/internal/pkg/tooltimeguard/tooltimeguard.go"; m = "ApplyTimeoutHint|EnlargeToolTimeout|isDevServerCommand|rewriteDevServerBackground"; d = "Patch 21: bash tool timeout guard + dev server background rewrite (A+B)"; regex = $true },
    @{ f = "backend/internal/pkg/tooltimeguard/tooltimeguard.go"; m = "questionHint|ApplySchemaHints"; d = "Patch 22: question tool options hint (structured choices)"; regex = $true },
    @{ f = "backend/internal/application/gateway/quality_retry.go"; m = "DegradeCircuitThreshold|degradeCircuitOpen"; d = "Patch 23: degrade retry circuit-breaker (withhold storm fail-open)"; regex = $true },
    @{ f = "backend/internal/application/gateway/hallucinated_edit.go"; m = "HallucinatedEditClaim|editClaimPatterns"; d = "Patch 24: hallucinated-edit detector (claim-tulis tanpa tool calls)"; regex = $true },
    @{ f = "backend/internal/domain/account/quota_build.go"; m = "BuildBillingQuotaWindow"; d = "Patch 25: Build quota awareness — billing snapshot to QuotaWindow in routing"; regex = $true }
)
foreach ($p in $patches) {
    if (-not (Test-Path $p.f)) { Fail "$($p.d) — file missing: $($p.f)"; continue }
    $content = Get-Content $p.f -Raw
    $hit = if ($p.regex) { $content -match $p.m } else { $content.Contains($p.m) }
    if ($hit) { Pass $p.d } else { Fail "$($p.d) — marker '$($p.m)' not found in $($p.f)" }
}

Write-Host "`n=== 2. Config drift vs config.example.yaml ===" -ForegroundColor Cyan
function Get-TopLevelKeys($path) {
    if (-not (Test-Path $path)) { return $null }
    Select-String -Path $path -Pattern "^[a-zA-Z][a-zA-Z0-9_]*:" | ForEach-Object { ($_.Line -split ":")[0] }
}
$cfgKeys = Get-TopLevelKeys "config.yaml"
$exampleKeys = Get-TopLevelKeys "config.example.yaml"
if ($null -eq $cfgKeys) { Fail "config.yaml not found" }
elseif ($null -eq $exampleKeys) { Fail "config.example.yaml not found" }
else {
    $missing = $exampleKeys | Where-Object { $cfgKeys -notcontains $_ }
    if ($missing) {
        foreach ($m in $missing) { Warn "config.yaml missing section: $m (copy from config.example.yaml if you want its new defaults)" }
    } else {
        Pass "config.yaml has all example sections: $($cfgKeys.Count) keys"
    }
    # Persona section must exist and be enabled (it is gitignored — check locally only).
    $personaEnabled = Select-String -Path "config.yaml" -Pattern "^\s*enabled:\s*true" -Context 2,0 |
        Where-Object { ($_.Context.PreContext + $_.Line) -join "" -match "persona" }
    if (Test-Path "config.yaml") {
        $raw = Get-Content "config.yaml" -Raw
        if ($raw -match "(?ms)^persona:.*?^\s*enabled:\s*true") { Pass "persona.enabled = true" }
        else { Warn "persona section not detected as enabled — verify config.yaml manually" }
    }
}

if ($SkipLive) { Write-Host "`n(Skipping live checks — -SkipLive)" -ForegroundColor DarkGray; exit $fail }

Write-Host "`n=== 3. Live service: $BaseUrl ===" -ForegroundColor Cyan
function Read-AdminKey() {
    # Reads the client key from .verify-key.txt next to the script (gitignored).
    $keyFile = Join-Path $PSScriptRoot ".verify-key.txt"
    if (Test-Path $keyFile) { return (Get-Content $keyFile -Raw).Trim() }
    return ""
}
if (-not $Key) { $Key = Read-AdminKey }
if (-not $Key) {
    Warn "No API key. Put your g2a_ client key in tools/.verify-key.txt (one line) or pass -Key."
    Warn "You can reveal it in the admin UI: Client Keys -> reveal secret."
} else {
    try {
        $models = Invoke-RestMethod -Uri "$BaseUrl/v1/models" -Headers @{Authorization = "Bearer $Key"} -TimeoutSec 15
        $xhigh = $models.data | Where-Object { $_.id -eq "grok-4.6-xhigh" }
        if ($null -eq $xhigh) { Fail "grok-4.6-xhigh missing from /v1/models" }
        elseif ($xhigh.context_window -ne 500000 -or $xhigh.max_output_tokens -ne 65536) {
            Fail "grok-4.6-xhigh limits wrong: context=$($xhigh.context_window) output=$($xhigh.max_output_tokens) (want 500000/65536)"
        } else { Pass "grok-4.6-xhigh limits: context=500000 output=65536" }
    } catch { Fail "/v1/models unreachable or key invalid: $($_.Exception.Message)" }

    try {
        $body = @{ model = "grok-4.6-xhigh"; stream = $false;
                   messages = @(@{ role = "user"; content = "Reply with exactly one short sentence." }) } | ConvertTo-Json -Depth 5
        $r = Invoke-RestMethod -Uri "$BaseUrl/v1/chat/completions" -Method Post -ContentType "application/json" `
            -Headers @{Authorization = "Bearer $Key"} -Body $body -TimeoutSec 180
        $content = $r.choices[0].message.content
        if ($null -eq $content -or $content.Length -eq 0) { Fail "chat completion returned empty content" }
        else { Pass "chat completion returned $($content.Length) chars (persona check needs human eyeball — expect AKIF voice/emotion markers)" }
        Write-Host "        sample: $($content.Substring(0, [Math]::Min(160, $content.Length)))" -ForegroundColor DarkGray
    } catch { Fail "chat completion failed: $($_.Exception.Message)" }
}

Write-Host ""
if ($fail -gt 0) { Write-Host "RESULT: $fail check(s) FAILED" -ForegroundColor Red; exit 1 }
else { Write-Host "RESULT: all checks passed" -ForegroundColor Green; exit 0 }
