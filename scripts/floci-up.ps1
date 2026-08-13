#Requires -Version 5.1
<#
.SYNOPSIS
  One-command Floci local AWS sandbox bootstrap for web-gobbler.

.DESCRIPTION
  Preflight → start Floci → terraform apply (deploy_mode=floci) → push images →
  discover RDS/Redis → migrate → health-check → optional smoke test → report.

  # ponytail: single script, no modules; push uses terraform ECR outputs (Floci
  # registry on :5100), not the outdated localhost:4566/goscrape-* tag from the
  # early issue draft.

.PARAMETER SmokeTest
  POST /api/v1/scrape and poll until completed (or failed).

.PARAMETER Pg
  Optional Postgres container name override for migrations (docker exec).

.PARAMETER ApiUrl
  API base URL for health/smoke (default http://localhost:8080).

.PARAMETER ReadyTimeoutSec
  Max seconds to wait for Floci /_localstack/health (default 120).

.PARAMETER HealthTimeoutSec
  Max seconds to wait for API health (default 180).
#>
[CmdletBinding()]
param(
    [switch]$SmokeTest,
    [string]$Pg = "",
    [string]$ApiUrl = "http://localhost:8080",
    [int]$ReadyTimeoutSec = 120,
    [int]$HealthTimeoutSec = 180
)

$ErrorActionPreference = "Stop"
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $RepoRoot

function Write-Step([string]$Msg) { Write-Host "`n==> $Msg" -ForegroundColor Cyan }
function Write-WarnStep([string]$Msg) { Write-Warning $Msg }
function Abort([string]$Msg) {
    Write-Host "ABORT: $Msg" -ForegroundColor Red
    exit 1
}

function Test-Command([string]$Name) {
    return [bool](Get-Command $Name -ErrorAction SilentlyContinue)
}

function Test-TcpPortFree([int]$Port) {
    try {
        $listeners = @()
        if (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue) {
            $listeners = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
        }
        if ($listeners.Count -gt 0) { return $false }
        # Cross-platform fallback (Linux pwsh / no NetTCPConnection)
        $client = New-Object System.Net.Sockets.TcpClient
        try {
            $iar = $client.BeginConnect("127.0.0.1", $Port, $null, $null)
            $ok = $iar.AsyncWaitHandle.WaitOne(200)
            if ($ok -and $client.Connected) { return $false }
            return $true
        } finally {
            $client.Close()
        }
    } catch {
        return $true
    }
}

function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$CmdArgs
    )
    # Always pass args as an array — bare -d is eaten as -Debug by PowerShell binding.
    & $FilePath @CmdArgs
    if ($LASTEXITCODE -ne 0) {
        throw "Command failed ($LASTEXITCODE): $FilePath $($CmdArgs -join ' ')"
    }
}

function Wait-HttpOk {
    param(
        [string]$Uri,
        [int]$TimeoutSec,
        [scriptblock]$Validate = { param($r) $r.StatusCode -ge 200 -and $r.StatusCode -lt 300 }
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    $lastErr = $null
    while ((Get-Date) -lt $deadline) {
        try {
            $resp = Invoke-WebRequest -Uri $Uri -UseBasicParsing -TimeoutSec 5
            if (& $Validate $resp) { return $resp }
        } catch {
            $lastErr = $_
        }
        Start-Sleep -Seconds 2
    }
    throw "Timed out waiting for $Uri ($TimeoutSec s). Last error: $lastErr"
}

function Get-TfOutput([string]$Name) {
    $val = & terraform "-chdir=terraform" output -raw $Name 2>$null
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($val) -or $val -eq "null") {
        return $null
    }
    return $val.Trim()
}

# ── Preflight ──────────────────────────────────────────────
Write-Step "Preflight"
if (-not (Test-Command "docker")) { Abort "docker not found on PATH" }
docker info 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) { Abort "docker daemon not reachable (is Docker Desktop running?)" }

