#!/bin/sh
# Tests nuxk-lite.sh end to end on a fake router: a temp root (NUXK_ROOT),
# fake opkg / ndmc / ip / lsmod, a release served as file:// and signed with a
# throwaway key, a fake XTLS archive. Needs curl, sha256sum, tar, ssh-keygen
# (OpenSSH 8.1+, to sign) and python3 (to make the zip).
#   sh install/lite_test.sh
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
TOP=$(cd "$HERE/.." && pwd)
LITE="$HERE/nuxk-lite.sh"
ROOT=$(mktemp -d)
trap 'rm -rf "$ROOT"' EXIT
PY=""
for p in python3 python; do "$p" -c 1 >/dev/null 2>&1 && PY=$(command -v "$p") && break; done
[ -n "$PY" ] || { echo "need python3"; exit 1; }
command -v ssh-keygen >/dev/null 2>&1 || { echo "need ssh-keygen"; exit 1; }
REAL_CURL=$(command -v curl)
# file:// URLs for curl; a Windows-native curl (Git Bash) wants D:/… paths
furl() { if command -v cygpath >/dev/null 2>&1; then echo "file:///$(cygpath -m "$1")"; else echo "file://$1"; fi; }

export NUXK_ROOT="$ROOT/router"
BIN="$ROOT/bin"
REL="$ROOT/release"
XTLS="$ROOT/xtls"
mkdir -p "$NUXK_ROOT/opt/tmp" "$NUXK_ROOT/opt/etc/nfqws2/lists" "$BIN" "$REL" "$XTLS/v26.3.27"
printf 'youtube.com\n# mine\nrutracker.org\n' >"$NUXK_ROOT/opt/etc/nfqws2/lists/user.list"

# the release key for these tests: the fake agent checks signatures with it,
# the way nuxk-core -verify does with the real one
ssh-keygen -q -t ed25519 -N "" -C "test release key" -f "$ROOT/key"
printf 'release@nuxk-horizon namespaces="nuxk-release" %s\n' "$(cut -d' ' -f1,2 "$ROOT/key.pub")" >"$ROOT/signers"

