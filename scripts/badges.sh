#!/bin/sh
# Write the README's badges into docs/assets/badges/. GitHub shows an image from
# another site through a proxy that gives up after about four seconds, and
# shields.io often takes longer, so badges linked from there showed as broken
# images. A file in the repository is served without that proxy. Run it once a
# release's section is in CHANGELOG.md:
#
#   make badges
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
dir=docs/assets/badges
mkdir -p "$dir"

# The newest release is the first "## [x.y.z]" heading: "## [Unreleased]" has no
# digit after its bracket.
version=$(awk '/^## \[[0-9]/ { gsub(/[][]/, "", $2); print $2; exit }' CHANGELOG.md)
[ -n "$version" ] || { echo "CHANGELOG.md names no release" >&2; exit 1; }

# badge NAME SPEC writes shields.io's static badge SPEC (label-message-color,
# spaces as %20, "|" as %7C) to NAME.svg, whole or not at all.
badge() {
  tmp="$dir/$1.svg.tmp"
  curl -fsS --retry 3 --max-time 30 -o "$tmp" "https://img.shields.io/badge/$2.svg"
  # An error page in place of a badge must not replace the one there is.
  grep -q '<svg' "$tmp" || { echo "shields.io sent no SVG for $1" >&2; rm -f "$tmp"; exit 1; }
  mv "$tmp" "$dir/$1.svg"
}

badge release "release-v$version-ea7233"
badge license "license-MIT-2a78d6"
badge runs-on "runs%20on-Windows%2010%20%7C%2011%2C%20WSL%202-555"

# The README's alt text says the same, for a reader without images.
sed -i -e "s/alt=\"Release v[0-9][0-9.]*\"/alt=\"Release v$version\"/" README.md
echo "wrote $dir: release v$version"