$composeOk = $false
docker compose version 2>$null | Out-Null
if ($LASTEXITCODE -eq 0) { $composeOk = $true }
if (-not $composeOk) { Abort "docker compose not available" }

if (-not (Test-Command "terraform")) { Abort "terraform not found on PATH" }
if (-not (Test-Command "aws")) { Abort "aws CLI not found on PATH" }

# Port 4566: allow if already our Floci (idempotent re-run)
$svc = docker compose --profile sandbox ps --status running --services 2>$null
$flociRunning = ($svc -match "(?m)^floci$")
if (-not $flociRunning -and -not (Test-TcpPortFree 4566)) {
    Abort "port 4566 is in use and Floci is not already running via compose profile sandbox"
}

# ── Start ──────────────────────────────────────────────────
Write-Step "Start Floci (compose profile sandbox)"
Invoke-Native -FilePath docker -CmdArgs @("compose", "--profile", "sandbox", "up", "--detach", "floci")

# ── Ready ──────────────────────────────────────────────────
Write-Step "Wait for Floci health (max ${ReadyTimeoutSec}s)"
try {
    Wait-HttpOk -Uri "http://localhost:4566/_localstack/health" -TimeoutSec $ReadyTimeoutSec | Out-Null
} catch {
    Abort $_.Exception.Message
}
Write-Host "Floci ready."

# ── Env ────────────────────────────────────────────────────
Write-Step "Set Floci AWS env"
$env:AWS_ENDPOINT_URL = "http://localhost:4566"
$env:AWS_ACCESS_KEY_ID = "test"
$env:AWS_SECRET_ACCESS_KEY = "test"
$env:AWS_DEFAULT_REGION = "us-east-1"
$env:AWS_EC2_METADATA_DISABLED = "true"

# Ensure state bucket exists (idempotent)
Write-Step "Ensure terraform state bucket in Floci S3"
aws --endpoint-url http://localhost:4566 s3 mb s3://goscrape-terraform-state 2>$null | Out-Null
# mb fails if exists — ignore

# ── Apply ──────────────────────────────────────────────────
Write-Step "Terraform init + apply (deploy_mode=floci)"
try {
    Invoke-Native -FilePath terraform -CmdArgs @("-chdir=terraform", "init", "-backend-config=backend.floci.hcl", "-input=false", "-reconfigure")
    Invoke-Native -FilePath terraform -CmdArgs @("-chdir=terraform", "plan", "-var", "deploy_mode=floci", "-input=false", "-out=floci.tfplan")
    Invoke-Native -FilePath terraform -CmdArgs @("-chdir=terraform", "apply", "-auto-approve", "-input=false", "floci.tfplan")
} catch {
    Abort "terraform failed: $($_.Exception.Message)"
}

# ── Push ───────────────────────────────────────────────────
Write-Step "Build + push api/worker images"
$apiRepo = Get-TfOutput "ecr_api_repository_url"
$workerRepo = Get-TfOutput "ecr_worker_repository_url"
$pushWarned = $false
if ([string]::IsNullOrWhiteSpace($apiRepo) -or [string]::IsNullOrWhiteSpace($workerRepo)) {
    Write-WarnStep "ECR repository URLs empty from terraform outputs; skipping push"
    $pushWarned = $true
} else {
    try {
        Invoke-Native -FilePath docker -CmdArgs @("build", "-t", "${apiRepo}:latest", "-f", "docker/Dockerfile.api", ".")
        Invoke-Native -FilePath docker -CmdArgs @("build", "-t", "${workerRepo}:latest", "-f", "docker/Dockerfile.worker", ".")
        Invoke-Native -FilePath docker -CmdArgs @("push", "${apiRepo}:latest")
        Invoke-Native -FilePath docker -CmdArgs @("push", "${workerRepo}:latest")
        # Force ECS to pick up :latest, then wait briefly for Floci to schedule tasks
        $cluster = Get-TfOutput "ecs_cluster_name"
        $apiSvc = Get-TfOutput "ecs_api_service_name"
        $workerSvc = Get-TfOutput "ecs_worker_service_name"
        aws --endpoint-url http://localhost:4566 ecs update-service --cluster $cluster --service $apiSvc --force-new-deployment 2>$null | Out-Null
        aws --endpoint-url http://localhost:4566 ecs update-service --cluster $cluster --service $workerSvc --force-new-deployment 2>$null | Out-Null
        Write-Host "Waiting for ECS api container to publish :8080..."
        $deadline = (Get-Date).AddSeconds(90)
        while ((Get-Date) -lt $deadline) {
            $apiCtr = docker ps --format "{{.Names}} {{.Ports}}" | Select-String -Pattern "floci-ecs-.*-api.*8080"
            if ($apiCtr) { break }
            Start-Sleep -Seconds 3
        }
    } catch {
        Write-WarnStep "image push failed (continuing): $($_.Exception.Message)"
        $pushWarned = $true
    }
}

