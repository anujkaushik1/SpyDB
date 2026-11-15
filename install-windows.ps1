# Installs or updates SpyDB on this PC from the latest GitHub release. Paste in PowerShell:
#   irm https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/install-windows.ps1 | iex
# Saved connections and queries are kept (they live in %AppData%\SpyDB).
# Downloaded this way, the installer isn't marked as from the internet, so
# Windows doesn't show its "Windows protected your PC" warning. It installs
# for your user only: no administrator needed.

# Everything runs inside this block, started on its last line: a
# half-downloaded script fails to parse instead of running halfway. It ends
# with return, never exit, which would close your PowerShell window.
& {
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue' # Windows PowerShell downloads far slower while it draws progress
  $url = 'https://github.com/anujkaushik1/SpyDB/releases/latest/download/SpyDB-windows-setup.exe'
  $key = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\SpyDB' # written by the installer

  # 1. The right kind of PC: Windows 10 or newer, 64-bit Intel/AMD. (The
  # machine's own setting: PowerShell itself may run emulated on an ARM PC.)
  if ([Environment]::OSVersion.Version.Major -lt 10) {
    Write-Host 'SpyDB needs Windows 10 or newer.'; return
  }
  $arch = (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment').PROCESSOR_ARCHITECTURE
  if ($arch -ne 'AMD64') {
    Write-Host "SpyDB's Windows build is for 64-bit Intel/AMD PCs (this one is $arch)."; return
  }

  # 2. Download into a temporary folder, removed at the end.
  $tmp = Join-Path ([IO.Path]::GetTempPath()) ('spydb-' + [Guid]::NewGuid())
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    $setup = Join-Path $tmp 'SpyDB-windows-setup.exe'
    Write-Host 'Downloading SpyDB...'
    try {
      [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
      Invoke-WebRequest -Uri $url -OutFile $setup -UseBasicParsing
    } catch {
      Write-Host "Couldn't download SpyDB (no internet, or GitHub is down?). Nothing was changed."; return
    }

    # 3. Check the download before touching the installed app: its
    # Properties > Details say "SpyDB Installer" and a version.
    $info = (Get-Item -LiteralPath $setup).VersionInfo
    if ($info.FileDescription -ne 'SpyDB Installer' -or $info.ProductVersion -notmatch '^[0-9]+(\.[0-9]+)*$') {
      Write-Host "The download isn't SpyDB's installer. Nothing was changed."; return
    }
    $version = $info.ProductVersion
    # A download can carry Windows' "from the internet" mark, which makes
    # SmartScreen stop an unsigned installer with "Windows protected your PC".
    # It was checked above, so the mark goes; the files the installer writes never have it.
    Unblock-File -LiteralPath $setup -ErrorAction SilentlyContinue

    # 4. Where SpyDB is (or goes): the folder the installer recorded, or its default.
    $dir = Join-Path $env:LOCALAPPDATA 'Programs\SpyDB'
    $installed = Get-ItemProperty -Path $key -ErrorAction SilentlyContinue
    if ($installed -and $installed.InstallLocation) { $dir = $installed.InstallLocation }
    $exe = Join-Path $dir 'SpyDB.exe'

    # 5. Close SpyDB if it's open (waiting up to 10 seconds), then install.
    # Found by its path, not its name: another SpyDB.exe (a developer's
    # build) would otherwise be the one closed.
    $open = @(Get-Process -Name SpyDB -ErrorAction SilentlyContinue | Where-Object {
      try { $_.Path -eq $exe } catch { $false } # another user's: not ours to close
    })
    if ($open.Count -gt 0) {
      foreach ($p in $open) { [void]$p.CloseMainWindow() }
      foreach ($i in 1..10) {
        if (-not ($open | Where-Object { -not $_.HasExited })) { break }
        Start-Sleep -Seconds 1
      }
      if ($open | Where-Object { -not $_.HasExited }) {
        Write-Host 'SpyDB is still open. Close it, then run this again. Nothing was changed.'; return
      }
    }

    # 6. Install it, or update the installed one: silently, into that folder
    # (/D= must come last, without quotes).
    Write-Host "Installing SpyDB $version..."
    $run = Start-Process -FilePath $setup -ArgumentList "/S /D=$dir" -Wait -PassThru
    if ($run.ExitCode -ne 0) {
      Write-Host "The installer stopped (code $($run.ExitCode)). If SpyDB was open, close it and run this again."; return
    }

    # 7. Say which version, and open it.
    Write-Host "SpyDB $((Get-ItemProperty -Path $key).DisplayVersion) installed. It's in the Start menu."
    Start-Process -FilePath $exe
  } finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }
}
