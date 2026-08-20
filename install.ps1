# ==============================================================================
# install.ps1 - PowerShell installer for skills-manager on Windows
# Packages standalone executable to $HOME\.local\bin\skills with cmd/ps1 wrappers
# ==============================================================================
$ErrorActionPreference = "Stop"

# Colors
function Write-Header ($text) { Write-Host "🚀 $text" -ForegroundColor Cyan }
function Write-Success ($text) { Write-Host "✨ $text" -ForegroundColor Green }
function Write-Warn ($text) { Write-Host "⚠️  $text" -ForegroundColor Yellow }
function Write-Err ($text) { Write-Host "❌ $text" -ForegroundColor Red }

Write-Header "Installing Skills Manager (skills) on Windows..."
Write-Host ""

# 1. Locate Python 3.10+
$candidates = @("python", "python3", "py")
$pythonExe = $null
$pyVer = $null
$foundMajor = 0
$foundMinor = 0

foreach ($cmd in $candidates) {
    if (Get-Command $cmd -ErrorAction SilentlyContinue) {
        try {
            $verOutput = (& $cmd -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')" 2>$null)
            $verStr = if ($verOutput -is [array]) { ($verOutput | Where-Object { $_ -match '^\d+\.\d+' } | Select-Object -Last 1) } else { [string]$verOutput }
            if ($LASTEXITCODE -eq 0 -and $verStr -and ($verStr.Trim() -match '^\d+\.\d+')) {
                $trimmed = $verStr.Trim()
                $parts = $trimmed.Split(".")
                $maj = [int]$parts[0]
                $min = [int]$parts[1]
                if ($maj -gt 3 -or ($maj -eq 3 -and $min -ge 10)) {
                    $pythonExe = $cmd
                    $pyVer = $trimmed
                    $foundMajor = $maj
                    $foundMinor = $min
                    break
                } elseif (-not $pythonExe) {
                    $pythonExe = $cmd
                    $pyVer = $trimmed
                    $foundMajor = $maj
                    $foundMinor = $min
                }
            }
        } catch {
            # Ignore errors and continue to next candidate
        }
    }
}

if (-not $pythonExe) {
    Write-Err "Python is required but was not found in PATH."
    Write-Host "Please install Python 3.10 or higher from https://python.org or Microsoft Store."
    exit 1
}

if ($foundMajor -lt 3 -or ($foundMajor -eq 3 -and $foundMinor -lt 10)) {
    Write-Err "Python 3.10 or higher is required (found Python $pyVer)."
    Write-Host "Please install Python 3.10 or higher from https://python.org or Microsoft Store."
    exit 1
}

# 2. Determine target directories
$targetDir = [System.IO.Path]::Combine($HOME, ".local", "bin")
if (-not (Test-Path $targetDir)) {
    New-Item -ItemType Directory -Path $targetDir -Force | Out-Null
}

$targetBin = [System.IO.Path]::Combine($targetDir, "skills")
$targetCmd = [System.IO.Path]::Combine($targetDir, "skills.cmd")
$targetPs1 = [System.IO.Path]::Combine($targetDir, "skills.ps1")

# 3. Determine source directory
$sourceDir = ""
if ($PSScriptRoot -and (Test-Path (Join-Path $PSScriptRoot "src\skills_manager\cli.py"))) {
    $sourceDir = Join-Path $PSScriptRoot "src"
} else {
    Write-Host "📦 Fetching latest skills-manager from GitHub..."
    $tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("skills_inst_" + [System.Guid]::NewGuid().ToString("N"))
    git clone --depth 1 "https://github.com/akunzai/skills-manager.git" $tempDir | Out-Null
    $sourceDir = Join-Path $tempDir "src"
}

# 4. Build standalone zipapp
Write-Host "⚙️  Building standalone executable $targetBin..."
& $pythonExe -m zipapp "$sourceDir" -m "skills_manager.cli:main" -p "/usr/bin/env python3" -o "$targetBin"

# 5. Create Windows CMD and PowerShell Wrappers
$cmdContent = "@echo off`r`n$pythonExe `"%~dp0skills`" %*`r`n"
[System.IO.File]::WriteAllText($targetCmd, $cmdContent, [System.Text.Encoding]::ASCII)

$ps1Content = "& $pythonExe `"`$PSScriptRoot\skills`" @args`r`n"
[System.IO.File]::WriteAllText($targetPs1, $ps1Content, [System.Text.Encoding]::UTF8)

Write-Host ""
Write-Success "Installation successful!"
Write-Host "   Installed at: $targetBin"
Write-Host "   Wrapper at:   $targetCmd"

# 6. Check PATH
$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
$envPath = $env:PATH
if ($envPath -notlike "*$targetDir*" -and $userPath -notlike "*$targetDir*") {
    Write-Host ""
    Write-Warn "$targetDir is not in your PATH."
    Write-Host "You can add it to your user PATH by running:"
    Write-Host "  [Environment]::SetEnvironmentVariable('PATH', `$([Environment]::GetEnvironmentVariable('PATH', 'User') + ';$targetDir'), 'User')" -ForegroundColor Cyan
}

Write-Host ""
Write-Host "Quick Start:" -ForegroundColor White
Write-Host "  skills ls            List all installed global skills"
Write-Host "  skills sync          Sync and restore skills from ~/.agents/skills.json"
Write-Host "  skills doctor        Check and repair global skills health"
Write-Host ""
