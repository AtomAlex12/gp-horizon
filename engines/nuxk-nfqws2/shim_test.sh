#!/bin/sh
# Tests S51nfqws2-nuxk against a fake stock nfqws2-keenetic under a temp root.
#   sh engines/nuxk-nfqws2/shim_test.sh
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
SHIM="$HERE/S51nfqws2-nuxk"
ROOT=$(mktemp -d)
trap 'kill "$SLEEPER" 2>/dev/null || true; rm -rf "$ROOT"' EXIT
export NUXK_ROOT="$ROOT"

mkdir -p "$ROOT/opt/etc/init.d" "$ROOT/opt/etc/nfqws2/lists" "$ROOT/opt/var/run"
cat >"$ROOT/opt/etc/init.d/S51nfqws2" <<EOF
#!/bin/sh
echo "stock \$1" >>"$ROOT/stock.log"
EOF
chmod +x "$ROOT/opt/etc/init.d/S51nfqws2"
cat >"$ROOT/opt/etc/nfqws2/nfqws2.conf" <<'EOF'
ISP_INTERFACE="eth3"
NFQUEUE_NUM=300
NFQWS_EXTRA_ARGS="$MODE_AUTO"
EOF
echo 'ISP_INTERFACE="ppp0"' >"$ROOT/opt/etc/nfqws2/nfqws2.conf.run"
printf 'a.com\nb.com\n' >"$ROOT/opt/etc/nfqws2/lists/user.list"

fail=0
check() { # check DESC ACTUAL EXPECTED
    if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got '$2', want '$3'"; fail=1; fi
}
kv() { sh "$SHIM" info | sed -n "s/^$1 //p"; }

check "stopped when no pidfile" "$(kv service.running)" "0"
check "runtime WAN wins over conf" "$(kv config.iface)" "ppp0"
check "queue from conf" "$(kv config.nfqueue_num)" "300"
check "mode parsed" "$(kv config.mode)" "auto"
check "desync count" "$(kv lists.desync_count)" "2"
check "items listed" "$(sh "$SHIM" info | grep -c '^item ')" "2"

sleep 60 &
SLEEPER=$!
echo "$SLEEPER" >"$ROOT/opt/var/run/nfqws2.pid"
check "running with live pid" "$(kv service.running)" "1"
check "pid reported" "$(kv service.pid)" "$SLEEPER"

check "apply-desync output" "$(printf 'x.com\ny.com\nz.com' | sh "$SHIM" apply-desync)" "applied 3"
check "user.list replaced" "$(cat "$ROOT/opt/etc/nfqws2/lists/user.list" | tr '\n' ',')" "x.com,y.com,z.com,"
check "reload sent while running" "$(tail -n 1 "$ROOT/stock.log")" "stock reload"
check "no temp file left" "$(ls -A "$ROOT/opt/etc/nfqws2/lists" | grep -c nuxk || true)" "0"

printf '162.159.198.2\n203.0.113.9\n' | sh "$SHIM" apply-endpoints >/dev/null
check "endpoints written" "$(kv lists.endpoints_count)" "2"
printf '' | sh "$SHIM" apply-endpoints >/dev/null
check "empty input clears endpoints" "$(kv lists.endpoints_count)" "0"

# --- apply-strategies: NFQWS_ARGS_CUSTOM + nuxk-sN.list, restart, restore ---
C="$ROOT/opt/etc/nfqws2/nfqws2.conf"
L="$ROOT/opt/etc/nfqws2/lists"
export NUXK_SETTLE=0
echo 'NFQWS_ARGS_CUSTOM=""' >>"$C"
# stock restart "fails" (no pidfile after it) when the conf holds a broken strategy
cat >"$ROOT/opt/etc/init.d/S51nfqws2" <<EOF
#!/bin/sh
echo "stock \$1" >>"$ROOT/stock.log"
if [ "\$1" = restart ]; then
  if grep -q 'lua-desync=broken' "$C"; then rm -f "$ROOT/opt/var/run/nfqws2.pid"; else echo "$SLEEPER" >"$ROOT/opt/var/run/nfqws2.pid"; fi
