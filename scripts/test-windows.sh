#!/bin/sh
# Run the tests as Windows programs, from WSL: each package's test binary is
# built for Windows and started through WSL's interop, so the code that
# reaches Windows (the API calls, wsl.exe's output) is tested where it runs.
# Tests that need Linux tools (mkfs.ext4) skip themselves there.
#
#   make test-windows
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
# Windows programs run from a Windows folder: the user's temporary one.
win_tmp=$(wslpath "$(cmd.exe /d /c 'echo %TEMP%' 2>/dev/null | tr -d '\r')")
dir="$win_tmp/wsl-shrinker-tests"
mkdir -p "$dir"
failed=0
for pkg in $(go list ./...); do
  name=$(basename "$pkg")
  exe="$dir/$name.test.exe"
  out="$dir/$name.out"
  if ! GOOS=windows go test -c -o "$exe" "$pkg" >"$out" 2>&1; then
    echo "FAIL  $pkg (does not build for Windows)"
    cat "$out"
    failed=1
    continue
  fi
  # Packages without tests produce no binary, only the build's output.
  if [ ! -f "$exe" ]; then
    rm -f "$out"
    continue
  fi
  # The binary runs in its package's folder, where its testdata is.
  if (cd "$root/${pkg#github.com/kanggu12/wsl-shrinker/}" 2>/dev/null || cd "$root"; "$exe" -test.count=1 >"$out" 2>&1); then
    echo "ok    $pkg"
  else
    echo "FAIL  $pkg"
    tr -d '\r' <"$out" | tail -20
    failed=1
  fi
  # Only what this run made goes away.
  rm -f "$exe" "$out"
done
rmdir "$dir" 2>/dev/null || true
exit $failed
