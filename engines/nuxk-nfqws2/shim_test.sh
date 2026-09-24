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

sh "$SHIM" restart >/dev/null
check "lifecycle passes through" "$(tail -n 1 "$ROOT/stock.log")" "stock restart"

rm "$ROOT/opt/etc/init.d/S51nfqws2"
if sh "$SHIM" start 2>/dev/null; then check "missing stock fails" "exit 0" "exit 1"; else check "missing stock fails" "exit 1" "exit 1"; fi

[ "$fail" = 0 ] && echo "all shim tests passed"
exit "$fail"
