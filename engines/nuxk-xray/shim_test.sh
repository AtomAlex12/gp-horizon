#!/bin/sh
# Tests S52xray-nuxk against a fake xray under a temp root.
#   sh engines/nuxk-xray/shim_test.sh
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
SHIM="$HERE/S52xray-nuxk"
ROOT=$(mktemp -d)
export NUXK_ROOT="$ROOT"
export SYSNET="$ROOT/sys/class/net"
export XRAY_BIN="$ROOT/bin/xray"
export NDMC="$ROOT/bin/no-ndmc" # none until the KeeneticOS part below
trap 'sh "$SHIM" stop >/dev/null 2>&1 || true; rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin" "$SYSNET"

# fake xray: -test refuses a config with "broken" — and, like the real one,
# one with a TUN inbound whose device is taken; run dies on "dies", never
# brings the TUN device up on "notun", otherwise makes it and waits
cat >"$XRAY_BIN" <<'EOF'
#!/bin/sh
case "$1" in
version)
    echo "Xray 26.3.27 (Xray, Penetrates Everything.) 2ad7b8f (go1.26.1 linux/arm64)"
    exit 0
    ;;
esac
shift
test=""
confs=""
while [ $# -gt 0 ]; do
    case "$1" in
    -test) test=1 ;;
    -config | -c) confs="$confs $2"; shift ;;
    esac
    shift
done
# shellcheck disable=SC2086
name=$(sed -n 's/.*"name": *"\([^"]*\)".*/\1/p' $confs | head -n 1)
if [ -n "$test" ]; then
    echo "2026/09/26 18:00:47 [Info] infra/conf/serial: Reading config"
    # shellcheck disable=SC2086
    grep -q '"broken"' $confs && { echo "Failed to start: app/proxyman/outbound: unknown protocol"; exit 23; }
    [ -n "$name" ] && [ -e "$SYSNET/$name" ] && { echo "Failed to start: main: failed to create server > device or resource busy"; exit 23; }
    echo "Configuration OK."
    exit 0
fi
# shellcheck disable=SC2086
grep -q '"dies"' $confs && { echo "Failed to start: dial tcp: refused"; exit 23; }
# shellcheck disable=SC2086
if ! grep -q '"notun"' $confs; then
    mkdir -p "$SYSNET/$name/statistics"
    echo 4242 >"$SYSNET/$name/statistics/rx_bytes"
    echo 2121 >"$SYSNET/$name/statistics/tx_bytes"
fi
trap 'rm -rf "$SYSNET/$name"; exit 0' TERM INT
while :; do sleep 1; done
EOF
chmod +x "$XRAY_BIN"

fail=0
check() { # check DESC ACTUAL EXPECTED
    if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got '$2', want '$3'"; fail=1; fi
}
kv() { sh "$SHIM" info | sed -n "s/^$1 //p"; }
conf() { # conf SERVER-ADDRESS [MARK] — a config as nuxk-core renders it
    printf 'server %s:443\nendpoint %s:443\nsecurity reality\nnetwork raw\nsni www.example.com\nfingerprint chrome\nflow xtls-rprx-vision\niface opkgtun1\n---\n' "$1" "$1"
    printf '{\n  "outbounds": [ { "address": "%s", "mark": "%s" } ]\n}\n' "$1" "${2:-}"
}

check "stopped at first" "$(kv service.running)" "0"
check "no config yet: start refused" "$(sh "$SHIM" start 2>&1 | grep -c 'задайте сервер')" "1"

check "set-config" "$(conf 203.0.113.9 | sh "$SHIM" set-config)" "applied"
check "running" "$(kv service.running)" "1"
check "tunnel up" "$(kv tunnel.state)" "connected"
check "iface" "$(kv iface.name)" "opkgtun1"
check "server from meta" "$(kv config.server)" "203.0.113.9:443"
check "endpoint from meta" "$(kv config.endpoint)" "203.0.113.9:443"
check "flow from meta" "$(kv config.flow)" "xtls-rprx-vision"
check "version once per start" "$(kv version.xray)" "26.3.27"
check "traffic from the device" "$(kv traffic.rx_bytes)/$(kv traffic.tx_bytes)" "4242/2121"
check "no staging left" "$(ls -A "$ROOT/opt/etc/xray" | grep -c 'stage\|prev' || true)" "0"
check "tun inbound in its own file" "$(grep -c '"name": "opkgtun1"' "$ROOT/opt/etc/xray/tun.json")" "1"
pid0=$(cat "$ROOT/opt/var/run/xray.pid")
check "same config: no restart" "$(conf 203.0.113.9 | sh "$SHIM" set-config)" "unchanged"
check "same config: same process" "$(cat "$ROOT/opt/var/run/xray.pid")" "$pid0"
check "new server while running" "$(conf 203.0.113.10 | sh "$SHIM" set-config)" "applied"
check "new server took" "$(kv config.server)" "203.0.113.10:443"
check "back to the first" "$(conf 203.0.113.9 | sh "$SHIM" set-config)" "applied"

