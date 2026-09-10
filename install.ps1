<#
Downloads the latest released StayWakeBlackScreenIdle.exe, installs it
under the current user's %LOCALAPPDATA%, registers it to autostart at
login, and (re)starts it - stopping any already-running copy first so the
file can be replaced and so at most one copy is ever running at a time.

This is a PowerShell equivalent of StayWakeInstall.exe (see the go/
directory) for bootstrapping without first having to manually download an
exe from the Releases page:

  irm https://raw.githubusercontent.com/SpikePy/StayWakeBlackScreen-Windows/main/install.ps1 | iex

Safe to re-run any time to update: it always ends up with exactly ONE
autostart entry (a single named registry value under HKCU - re-running
never creates a duplicate) and exactly ONE running instance:
  - This script terminates any already-running copy (matched by full
    install path, so it never touches an unrelated same-named process
    elsewhere) before replacing the file and starting the new one.
  - StayWakeBlackScreenIdle.exe also refuses to start a second copy of
    itself, via a named mutex - belt and suspenders even if it's ever
    launched some other way while already running.

Usage:
  .\install.ps1
  .\install.ps1 -InstallDir 'D:\Tools\StayWake' -NoAutostart
  .\install.ps1 -GitHubToken $env:GITHUB_TOKEN   # avoid the unauthenticated API rate limit
#>

[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'StayWakeBlackScreen'),
    [string]$GitHubToken = '',
    [switch]$NoLaunch,
    [switch]$NoAutostart
)

$ErrorActionPreference = 'Stop'

$RepoOwner    = 'SpikePy'
$RepoName     = 'StayWakeBlackScreen-Windows'
$AssetName    = 'StayWakeBlackScreenIdle.exe'
$RunValueName = 'StayWakeBlackScreenIdle'
$RunKeyPath   = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'

# Some older PowerShell 5.1 setups default to TLS 1.0, which GitHub rejects.
try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.ServicePointManager]::SecurityProtocol } catch { }

function New-ApiHeaders
{
    $headers = @{
        'Accept'     = 'application/vnd.github+json'
        'User-Agent' = 'StayWakeBlackScreen-install.ps1'
    }
    if ($GitHubToken) { $headers['Authorization'] = "Bearer $GitHubToken" }
    return $headers
}

function Stop-RunningInstance([string]$exePath)
{
    $procs = Get-Process -Name ([IO.Path]::GetFileNameWithoutExtension($AssetName)) -ErrorAction SilentlyContinue |
        Where-Object {
            try { $_.Path -and ([IO.Path]::GetFullPath($_.Path) -eq [IO.Path]::GetFullPath($exePath)) }
            catch { $false } # e.g. access denied reading .Path on some other user's process - not ours, skip
        }
    foreach ($p in $procs)
    {
        Write-Host "Stopping running instance (PID $($p.Id))..."
        try { Stop-Process -Id $p.Id -Force -ErrorAction Stop } catch { }
        $deadline = (Get-Date).AddSeconds(5)
        while ((Get-Date) -lt $deadline -and (Get-Process -Id $p.Id -ErrorAction SilentlyContinue)) {
            Start-Sleep -Milliseconds 200
        }
    }
}

function Move-DownloadedFile([string]$from, [string]$to)
{
    # $to may still be momentarily locked right after Stop-RunningInstance
    # killed the process that had it open/mapped - retry briefly.
    for ($i = 0; $i -lt 10; $i++) {
        try { Move-Item -LiteralPath $from -Destination $to -Force; return }
        catch { Start-Sleep -Milliseconds 300 }
    }
    Move-Item -LiteralPath $from -Destination $to -Force
}

Write-Host "Looking up latest release of $RepoOwner/$RepoName..."
$release = Invoke-RestMethod -Uri "https://api.github.com/repos/$RepoOwner/$RepoName/releases/latest" -Headers (New-ApiHeaders)

$asset = $release.assets | Where-Object { $_.name -eq $AssetName } | Select-Object -First 1
if (-not $asset) {
    throw "Release $($release.tag_name) has no asset named $AssetName"
}

New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
$targetPath = Join-Path $InstallDir $AssetName
$tmpPath    = "$targetPath.download"

Write-Host "Downloading $($release.tag_name) ($($asset.browser_download_url))..."
Invoke-WebRequest -Uri $asset.browser_download_url -Headers (New-ApiHeaders) -OutFile $tmpPath -UseBasicParsing

Write-Host 'Stopping any already-running instance...'
Stop-RunningInstance -exePath $targetPath

Write-Host "Installing to $targetPath..."
Move-DownloadedFile -from $tmpPath -to $targetPath

if (-not $NoAutostart) {
    Write-Host 'Registering autostart...'
    New-Item -Path $RunKeyPath -Force | Out-Null
    Set-ItemProperty -Path $RunKeyPath -Name $RunValueName -Value ('"' + $targetPath + '"')
}

if (-not $NoLaunch) {
    Write-Host 'Starting it now...'
    Start-Process -FilePath $targetPath
}

Write-Host 'Done.'
