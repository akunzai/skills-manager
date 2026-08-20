# ==============================================================================
# install.ps1 - PowerShell installer for skills-manager on Windows (Go)
# Downloads or builds standalone executable to $HOME\.local\bin\skills.exe
# ==============================================================================
$ErrorActionPreference = "Stop"

function Write-Header ($text) { Write-Host "🚀 $text" -ForegroundColor Cyan }
function Write-Success ($text) { Write-Host "✨ $text" -ForegroundColor Green }
function Write-Warn ($text) { Write-Host "⚠️  $text" -ForegroundColor Yellow }
function Write-Err ($text) { Write-Host "❌ $text" -ForegroundColor Red }

Write-Header "Installing Skills Manager (skills) on Windows..."
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
        Write-Host "⚙️  Building from local source with Go..."
        Push-Location $PSScriptRoot
        try {
            & go build -ldflags="-s -w" -o $targetBin .\cmd\skills
            Write-Host ""
            Write-Success "Installation successful!"
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

Write-Host "🔍 Detected architecture: windows_$arch"

# 3. Fetch latest release from GitHub API
$apiUrl = "https://api.github.com/repos/$githubRepo/releases/latest"
Write-Host "📦 Fetching latest release information..."

$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("skills_inst_" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempDir -Force | Out-Null

try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $headers = @{ "User-Agent" = "skills-manager-installer" }
    $release = Invoke-RestMethod -Uri $apiUrl -Headers $headers -UseBasicParsing

    $assetUrl = $null
    foreach ($asset in $release.assets) {
        $name = $asset.name.ToLower()
        if ($name.Contains("windows") -and $name.Contains($arch)) {
            $assetUrl = $asset.browser_download_url
            break
        }
    }

    if (-not $assetUrl) {
        foreach ($asset in $release.assets) {
            if ($asset.name -eq "skills.exe" -or $asset.name -eq "skills") {
                $assetUrl = $asset.browser_download_url
                break
            }
        }
    }

    if (-not $assetUrl) {
        Write-Err "No prebuilt binary found for windows_$arch."
        exit 1
    }

    Write-Host "📥 Downloading: $assetUrl"
    $archiveFile = Join-Path $tempDir "downloaded.zip"
    Invoke-WebRequest -Uri $assetUrl -OutFile $archiveFile -Headers $headers -UseBasicParsing

    if ($assetUrl.EndsWith(".zip")) {
        Expand-Archive -Path $archiveFile -DestinationPath $tempDir -Force
        $extractedExe = Join-Path $tempDir "skills.exe"
        if (-not (Test-Path $extractedExe)) {
            $extractedExe = Join-Path $tempDir "skills"
        }
        Move-Item -Path $extractedExe -Destination $targetBin -Force
    } else {
        Move-Item -Path $archiveFile -Destination $targetBin -Force
    }

    Write-Host ""
    Write-Success "Installation successful!"
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