check "refused by xray -test" "$(conf 198.51.100.7 broken | sh "$SHIM" set-config 2>&1 | grep -c 'не принял конфиг: Failed to start')" "1"
check "old config kept" "$(kv config.server)" "203.0.113.9:443"
check "still running" "$(kv service.running)" "1"

check "won't start: old one back" "$(conf 198.51.100.7 dies | sh "$SHIM" set-config 2>&1 | grep -c 'вернул прежний')" "1"
check "old server again" "$(kv config.server)" "203.0.113.9:443"
check "running on the old one" "$(kv tunnel.state)" "connected"

check "no tunnel in 10 s: old one back" "$(conf 198.51.100.7 notun | sh "$SHIM" set-config 2>&1 | grep -c 'вернул прежний')" "1"
check "tunnel up on the old one" "$(kv tunnel.state)" "connected"

check "bad meta refused" "$(printf 'server a;reboot\n---\n{}\n' | sh "$SHIM" set-config 2>&1 | grep -c 'bad meta')" "1"
check "no config after --- refused" "$(printf 'server a\n' | sh "$SHIM" set-config 2>&1 | grep -c 'no config')" "1"

# probe through the tunnel (fake curl answers like cdn-cgi/trace)
cat >"$ROOT/bin/curl" <<'EOF'
#!/bin/sh
printf 'fl=1\nip=203.0.113.9\ncolo=AMS\n\ntime_total=0.137\n'
EOF
chmod +x "$ROOT/bin/curl"
check "probe ok" "$(PATH="$ROOT/bin:$PATH" sh "$SHIM" probe | grep -E '^(ok|egress_ip|rtt_ms) ' | tr '\n' ,)" "ok 1,egress_ip 203.0.113.9,rtt_ms 137,"

sh "$SHIM" stop
check "stopped" "$(kv service.running)/$(kv tunnel.state)" "0/stopped"
check "device gone with xray" "$(ls -A "$SYSNET" | wc -l | tr -d ' ')" "0"
check "probe when stopped" "$(sh "$SHIM" probe | sed -n 's/^reason //p')" "stopped"
check "restart" "$(sh "$SHIM" restart >/dev/null && kv tunnel.state)" "connected"

# --- KeeneticOS: OpkgTun1 is configured only when nuxk created it -----------
# like the firmware's: dies under Entware's LD_LIBRARY_PATH
cat >"$ROOT/bin/ndmc" <<EOF
#!/bin/sh
[ -n "\${LD_LIBRARY_PATH:-}" ] && { echo "ndm: Cli::Main: failed to initialize." >&2; exit 1; }
echo "\$2" >>"$ROOT/ndmc.log"
case "\$2" in
"show interface OpkgTun1") printf 'id: OpkgTun1\n  description: %s\n' "\$(cat "$ROOT/ndm.descr")" ;;
esac
EOF
chmod +x "$ROOT/bin/ndmc"
export NDMC="$ROOT/bin/ndmc"
echo nuxk-vless >"$ROOT/ndm.descr"
: >"$ROOT/ndmc.log"
LD_LIBRARY_PATH=/opt/lib:/opt/usr/lib sh "$SHIM" restart
check "ndm: ours configured on start" "$(grep -v '^show' "$ROOT/ndmc.log" | tr '\n' ,)" "interface OpkgTun1 down,interface OpkgTun1 ip global auto,interface OpkgTun1 ip tcp adjust-mss pmtu,interface OpkgTun1 ip address 172.16.2.1 255.255.255.255,interface OpkgTun1 up,"
: >"$ROOT/ndmc.log"
sh "$SHIM" stop
check "ndm: down on stop" "$(grep -v '^show' "$ROOT/ndmc.log")" "interface OpkgTun1 down"
echo my-tunnel >"$ROOT/ndm.descr"
: >"$ROOT/ndmc.log"
check "ndm: someone else's says why" "$(sh "$SHIM" start 2>&1 | grep -c 'не создан nuxk')" "1"
check "ndm: someone else's never touched" "$(grep -vc '^show' "$ROOT/ndmc.log" || true)" "0"
check "xray still runs on it" "$(kv tunnel.state)" "connected"
export NDMC="$ROOT/bin/no-ndmc"

# the very first config that won't start leaves nothing behind
sh "$SHIM" stop
rm -f "$ROOT/opt/etc/xray/config.json" "$ROOT/opt/etc/xray/nuxk.meta"
check "first config won't start" "$(conf 198.51.100.7 dies | sh "$SHIM" set-config 2>&1 | grep -c 'с этим конфигом')" "1"
check "nothing left" "$(ls "$ROOT/opt/etc/xray" | grep -c 'config.json\|nuxk.meta' || true)" "0"

[ "$fail" = 0 ] && echo "all xray shim tests passed"
exit "$fail"
