#!/bin/sh
# Build the release archives into dist/: for each Windows architecture, a zip
# holding wsl-shrinker.exe and its WSL launcher (the Linux build of the same
# architecture, which hands over to the .exe), the license, the notices, the
# changelog and a short README.txt, then checksums.txt over every zip.
#
#   scripts/dist.sh 0.1.0
set -eu
# zip records local times: the same time zone everywhere gives the same zip.
export TZ=UTC

version=${1:?usage: scripts/dist.sh VERSION}
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
pkg=github.com/kanggu12/wsl-shrinker/internal/version
# A zip is labelled with the commit: changes not committed, and new files
# not added (a new .go file is built all the same), would ship under a
# commit that does not hold them. Ignored files (dist/) do not count.
if [ -n "$(git status --porcelain)" ]; then
  echo "scripts/dist.sh: the work tree differs from HEAD (changed or untracked files); commit or stash first" >&2
  exit 1
fi
# The commit and its date, so `wsl-shrinker version` says which build it is.
# The date is the commit's in UTC, as a build from source reports it.
commit=$(git rev-parse --short HEAD)
date=$(git log -1 --date=format-local:%Y-%m-%d --format=%cd)
ldflags="-s -w -X $pkg.Version=v$version -X $pkg.Commit=$commit -X $pkg.Date=$date"
mkdir -p dist
# Only what an earlier run made goes away.
rm -f dist/*.zip dist/checksums.txt dist/install.ps1
for arch in amd64 arm64; do
  stage="dist/stage-$arch"
  mkdir -p "$stage"
  CGO_ENABLED=0 GOOS=windows GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$stage/wsl-shrinker.exe" ./cmd/wsl-shrinker
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$stage/wsl-shrinker" ./cmd/wsl-shrinker
  cp LICENSE CHANGELOG.md "$stage/"
  # The notices sit next to LICENSE in the zip, not one folder down as in docs/.
  sed 's#](\.\./LICENSE)#](LICENSE)#' docs/THIRD_PARTY_NOTICES.md >"$stage/THIRD_PARTY_NOTICES.md"
  # The repository's README draws on pictures and pages the zip does not
  # hold; the zip's own says what is in it and where the rest is.
  cat >"$stage/README.txt" <<EOF
wsl-shrinker v$version

wsl-shrinker.exe  the program, for Windows
wsl-shrinker      the launcher for WSL: run inside a distribution, it hands
                  over to wsl-shrinker.exe next to it or on the Windows PATH

Run wsl-shrinker in a terminal for the screen, or wsl-shrinker --help for the
commands. Put this folder on your PATH to run it from anywhere, in Windows and
in WSL, or install with install.ps1 from the release, which does that itself.
Documentation: https://github.com/kanggu12/wsl-shrinker
EOF
  # Files in the zip carry the commit's time, so a rebuild gives the same zip.
  touch -d "@$(git log -1 --format=%ct)" "$stage"/*
  (cd "$stage" && zip -q -X -9 "../wsl-shrinker_${version}_windows_$arch.zip" ./*)
  rm -f "$stage"/*
  rmdir "$stage"
done
(cd dist && sha256sum ./*.zip | sed 's# \./# #' > checksums.txt)
cp scripts/install.ps1 dist/
ls -l dist
