param(
  [Parameter(Mandatory=$true)][string]$BinarySource,
  [string]$Destination = "$env:LOCALAPPDATA\Baron\baron.exe",
  [string]$Sha256,
  [switch]$AllowReplace
)

if ((Test-Path -LiteralPath $Destination) -and -not $AllowReplace) {
  throw "Refusing to overwrite existing baron command at $Destination. Use -AllowReplace only for explicit migration."
}
if ($Sha256) {
  $actual = (Get-FileHash -LiteralPath $BinarySource -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actual -ne $Sha256.ToLowerInvariant()) {
    throw "Refusing to install: binary SHA-256 does not match -Sha256."
  }
}
$parent = Split-Path -Parent $Destination
New-Item -ItemType Directory -Force -Path $parent | Out-Null
Copy-Item -LiteralPath $BinarySource -Destination $Destination -Force
Write-Output "Installed $Destination"
