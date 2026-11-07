#!/bin/sh
# Build the release archives into dist/: for each Windows architecture, a zip
# holding wsl-shrinker.exe and its WSL launcher (the Linux build of the same
# architecture, which hands over to the .exe), the license, the notices and
# the docs, then checksums.txt over every zip.
#
#   scripts/dist.sh 0.1.0
set -eu
# zip records local times: the same time zone everywhere gives the same zip.
export TZ=UTC

version=${1:?usage: scripts/dist.sh VERSION}
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
pkg=github.com/kanggu12/wsl-shrinker/internal/version
# The commit and its date, so `wsl-shrinker version` says which build it is.
commit=$(git rev-parse --short HEAD)
date=$(git log -1 --format=%cs)
ldflags="-s -w -X $pkg.Version=v$version -X $pkg.Commit=$commit -X $pkg.Date=$date"
mkdir -p dist
# Only what an earlier run made goes away.
rm -f dist/*.zip dist/checksums.txt dist/install.ps1
for arch in amd64 arm64; do
  stage="dist/stage-$arch"
  mkdir -p "$stage"
  CGO_ENABLED=0 GOOS=windows GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$stage/wsl-shrinker.exe" ./cmd/wsl-shrinker
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$stage/wsl-shrinker" ./cmd/wsl-shrinker
  cp LICENSE README.md CHANGELOG.md docs/THIRD_PARTY_NOTICES.md "$stage/"
  # Files in the zip carry the commit's time, so a rebuild gives the same zip.
  touch -d "@$(git log -1 --format=%ct)" "$stage"/*
  (cd "$stage" && zip -q -X -9 "../wsl-shrinker_${version}_windows_$arch.zip" ./*)
  rm -f "$stage"/*
  rmdir "$stage"
done
(cd dist && sha256sum ./*.zip | sed 's# \./# #' > checksums.txt)
cp scripts/install.ps1 dist/
ls -l dist
