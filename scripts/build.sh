#!/bin/sh
# Make-free build helper (Windows/Git-Bash friendly).
#
#   scripts/build.sh web     build only the usque-keenetic-web .ipk
#   scripts/build.sh core    build the core .ipk packages (needs curl + unzip)
#   scripts/build.sh all     both
#
# CI uses the Makefile (`make packages`); this mirrors the `web` target so the
# web UI can be built and inspected without a make/toolchain.

set -e
cd "$(dirname "$0")/.."

VERSION="$(cat VERSION | tr -d '[:space:]')"
USQUE_VERSION="$(cat USQUE_VERSION | tr -d '[:space:]')"
OUT="out"
TMP="$OUT/tmp"

WEB_DEPENDS="usque-keenetic, php8-cgi, php8-mod-session, php8-mod-curl, curl, lighttpd, lighttpd-mod-cgi, lighttpd-mod-setenv"

build_web() {
    echo "Build usque-keenetic-web ${VERSION}..."
    local root="$OUT/web"
    local ctl="$root/control"
    local data="$root/data"
    local www="$data/opt/share/www/usque"

    rm -rf "$root"
    mkdir -p "$ctl" "$www/api" "$data/opt/etc/lighttpd/conf.d" "$data/opt/etc" "$TMP"

    cp web/public/index.html  "$www/index.html"
    cp web/public/app.js      "$www/app.js"
    cp web/public/style.css   "$www/style.css"
    cp web/public/favicon.svg "$www/favicon.svg"
    cp web/backend/index.php  "$www/api/index.php"
    printf '%s\n' "$VERSION" > "$www/version"
    cp web/lighttpd/81-usque.conf "$data/opt/etc/lighttpd/conf.d/81-usque.conf"
    cp web/conf/usque_web.conf    "$data/opt/etc/usque_web.conf"

    {
        echo "Package: usque-keenetic-web"
        echo "Version: $VERSION"
        echo "Depends: $WEB_DEPENDS"
        echo "License: MIT"
        echo "Section: net"
        echo "URL: https://github.com/side-effect-tm/usque-keenetic"
        echo "Architecture: all"
        echo "Description: usque-keenetic web interface (monitoring & control)"
        echo ""
    } > "$ctl/control"
    cp scripts/ipk-web/conffiles "$ctl/conffiles"
    cp scripts/ipk-web/postinst  "$ctl/postinst"
    cp scripts/ipk-web/prerm     "$ctl/prerm"
    cp scripts/ipk-web/postrm    "$ctl/postrm"
    chmod +x "$ctl/postinst" "$ctl/prerm" "$ctl/postrm"

    ( cd "$ctl"  && tar czf ../control.tar.gz . )
    ( cd "$data" && tar czf ../data.tar.gz . )
    echo 2.0 > "$root/debian-binary"
    ( cd "$root" && tar czf "../tmp/usque-keenetic-web_${VERSION}_all_entware.ipk" \
        control.tar.gz data.tar.gz debian-binary )

    echo "  -> $TMP/usque-keenetic-web_${VERSION}_all_entware.ipk"
}

build_core_note() {
    cat <<EOF
Core packages download prebuilt usque binaries from GitHub releases and are
built via the Makefile:  make pkg-all
(needs: make, curl, unzip — run on Linux/WSL or the CI runner)
EOF
}

case "${1:-all}" in
    web)  build_web ;;
    core) build_core_note ;;
    all)  build_web; echo; build_core_note ;;
    *)    echo "usage: $0 {web|core|all}" >&2; exit 2 ;;
esac
