#!/bin/sh
# Build the release archives into dist/: for each Windows architecture, a zip
# holding wsl-shrinker.exe and its WSL launcher (the Linux build of the same
# architecture, which hands over to the .exe), the license, the notices, the
# changelog and a short README.txt; then the checksums of every zip, the
# install script set to this version, and the Scoop manifest. Every file is
# named with the version, wsl-shrinker-0.8.3-windows-x64.zip and so on, but the
# Scoop manifest: Scoop names the app after its file, so it stays
# wsl-shrinker.json.
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
# Only what an earlier run made goes away, under the names of before too.
rm -f dist/*.zip dist/*checksums.txt dist/*install.ps1 dist/wsl-shrinker.json
# Each file's name, the version in it. An architecture is named as Windows
# names it: x64 (Go's amd64) and arm64.
base="wsl-shrinker-$version"
sums="$base-checksums.txt"
label() { case $1 in amd64) echo x64 ;; *) echo "$1" ;; esac; }
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
in WSL, or install with the release's $base-install.ps1, which does that
itself.
Documentation: https://github.com/kanggu12/wsl-shrinker
EOF
  # Files in the zip carry the commit's time, so a rebuild gives the same zip.
  touch -d "@$(git log -1 --format=%ct)" "$stage"/*
  (cd "$stage" && zip -q -X -9 "../$base-windows-$(label "$arch").zip" ./*)
  rm -f "$stage"/*
  rmdir "$stage"
done
(cd dist && sha256sum ./*.zip | sed 's# \./# #' >"$sums")
# The install script of this release installs this release: its version is
# set in it, where the repository's copy leaves it empty and finds the latest.
sed "s/^\( *\)\$pinned = ''\$/\1\$pinned = 'v$version'/" scripts/install.ps1 >"dist/$base-install.ps1"
grep -q "^ *\$pinned = 'v$version'\$" "dist/$base-install.ps1" || { echo "scripts/dist.sh: install.ps1 has no \$pinned line to set" >&2; exit 1; }
# The Scoop manifest, with this release's zips and their checksums. Its own
# checkver and autoupdate let `scoop update` find later releases.
zip_hash() { awk -v f="$base-windows-$1.zip" '$2 == f { print $1 }' "dist/$sums"; }
url="https://github.com/kanggu12/wsl-shrinker/releases/download"
cat >dist/wsl-shrinker.json <<EOF
{
    "version": "$version",
    "description": "See where your WSL disk space went, clean it, shrink it, and check it worked.",
    "homepage": "https://github.com/kanggu12/wsl-shrinker",
    "license": "MIT",
    "architecture": {
        "64bit": {
            "url": "$url/v$version/$base-windows-x64.zip",
            "hash": "$(zip_hash x64)"
        },
        "arm64": {
            "url": "$url/v$version/$base-windows-arm64.zip",
            "hash": "$(zip_hash arm64)"
        }
    },
    "env_add_path": ".",
    "checkver": "github",
    "autoupdate": {
        "architecture": {
            "64bit": {
                "url": "$url/v\$version/wsl-shrinker-\$version-windows-x64.zip"
            },
            "arm64": {
                "url": "$url/v\$version/wsl-shrinker-\$version-windows-arm64.zip"
            }
        },
        "hash": {
            "url": "\$baseurl/wsl-shrinker-\$version-checksums.txt"
        }
    },
    "notes": "Run wsl-shrinker in a new terminal, in Windows or inside WSL."
}
EOF
# A manifest Scoop could not read would fail people, not this script.
python3 -m json.tool dist/wsl-shrinker.json >/dev/null
ls -l dist
