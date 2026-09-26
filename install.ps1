# ==============================================================================
# install.ps1 - PowerShell installer for skills-manager on Windows (Go)
# Downloads or builds standalone executable to $HOME\.local\bin\skills.exe
# ==============================================================================
$ErrorActionPreference = "Stop"

function Write-Header ($text) { Write-Host $text -ForegroundColor Cyan }
function Write-Success ($text) { Write-Host $text -ForegroundColor Green }
function Write-Warn ($text) { Write-Host "Note: $text" -ForegroundColor Yellow }
function Write-Err ($text) { Write-Host "Error: $text" -ForegroundColor Red }

Write-Header "Installing Skills Manager..."
Write-Host ""

$targetDir = [System.IO.Path]::Combine($HOME, ".local", "bin")
if (-not (Test-Path $targetDir)) {
    New-Item -ItemType Directory -Path $targetDir -Force | Out-Null
}

$targetBin = [System.IO.Path]::Combine($targetDir, "skills.exe")
$githubRepo = "akunzai/skills-manager"

# 1. Check if building from local clone
if ($PSScriptRoot -and (Test-Path (Join-Path $PSScriptRoot "cmd\skills\main.go"))) {
    if (Get-Command "go" -ErrorAction SilentlyContinue) {
        Write-Host "Building from local source with Go..."
        Push-Location $PSScriptRoot
        try {
            & go build -ldflags="-s -w" -o $targetBin .\cmd\skills
            Write-Host ""
            Write-Success "Installed Skills Manager."
            Write-Host "   Installed at: $targetBin"
            exit 0
        } finally {
            Pop-Location
        }
    }
}

# 2. Detect Architecture
$arch = if ([System.Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else {
    "386"
}

Write-Host "Platform: windows_$arch"

# 3. Download the archive and checksum manifest; names mirror .goreleaser.yaml
$assetName = "skills_windows_$arch.zip"
$downloadBase = "https://github.com/$githubRepo/releases/latest/download"

$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("skills_inst_" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempDir -Force | Out-Null

try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $headers = @{ "User-Agent" = "skills-manager-installer" }
    $archiveFile = Join-Path $tempDir $assetName
    $checksumsFile = Join-Path $tempDir "checksums.txt"

    Write-Host "Downloading: $downloadBase/$assetName"
    try {
        Invoke-WebRequest -Uri "$downloadBase/$assetName" -OutFile $archiveFile -Headers $headers -UseBasicParsing
    } catch {
        Write-Err "No prebuilt binary found for windows_$arch."
        exit 1
    }
    try {
        Invoke-WebRequest -Uri "$downloadBase/checksums.txt" -OutFile $checksumsFile -Headers $headers -UseBasicParsing
    } catch {
        Write-Err "Failed to download checksums.txt; refusing to install an unverified binary."
        exit 1
    }

    $expected = $null
    foreach ($line in Get-Content $checksumsFile) {
        $fields = -split $line
        if ($fields.Count -eq 2 -and $fields[1] -eq $assetName) { $expected = $fields[0] }
    }
    $actual = (Get-FileHash -Algorithm SHA256 $archiveFile).Hash
    if (-not $expected -or $expected -ne $actual) {
        Write-Err "Checksum verification failed for $assetName."
        exit 1
    }
    Write-Host "Checksum verified."

    Expand-Archive -Path $archiveFile -DestinationPath $tempDir -Force
    Move-Item -Path (Join-Path $tempDir "skills.exe") -Destination $targetBin -Force

    Write-Host ""
    Write-Success "Installed Skills Manager."
    Write-Host "   Installed at: $targetBin"

    # Check PATH
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$targetDir*") {
        Write-Host ""
        Write-Warn "$targetDir is not currently in your User PATH."
        Write-Host "Run the following in PowerShell to add it permanently:"
        Write-Host "[Environment]::SetEnvironmentVariable('Path', `$userPath + ';$targetDir', 'User')" -ForegroundColor Cyan
    }
} finally {
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
