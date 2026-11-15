$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
Add-Type -AssemblyName System.Net.Http

$baseUrl = "http://localhost:8080"
Write-Host "================================================================="
Write-Host " Testing Live Instance Google Auth & RBAC Architecture on $baseUrl"
Write-Host "================================================================="

function Perform-MockLogin {
    param(
        [string]$RoleName
    )
    $handler = [System.Net.Http.HttpClientHandler]::new()
    $handler.AllowAutoRedirect = $false
    $handler.UseCookies = $false
    $client = [System.Net.Http.HttpClient]::new($handler)
    $client.BaseAddress = [System.Uri]::new($baseUrl)

    # 1. Initiate login
    $initResp = $client.GetAsync("/api/auth/google?mock_role=$RoleName").GetAwaiter().GetResult()
    if ($initResp.StatusCode -ne [System.Net.HttpStatusCode]::Found) {
        throw "Failed to initiate login for $RoleName : Status $($initResp.StatusCode)"
    }
    
    $redirectUrl = $initResp.Headers.Location.ToString()
    $cookies = @{}
    foreach ($cookieHeader in $initResp.Headers.GetValues("Set-Cookie")) {
        $parts = $cookieHeader.Split(';')[0].Split('=')
        $cookies[$parts[0].Trim()] = $parts[1].Trim()
    }
    if (-not $cookies.ContainsKey("bliss_oauth_tx")) {
        throw "Missing bliss_oauth_tx cookie on login initiation for $RoleName"
    }

    # 2. Callback with transaction binding cookie
    $cbRequest = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Get, $redirectUrl)
    $cbRequest.Headers.Add("Cookie", "bliss_oauth_tx=$($cookies['bliss_oauth_tx'])")
    $cbResp = $client.SendAsync($cbRequest).GetAwaiter().GetResult()
    if ($cbResp.StatusCode -ne [System.Net.HttpStatusCode]::Found) {
        $body = $cbResp.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        throw "Failed to complete callback for $RoleName : Status $($cbResp.StatusCode), Body: $body"
    }

    foreach ($cookieHeader in $cbResp.Headers.GetValues("Set-Cookie")) {
        $parts = $cookieHeader.Split(';')[0].Split('=')
        $cookies[$parts[0].Trim()] = $parts[1].Trim()
    }
    if (-not $cookies.ContainsKey("bliss_session")) {
        throw "Missing bliss_session cookie on callback completion for $RoleName"
    }

    # 3. Retrieve /api/auth/me to get user details & csrf token
    $meRequest = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Get, "/api/auth/me")
    $meRequest.Headers.Add("Cookie", "bliss_session=$($cookies['bliss_session'])")
    $meResp = $client.SendAsync($meRequest).GetAwaiter().GetResult()
    if ($meResp.StatusCode -ne [System.Net.HttpStatusCode]::OK) {
        throw "Failed to fetch /api/auth/me for $RoleName : Status $($meResp.StatusCode)"
    }
    $meJson = $meResp.Content.ReadAsStringAsync().GetAwaiter().GetResult() | ConvertFrom-Json

    return @{
        RoleName     = $RoleName
        User         = $meJson.user
        CsrfToken    = $meJson.csrf_token
        SessionToken = $cookies['bliss_session']
    }
}

function Invoke-Api {
    param(
        [string]$Method,
        [string]$Path,
        [hashtable]$AuthContext,
        [object]$Body = $null,
        [int[]]$ExpectedStatuses = @(200)
    )
    $handler = [System.Net.Http.HttpClientHandler]::new()
    $handler.AllowAutoRedirect = $false
    $handler.UseCookies = $false
    $client = [System.Net.Http.HttpClient]::new($handler)
    $client.BaseAddress = [System.Uri]::new($baseUrl)

    $httpMethod = [System.Net.Http.HttpMethod]::new($Method)
    $request = [System.Net.Http.HttpRequestMessage]::new($httpMethod, $Path)
    if ($AuthContext -and $AuthContext.SessionToken) {
        $request.Headers.Add("Cookie", "bliss_session=$($AuthContext.SessionToken)")
    }
    if ($AuthContext -and $AuthContext.CsrfToken -and $Method -ne "GET") {
        $request.Headers.Add("X-CSRF-Token", $AuthContext.CsrfToken)
    }
    if ($Body) {
        $jsonStr = $Body | ConvertTo-Json -Compress
        $request.Content = [System.Net.Http.StringContent]::new($jsonStr, [System.Text.Encoding]::UTF8, "application/json")
    }

    $resp = $client.SendAsync($request).GetAwaiter().GetResult()
    $statusCode = [int]$resp.StatusCode
    $bodyStr = $resp.Content.ReadAsStringAsync().GetAwaiter().GetResult()
    
    if ($ExpectedStatuses -notcontains $statusCode) {
        throw "[$Method $Path] Expected status $([string]::Join(',', $ExpectedStatuses)), got $statusCode. Response: $bodyStr"
    }

    $parsed = $null
    if ($bodyStr -and $bodyStr.Trim().StartsWith("{") -or $bodyStr.Trim().StartsWith("[")) {
        try { $parsed = $bodyStr | ConvertFrom-Json } catch {}
    }
    return @{
        StatusCode = $statusCode
        Body       = $bodyStr
        Json       = $parsed
    }
}

