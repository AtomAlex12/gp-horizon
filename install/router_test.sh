#!/bin/sh
# Runs the router-side tests with only what a fresh Entware on KeeneticOS has:
# Entware's own BusyBox — no nohup, setsid or timeout, an od without -A/-t —
# and curl; the tests fake opkg, ndmc and the engines as before. A full Linux
# has every tool, so a script leaning on one the router lacks passes there and
# fails on the router ("nohup: not found").
#
# It unpacks Entware into /opt, so it runs in a throwaway container:
#   docker run --rm -v "$PWD":/src -w /src debian:bookworm-slim sh install/router_test.sh
# ENTWARE_FEED picks the feed (default: by the machine — aarch64-k3.10, x64-k3.2).
set -eu

case "${ENTWARE_FEED:-$(uname -m)}" in
aarch64 | arm64) FEED=aarch64-k3.10 ;;
x86_64 | amd64) FEED=x64-k3.2 ;;
*) FEED=$ENTWARE_FEED ;;
esac
URL="http://bin.entware.net/$FEED"

# the test harness itself needs these (the router has curl from opkg; the
# fake agent checks signatures with ssh-keygen, the fake unzip is python)
if ! command -v ssh-keygen >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1 || ! command -v curl >/dev/null 2>&1; then
    apt-get update -qq >/dev/null && apt-get install -y -qq --no-install-recommends curl ca-certificates openssh-client python3 >/dev/null
fi
mkdir -p /allow
for t in curl ssh-keygen python3; do ln -sf "$(command -v "$t")" "/allow/$t"; done

# Entware: BusyBox and the C library it links against, into /opt
TMP=$(mktemp -d)
curl -fsSL "$URL/Packages" -o "$TMP/Packages"
for pkg in busybox libc libgcc libpthread librt; do
    f=$(awk -v p="Package: $pkg" '$0 == p { on = 1 } on && /^Filename:/ { print $2; exit }' "$TMP/Packages")
    [ -n "$f" ] || continue
    curl -fsSL "$URL/$f" -o "$TMP/$pkg.ipk"
    mkdir -p "$TMP/$pkg" && (cd "$TMP/$pkg" && tar xzf "../$pkg.ipk" && tar xzf data.tar.gz -C /)
done
BB=/opt/bin/busybox
for a in $("$BB" --list); do [ -e "/opt/bin/$a" ] || ln -s busybox "/opt/bin/$a"; done
echo "Entware $FEED: $("$BB" | head -n 1)"
for t in nohup setsid timeout; do [ -e "/opt/bin/$t" ] && echo "note: $t is there"; done

# the router's toolset and nothing else
export PATH="/opt/bin:/allow"
fail=0
for t in engines/nuxk-nfqws2/shim_test.sh engines/nuxk-xray/shim_test.sh engines/nuxk-smartdns/shim_test.sh install/lite_test.sh; do
    echo "== $t"
    if ! sh "$t" >"$TMP/out" 2>&1; then
        grep -v '^ok ' "$TMP/out" | tail -n 25
        echo "FAILED $t"
        fail=1
    else
        tail -n 1 "$TMP/out"
    fi
done
rm -rf "$TMP"
exit "$fail"
