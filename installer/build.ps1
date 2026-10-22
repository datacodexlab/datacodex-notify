# Builds the release executables, the Windows installer, and the portable zip into dist\.
#
#   ./installer/build.ps1 -Version 1.0.0
#
# For tests, -AppGuid and -OutputName build an installer that registers under a
# different identity, so it can never be mistaken for a real installation.
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$AppGuid = "",
    [string]$OutputName = "",
    [switch]$SkipPortable
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $root "dist"
New-Item -ItemType Directory -Force $dist | Out-Null

$env:CGO_ENABLED = "0"
Push-Location $root
try {
    go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$Version" -o (Join-Path $dist "datacodex-notify.exe") ./cmd/datacodex-notify
    if ($LASTEXITCODE -ne 0) { throw "app build failed" }
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $dist "datacodex-notify-cli.exe") ./cmd/datacodex-notify-cli
    if ($LASTEXITCODE -ne 0) { throw "cli build failed" }
}
finally {
    Pop-Location
}

$iscc = @(
    (Get-Command ISCC.exe -ErrorAction SilentlyContinue).Source,
    "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe",
    "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
    "$env:ProgramFiles\Inno Setup 6\ISCC.exe"
) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $iscc) { throw "Inno Setup (ISCC.exe) was not found. Install it with: winget install JRSoftware.InnoSetup" }

$defines = @("/DAppVersion=$Version")
if ($AppGuid -ne "") { $defines += "/DAppGuid=$AppGuid" }
if ($OutputName -ne "") { $defines += "/DOutputName=$OutputName" }
& $iscc @defines (Join-Path $PSScriptRoot "datacodex-notify.iss")
if ($LASTEXITCODE -ne 0) { throw "installer build failed" }

if (-not $SkipPortable) {
    $stage = Join-Path $dist "portable-stage"
    Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force $stage | Out-Null
    Copy-Item (Join-Path $dist "datacodex-notify.exe"), (Join-Path $dist "datacodex-notify-cli.exe") $stage
    Copy-Item (Join-Path $PSScriptRoot "README-portable.txt") $stage
    Copy-Item (Join-Path $root "LICENSE"), (Join-Path $root "THIRD_PARTY_NOTICES.md") $stage
    $zip = Join-Path $dist "DatacodexNotify-Portable-$Version.zip"
    Remove-Item -LiteralPath $zip -Force -ErrorAction SilentlyContinue
    Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $zip
    Remove-Item -LiteralPath $stage -Recurse -Force
}

Get-ChildItem $dist -File | Where-Object { $_.Name -match "Setup|Portable" } | ForEach-Object { "{0}  {1:N1} MB" -f $_.Name, ($_.Length / 1MB) }