# ── Discover ───────────────────────────────────────────────
Write-Step "Discover RDS/Redis endpoints"
$rdsHost = Get-TfOutput "rds_host"
$rdsPort = Get-TfOutput "rds_port"
$redisHost = Get-TfOutput "redis_host"
$redisPort = Get-TfOutput "redis_port"
$apiUrlOut = Get-TfOutput "api_url"
if ($apiUrlOut) { $ApiUrl = $apiUrlOut }

$rdsJson = ((aws --endpoint-url http://localhost:4566 rds describe-db-instances --output json 2>$null) | Out-String)
$redisJson = ((aws --endpoint-url http://localhost:4566 elasticache describe-replication-groups --output json 2>$null) | Out-String)
if ([string]::IsNullOrWhiteSpace($rdsHost) -or [string]::IsNullOrWhiteSpace($rdsPort)) {
    Abort "RDS host/port empty from terraform outputs"
}
if ([string]::IsNullOrWhiteSpace($redisHost) -or [string]::IsNullOrWhiteSpace($redisPort)) {
    Abort "Redis host/port empty from terraform outputs"
}
if ($rdsJson -notmatch "DBInstances") {
    Abort "aws rds describe-db-instances returned nothing usable"
}
if ($redisJson -notmatch "ReplicationGroups") {
    Abort "aws elasticache describe-replication-groups returned nothing usable"
}
Write-Host "RDS  = ${rdsHost}:${rdsPort}"
Write-Host "Redis= ${redisHost}:${redisPort}"

$valkeyName = (docker ps --format "{{.Names}}" | Select-String -Pattern "^floci-valkey-" | Select-Object -First 1)
if (-not $valkeyName) {
    Abort "Floci Valkey container floci-valkey-* is not running (ElastiCache backing store missing). Try: terraform apply -replace=aws_elasticache_replication_group.redis -var deploy_mode=floci"
}
Write-Host "Valkey container: $($valkeyName.ToString().Trim())"

# ── Migrate ────────────────────────────────────────────────
Write-Step "Apply migrations into Floci RDS"
$migFile = Join-Path $RepoRoot "migrations/000001_create_jobs_table.up.sql"
if (-not (Test-Path $migFile)) { Abort "missing $migFile" }

$pgContainer = $Pg
if ([string]::IsNullOrWhiteSpace($pgContainer)) {
    # Heuristic: Floci RDS container name floci-rds-*
    $pgContainer = (docker ps --format "{{.Names}}" | Select-String -Pattern "^floci-rds-" | Select-Object -First 1)
    if ($pgContainer) { $pgContainer = $pgContainer.ToString().Trim() }
}
if ([string]::IsNullOrWhiteSpace($pgContainer)) {
    Abort "could not find floci-rds-* container; pass -Pg <name>"
}

$migSql = Get-Content -Raw -Path $migFile
# Floci RDS listens inside the container on 5432 (proxy port on host network differs)
$migCmd = @(
    "exec", "-i", $pgContainer,
    "psql", "postgres://goscrape_admin:devpass@127.0.0.1:5432/goscrape",
    "-v", "ON_ERROR_STOP=1", "-f", "-"
)
$migSql | & docker @migCmd
if ($LASTEXITCODE -ne 0) {
    Abort "migration failed via docker exec $pgContainer"
}
Write-Host "Migration applied via $pgContainer"

# ── Health ─────────────────────────────────────────────────
Write-Step "Poll API health ($ApiUrl/api/v1/health)"
$healthOk = $false
try {
    $healthResp = Wait-HttpOk -Uri "$ApiUrl/api/v1/health" -TimeoutSec $HealthTimeoutSec -Validate {
        param($r)
        if ($r.StatusCode -ne 200) { return $false }
        return ($r.Content -match '"db"\s*:\s*"ok"' -and $r.Content -match '"redis"\s*:\s*"ok"')
    }
    Write-Host $healthResp.Content
    $healthOk = $true
} catch {
    Write-Host "HEALTH FAILED: $($_.Exception.Message)" -ForegroundColor Red
}

# ── Smoke (optional) ───────────────────────────────────────
$smokeOk = $null
if ($SmokeTest) {
    Write-Step "SmokeTest: POST /api/v1/scrape → poll completed"
    $smokeOk = $false
    try {
        $body = '{"url":"https://example.com","extract":["links"]}'
        $create = Invoke-WebRequest -Uri "$ApiUrl/api/v1/scrape" -Method POST -Body $body -ContentType "application/json" -UseBasicParsing -TimeoutSec 30
        $job = $create.Content | ConvertFrom-Json
        $jobId = $job.job_id
        if (-not $jobId) { $jobId = $job.id }
        if (-not $jobId) { throw "no job_id in scrape response: $($create.Content)" }

        $deadline = (Get-Date).AddSeconds(120)
        $final = $null
        while ((Get-Date) -lt $deadline) {
            $poll = Invoke-WebRequest -Uri "$ApiUrl/api/v1/jobs/$jobId" -UseBasicParsing -TimeoutSec 10
            $final = $poll.Content | ConvertFrom-Json
            if ($final.status -eq "completed" -or $final.status -eq "failed") { break }
            Start-Sleep -Seconds 2
        }
        if ($null -eq $final -or $final.status -ne "completed") {
            throw "job $jobId did not complete (status=$($final.status))"
        }
        if (-not $final.results) {
            throw "job $jobId completed but has no results"
        }
        Write-Host "Smoke OK job_id=$jobId status=completed"
        $smokeOk = $true
    } catch {
        Write-Host "SMOKE FAILED: $($_.Exception.Message)" -ForegroundColor Red
        $smokeOk = $false
    }
}

# ── Report ─────────────────────────────────────────────────
Write-Step "Report"
Write-Host @"
Floci sandbox ready (local AWS, `$0)

  API URL     : $ApiUrl
  Health      : $ApiUrl/api/v1/health$(if ($healthOk) { ' (ok)' } else { ' (FAILED)' })
  RDS         : ${rdsHost}:${rdsPort}  user=goscrape_admin pass=devpass db=goscrape
  Redis       : ${redisHost}:${redisPort}  (auth token=devpass when required)
  ECR API     : $apiRepo
  ECR Worker  : $workerRepo
  Push        : $(if ($pushWarned) { 'warned / skipped' } else { 'ok' })
  SmokeTest   : $(if ($null -eq $smokeOk) { 'skipped' } elseif ($smokeOk) { 'ok' } else { 'FAILED' })

Teardown:
  docker compose --profile sandbox down -v

Re-run this script anytime; terraform apply is idempotent under deploy_mode=floci.
"@

if (-not $healthOk) { exit 2 }
if ($SmokeTest -and -not $smokeOk) { exit 3 }
exit 0
