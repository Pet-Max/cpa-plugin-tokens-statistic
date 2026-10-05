$ErrorActionPreference = "Stop"

$rootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$version = if ($env:VERSION) { $env:VERSION } else { "v0.1.1" }
# Store assets carry the version without the leading "v" (e.g. tokens-statistic_0.1.0_windows_amd64.zip).
$releaseVersion = $version.TrimStart("v")
$releaseDir = Join-Path $rootDir "dist\release\$version"
$env:GOOS = "windows"
$env:CGO_ENABLED = "1"
$env:Path = "C:\mingw64\mingw64\bin;" + $env:Path

function Build-WindowsDll {
    param(
        [Parameter(Mandatory = $true)][string]$Arch,
        [Parameter(Mandatory = $true)][string]$OutputPath
    )
    $env:GOARCH = $Arch
    # Prefer zig cc (cross-compiles both windows/amd64 and windows/arm64 without a native
    # MinGW toolchain); fall back to gcc when zig is unavailable.
    if (Get-Command zig -ErrorAction SilentlyContinue) {
        if ($Arch -eq "arm64") {
            $env:CC = "zig cc -target aarch64-windows-gnu"
        }
        else {
            $env:CC = "zig cc -target x86_64-windows-gnu"
        }
        Write-Host "using CC=$($env:CC)"
    }
    else {
        $env:CC = "gcc"
    }
    go build -buildmode=c-shared -buildvcs=false -ldflags="-s -w -X github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin.version=$version" -o $OutputPath .
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed for windows/$Arch (exit $LASTEXITCODE)"
    }
    Get-Item $OutputPath | Format-List Name, Length, LastWriteTime
}

function Package-Zip {
    param(
        [Parameter(Mandatory = $true)][string]$ZipPath,
        [Parameter(Mandatory = $true)][string]$LibraryPath
    )
    # Python's zipfile keeps the entry at the zip root with forward slashes, exactly what the
    # CLIProxyAPI store installer requires (nested paths and multiple libraries are rejected).
    $code = "import sys, zipfile; z = zipfile.ZipFile(sys.argv[1], 'w', zipfile.ZIP_DEFLATED); z.write(sys.argv[2], sys.argv[3]); z.close()"
    python -c $code $ZipPath $LibraryPath "tokens-statistic-$version.dll"

    if ($LASTEXITCODE -ne 0) {
        throw "packaging failed for $ZipPath"
    }
}

function Write-Checksums {
    param([Parameter(Mandatory = $true)][string]$Directory)
    # sha256sum-compatible layout: "<hex>  <filename>" (two spaces), matching the store's checksums.txt.
    $lines = Get-ChildItem $Directory -Filter *.zip | Sort-Object Name | ForEach-Object {
        $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        "$hash  $($_.Name)"
    }
    Set-Content -Path (Join-Path $Directory "checksums.txt") -Value $lines -Encoding ascii
}

Push-Location $rootDir
try {
    New-Item -ItemType Directory -Force -Path $releaseDir | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $rootDir "dist") | Out-Null

    Build-WindowsDll -Arch "amd64" -OutputPath (Join-Path $rootDir "dist\tokens-statistic.dll")
    Package-Zip -ZipPath (Join-Path $releaseDir "tokens-statistic_${releaseVersion}_windows_amd64.zip") -LibraryPath (Join-Path $rootDir "dist\tokens-statistic.dll")
    Write-Host "release package: tokens-statistic_${releaseVersion}_windows_amd64.zip"

    # Best effort: windows/arm64 via zig cc. Reported, not fatal, when the toolchain misbehaves.
    try {
        Build-WindowsDll -Arch "arm64" -OutputPath (Join-Path $rootDir "dist\tokens-statistic-windows-arm64.dll")
        Package-Zip -ZipPath (Join-Path $releaseDir "tokens-statistic_${releaseVersion}_windows_arm64.zip") -LibraryPath (Join-Path $rootDir "dist\tokens-statistic-windows-arm64.dll")
        Write-Host "release package: tokens-statistic_${releaseVersion}_windows_arm64.zip"
    }
    catch {
        Write-Warning "windows/arm64 build skipped: $($_.Exception.Message)"
    }

    Write-Checksums -Directory $releaseDir
    Write-Host "checksums: $(Join-Path $releaseDir 'checksums.txt')"
}
finally {
    Pop-Location
}
