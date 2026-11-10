param(
    [Parameter(Mandatory = $true)]
    [string]$Token,

    [string]$InputJson = "D:\name_surname_email_registration_ready.json",

    [string]$ApiUrl = "http://localhost:8080/api/v1/users",

    [int]$BatchSize = 100,

    [int]$MinistryId = 15,

    [int]$StateBodyId = 18,

    [int]$DepartmentId = 40,

    [int]$DelayMs = 200,

    [string]$SuccessLog = "D:\bulk_register_success.log",

    [string]$ErrorLog = "D:\bulk_register_errors.log",

    [string]$StateDir = "D:\bulk_register_state",

    [switch]$ResetLogs
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $InputJson)) {
    throw "Input JSON not found: $InputJson"
}

$authToken = if ($Token.StartsWith("Bearer ")) { $Token } else { "Bearer $Token" }

$data = Get-Content $InputJson -Encoding UTF8 | ConvertFrom-Json
if (-not $data.users -or $data.users.Count -eq 0) {
    throw "No users found in $InputJson"
}

if (-not (Test-Path $StateDir)) {
    New-Item -ItemType Directory -Path $StateDir | Out-Null
}

$progressFile = Join-Path $StateDir "progress.json"
$remainingFile = Join-Path $StateDir "remaining.json"
$tokenInvalidFile = Join-Path $StateDir "token_invalid.json"

if ($ResetLogs) {
    "" | Set-Content $SuccessLog -Encoding UTF8
    "" | Set-Content $ErrorLog -Encoding UTF8
}
elseif (-not (Test-Path $SuccessLog)) {
    "" | Set-Content $SuccessLog -Encoding UTF8
}
elseif ((Get-Item $SuccessLog).Length -eq 0) {
    "" | Set-Content $SuccessLog -Encoding UTF8
}

if (-not (Test-Path $ErrorLog)) {
    "" | Set-Content $ErrorLog -Encoding UTF8
}
elseif ((Get-Item $ErrorLog).Length -eq 0) {
    "" | Set-Content $ErrorLog -Encoding UTF8
}

$successEmails = @{}
Get-Content $SuccessLog -Encoding UTF8 | ForEach-Object {
    if ($_ -and $_.Trim()) {
        $parts = $_ -split "`t"
        if ($parts.Count -ge 1) {
            $successEmails[$parts[0].Trim().ToLower()] = $true
        }
    }
}

$skipEmails = @{}
Get-Content $ErrorLog -Encoding UTF8 | ForEach-Object {
    if ($_ -and $_.Trim()) {
        $parts = $_ -split "`t", 3
        if ($parts.Count -ge 3) {
            $email = $parts[0].Trim().ToLower()
            $msg = $parts[2]
            if ($msg -like '*CONFLICT*' -or
                $msg -like '*already exists in SSO*' -or
                $msg -like '*LastName*') {
                $skipEmails[$email] = $true
            }
        }
    }
}

$usersToProcess = @()
foreach ($user in $data.users) {
    $email = $user.email.Trim().ToLower()
    if (-not $successEmails.ContainsKey($email) -and -not $skipEmails.ContainsKey($email)) {
        $usersToProcess += $user
    }
}

@{
    generated_at = (Get-Date).ToString("s")
    source_json = $InputJson
    total_source_users = $data.users.Count
    already_successful = $successEmails.Count
    skipped_permanent_errors = $skipEmails.Count
    remaining_users = $usersToProcess.Count
} | ConvertTo-Json -Depth 5 | Set-Content $progressFile -Encoding UTF8

$total = $usersToProcess.Count
$created = 0
$failed = 0

Write-Host "Users to process: $total"
Write-Host "Batch size: $BatchSize"
Write-Host "MinistryId=$MinistryId StateBodyId=$StateBodyId DepartmentId=$DepartmentId"
Write-Host "Already successful from log: $($successEmails.Count)"
Write-Host "Skipped permanent errors from log: $($skipEmails.Count)"

if ($total -eq 0) {
    Write-Host ""
    Write-Host "Nothing to process."
    Write-Host "Progress file: $progressFile"
    return
}