# --- a release, as `make release` lays it out and release.yml signs it -------------------
release() { # release VERSION [broken] — broken: an agent that never answers
    v=$1
    rm -rf "${REL:?}"/*
    cat >"$REL/nuxk-core-x86_64" <<EOF
#!/bin/sh
# ${2:+BROKEN}
case "\$1" in
-version) echo "nuxk-core $v abc123" ;;
-verify)
    ssh-keygen -Y verify -f "$ROOT/signers" -I release@nuxk-horizon -n nuxk-release -s "\$2.sig" <"\$2" >/dev/null 2>&1 ||
        { echo "подпись не сходится" >&2; exit 1; }
    echo "release@nuxk-horizon SHA256:test-key" ;;
esac
true
EOF
    # the init: restart marks it running, status says so (like the real one)
    cat >"$REL/S99nuxk-core" <<'EOF'
#!/bin/sh
M="$NUXK_ROOT/opt/var/run/nuxk.running"
case "$1" in
restart | start) mkdir -p "$(dirname "$M")"; touch "$M"; echo "Started nuxk-core" ;;
stop) rm -f "$M" ;;
status) [ -f "$M" ] && echo "nuxk-core is running" || echo "nuxk-core is stopped" ;;
esac
EOF
    cp "$TOP/engines/nuxk-nfqws2/S51nfqws2-nuxk" "$TOP/engines/nuxk-xray/S52xray-nuxk" "$REL/"
    echo "# release $v" >>"$REL/S52xray-nuxk" # which release's adapter is on the router
    sed "s/^VERSION=\"@VERSION@\"/VERSION=\"$v\"/" "$LITE" >"$REL/nuxk-lite.sh"
    mkdir -p "$ROOT/web/assets" && echo "<!doctype html><title>nuxk $v</title>" >"$ROOT/web/index.html" && echo 1 >"$ROOT/web/assets/app.js"
    tar -C "$ROOT/web" -czf "$REL/nuxk-web-lite-$v.tar.gz" .
    echo "fake ipk" >"$REL/usque-keenetic-x86_64.ipk"
    printf 'version %s\ncommit abc123\n' "$v" >"$REL/BUILD"
    (cd "$REL" && sha256sum -- * >SHA256SUMS && ssh-keygen -Y sign -f "$ROOT/key" -n nuxk-release SHA256SUMS 2>/dev/null)
}

# --- XTLS: a zip with a script for xray ---------------------------------------------------
printf '#!/bin/sh\n[ "$1" = version ] && echo "Xray 26.3.27 (Xray, Penetrates Everything.) test"\ntrue\n' >"$ROOT/xray"
"$PY" -c "import zipfile,sys; z=zipfile.ZipFile(sys.argv[1],'w'); z.write(sys.argv[2],'xray'); z.writestr('geoip.dat', b'0'*1000); z.close()" \
    "$XTLS/v26.3.27/Xray-linux-64.zip" "$ROOT/xray"
export XRAY_TEST_SUM
XRAY_TEST_SUM=$(sha256sum "$XTLS/v26.3.27/Xray-linux-64.zip" | cut -d' ' -f1)
export XRAY_BASE_URL=$(furl "$XTLS")

# --- the router's tools --------------------------------------------------------------------
STATE="$ROOT/opkg.installed"
echo "curl - 8.9.1-1" >"$STATE"
cat >"$BIN/opkg" <<EOF
#!/bin/sh
echo "opkg \$*" >>"$ROOT/opkg.log"
case "\$1" in
print-architecture) echo "arch all 1"; echo "arch noarch 1"; echo "arch x64-3.2 10" ;;
list-installed) [ -n "\${2:-}" ] && grep "^\$2 " "$STATE" || cat "$STATE" ;;
update) echo "Downloading ..." ;;
install) shift; for p in "\$@"; do case "\$p" in --*) continue ;; esac; echo "Installing \$p"
  case "\$p" in
  *.ipk)
    [ "\$(cat "\$p")" = "fake ipk" ] || exit 1
    echo "usque-keenetic - 0.4.0" >>"$STATE"
    mkdir -p "$NUXK_ROOT/opt/etc/usque" "$NUXK_ROOT/opt/etc/init.d"; echo 'IFACE="opkgtun0"' >"$NUXK_ROOT/opt/etc/usque/usque.conf"
    printf '#!/bin/sh\n[ "\$1" = info ] && { echo "service.running 1"; echo "tunnel.state connected"; }\ntrue\n' >"$NUXK_ROOT/opt/etc/init.d/S51usque"
    chmod +x "$NUXK_ROOT/opt/etc/init.d/S51usque"; continue ;;
  nfqws2-keenetic)
    mkdir -p "$NUXK_ROOT/opt/etc/init.d"; printf '#!/bin/sh\necho stock \$1\n' >"$NUXK_ROOT/opt/etc/init.d/S51nfqws2"; chmod +x "$NUXK_ROOT/opt/etc/init.d/S51nfqws2" ;;
  esac
  echo "\$p - 1.0-test" >>"$STATE"; done ;;
esac
EOF
cat >"$BIN/ndmc" <<EOF
#!/bin/sh
echo "\$2" >>"$ROOT/ndmc.log"
D="$ROOT/ndm"
case "\$2" in
"show version") echo '   release: 5.01'; echo '     model: Keenetic Test' ;;
"show interface "*) f="\$D/\${2#show interface }"; [ -f "\$f" ] || exit 1; echo "  description: \$(cat "\$f")" ;;
"interface "*" description "*) r=\${2#interface }; mkdir -p "\$D"; echo "\${r#* description }" >"\$D/\${r%% *}" ;;
"no interface "*) rm -f "\$D/\${2#no interface }" ;;
"interface "*) i=\${2#interface }; mkdir -p "\$D"; [ -f "\$D/\$i" ] || echo - >"\$D/\$i" ;;
"system configuration save") echo saved >>"$ROOT/ndm.saved" ;;
esac
EOF
printf '#!/bin/sh\necho "5: br0    inet 192.168.9.1/24 brd 192.168.9.255 scope global br0"\n' >"$BIN/ip"
printf '#!/bin/sh\nfor m in nfnetlink_queue xt_NFQUEUE xt_connbytes xt_multiport; do echo "$m 1 0"; done\n' >"$BIN/lsmod"
# curl: the real one for file://; the agent's healthz (not a broken one's) and
# the router's RCI answer here
cat >"$BIN/curl" <<EOF
#!/bin/sh
case "\$*" in
*/api/v1/healthz*) grep -q BROKEN "$NUXK_ROOT/opt/usr/bin/nuxk-core" 2>/dev/null && exit 7; echo '{"status":"ok"}'; exit 0 ;;
*:79/rci/*) echo "\$*" >>"$ROOT/rci.log"; echo '{}'; exit 0 ;;
esac
exec "$REAL_CURL" "\$@"
EOF
printf '#!/bin/sh\nexec "%s" -c "import zipfile,sys; sys.stdout.buffer.write(zipfile.ZipFile(sys.argv[2]).read(sys.argv[3]))" "$@"\n' "$PY" >"$BIN/unzip"
chmod +x "$BIN"/*
export PATH="$BIN:$PATH"
export NUXK_BASE_URL=$(furl "$REL")
export NO_COLOR=1
export NUXK_TEST_WAIT=2 # a dead agent: 2 s, not 15

fail=0
check() { # check DESC ACTUAL EXPECTED
    if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got '$2', want '$3'"; fail=1; fi
}
has() { grep -c -- "$2" "$1" 2>/dev/null || true; } # has FILE TEXT → count
f() { echo "$NUXK_ROOT$1"; }
SH="${SH:-sh}" # the shell under test: SH="busybox sh" is the router's
lite() { $SH "$LITE" "$@" >"$ROOT/out" 2>&1; }

release 0.3.0

# 1. a file that isn't the release's: refused before anything is written
cp "$REL/nuxk-core-x86_64" "$ROOT/good"
echo "evil" >>"$REL/nuxk-core-x86_64"
lite --yes && check "tampered: fails" "exit 0" "exit 1"
check "tampered: says why" "$(has "$ROOT/out" 'не совпал с SHA256SUMS')" "1"
check "tampered: nothing installed" "$(ls "$(f /opt/usr/bin)" 2>/dev/null | wc -l | tr -d ' ')" "0"
check "tampered: not even packages" "$(grep -c '^opkg install\|^opkg update' "$ROOT/opkg.log" 2>/dev/null || true)" "0"
cp "$ROOT/good" "$REL/nuxk-core-x86_64"

# 2. a fresh router, defaults: deps, nfqws2, agent, config — no WARP, no VLESS
lite --yes || { cat "$ROOT/out"; exit 1; }
[ -n "${SHOW:-}" ] && cat "$ROOT/out" # SHOW=1: what a person sees
check "installed agent" "$("$(f /opt/usr/bin/nuxk-core)" -version)" "nuxk-core 0.3.0 abc123"
check "deps installed" "$(has "$ROOT/opkg.log" 'install ca-certificates ipset')" "1"
check "nfqws2 feed" "$(has "$(f /opt/etc/opkg/nfqws2-keenetic.conf)" 'nfqws2-keenetic https://nfqws.github.io')" "1"
check "web unpacked" "$(has "$(f /opt/share/www/nuxk/index.html)" 'nuxk 0.3.0')" "1"
check "adapter" "$(has "$(f /opt/etc/nuxk/engines/S51nfqws2-nuxk)" 'nuxk adapter shim over the STOCK')" "1"
check "nuxk command" "$(has "$(f /opt/bin/nuxk)" 'VERSION="0.3.0"')" "1"
C=$(f /opt/etc/nuxk/nuxk.conf)
check "conf: LAN address" "$(has "$C" 'LISTEN="192.168.9.1:4141"')" "1"
check "conf: nfqws2 wired" "$(has "$C" 'ENGINE_NFQWS2="/opt/etc/nuxk/engines/S51nfqws2-nuxk"')" "1"
check "conf: no WARP" "$(has "$C" 'ENGINE_USQUE=""')" "1"
check "conf: plan only" "$(has "$C" '^PLANE_APPLY="0"')" "1"
TOKEN=$(sed -n 's/^API_TOKEN="\(.*\)"/\1/p' "$C")
check "conf: a 128-bit token" "${#TOKEN}" "32"
check "plane: DPI from user.list" "$(has "$(f /opt/etc/nuxk/plane.json)" '"youtube.com", "rutracker.org"')" "1"
check "plane: comments left out" "$(has "$(f /opt/etc/nuxk/plane.json)" 'mine')" "0"
check "started" "$(has "$ROOT/out" 'nuxk-core отвечает')" "1"
check "the panel's address" "$(has "$ROOT/out" 'Панель   http://192.168.9.1:4141')" "1"
check "no temp left" "$(ls "$(f /opt/tmp)" | wc -l | tr -d ' ')" "0"
check "first install: no agent to check the signature yet" "$(has "$ROOT/out" 'подпись релиза проверит уже установленный агент')" "1"

# 3. again: nothing to do — but a router set up before this installer gets
# the nuxk command; the agent now there checks the release's signature
rm -f "$(f /opt/bin/nuxk)"
lite --yes
check "rerun: nothing to install" "$(has "$ROOT/out" 'всё уже стоит')" "1"
check "rerun: the command comes back" "$(has "$(f /opt/bin/nuxk)" 'VERSION="0.3.0"')" "1"
check "rerun: signature checked by the agent" "$(has "$ROOT/out" 'подпись релиза ✓ SHA256:test-key')" "1"

# 4. WARP and VLESS by name; usque sits on OpkgTun0, someone's own VPN on OpkgTun1
mkdir -p "$ROOT/ndm" && echo usque >"$ROOT/ndm/OpkgTun0" && echo my-vpn >"$ROOT/ndm/OpkgTun1"
$SH "$(f /opt/bin/nuxk)" warp --yes >"$ROOT/out" 2>&1 || { cat "$ROOT/out"; exit 1; }
check "warp: wired" "$(has "$C" 'ENGINE_USQUE="/opt/etc/init.d/S51usque"')/$(has "$C" 'PLANE_IFACE_WARP="OpkgTun0"')" "1/1"
check "warp: hosts desynced" "$(has "$(f /opt/etc/nfqws2/lists/user.list)" 'cloudflareclient.com')" "1"
$SH "$(f /opt/bin/nuxk)" vless --yes >"$ROOT/out" 2>&1 || { cat "$ROOT/out"; exit 1; }
check "vless: xray" "$(has "$(f /opt/sbin/xray)" 'Penetrates')" "1"
check "vless: no geoip on the router" "$(ls "$(f /opt/sbin)")" "xray"
check "vless: init" "$(has "$(f /opt/etc/init.d/S52xray-nuxk)" 'S52xray-nuxk')" "1"
check "vless: a free OpkgTun, ours" "$(cat "$ROOT/ndm/OpkgTun2" 2>/dev/null)" "nuxk-vless"
check "vless: someone else's untouched" "$(cat "$ROOT/ndm/OpkgTun1")" "my-vpn"
check "vless: router config saved" "$(has "$ROOT/ndm.saved" saved)" "1"
check "vless: wired" "$(has "$C" 'ENGINE_XRAY="/opt/etc/init.d/S52xray-nuxk"')/$(has "$C" 'PLANE_IFACE_VLESS="OpkgTun2"')" "1/1"
check "token kept" "$(has "$C" "API_TOKEN=\"$TOKEN\"")" "1"

# 5. an archive that isn't XTLS's
XRAY_TEST_SUM=0000 $SH "$(f /opt/bin/nuxk)" vless --yes >"$ROOT/out" 2>&1 || true
check "vless: another archive refused" "$(has "$ROOT/out" 'хеш архива xray не совпал')" "1"

# 6. a newer release, the way the panel starts it: the new release's own
# script does it, the agent moves, the old version is kept aside, the config
# isn't touched, and the panel's status file says how it went
nuxk() { $SH "$(f /opt/bin/nuxk)" "$@" >"$ROOT/out" 2>&1; }
RUN="$ROOT/update-run"
panel() { NUXK_STATUS=$RUN NUXK_FROM=$1 NUXK_STARTED=1700000000 nuxk update --yes; }
agent() { "$(f /opt/usr/bin/nuxk-core)" -version | cut -d' ' -f2; }
release 0.3.1
panel 0.3.0 || { cat "$ROOT/out"; exit 1; }
[ -n "${SHOW:-}" ] && cat "$ROOT/out"
check "update: new agent" "$(agent)" "0.3.1"
check "update: signature checked" "$(has "$ROOT/out" 'подпись релиза ✓')" "1"
check "update: the old version kept" "$("$(f /opt/var/lib/nuxk/prev/nuxk-core)" -version)" "nuxk-core 0.3.0 abc123"
check "update: the old web kept" "$(has "$(f /opt/var/lib/nuxk/prev/web/index.html)" 'nuxk 0.3.0')" "1"
check "update: new web" "$(has "$(f /opt/share/www/nuxk/index.html)" 'nuxk 0.3.1')" "1"
check "update: new command" "$(has "$(f /opt/bin/nuxk)" 'VERSION="0.3.1"')" "1"
check "update: config as it was" "$(has "$C" "API_TOKEN=\"$TOKEN\"")/$(has "$C" 'PLANE_IFACE_VLESS="OpkgTun2"')" "1/1"
check "update: the panel sees it done" "$(sed -n 's/^state //p' "$RUN")/$(sed -n 's/^from //p' "$RUN")/$(sed -n 's/^to //p' "$RUN")/$(sed -n 's/^started //p' "$RUN")" "done/0.3.0/0.3.1/1700000000"
check "update: no temp left" "$(ls "$(f /opt/tmp)" | wc -l | tr -d ' ')" "0"
check "update: router config not saved again" "$(has "$ROOT/ndm.saved" saved)" "1"
# the xray adapter moves with the agent that drives it (xray itself didn't change)
check "update: new xray adapter" "$(has "$(f /opt/etc/init.d/S52xray-nuxk)" '# release 0.3.1')" "1"
check "update: the old xray adapter kept" "$(has "$(f /opt/var/lib/nuxk/prev/S52xray-nuxk)" '# release 0.3.0')" "1"
check "update: xray itself not downloaded again" "$(has "$ROOT/out" 'Xray-linux')" "0"

# 7. releases the agent refuses: SHA256SUMS changed after signing, no
# signature at all — nothing on the router changes
release 0.3.2
echo "0000  nuxk-core-mips" >>"$REL/SHA256SUMS"
panel 0.3.1 && check "changed sums: fails" "exit 0" "exit 1"
check "changed sums: says why" "$(has "$ROOT/out" 'не подписан ключом nuxk Horizon')" "1"
check "changed sums: agent as it was" "$(agent)" "0.3.1"
check "changed sums: the panel sees it failed" "$(sed -n 's/^state //p' "$RUN")" "failed"
release 0.3.2
rm -f "$REL/SHA256SUMS.sig"
nuxk update --yes && check "no signature: fails" "exit 0" "exit 1"
check "no signature: says why" "$(has "$ROOT/out" 'не подписан ключом nuxk Horizon')" "1"
check "no signature: agent as it was" "$(agent)" "0.3.1"

# 8. the latest release is older than what runs: nothing to do
release 0.3.0
nuxk update --yes || { cat "$ROOT/out"; exit 1; }
check "older release: left alone" "$(has "$ROOT/out" 'новее последнего релиза 0.3.0')/$(agent)" "1/0.3.1"

# 9. nuxk rollback: the version kept aside comes back (offline)
NUXK_BASE_URL=file:///nonexistent nuxk rollback --yes || { cat "$ROOT/out"; exit 1; }
check "rollback: old agent" "$(agent)" "0.3.0"
check "rollback: old web" "$(has "$(f /opt/share/www/nuxk/index.html)" 'nuxk 0.3.0')" "1"
check "rollback: the command stays new" "$(has "$(f /opt/bin/nuxk)" 'VERSION="0.3.1"')" "1"
check "rollback: old xray adapter" "$(has "$(f /opt/etc/init.d/S52xray-nuxk)" '# release 0.3.0')" "1"
check "rollback: nuxk DNS out of the DNS proxy first" "$(has "$ROOT/rci.log" '"parse":"no ip name-server 127.0.0.1:53053"')" "1"
release 0.3.1
nuxk update --yes || { cat "$ROOT/out"; exit 1; }
check "and forward again" "$(agent)" "0.3.1"

# 10. a new version that won't answer: the old one back by itself
release 0.3.2 broken
panel 0.3.1 && check "broken: fails" "exit 0" "exit 1"
[ -n "${SHOW:-}" ] && cat "$ROOT/out"
check "broken: rolled back" "$(agent)" "0.3.1"
check "broken: old web back" "$(has "$(f /opt/share/www/nuxk/index.html)" 'nuxk 0.3.1')" "1"
check "broken: says so" "$(has "$ROOT/out" 'Вернул прежнюю версию 0.3.1')" "1"
check "broken: the panel sees it rolled back" "$(sed -n 's/^state //p' "$RUN")/$(sed -n 's/^to //p' "$RUN")" "rolled_back/0.3.2"
check "broken: agent running" "$([ -f "$(f /opt/var/run/nuxk.running)" ] && echo yes)" "yes"
check "broken: xray adapter back" "$(has "$(f /opt/etc/init.d/S52xray-nuxk)" '# release 0.3.1')" "1"

# 11. the downloaded script run over an older nuxk (no `update`, as from a
# router set up before `nuxk` existed): the same safety net
release 0.3.3 broken
lite --yes && check "reinstall broken: fails" "exit 0" "exit 1"
check "reinstall broken: rolled back" "$(agent)" "0.3.1"
check "reinstall broken: says so" "$(has "$ROOT/out" 'Вернул прежнюю версию 0.3.1')" "1"
check "reinstall broken: xray adapter back" "$(has "$(f /opt/etc/init.d/S52xray-nuxk)" '# release 0.3.1')" "1"

# 12. status
$SH "$(f /opt/bin/nuxk)" >"$ROOT/out" 2>&1
check "status: running" "$(has "$ROOT/out" '0.3.1 · работает')" "1"

# 13. uninstall: agent gone, routing objects dropped, config and lists aside
: >"$ROOT/rci.log"
$SH "$(f /opt/bin/nuxk)" uninstall --yes >"$ROOT/out" 2>&1 || { cat "$ROOT/out"; exit 1; }
check "uninstall: agent gone" "$(ls "$(f /opt/usr/bin)" | wc -l | tr -d ' ')" "0"
check "uninstall: the kept version gone" "$([ -e "$(f /opt/var/lib/nuxk/prev)" ] && echo left || echo gone)" "gone"
check "uninstall: command gone" "$([ -e "$(f /opt/bin/nuxk)" ] && echo left || echo gone)" "gone"
check "uninstall: config aside" "$(has "$(f /opt/etc/nuxk.removed/nuxk.conf)" 'API_TOKEN')" "1"
check "uninstall: nuxk routes dropped" "$(has "$ROOT/rci.log" '"group":"nuxk-vless","interface":"OpkgTun2","no":true')" "1"
check "uninstall: nuxk DNS taken out of the DNS proxy" "$(has "$ROOT/rci.log" '"parse":"no ip name-server 127.0.0.1:53053"')" "1"
check "uninstall: xray kept by default" "$([ -x "$(f /opt/sbin/xray)" ] && echo kept || echo gone)" "kept"
check "uninstall: nfqws2's list stays" "$(has "$(f /opt/etc/nfqws2/lists/user.list)" 'youtube.com')" "1"

[ "$fail" = 0 ] && echo "all lite installer tests passed"
exit "$fail"