# --- STEP 1: Bootstrapped Owner Login ---
Write-Host "`n[STEP 1] Logging in as Owner (owner@blissatelier.com)..." -ForegroundColor Cyan
$ownerAuth = Perform-MockLogin -RoleName "owner"
Write-Host "  -> Owner Logged In: ID=$($ownerAuth.User.id), Email=$($ownerAuth.User.email), Role=$($ownerAuth.User.role)" -ForegroundColor Green
if ($ownerAuth.User.role -ne "owner") {
    throw "Owner role was not bootstrapped! Expected 'owner', got '$($ownerAuth.User.role)'"
}

# --- STEP 2: Manager, Employee, and Customer Logins ---
Write-Host "`n[STEP 2] Logging in candidate Manager, Employee, and Customer..." -ForegroundColor Cyan
$managerAuth = Perform-MockLogin -RoleName "manager"
Write-Host "  -> Manager Candidate ID=$($managerAuth.User.id), Email=$($managerAuth.User.email), Initial Role=$($managerAuth.User.role)"
$employeeAuth = Perform-MockLogin -RoleName "employee"
Write-Host "  -> Employee Candidate ID=$($employeeAuth.User.id), Email=$($employeeAuth.User.email), Initial Role=$($employeeAuth.User.role)"
$customerAuth = Perform-MockLogin -RoleName "customer"
Write-Host "  -> Customer ID=$($customerAuth.User.id), Email=$($customerAuth.User.email), Initial Role=$($customerAuth.User.role)"

# --- STEP 3: Owner Promotes Manager and Employee ---
Write-Host "`n[STEP 3] Owner promoting candidates to 'manager' and 'employee'..." -ForegroundColor Cyan
Invoke-Api -Method "POST" -Path "/api/admin/users/$($managerAuth.User.id)/role" -AuthContext $ownerAuth -Body @{ role = "manager" } -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Promoted $($managerAuth.User.email) to manager" -ForegroundColor Green
Invoke-Api -Method "POST" -Path "/api/admin/users/$($employeeAuth.User.id)/role" -AuthContext $ownerAuth -Body @{ role = "employee" } -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Promoted $($employeeAuth.User.email) to employee" -ForegroundColor Green

# Refresh auth contexts after role update
$managerAuth = Perform-MockLogin -RoleName "manager"
if ($managerAuth.User.role -ne "manager") { throw "Manager role update failed" }
$employeeAuth = Perform-MockLogin -RoleName "employee"
if ($employeeAuth.User.role -ne "employee") { throw "Employee role update failed" }

# --- STEP 4: Upward Visibility & Downward Isolation Verification ---
Write-Host "`n[STEP 4] Verifying Upward Visibility & Downward Isolation..." -ForegroundColor Cyan

# 4.1: Owner sees ALL users
$ownerList = Invoke-Api -Method "GET" -Path "/api/admin/users" -AuthContext $ownerAuth
$ownerUserRoles = $ownerList.Json.users | ForEach-Object { $_.role }
Write-Host "  -> Owner sees $($ownerList.Json.users.Count) users: $($ownerUserRoles -join ', ')" -ForegroundColor Green
if ($ownerList.Json.users.Count -lt 4) { throw "Owner should see all 4 users" }

# 4.2: Manager sees Employee, Customer — NEVER Owner or other Managers!
$managerList = Invoke-Api -Method "GET" -Path "/api/admin/users" -AuthContext $managerAuth
$managerUserRoles = $managerList.Json.users | ForEach-Object { $_.role }
Write-Host "  -> Manager sees $($managerList.Json.users.Count) downward users: $($managerUserRoles -join ', ')" -ForegroundColor Green
if ($managerUserRoles -contains "owner" -or $managerUserRoles -contains "manager") {
    throw "SECURITY BREACH: Manager can see Owner or Manager! Downward Isolation violated!"
}
if ($managerList.Json.users.Count -ne 2) {
    throw "Manager should see exactly 2 downward users (employee and customer)"
}
Write-Host "  -> Downward Isolation Confirmed: Owner and Manager tiers are hidden from Manager" -ForegroundColor Green