for ($offset = 0; $offset -lt $total; $offset += $BatchSize) {
    $batch = $usersToProcess | Select-Object -Skip $offset -First $BatchSize
    $batchNo = [math]::Floor($offset / $BatchSize) + 1

    Write-Host ""
    Write-Host "Processing batch $batchNo ($($offset + 1)-$([math]::Min($offset + $BatchSize, $total)) of $total)"

    foreach ($user in $batch) {
        $body = @{
            email         = $user.email
            username      = $user.username
            password      = $user.password
            first_name    = $user.first_name
            last_name     = $user.last_name
            iin           = $user.iin
            role          = "user"
            position      = $user.position
            ministry_id   = $MinistryId
            state_body_id = $StateBodyId
            department_id = $DepartmentId
            division_id   = $null
        } | ConvertTo-Json -Depth 5

        try {
            $response = Invoke-RestMethod `
                -Uri $ApiUrl `
                -Method Post `
                -Headers @{
                    Authorization = $authToken
                    Accept        = "application/json"
                } `
                -ContentType "application/json; charset=utf-8" `
                -Body $body

            $created++
            $responseId = ""
            if ($null -ne $response -and $null -ne $response.id) {
                $responseId = [string]$response.id
            }
            $line = "{0}`t{1}`t{2}" -f $user.email, $user.username, $responseId
            Add-Content -Path $SuccessLog -Value $line -Encoding UTF8
            Write-Host "CREATED: $($user.email)"
        }
        catch {
            $failed++
            $message = $_.Exception.Message

            if ($_.ErrorDetails -and $_.ErrorDetails.Message) {
                $message = $_.ErrorDetails.Message
            }

            $line = "{0}`t{1}`t{2}" -f $user.email, $user.username, $message
            Add-Content -Path $ErrorLog -Value $line -Encoding UTF8
            Write-Host "FAILED: $($user.email)"

            if ($message -like '*TOKEN_INVALID*' -or
                $message -like '*TOKEN_EXPIRED*' -or
                $message -like '*Invalid token*' -or
                $message -like '*invalid token signature*' -or
                $message -like '*Token has expired*') {
                $remainingUsers = @()

                foreach ($remainingUser in ($usersToProcess | Select-Object -Skip ($offset))) {
                    if ($remainingUser.email -ne $user.email) {
                        $remainingUsers += $remainingUser
                    }
                }

                $remainingUsers = @($user) + $remainingUsers

                @{
                    target_unit = $data.target_unit
                    users = $remainingUsers
                } | ConvertTo-Json -Depth 8 | Set-Content $remainingFile -Encoding UTF8

                @{
                    stopped_at = (Get-Date).ToString("s")
                    reason = "token_invalid"
                    failed_email = $user.email
                    remaining_users = $remainingUsers.Count
                    source_json = $InputJson
                    remaining_file = $remainingFile
                } | ConvertTo-Json -Depth 5 | Set-Content $tokenInvalidFile -Encoding UTF8

                Write-Host ""
                Write-Host "Token became invalid. Stopping safely."
                Write-Host "Continue with: $remainingFile"
                Write-Host "Details: $tokenInvalidFile"
                return
            }
        }

        if ($DelayMs -gt 0) {
            Start-Sleep -Milliseconds $DelayMs
        }
    }

    $remainingAfterBatch = @($usersToProcess | Select-Object -Skip ($offset + $BatchSize))
    @{
        target_unit = $data.target_unit
        users = $remainingAfterBatch
    } | ConvertTo-Json -Depth 8 | Set-Content $remainingFile -Encoding UTF8

    @{
        updated_at = (Get-Date).ToString("s")
        source_json = $InputJson
        total_source_users = $data.users.Count
        total_to_process_this_run = $total
        processed_in_this_run = [math]::Min($offset + $BatchSize, $total)
        created_in_this_run = $created
        failed_in_this_run = $failed
        already_successful = $successEmails.Count
        skipped_permanent_errors = $skipEmails.Count
        remaining_users = $remainingAfterBatch.Count
        remaining_file = $remainingFile
    } | ConvertTo-Json -Depth 5 | Set-Content $progressFile -Encoding UTF8
}

Write-Host ""
Write-Host "Done."
Write-Host "Created: $created"
Write-Host "Failed: $failed"
Write-Host "Success log: $SuccessLog"
Write-Host "Error log: $ErrorLog"
Write-Host "Progress file: $progressFile"
Write-Host "Remaining file: $remainingFile"
