# install_shortcut.ps1
# Builds the server + launcher, then creates a Desktop shortcut.
# Run once from the project directory:  .\install_shortcut.ps1

$ErrorActionPreference = "Stop"
$ProjectDir = Split-Path -Parent $MyInvocation.MyCommand.Definition

Write-Host "Building server (garmin_dashboard.exe)..." -ForegroundColor Cyan
Push-Location $ProjectDir
go build -ldflags="-H windowsgui" -o garmin_dashboard.exe .
if ($LASTEXITCODE -ne 0) { Write-Error "Server build failed"; exit 1 }

Write-Host "Building launcher (GarminDashboard.exe)..." -ForegroundColor Cyan
go build -ldflags="-H windowsgui" -o GarminDashboard.exe ./cmd/launcher
if ($LASTEXITCODE -ne 0) { Write-Error "Launcher build failed"; exit 1 }
Pop-Location

# ── Create Desktop shortcut ───────────────────────────────────────────────
$DesktopPath  = [Environment]::GetFolderPath("Desktop")
$ShortcutPath = Join-Path $DesktopPath "Garmin Dashboard.lnk"
$TargetPath   = Join-Path $ProjectDir  "GarminDashboard.exe"

$WshShell = New-Object -ComObject WScript.Shell
$Shortcut = $WshShell.CreateShortcut($ShortcutPath)
$Shortcut.TargetPath       = $TargetPath
$Shortcut.WorkingDirectory = $ProjectDir
$Shortcut.Description      = "Open Garmin Dashboard"
$Shortcut.WindowStyle      = 1   # normal window

# Use the launcher exe itself as the icon (Go embeds a default icon,
# which beats the blank-page icon of a plain shortcut).
$Shortcut.IconLocation = "$TargetPath,0"
$Shortcut.Save()

Write-Host ""
Write-Host "Done! Shortcut created at:" -ForegroundColor Green
Write-Host "  $ShortcutPath" -ForegroundColor White
Write-Host ""
Write-Host "Double-click 'Garmin Dashboard' on your Desktop to launch." -ForegroundColor Green
