#!/bin/sh
# Puts GP Plane's shared UI package (@gp-plane/gp-ui) into vendor/: the exact
# archive GP Plane handed over (GP-coordination, GP-UI-SHARING-001), checked
# against its SHA256 — fetched, not committed. Run it before `npm ci`.
# A new version of the package: change all three lines below together with
# the "file:" entry in package.json, then `npm install` to refresh the lock.
set -eu
cd "$(dirname "$0")/.."
NAME=gp-plane-gp-ui-0.1.0-s2a.1.tgz
URL=https://raw.githubusercontent.com/balbomush/GP-plane-web/36525eddcba1114821cb0d3093651e58befa88f9/frontend/vendor/$NAME
SUM=a1ed233f5ae2fd9830c2158737ad080f1deacfb84090d0efe88ce365b3289498

ok() { echo "$SUM  $1" | sha256sum -c >/dev/null 2>&1; } # GNU and busybox alike
mkdir -p vendor
ok "vendor/$NAME" && exit 0
rm -f "vendor/$NAME.part"
if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 -o "vendor/$NAME.part" "$URL"
else
    wget -q -O "vendor/$NAME.part" "$URL" # node:*-alpine has busybox wget, no curl
fi
if ! ok "vendor/$NAME.part"; then
    rm -f "vendor/$NAME.part"
    echo "gp-ui: SHA256 архива не совпал — это не тот пакет, который передал GP Plane" >&2
    exit 1
fi
mv "vendor/$NAME.part" "vendor/$NAME"
echo "gp-ui: vendor/$NAME"
