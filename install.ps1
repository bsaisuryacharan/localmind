# localmind installer (Windows)
# Usage: iwr -useb https://raw.githubusercontent.com/bsaisuryacharan/localmind/main/install.ps1 | iex

$ErrorActionPreference = 'Stop'

$Repo       = if ($env:LOCALMIND_REPO)        { $env:LOCALMIND_REPO }        else { 'bsaisuryacharan/localmind' }
$Version    = if ($env:LOCALMIND_VERSION)     { $env:LOCALMIND_VERSION }     else { 'latest' }
$InstallDir = if ($env:LOCALMIND_INSTALL_DIR) { $env:LOCALMIND_INSTALL_DIR } else { Join-Path $env:USERPROFILE '.localmind' }
$BinDir     = if ($env:LOCALMIND_BIN_DIR)     { $env:LOCALMIND_BIN_DIR }     else { Join-Path $env:USERPROFILE '.localmind\bin' }

function Log($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Die($m) { Write-Host "error: $m" -ForegroundColor Red; exit 1 }

$arch = if ([Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
} else { Die 'localmind requires a 64-bit Windows' }

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Log 'warning: docker not found on PATH; install Docker Desktop before running `localmind up`'
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $BinDir     | Out-Null

$url = if ($Version -eq 'latest') {
    "https://github.com/$Repo/releases/latest/download/localmind-windows-$arch.zip"
} else {
    "https://github.com/$Repo/releases/download/$Version/localmind-windows-$arch.zip"
}

$tmp = Join-Path $env:TEMP "localmind-install-$([guid]::NewGuid())"
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    Log "downloading $url"
    Invoke-WebRequest -Uri $url -OutFile (Join-Path $tmp 'localmind.zip')
    Expand-Archive -Path (Join-Path $tmp 'localmind.zip') -DestinationPath $tmp -Force
    Copy-Item (Join-Path $tmp 'localmind.exe') -Destination (Join-Path $BinDir 'localmind.exe') -Force
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not ($userPath -split ';' | Where-Object { $_ -eq $BinDir })) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$BinDir", 'User')
    Log "added $BinDir to user PATH (open a new shell to pick it up)"
}

Log "installed: $BinDir\localmind.exe"
Log 'next: run `localmind init` to configure, then `localmind up`'
