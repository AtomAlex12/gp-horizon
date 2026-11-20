#!/bin/sh
set -eu

dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT HUP INT TERM
export PATH="$scratch:$PATH"
export AWG_TEST_TRACE="$scratch/trace"

cat > "$scratch/ip" <<'EOF'
#!/bin/sh
case "$*" in
    '-4 rule show') printf '%s\n' "${AWG_TEST_RULE:-}"; exit 0 ;;
    '-4 route show table 28278') exit 0 ;;
    'link show dev awg0') exit 1 ;;
esac
printf 'ip %s\n' "$*" >> "$AWG_TEST_TRACE"
EOF
cat > "$scratch/iptables" <<'EOF'
#!/bin/sh
case "$*" in *' -C '*) exit 1 ;; esac
printf 'iptables %s\n' "$*" >> "$AWG_TEST_TRACE"
EOF
cat > "$scratch/ipset" <<'EOF'
#!/bin/sh
printf 'ipset %s\n' "$*" >> "$AWG_TEST_TRACE"
EOF
chmod +x "$scratch/ip" "$scratch/ipset" "$scratch/iptables"

printf '%s\n' block | sh "$dir/warp-awg-device" route-enable
grep -q '^ip -4 route replace blackhole default metric 32767 table 28278$' "$AWG_TEST_TRACE"
grep -q '^ip -4 rule add pref 28278 fwmark 0x3a4e0000/0xffffffff table 28278$' "$AWG_TEST_TRACE"
grep -q 'PREROUTING .*--match-set NUXK_WARP_AWG4 dst .*--mark 0x0/0xffffffff' "$AWG_TEST_TRACE"
grep -q 'PREROUTING .*--match-set NUXK_WARP_AWG4 dst .*--mark 0xffffaaa/0xffffffff' "$AWG_TEST_TRACE"
grep -q 'POSTROUTING .* -o awg0 -j MASQUERADE$' "$AWG_TEST_TRACE"

: > "$AWG_TEST_TRACE"
export AWG_TEST_RULE='28278: from all fwmark 0x10 lookup 50'
if printf '%s\n' block | sh "$dir/warp-awg-device" route-enable > "$scratch/out" 2>&1; then
    echo 'reserved rule priority was occupied' >&2
    exit 1
fi
test ! -s "$AWG_TEST_TRACE"

cat > "$scratch/curl" <<'EOF'
#!/bin/sh
if [ "$#" -ne 11 ] || [ "$5" != '--interface' ] || [ "$6" != 'awg0' ] ||
    [ "$9" != '--resolve' ] || [ "${10}" != 'one.one.one.one:443:1.1.1.1' ] ||
    [ "${11}" != 'https://one.one.one.one/cdn-cgi/trace' ]; then
    exit 1
fi
printf 'ip=192.0.2.2\nwarp=on\ncolo=TST\n'
EOF
chmod +x "$scratch/curl"
# Load the functions without the command dispatcher and simulate an owned
# live device. The fake curl rejects unexpected operands in the real probe.
sed '/^case "${1:-}" in/,$d' "$dir/warp-awg-device" > "$scratch/functions"
(
    . "$scratch/functions"
    owned() { return 0; }
    link_exists() { return 0; }
    probe
) > "$scratch/probe"
grep -qx 'warp on' "$scratch/probe"
grep -qx 'ok 1' "$scratch/probe"
echo 'AmneziaWG route shim: OK'
