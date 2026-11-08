# Install wsl-shrinker from a GitHub release, for the current user.
#
#   irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex
#
# Settings, all optional, as environment variables:
#   WSL_SHRINKER_VERSION   release to install, like v0.1.0 (default: the latest)
#   WSL_SHRINKER_BIN_DIR   where it goes (default: %LOCALAPPDATA%\Programs\wsl-shrinker)
#   WSL_SHRINKER_BASE_URL  where releases are downloaded from (mirrors, tests)
#   WSL_SHRINKER_ZIP       a zip already downloaded, installed as it is (tests)
#   WSL_SHRINKER_NO_SETUP  1: do not open setup after a first install
#   WSL_SHRINKER_UNINSTALL 1: remove wsl-shrinker instead: the program and its
#                          PATH entry; the settings and history stay unless
#   WSL_SHRINKER_PURGE     is 1 too
#
# Everything runs in a script block of its own: run through iex, the script
# shares the caller's session, and a failure must end the install, not the
# person's PowerShell window, nor leave settings like ErrorActionPreference
# behind.
& {
  $ErrorActionPreference = 'Stop'
  # Windows PowerShell 5.1 speaks only older TLS unless told otherwise.
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

  $repo = 'kanggu12/wsl-shrinker'
  $base = if ($env:WSL_SHRINKER_BASE_URL) { $env:WSL_SHRINKER_BASE_URL } else { "https://github.com/$repo/releases/download" }
  $binDir = if ($env:WSL_SHRINKER_BIN_DIR) { $env:WSL_SHRINKER_BIN_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\wsl-shrinker' }
  # The program's two files, in the folder and in each zip.
  $files = 'wsl-shrinker.exe', 'wsl-shrinker'

  # The user's PATH as it is stored, so entries like %USERPROFILE%\bin keep
  # their variables when it is written back; and whether an entry is the
  # program's folder, however it was written.
  $pathKey = { [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true) }
  $isBinDir = { param($entry) [Environment]::ExpandEnvironmentVariables($entry).TrimEnd('\') -eq $binDir.TrimEnd('\') }
  # Tell Windows the environment changed, so new terminals see it: setting a
  # variable through .NET sends that message.
  $announce = {
    [Environment]::SetEnvironmentVariable('WSL_SHRINKER_INSTALLING', '1', 'User')
    [Environment]::SetEnvironmentVariable('WSL_SHRINKER_INSTALLING', $null, 'User')
  }

  if ($env:WSL_SHRINKER_UNINSTALL -eq '1') {
    # A running wsl-shrinker holds its files: removing them would fail half
    # way, so that is found out first.
    foreach ($f in $files) {
      $p = Join-Path $binDir $f
      if (Test-Path -LiteralPath $p) {
        try { [IO.File]::Open($p, 'Open', 'ReadWrite', 'None').Close() }
        catch { throw "wsl-shrinker uninstall: $p is in use; close wsl-shrinker and run this again" }
      }
    }
    # The two programs, and the copies an install cut short left beside them.
    foreach ($f in $files) {
      foreach ($n in $f, "$f.new") { Remove-Item -LiteralPath (Join-Path $binDir $n) -Force -ErrorAction SilentlyContinue }
    }
    Write-Host "removed wsl-shrinker from $binDir"
    # The folder goes too once nothing else is in it, and with it its PATH
    # entry. A folder that holds other files, as one the PATH had before
    # (WSL_SHRINKER_BIN_DIR set to it), keeps them and its entry: the install
    # may not be what put it there.
    if ((Test-Path -LiteralPath $binDir) -and (Get-ChildItem -LiteralPath $binDir -Force)) {
      Write-Host "kept $binDir and its PATH entry: other files are in it"
    } else {
      Remove-Item -LiteralPath $binDir -Force -ErrorAction SilentlyContinue
      $key = & $pathKey
      try {
        $userPath = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $entries = @($userPath -split ';' | Where-Object { $_ })
        $kept = @($entries | Where-Object { -not (& $isBinDir $_) })
        if ($kept.Count -ne $entries.Count) {
          $key.SetValue('Path', ($kept -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
          & $announce
          Write-Host "took $binDir out of your PATH"
        }
      } finally {
        $key.Close()
      }
    }
    $data = Join-Path $env:APPDATA 'wsl-shrinker'
    $state = Join-Path $env:LOCALAPPDATA 'wsl-shrinker'
    if ($env:WSL_SHRINKER_PURGE -ne '1') {
      Write-Host "kept the settings in $data and the history in $state; WSL_SHRINKER_PURGE=1 removes them too"
    } elseif (Test-Path -LiteralPath (Join-Path $state 'run.json')) {
      # The journal of a run cut off is what recover needs to start a
      # distribution and its containers again: it is not thrown away.
      Write-Host "kept ${state}: a run was cut off there; install wsl-shrinker again and run wsl-shrinker recover first"
      Remove-Item -LiteralPath $data -Recurse -Force -ErrorAction SilentlyContinue
    } else {
      foreach ($d in $data, $state) { Remove-Item -LiteralPath $d -Recurse -Force -ErrorAction SilentlyContinue }
      Write-Host "removed the settings and the history"
    }
    # The two are one-shot: left set, a later install in this window would
    # remove again what it installs.
    Remove-Item Env:WSL_SHRINKER_UNINSTALL, Env:WSL_SHRINKER_PURGE -ErrorAction SilentlyContinue
    return
  }

  # x64 or ARM64 Windows. The machine's own architecture counts, not this
  # PowerShell's: an x64 or x86 PowerShell on ARM64 Windows runs emulated and
  # says AMD64 or X86, and the x64 launcher it would install cannot run in an
  # ARM64 distribution. The system's environment in the registry holds the
  # machine's, whatever the reader; .NET's OSArchitecture says it from .NET 5
  # (PowerShell 7) on, and a 32-bit PowerShell on x64 finds it in
  # PROCESSOR_ARCHITEW6432.
  $machine = $null
  try { $machine = [string](Get-ItemProperty -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment' -Name PROCESSOR_ARCHITECTURE -ErrorAction Stop).PROCESSOR_ARCHITECTURE } catch { }
  if (-not $machine) { try { $machine = [string][Runtime.InteropServices.RuntimeInformation]::OSArchitecture } catch { } }
  if (-not $machine) { $machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE } }
  $arch = switch ($machine) { { $_ -in 'AMD64', 'X64' } { 'amd64' } 'ARM64' { 'arm64' } default { throw "wsl-shrinker install: no release for $machine" } }

  $tmp = Join-Path ([IO.Path]::GetTempPath()) ('wsl-shrinker-' + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    if ($env:WSL_SHRINKER_ZIP) {
      $zip = $env:WSL_SHRINKER_ZIP
    } else {
      # The latest release is where github.com/<repo>/releases/latest redirects.
      $version = $env:WSL_SHRINKER_VERSION
      if (-not $version) {
        try {
          $req = [Net.WebRequest]::Create("https://github.com/$repo/releases/latest")
          $req.Method = 'HEAD'
          $resp = $req.GetResponse()
          $url = $resp.ResponseUri.AbsoluteUri
          $resp.Close()
        } catch {
          throw 'wsl-shrinker install: could not find the latest release; set WSL_SHRINKER_BASE_URL and WSL_SHRINKER_VERSION'
        }
        if ($url -notmatch '/releases/tag/(.+)$') { throw 'wsl-shrinker install: no release published yet; set WSL_SHRINKER_VERSION' }
        $version = $Matches[1]
      }
      if (-not $version.StartsWith('v')) { $version = "v$version" }
      $name = "wsl-shrinker_$($version.Substring(1))_windows_$arch.zip"
      $zip = Join-Path $tmp $name
      Write-Host "downloading wsl-shrinker $version for windows/$arch"
      $wc = New-Object Net.WebClient
      try {
        $wc.DownloadFile("$base/$version/$name", $zip)
        $wc.DownloadFile("$base/$version/checksums.txt", (Join-Path $tmp 'checksums.txt'))
      } catch {
        throw "wsl-shrinker install: could not download $name"
      }
      # The release's checksum. Get-FileHash is missing from some PowerShell
      # setups, so the hash is computed directly.
      $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match (' ' + [regex]::Escape($name) + '$') } | Select-Object -First 1
      if (-not $line) { throw "wsl-shrinker install: $name is not in checksums.txt" }
      $want = ($line -split ' ')[0]
      $sha = [Security.Cryptography.SHA256]::Create()
      $stream = [IO.File]::OpenRead($zip)
      try { $got = -join ($sha.ComputeHash($stream) | ForEach-Object { $_.ToString('x2') }) } finally { $stream.Close() }
      if ($got -ne $want) { throw "wsl-shrinker install: checksum mismatch for $name" }
    }

    Expand-Archive -Path $zip -DestinationPath (Join-Path $tmp 'x') -Force
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    # The Windows program, and the launcher WSL finds by name on the PATH it
    # takes from Windows ($files above).
    # A running wsl-shrinker holds its files: find that out before anything
    # is replaced, so an install never leaves a new program beside an old
    # launcher.
    foreach ($f in $files) {
      $dest = Join-Path $binDir $f
      if (Test-Path -LiteralPath $dest) {
        try { [IO.File]::Open($dest, 'Open', 'ReadWrite', 'None').Close() }
        catch { throw "wsl-shrinker install: $dest is in use; close wsl-shrinker and run the install again" }
      }
    }
    # Each file lands beside its old copy first, then takes its place: a copy
    # cut short leaves the old one whole.
    foreach ($f in $files) {
      Copy-Item -LiteralPath (Join-Path $tmp "x\$f") -Destination (Join-Path $binDir "$f.new") -Force
    }
    foreach ($f in $files) {
      Move-Item -LiteralPath (Join-Path $binDir "$f.new") -Destination (Join-Path $binDir $f) -Force
    }
    Write-Host "installed $(Join-Path $binDir 'wsl-shrinker.exe')"
  } finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  }

  # The user's PATH gets the folder once, for terminals opened from now on.
  $key = & $pathKey
  try {
    $userPath = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $entries = @($userPath -split ';' | Where-Object { $_ })
    if (-not ($entries | Where-Object { & $isBinDir $_ })) {
      $key.SetValue('Path', (@($entries + $binDir) -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
      & $announce
      Write-Host "added $binDir to your PATH; open a new terminal, then run wsl-shrinker"
    }
  } finally {
    $key.Close()
  }

  # In a console, a first install opens setup to choose the language, the
  # colors, TUI or CLI, and the disks to protect; an upgrade keeps the
  # settings it has. WSL_SHRINKER_NO_SETUP=1 skips it, as does a script: a
  # redirected console, or PowerShell started with -NonInteractive, where
  # nobody answers.
  $nonInteractive = [Environment]::GetCommandLineArgs() | Where-Object { $_ -match '^-noni' }
  if ($env:WSL_SHRINKER_NO_SETUP -ne '1' -and -not $nonInteractive -and -not [Console]::IsInputRedirected -and -not [Console]::IsOutputRedirected) {
    # The install is done whatever setup does: a setup that fails or is
    # left with Ctrl+C says so itself and opens again at the first start.
    try {
      & (Join-Path $binDir 'wsl-shrinker.exe') setup --installer
    } catch {
      Write-Host "wsl-shrinker setup did not finish; wsl-shrinker setup opens it again"
    }
  }
}
