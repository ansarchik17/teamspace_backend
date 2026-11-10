param([string]$PostgresImage = 'postgres:16')
$ErrorActionPreference = 'Stop'

# This script creates its own database and never accepts a production DSN.
$rehearsalID = [Guid]::NewGuid().ToString('N')
$containerName = 'ai-budget-rehearsal-' + $rehearsalID
$tempDirectory = Join-Path ([IO.Path]::GetTempPath()) $containerName
$evidenceFile = Join-Path $tempDirectory 'evidence.json'
$envNames = @('POSTGRES_HOST','POSTGRES_PORT','POSTGRES_DB','POSTGRES_USER','POSTGRES_PASSWORD','POSTGRES_SSL_MODE','MIGRATIONS_DIR','RUN_DB_MIGRATIONS','AI_OPS_DATABASE_URL','TEST_AI_OPS_REHEARSAL','TEST_AI_OPS_EVIDENCE_FILE','TEST_AI_MIGRATION_DATABASE_URL')
$savedEnvironment = @{}
foreach ($name in $envNames) { $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
function Invoke-Checked([string]$Program, [string[]]$Arguments) {
    $result = & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Command failed: $Program (exit $LASTEXITCODE)" }
    return $result
}
function Read-AccountingFingerprint([string]$Database) {
    $tables = @('organization_ai_budgets','organization_ai_user_quotas','organization_ai_weekly_anchors','organization_ai_weekly_periods','organization_ai_user_weekly_usage','organization_ai_service_weekly_usage','user_ai_five_hour_anchors','user_ai_five_hour_periods','ai_usage_operations','ai_usage_attempts','ai_usage_reservations','ai_usage_settlements','ai_usage_failure_events','ai_usage_reconciliation_jobs','ai_usage_budget_observations','goose_db_version')
    $queries = foreach ($table in $tables) {
        "SELECT '$table',count(*),md5(COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]')) FROM $table t;"
    }
    return (Invoke-Checked docker @('exec',$containerName,'psql','-U','postgres','-d',$Database,'-At','-v','ON_ERROR_STOP=1','-c',($queries -join "`n"))) -join "`n"
}
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    New-Item -ItemType Directory -Path $tempDirectory | Out-Null
    Invoke-Checked docker @('run','--detach','--name',$containerName,'--label',"ai-budget-rehearsal=$rehearsalID",'--publish','127.0.0.1::5432','--env','POSTGRES_PASSWORD=local-rehearsal-only','--env','POSTGRES_DB=ai_budget_rehearsal',$PostgresImage) | Out-Null
    $mapping = (Invoke-Checked docker @('port',$containerName,'5432/tcp')) -join ''
    if ($mapping -notmatch '^127\.0\.0\.1:(\d+)$') { throw 'Unexpected temporary database port mapping' }
    $port = $Matches[1]
    $ready = $false
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        & docker exec $containerName pg_isready -h 127.0.0.1 -U postgres -d ai_budget_rehearsal *> $null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Seconds 1
    }
    if (!$ready) { throw 'Temporary database did not become ready' }
    $env:POSTGRES_HOST='127.0.0.1'; $env:POSTGRES_PORT=$port; $env:POSTGRES_DB='ai_budget_rehearsal'
    $env:POSTGRES_USER='postgres'; $env:POSTGRES_PASSWORD='local-rehearsal-only'; $env:POSTGRES_SSL_MODE='disable'
    $env:MIGRATIONS_DIR = Join-Path (Get-Location) 'db/migrations'; $env:RUN_DB_MIGRATIONS='true'
    $env:AI_OPS_DATABASE_URL="postgres://postgres:local-rehearsal-only@127.0.0.1:$port/ai_budget_rehearsal?sslmode=disable"
    $env:TEST_AI_OPS_REHEARSAL='1'; $env:TEST_AI_OPS_EVIDENCE_FILE=$evidenceFile
    Invoke-Checked docker @('exec',$containerName,'createdb','-U','postgres','ai_migration_upgrade_test')
    $env:TEST_AI_MIGRATION_DATABASE_URL="postgres://postgres:local-rehearsal-only@127.0.0.1:$port/ai_migration_upgrade_test?sslmode=disable"
    Invoke-Checked go @('test','./cmd/migrate','-run','^TestAIUpgradeFromReleasedIForm$','-count=1','-timeout=240s')
    Invoke-Checked go @('run','./cmd/migrate')
    Invoke-Checked go @('run','./cmd/migrate') # Repeated Up must be a no-op.
    Invoke-Checked go @('test','./cmd/ai-accounting','-run','^TestManualResolutionCLIRehearsal$','-count=1','-timeout=120s')
    $before = Read-AccountingFingerprint 'ai_budget_rehearsal'
    Invoke-Checked docker @('exec',$containerName,'pg_dump','-U','postgres','-d','ai_budget_rehearsal','--format=custom','--file=/tmp/ai-budget.dump')
    Invoke-Checked docker @('exec',$containerName,'createdb','-U','postgres','ai_budget_restored')
    Invoke-Checked docker @('exec',$containerName,'pg_restore','-U','postgres','--exit-on-error','--dbname=ai_budget_restored','/tmp/ai-budget.dump')
    $after = Read-AccountingFingerprint 'ai_budget_restored'
    if ($before -ne $after) { throw 'Restored accounting rows or migration ledger differ from backup' }
    $env:AI_OPS_DATABASE_URL="postgres://postgres:local-rehearsal-only@127.0.0.1:$port/ai_budget_restored?sslmode=disable"
    Invoke-Checked go @('run','./cmd/ai-accounting','resolve','--database-name','ai_budget_restored','--evidence',$evidenceFile,'--apply')
    $replayed = Read-AccountingFingerprint 'ai_budget_restored'
    if ($after -ne $replayed) { throw 'Replaying evidence after restore changed accounting data' }
    Write-Output 'PASS: released iForm baseline upgraded without receipt loss; full migrations and repeated Up; real reservation/manual settlement; unknown hold retained; pg_dump/pg_restore fingerprints; idempotent replay after restore.'
}
finally {
    # Only remove the container created by this invocation, after checking ownership.
    $inspection = & docker inspect $containerName 2>$null
    $owner = $null
    if ($LASTEXITCODE -eq 0) {
        $details = ($inspection -join "`n") | ConvertFrom-Json
        $owner = $details[0].Config.Labels.'ai-budget-rehearsal'
    }
    if ($owner -eq $rehearsalID) {
        & docker rm --force --volumes $containerName | Out-Null
    }
    if (Test-Path -LiteralPath $evidenceFile) { Remove-Item -LiteralPath $evidenceFile }
    if (Test-Path -LiteralPath $tempDirectory) { Remove-Item -LiteralPath $tempDirectory }
    foreach ($name in $envNames) { [Environment]::SetEnvironmentVariable($name,$savedEnvironment[$name],'Process') }
    Pop-Location
}
