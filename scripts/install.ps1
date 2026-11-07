# Install wsl-shrinker from a GitHub release, for the current user.
#
#   irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex
#
# Settings, all optional, as environment variables:
#   WSL_SHRINKER_VERSION   release to install, like v0.1.0 (default: the latest)
#   WSL_SHRINKER_BIN_DIR   where it goes (default: %LOCALAPPDATA%\Programs\wsl-shrinker)
#   WSL_SHRINKER_BASE_URL  where releases are downloaded from (mirrors, tests)
#   WSL_SHRINKER_ZIP       a zip already downloaded, installed as it is (tests)
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

  # x64 or ARM64 Windows; a 32-bit PowerShell on x64 reports the machine in
  # PROCESSOR_ARCHITEW6432.
  $machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
  $arch = switch ($machine) { 'AMD64' { 'amd64' } 'ARM64' { 'arm64' } default { throw "wsl-shrinker install: no release for $machine" } }

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
    # takes from Windows.
    foreach ($f in 'wsl-shrinker.exe', 'wsl-shrinker') {
      Copy-Item (Join-Path $tmp "x\$f") (Join-Path $binDir $f) -Force
    }
    Write-Host "installed $(Join-Path $binDir 'wsl-shrinker.exe')"
  } finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  }

  # The user's PATH gets the folder once, for terminals opened from now on. It
  # is read and written as it is stored, so entries like %USERPROFILE%\bin
  # keep their variables.
  $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
  try {
    $userPath = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $entries = @($userPath -split ';' | Where-Object { $_ })
    if (-not ($entries | Where-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') -eq $binDir.TrimEnd('\') })) {
      $key.SetValue('Path', (@($entries + $binDir) -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
      # Tell Windows the environment changed, so new terminals see it: setting
      # a variable through .NET sends that message.
      [Environment]::SetEnvironmentVariable('WSL_SHRINKER_INSTALLING', '1', 'User')
      [Environment]::SetEnvironmentVariable('WSL_SHRINKER_INSTALLING', $null, 'User')
      Write-Host "added $binDir to your PATH; open a new terminal, then run wsl-shrinker"
    }
  } finally {
    $key.Close()
  }
}
