param(
    [string]$InputJson = "D:\bulk_register_state\remaining.json",

    [int]$Parts = 4,

    [string]$OutputDir = "D:\bulk_register_parallel",

    [int]$BatchSize = 100,

    [int]$DelayMs = 300
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $InputJson)) {
    throw "Input JSON not found: $InputJson"
}

if ($Parts -lt 2) {
    throw "Parts must be >= 2"
}

$data = Get-Content $InputJson -Encoding UTF8 | ConvertFrom-Json
if (-not $data.users -or $data.users.Count -eq 0) {
    throw "No users found in $InputJson"
}

if (-not (Test-Path $OutputDir)) {
    New-Item -ItemType Directory -Path $OutputDir | Out-Null
}

$users = @($data.users)
$total = $users.Count
$chunkSize = [math]::Ceiling($total / $Parts)

Write-Host "Source: $InputJson"
Write-Host "Total users: $total"
Write-Host "Parts: $Parts"
Write-Host "Chunk size: $chunkSize"
Write-Host ""

for ($index = 0; $index -lt $Parts; $index++) {
    $offset = $index * $chunkSize
    $chunkUsers = @($users | Select-Object -Skip $offset -First $chunkSize)

    if ($chunkUsers.Count -eq 0) {
        continue
    }

    $partNo = $index + 1
    $partDir = Join-Path $OutputDir ("part_{0}" -f $partNo)
    if (-not (Test-Path $partDir)) {
        New-Item -ItemType Directory -Path $partDir | Out-Null
    }

    $partJson = Join-Path $partDir ("remaining_part_{0}.json" -f $partNo)
    $successLog = Join-Path $partDir "bulk_register_success.log"
    $errorLog = Join-Path $partDir "bulk_register_errors.log"
    $stateDir = Join-Path $partDir "state"

    @{
        target_unit = $data.target_unit
        users = $chunkUsers
    } | ConvertTo-Json -Depth 8 | Set-Content $partJson -Encoding UTF8

    $commandLines = @(
        'powershell -ExecutionPolicy Bypass -File "D:\super-app-back\alem-super-app-backend\scripts\bulk_register_users.ps1" `'
        '  -Token $token `'
        ('  -InputJson "{0}" `' -f $partJson)
        ('  -BatchSize {0} `' -f $BatchSize)
        ('  -DelayMs {0} `' -f $DelayMs)
        ('  -SuccessLog "{0}" `' -f $successLog)
        ('  -ErrorLog "{0}" `' -f $errorLog)
        ('  -StateDir "{0}" `' -f $stateDir)
        '  -ResetLogs'
    )

    Write-Host ("Part {0}: {1} users" -f $partNo, $chunkUsers.Count)
    Write-Host ("JSON: {0}" -f $partJson)
    Write-Host ("StateDir: {0}" -f $stateDir)
    Write-Host "Command:"
    $commandLines | ForEach-Object { Write-Host $_ }
    Write-Host ""
}

Write-Host "Split completed."
Write-Host ("Output directory: {0}" -f $OutputDir)
