param(
  [string]$BinarySource,
  [string]$Destination = "$env:LOCALAPPDATA\Baron\baron.exe",
  [string]$Sha256,
  [string]$Repository = "thienty1207/Baron-Nexus",
  [string]$Version = "latest",
  [switch]$AllowReplace
)

$ErrorActionPreference = "Stop"
$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("baron-install-" + [guid]::NewGuid().ToString("N"))

function Set-BaronBinaryAcl {
  param([Parameter(Mandatory=$true)][string]$Path)
  $acl = Get-Acl -LiteralPath $Path
  $acl.SetAccessRuleProtection($true, $true)
  $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
  $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
    $identity,
    [System.Security.AccessControl.FileSystemRights]::FullControl,
    [System.Security.AccessControl.AccessControlType]::Allow
  )
  $acl.SetAccessRule($rule)
  Set-Acl -LiteralPath $Path -AclObject $acl
}

try {
  if ((Test-Path -LiteralPath $Destination) -and -not $AllowReplace) {
    throw "Baron is already installed at $Destination. Run 'baron update' to update Baron or 'baron deepseek api_key' to change the DeepSeek API key. Do not rerun install.ps1; use -AllowReplace only for an explicit binary migration."
  }
  New-Item -ItemType Directory -Force -Path $tempRoot | Out-Null
  if (-not $BinarySource) {
    if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') {
      throw "Invalid Repository; expected owner/repository."
    }
    if ($Version -eq "latest") {
      $releaseApi = "https://api.github.com/repos/$Repository/releases/latest"
      $release = Invoke-RestMethod -Headers @{ Accept = "application/vnd.github+json"; "User-Agent" = "Baron-Nexus/$Version" } -Uri $releaseApi
      $latestTag = [string]$release.tag_name
      if ($latestTag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
        throw "GitHub latest Baron release has no valid v-prefixed semantic tag."
      }
      $baseUrl = "https://github.com/$Repository/releases/download/$latestTag"
    } elseif ($Version -match '^v[0-9]+\.[0-9]+\.[0-9]+$') {
      $baseUrl = "https://github.com/$Repository/releases/download/$Version"
    } elseif ($Version -match '^[0-9]+\.[0-9]+\.[0-9]+$') {
      $baseUrl = "https://github.com/$Repository/releases/download/v$Version"
    } else {
      throw "Version must be latest or a semantic version such as 0.1.5."
    }
    $manifestPath = Join-Path $tempRoot "release-manifest.json"
    $sumsPath = Join-Path $tempRoot "SHA256SUMS"
    $BinarySource = Join-Path $tempRoot "baron-windows-amd64.exe"
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/release-manifest.json" -OutFile $manifestPath
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/SHA256SUMS" -OutFile $sumsPath
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/baron-windows-amd64.exe" -OutFile $BinarySource
    $manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
    $releaseVersion = [string]$manifest.version
    if ($releaseVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
      throw "Baron release manifest has no valid version."
    }
    if ($Version -ne "latest" -and $releaseVersion -ne $Version.TrimStart('v')) {
      throw "Baron release tag and manifest version differ."
    }
    $sumLine = Get-Content -LiteralPath $sumsPath | Where-Object { $_ -match '(^|\s)baron-windows-amd64\.exe$' } | Select-Object -First 1
    if (-not $sumLine) { throw "SHA256SUMS has no Windows asset entry." }
    $expected = ($sumLine -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -LiteralPath $BinarySource -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw "Refusing to install: SHA-256 verification failed." }
    $reported = (& $BinarySource --version | Out-String).Trim()
    if ($reported -ne "baron $releaseVersion") { throw "Downloaded Baron binary failed version validation." }
  } else {
    $item = Get-Item -LiteralPath $BinarySource -ErrorAction Stop
    if ($item.PSIsContainer) { throw "-BinarySource must be a regular file." }
    $BinarySource = $item.FullName
  }
  if ($Sha256) {
    $actual = (Get-FileHash -LiteralPath $BinarySource -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $Sha256.ToLowerInvariant()) { throw "Refusing to install: binary SHA-256 does not match -Sha256." }
  }
  $parent = Split-Path -Parent $Destination
  New-Item -ItemType Directory -Force -Path $parent | Out-Null
  Copy-Item -LiteralPath $BinarySource -Destination $Destination -Force
  Set-BaronBinaryAcl -Path $Destination
  Write-Output "Installed $Destination"
} finally {
  if (Test-Path -LiteralPath $tempRoot) { Remove-Item -LiteralPath $tempRoot -Recurse -Force }
}