fi
EOF
good='--filter-tcp=443 --filter-l7=tls --hostlist=/opt/etc/nfqws2/lists/nuxk-s1.list --payload=tls_client_hello --lua-desync=multisplit:pos=1'
spec="list nuxk-s1.list
rutracker.org
browserleaks.com
.
custom $good"
check "apply-strategies" "$(printf '%s\n' "$spec" | sh "$SHIM" apply-strategies)" "applied"
check "strategy hostlist" "$(tr '\n' , <"$L/nuxk-s1.list")" "rutracker.org,browserleaks.com,"
check "custom args written" "$(grep '^NFQWS_ARGS_CUSTOM=' "$C")" "NFQWS_ARGS_CUSTOM=\"$good\""
check "untouched original kept" "$(grep -c '^NFQWS_ARGS_CUSTOM=""' "$C.nuxk-orig")" "1"
check "other lines kept" "$(grep -c '^NFQUEUE_NUM=300' "$C")" "1"
check "nfqws2 restarted" "$(tail -n 1 "$ROOT/stock.log")" "stock restart"
check "same args: lists only" "$(printf '%s\n' "$spec" | sh "$SHIM" apply-strategies)" "lists updated"
check "lists-only change reloads" "$(tail -n 1 "$ROOT/stock.log")" "stock reload"

if printf 'list nuxk-s1.list\na.com\n.\ncustom --lua-desync=broken\n' | sh "$SHIM" apply-strategies 2>"$ROOT/err"; then
    check "broken strategy fails" "exit 0" "exit 1"
fi
check "broken strategy reported" "$(grep -c 'не запустился' "$ROOT/err")" "1"
check "old conf restored" "$(grep '^NFQWS_ARGS_CUSTOM=' "$C")" "NFQWS_ARGS_CUSTOM=\"$good\""
check "old hostlist restored too" "$(tr '\n' , <"$L/nuxk-s1.list")" "rutracker.org,browserleaks.com,"
check "no staging left" "$(ls -A "$L" | grep -c '^\.nuxk' || true)" "0"
check "running again" "$(kv service.running)" "1"

printf 'custom\n' | sh "$SHIM" apply-strategies >/dev/null
check "cleared" "$(grep '^NFQWS_ARGS_CUSTOM=' "$C")" 'NFQWS_ARGS_CUSTOM=""'
check "strategy lists removed" "$(ls "$L" | grep -c '^nuxk-s' || true)" "0"

check "bad list name refused" "$(printf 'list ../../x\n.\n' | sh "$SHIM" apply-strategies 2>&1 | grep -c 'bad list name')" "1"
sed -i 's/^NFQWS_ARGS_CUSTOM=.*/NFQWS_ARGS_CUSTOM="--filter-tcp=5222 --lua-desync=mine"/' "$C"
if printf '%s\n' "$spec" | sh "$SHIM" apply-strategies 2>"$ROOT/err"; then
    check "own custom args refused" "exit 0" "exit 1"
fi
check "own custom args kept" "$(grep '^NFQWS_ARGS_CUSTOM=' "$C")" 'NFQWS_ARGS_CUSTOM="--filter-tcp=5222 --lua-desync=mine"'
check "refusal says why" "$(grep -c 'ваши собственные' "$ROOT/err")" "1"

sh "$SHIM" restart >/dev/null
check "lifecycle passes through" "$(tail -n 1 "$ROOT/stock.log")" "stock restart"

rm "$ROOT/opt/etc/init.d/S51nfqws2"
if sh "$SHIM" start 2>/dev/null; then check "missing stock fails" "exit 0" "exit 1"; else check "missing stock fails" "exit 1" "exit 1"; fi

[ "$fail" = 0 ] && echo "all shim tests passed"
exit "$fail"
