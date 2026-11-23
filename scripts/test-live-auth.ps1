$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
Add-Type -AssemblyName System.Net.Http

$baseUrl = "http://localhost:8080"
Write-Host "================================================================="
Write-Host " Testing Live Instance Google Auth & RBAC Architecture on $baseUrl"
Write-Host "================================================================="

function Invoke-MockLogin {
    param(
        [string]$RoleName,
        [int[]]$ExpectedStatuses = @(302)
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
    $cbStatusCode = [int]$cbResp.StatusCode
    if ($ExpectedStatuses -notcontains $cbStatusCode) {
        $body = $cbResp.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        throw "Failed to complete callback for $RoleName : Expected status $([string]::Join(',', $ExpectedStatuses)), got $cbStatusCode, Body: $body"
    }

    if ($cbStatusCode -ne 302) {
        return @{
            StatusCode = $cbStatusCode
            Body       = $cbResp.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        }
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
$ownerAuth = Invoke-MockLogin -RoleName "owner"
Write-Host "  -> Owner Logged In: ID=$($ownerAuth.User.id), Email=$($ownerAuth.User.email), Role=$($ownerAuth.User.role)" -ForegroundColor Green
if ($ownerAuth.User.role -ne "owner") {
    throw "Owner role was not bootstrapped! Expected 'owner', got '$($ownerAuth.User.role)'"
}

# --- STEP 2: Employee and Customer Logins ---
Write-Host "`n[STEP 2] Logging in candidate Employee and Customer..." -ForegroundColor Cyan
$employeeAuth = Invoke-MockLogin -RoleName "employee"
Write-Host "  -> Employee Candidate ID=$($employeeAuth.User.id), Email=$($employeeAuth.User.email), Initial Role=$($employeeAuth.User.role)"
$customerAuth = Invoke-MockLogin -RoleName "customer"
Write-Host "  -> Customer ID=$($customerAuth.User.id), Email=$($customerAuth.User.email), Initial Role=$($customerAuth.User.role)"

# --- STEP 3: Owner Promotes Employee and Rejects Manager ---
Write-Host "`n[STEP 3] Owner promoting candidate to 'employee' and testing 'manager' rejection..." -ForegroundColor Cyan
Invoke-Api -Method "POST" -Path "/api/admin/users/$($employeeAuth.User.id)/role" -AuthContext $ownerAuth -Body @{ role = "employee" } -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Promoted $($employeeAuth.User.email) to employee" -ForegroundColor Green

# Verify 'manager' role promotion is rejected
$rejectedManager = Invoke-Api -Method "POST" -Path "/api/admin/users/$($customerAuth.User.id)/role" -AuthContext $ownerAuth -Body @{ role = "manager" } -ExpectedStatuses @(400)
Write-Host "  -> Correctly rejected invalid 'manager' role assignment (400)" -ForegroundColor Green

# Refresh auth context after role update
$employeeAuth = Invoke-MockLogin -RoleName "employee"
if ($employeeAuth.User.role -ne "employee") { throw "Employee role update failed" }

# --- STEP 4: Upward Visibility & Access Control Verification ---
Write-Host "`n[STEP 4] Verifying Role Visibility & Access Control..." -ForegroundColor Cyan

# 4.1: Owner sees ALL users
$ownerList = Invoke-Api -Method "GET" -Path "/api/admin/users" -AuthContext $ownerAuth
$ownerUserRoles = $ownerList.Json.users | ForEach-Object { $_.role }
Write-Host "  -> Owner sees $($ownerList.Json.users.Count) users: $($ownerUserRoles -join ', ')" -ForegroundColor Green
if ($ownerList.Json.users.Count -lt 3) { throw "Owner should see at least 3 users (owner, employee, customer)" }

# 4.2: Employee cannot access admin user directory (403 Forbidden)
Write-Host "  -> Testing Employee access to /api/admin/users..."
$employeeForbidden = Invoke-Api -Method "GET" -Path "/api/admin/users" -AuthContext $employeeAuth -ExpectedStatuses @(403)
Write-Host "  -> Employee correctly received 403 Forbidden" -ForegroundColor Green

# 4.3: Customer cannot access admin user directory (403 Forbidden)
Write-Host "  -> Testing Customer access to /api/admin/users..."
$customerForbidden = Invoke-Api -Method "GET" -Path "/api/admin/users" -AuthContext $customerAuth -ExpectedStatuses @(403)
Write-Host "  -> Customer correctly received 403 Forbidden" -ForegroundColor Green

# --- STEP 5: Audit Log Integrity & Access Restriction ---
Write-Host "`n[STEP 5] Verifying Audit Logs & Access Restriction..." -ForegroundColor Cyan
$ownerAudit = Invoke-Api -Method "GET" -Path "/api/admin/audit-logs" -AuthContext $ownerAuth
Write-Host "  -> Owner sees $($ownerAudit.Json.audit_logs.Count) audit log entries" -ForegroundColor Green
if ($ownerAudit.Json.audit_logs.Count -eq 0) { throw "Expected audit log entries from role changes" }

# Non-owner cannot access audit logs
$employeeAuditForbidden = Invoke-Api -Method "GET" -Path "/api/admin/audit-logs" -AuthContext $employeeAuth -ExpectedStatuses @(403)
Write-Host "  -> Employee correctly rejected from audit logs (403 Forbidden)" -ForegroundColor Green

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
$employeeAuth = Invoke-MockLogin -RoleName "employee"
Invoke-Api -Method "POST" -Path "/api/auth/logout" -AuthContext $employeeAuth -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Employee logged out" -ForegroundColor Green
$loggedOutResp = Invoke-Api -Method "GET" -Path "/api/auth/me" -AuthContext $employeeAuth -ExpectedStatuses @(401)
Write-Host "  -> Session successfully cleared (Status 401: $($loggedOutResp.Json.error))" -ForegroundColor Green

# --- STEP 8: User Self-Deactivation & Staff Reactivation ---
Write-Host "`n[STEP 8] Testing User Self-Deactivation & Downward Isolation..." -ForegroundColor Cyan

# 8.1: Sole Owner cannot deactivate themselves (409 Conflict)
Write-Host "  -> Verifying Sole Owner cannot self-deactivate..."
$ownerDeactBlocked = Invoke-Api -Method "POST" -Path "/api/auth/deactivate" -AuthContext $ownerAuth -ExpectedStatuses @(409)
Write-Host "  -> Sole Owner correctly blocked with 409 Conflict: $($ownerDeactBlocked.Json.error)" -ForegroundColor Green

# 8.2: Customer self-deactivates
Write-Host "  -> Customer logging in..."
$customerAuth = Invoke-MockLogin -RoleName "customer"
Write-Host "  -> Customer deactivating account..."
Invoke-Api -Method "POST" -Path "/api/auth/deactivate" -AuthContext $customerAuth -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Customer self-deactivated successfully" -ForegroundColor Green

# 8.3: Customer session is immediately evicted
$deactivatedMeResp = Invoke-Api -Method "GET" -Path "/api/auth/me" -AuthContext $customerAuth -ExpectedStatuses @(401)
Write-Host "  -> Customer session immediately rejected (401)" -ForegroundColor Green

# 8.4: Subsequent OAuth login for deactivated customer is rejected with 403 Forbidden
Write-Host "  -> Verifying deactivated customer cannot log in via OAuth..."
$deactivatedLogin = Invoke-MockLogin -RoleName "customer" -ExpectedStatuses @(403)
if ($deactivatedLogin.Body -notmatch "Account Deactivated") {
    throw "Expected deactivated notice page or message, got: $($deactivatedLogin.Body)"
}
Write-Host "  -> OAuth login correctly rejected with 403 and Atelier Account Deactivated notice" -ForegroundColor Green

# 8.5: Downward Isolation - Employee cannot deactivate another employee or owner (403 Forbidden)
Write-Host "  -> Verifying employee cannot deactivate owner or staff..."
$employeeAuth = Invoke-MockLogin -RoleName "employee"
$employeeForbiddenDeact = Invoke-Api -Method "POST" -Path "/api/admin/users/$($ownerAuth.User.id)/status" -AuthContext $employeeAuth -Body @{ is_active = $false } -ExpectedStatuses @(403)
Write-Host "  -> Employee correctly blocked from deactivating Owner (403 Forbidden)" -ForegroundColor Green

# 8.6: Staff customer reactivation - Employee can reactivate customer
Write-Host "  -> Employee reactivating Customer account..."
Invoke-Api -Method "POST" -Path "/api/admin/users/$($customerAuth.User.id)/status" -AuthContext $employeeAuth -Body @{ is_active = $true } -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Employee successfully reactivated customer account" -ForegroundColor Green

# 8.7: Reactivated customer can log in again
Write-Host "  -> Customer logging in after reactivation..."
$customerReactivatedAuth = Invoke-MockLogin -RoleName "customer"
if ($customerReactivatedAuth.User.id -ne $customerAuth.User.id) {
    throw "Customer ID mismatch after reactivation"
}
Write-Host "  -> Customer successfully logged in after reactivation" -ForegroundColor Green

# --- STEP 9: Owner-Managed Employee Inventory Permissions ---
Write-Host "`n[STEP 9] Testing Owner-Managed Employee Inventory Permissions..." -ForegroundColor Cyan

# 9.1: Employee without inventory permission cannot access inventory mutation or query endpoints
Write-Host "  -> Verifying employee without inventory permission is rejected on /catalog/invalidate and /operations/inventory..."
$empInvBlocked = Invoke-Api -Method "POST" -Path "/api/catalog/invalidate" -AuthContext $employeeAuth -ExpectedStatuses @(403)
Write-Host "  -> Employee catalog invalidation rejected with 403 Forbidden" -ForegroundColor Green
$empGetInvBlocked = Invoke-Api -Method "GET" -Path "/api/operations/inventory" -AuthContext $employeeAuth -ExpectedStatuses @(403)
Write-Host "  -> Employee inventory query rejected with 403 Forbidden" -ForegroundColor Green

# 9.2: Employee cannot update permissions (403 Forbidden)
Write-Host "  -> Verifying employee cannot modify permissions..."
$empPermBlocked = Invoke-Api -Method "POST" -Path "/api/admin/users/$($employeeAuth.User.id)/permissions" -AuthContext $employeeAuth -Body @{ can_manage_inventory = $true } -ExpectedStatuses @(403)
Write-Host "  -> Employee correctly blocked from modifying permissions (403 Forbidden)" -ForegroundColor Green

# 9.3: Owner grants inventory permission to Employee
Write-Host "  -> Owner granting can_manage_inventory=true to Employee..."
Invoke-Api -Method "POST" -Path "/api/admin/users/$($employeeAuth.User.id)/permissions" -AuthContext $ownerAuth -Body @{ can_manage_inventory = $true } -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Inventory permission granted" -ForegroundColor Green

# 9.4: Employee with permission can access inventory endpoints
Write-Host "  -> Verifying employee with permission can invalidate catalog and query inventory..."
$empInvAllowed = Invoke-Api -Method "POST" -Path "/api/catalog/invalidate" -AuthContext $employeeAuth -ExpectedStatuses @(200)
Write-Host "  -> Employee successfully invalidated catalog (200 OK)" -ForegroundColor Green
$empGetInvAllowed = Invoke-Api -Method "GET" -Path "/api/operations/inventory" -AuthContext $employeeAuth -ExpectedStatuses @(200)
Write-Host "  -> Employee successfully retrieved inventory (200 OK)" -ForegroundColor Green

# 9.5: Owner revokes inventory permission
Write-Host "  -> Owner revoking inventory permission from Employee..."
Invoke-Api -Method "POST" -Path "/api/admin/users/$($employeeAuth.User.id)/permissions" -AuthContext $ownerAuth -Body @{ can_manage_inventory = $false } -ExpectedStatuses @(200) | Out-Null
Write-Host "  -> Inventory permission revoked" -ForegroundColor Green

# 9.6: Employee is once again rejected
$empRevokedInvBlocked = Invoke-Api -Method "POST" -Path "/api/catalog/invalidate" -AuthContext $employeeAuth -ExpectedStatuses @(403)
Write-Host "  -> Employee correctly blocked after permission revocation (403 Forbidden)" -ForegroundColor Green

Write-Host "`n================================================================="
Write-Host " ALL LIVE INSTANCE AUTH & RBAC TESTS PASSED SUCCESSFULLY! " -ForegroundColor Green
Write-Host "=================================================================`n"
