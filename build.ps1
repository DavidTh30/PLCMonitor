# Builds every variant of PLC Monitor and the Windows installers.
#
# Windows, 64-bit (x64) and 32-bit (x86):
#   PLCMonitorApp.exe / PLCMonitorApp-x86.exe    desktop app (own window, no console)
#   plcmonitor.exe    / plcmonitor-x86.exe       console program with a web dashboard
#   PLCMonitorSetup-<ver>-x64.exe / -x86.exe     installers containing the above
# Linux (dashboard, or the PROFINET DCP agent; pure Go, one file to copy):
#   plcmonitor-linux-amd64   PC, 64-bit
#   plcmonitor-linux-386     PC, 32-bit
#   plcmonitor-linux-arm64   Raspberry Pi 3/4/5 with a 64-bit OS, other ARM64 boards
#   plcmonitor-linux-armv7   Raspberry Pi with a 32-bit OS, other ARMv7 boards
#
# Usage: .\build.ps1 -Version 1.2.0
param([string]$Version = '1.0.0')
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$ld = "-X main.version=$Version"

# Run a native command and stop on failure ($ErrorActionPreference does not
# cover exit codes in Windows PowerShell).
function Invoke-Go {
    & go @args
    if ($LASTEXITCODE -ne 0) { throw "go $($args -join ' ') failed" }
}

$old = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; GOARM = $env:GOARM; CGO_ENABLED = $env:CGO_ENABLED }
try {
    $env:CGO_ENABLED = '0'
    Invoke-Go vet ./...
    Invoke-Go vet -tags gui ./...

    # Program icon + manifest from winres/icon.png, one COFF object per architecture.
    Invoke-Go run ./tools/mkres
    Invoke-Go run ./tools/mkres -arch 386 -o rsrc_windows_386.syso '-ico='
    Invoke-Go run ./tools/mkres -o installer/rsrc_windows_amd64.syso '-ico='
    Invoke-Go run ./tools/mkres -arch 386 -o installer/rsrc_windows_386.syso '-ico='

    # Windows
    $env:GOOS = 'windows'
    $win = @(
        @{ Arch = 'amd64'; Suffix = '';     Label = 'x64' },
        @{ Arch = '386';   Suffix = '-x86'; Label = 'x86' }
    )
    foreach ($w in $win) {
        $env:GOARCH = $w.Arch
        Invoke-Go build -tags gui -ldflags "-H windowsgui -s -w $ld" -o "PLCMonitorApp$($w.Suffix).exe" .
        Invoke-Go build -ldflags "-s -w $ld" -o "plcmonitor$($w.Suffix).exe" .
    }

    # Linux
    $env:GOOS = 'linux'
    foreach ($l in @(@{ Arch = 'amd64' }, @{ Arch = '386' }, @{ Arch = 'arm64' }, @{ Arch = 'arm'; Arm = '7'; Name = 'armv7' })) {
        $env:GOARCH = $l.Arch
        $env:GOARM = $l.Arm
        $name = if ($l.Name) { $l.Name } else { $l.Arch }
        Invoke-Go vet .
        Invoke-Go build -ldflags "-s -w $ld" -o "plcmonitor-linux-$name" .
    }
    $env:GOARM = $null

    # Installers: the program files go into installer/payload/payload.zip,
    # which the setup program embeds; one installer per Windows architecture.
    $env:GOOS = 'windows'
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    foreach ($w in $win) {
        $stage = Join-Path ([IO.Path]::GetTempPath()) "plcmonitor-stage-$PID-$($w.Label)"
        New-Item -ItemType Directory -Force (Join-Path $stage 'linux') | Out-Null
        try {
            Copy-Item "PLCMonitorApp$($w.Suffix).exe" (Join-Path $stage 'PLCMonitorApp.exe')
            Copy-Item "plcmonitor$($w.Suffix).exe" (Join-Path $stage 'plcmonitor.exe')
            Copy-Item README.md, CHANGELOG.md $stage
            Copy-Item plcmonitor-linux-amd64, plcmonitor-linux-arm64, plcmonitor-linux-armv7 (Join-Path $stage 'linux')
            $zip = Join-Path $PSScriptRoot 'installer\payload\payload.zip'
            Remove-Item $zip -ErrorAction SilentlyContinue
            [IO.Compression.ZipFile]::CreateFromDirectory($stage, $zip, [IO.Compression.CompressionLevel]::Optimal, $false)
        } finally {
            Remove-Item -Recurse -Force $stage
        }
        $env:GOARCH = $w.Arch
        Invoke-Go vet ./installer
        Invoke-Go build -ldflags "-H windowsgui -s -w $ld" -o "PLCMonitorSetup-$Version-$($w.Label).exe" ./installer
    }
} finally {
    foreach ($k in $old.Keys) { Set-Item "env:$k" $old[$k] -ErrorAction SilentlyContinue; if ($null -eq $old[$k]) { Remove-Item "env:$k" -ErrorAction SilentlyContinue } }
}

Write-Host "Built version $Version for Windows x64/x86 (app, console, installer) and Linux amd64/386/arm64/armv7."
