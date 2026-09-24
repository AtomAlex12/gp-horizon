#!/bin/sh
# Version management. The repo-root VERSION file is the single source of truth
# (SemVer: MAJOR.MINOR.PATCH[-pre.N]); nuxk-web/package.json mirrors it for the
# web build. nuxk-core gets it via -ldflags, Docker images via COPY VERSION.
#
#   scripts/version.sh            print the current version
#   scripts/version.sh check      fail if package.json drifted from VERSION
#   scripts/version.sh set X.Y.Z  write X.Y.Z to VERSION and package.json
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
PKG="$ROOT/nuxk-web/package.json"
SEMVER='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'

cur() { tr -d ' \n\r' <"$ROOT/VERSION"; }
pkg() { sed -n 's/^  "version": "\(.*\)",$/\1/p' "$PKG"; }

case "${1:-}" in
"") cur; echo ;;
check)
    v=$(cur)
    echo "$v" | grep -Eq "$SEMVER" || { echo "VERSION '$v' is not SemVer" >&2; exit 1; }
    [ "$(pkg)" = "$v" ] || { echo "nuxk-web/package.json version '$(pkg)' != VERSION '$v' — run scripts/version.sh set $v" >&2; exit 1; }
    echo "version $v ok"
    ;;
set)
    v="${2:?usage: $0 set X.Y.Z[-pre.N]}"
    echo "$v" | grep -Eq "$SEMVER" || { echo "'$v' is not SemVer" >&2; exit 1; }
    printf '%s\n' "$v" >"$ROOT/VERSION"
    sed -i "s/^  \"version\": \".*\",$/  \"version\": \"$v\",/" "$PKG"
    "$0" check
    echo "next: add a '## [$v]' section to CHANGELOG.md, commit, tag v$v after merge"
    ;;
*) echo "usage: $0 [check | set X.Y.Z]" >&2; exit 2 ;;
esac