# 4.3: Manager cannot promote anyone to Owner (403 Forbidden)
Write-Host "  -> Testing Manager attempting to promote Employee to Owner..."
$forbiddenAttempt = Invoke-Api -Method "POST" -Path "/api/admin/users/$($employeeAuth.User.id)/role" -AuthContext $managerAuth -Body @{ role = "owner" } -ExpectedStatuses @(403)
Write-Host "  -> Correctly rejected with 403 Forbidden: $($forbiddenAttempt.Json.error)" -ForegroundColor Green

# 4.4: Manager cannot touch Owner (403 or 404)
Write-Host "  -> Testing Manager attempting to demote Owner..."
$forbiddenOwnerTouch = Invoke-Api -Method "POST" -Path "/api/admin/users/$($ownerAuth.User.id)/role" -AuthContext $managerAuth -Body @{ role = "customer" } -ExpectedStatuses @(403, 404)
Write-Host "  -> Correctly blocked ($($forbiddenOwnerTouch.StatusCode))" -ForegroundColor Green

# 4.5: Employee and Customer cannot access admin endpoints (403 Forbidden)
Write-Host "  -> Testing Employee access to /api/admin/users..."
$employeeForbidden = Invoke-Api -Method "GET" -Path "/api/admin/users" -AuthContext $employeeAuth -ExpectedStatuses @(403)
Write-Host "  -> Employee correctly received 403 Forbidden" -ForegroundColor Green

Write-Host "  -> Testing Customer access to /api/admin/users..."
$customerForbidden = Invoke-Api -Method "GET" -Path "/api/admin/users" -AuthContext $customerAuth -ExpectedStatuses @(403)
Write-Host "  -> Customer correctly received 403 Forbidden" -ForegroundColor Green

# --- STEP 5: Audit Log Integrity & Isolation ---
Write-Host "`n[STEP 5] Verifying Audit Logs & Downward Isolation..." -ForegroundColor Cyan
$ownerAudit = Invoke-Api -Method "GET" -Path "/api/admin/audit-logs" -AuthContext $ownerAuth
Write-Host "  -> Owner sees $($ownerAudit.Json.audit_logs.Count) audit log entries" -ForegroundColor Green
if ($ownerAudit.Json.audit_logs.Count -eq 0) { throw "Expected audit log entries from role changes" }

$managerAudit = Invoke-Api -Method "GET" -Path "/api/admin/audit-logs" -AuthContext $managerAuth
Write-Host "  -> Manager sees $($managerAudit.Json.audit_logs.Count) audit log entries" -ForegroundColor Green
# Ensure manager doesn't see owner actor audit entries
$managerAuditOwners = $managerAudit.Json.audit_logs | Where-Object { $_.actor_role -eq "owner" }
if ($managerAuditOwners.Count -gt 0) {
    throw "SECURITY BREACH: Manager sees owner audit log records!"
}
Write-Host "  -> Audit Log Downward Isolation Confirmed: Owner audit actions hidden from Manager" -ForegroundColor Green

# --- STEP 6: Session Revocation & Redis Eviction ---
Write-Host "`n[STEP 6] Testing Session Revocation & Redis Cache Eviction..." -ForegroundColor Cyan
Write-Host "  -> Owner deactivating Employee account..."
Invoke-Api -Method "POST" -Path "/api/admin/users/$($employeeAuth.User.id)/status" -AuthContext $ownerAuth -Body @{ is_active = $false } -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Employee deactivated. Now testing immediate session eviction..."

# Employee request must immediately fail with 401 Unauthorized even if session was cached in Redis
$evictedResp = Invoke-Api -Method "GET" -Path "/api/auth/me" -AuthContext $employeeAuth -ExpectedStatuses @(401)
Write-Host "  -> Employee session successfully evicted from Redis and DB! (Status 401: $($evictedResp.Json.error))" -ForegroundColor Green

# Reactivate Employee
Invoke-Api -Method "POST" -Path "/api/admin/users/$($employeeAuth.User.id)/status" -AuthContext $ownerAuth -Body @{ is_active = $true } -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Reactivated Employee account" -ForegroundColor Green

# --- STEP 7: User Self-Logout ---
Write-Host "`n[STEP 7] Testing User Self-Logout..." -ForegroundColor Cyan
Invoke-Api -Method "POST" -Path "/api/auth/logout" -AuthContext $managerAuth -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Manager logged out" -ForegroundColor Green
$loggedOutResp = Invoke-Api -Method "GET" -Path "/api/auth/me" -AuthContext $managerAuth -ExpectedStatuses @(401)
Write-Host "  -> Session successfully cleared (Status 401: $($loggedOutResp.Json.error))" -ForegroundColor Green

Write-Host "`n================================================================="
Write-Host " ALL LIVE INSTANCE AUTH & RBAC TESTS PASSED SUCCESSFULLY! " -ForegroundColor Green
Write-Host "=================================================================`n"
