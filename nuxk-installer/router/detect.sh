# detect.sh — read-only survey of a Keenetic/Entware router for nuxk-installer.
# Runs over SSH under busybox ash; prints flat "key value" lines, changes nothing.
# $R prefixes every path (tests point it at a fake /opt).
R="${NUXK_ROOT:-}"

kv() { echo "$1 $2"; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- platform ---------------------------------------------------------------
kv uname_m "$(uname -m 2>/dev/null)"
kv kernel "$(uname -r 2>/dev/null)"
if have opkg; then
    kv opkg 1
    # package arch carries byte order (mipsel-3.4), unlike `uname -m`
    kv opkg_arch "$(opkg print-architecture 2>/dev/null | awk '$2 != "all" && $2 != "noarch" {if ($3+0 >= p) {p=$3+0; a=$2}} END {print a}')"
else
    kv opkg 0
fi
if [ "$(uname -m 2>/dev/null)" = mips ]; then
    # ELF EI_DATA byte: 1 = little endian, 2 = big endian
    kv mips_endian "$(od -An -t u1 -j 5 -N 1 /bin/busybox 2>/dev/null | tr -d ' ')"
fi
if have ndmc; then
    kv keenetic_os "$(ndmc -c 'show version' 2>/dev/null | sed -n 's/^ *release: *//p' | head -n 1)"
    kv keenetic_model "$(ndmc -c 'show version' 2>/dev/null | sed -n 's/^ *model: *//p' | head -n 1)"
fi
kv opt_free_kb "$(df -k "$R/opt" 2>/dev/null | awk 'NR==2 {print $4}')"
kv mem_avail_kb "$(awk '/MemAvailable/ {print $2}' /proc/meminfo 2>/dev/null)"
kv clock "$(date +%s)"
kv lan_ip "$(ip -4 -o addr show br0 2>/dev/null | awk '{sub(/\/.*/, "", $4); print $4; exit}')"

# --- packages -----------------------------------------------------------------
for p in curl ca-certificates ipset iptables nfqws2-keenetic; do
    v=""
    have opkg && v=$(opkg list-installed "$p" 2>/dev/null | awk -v p="$p" '$1 == p {print $3}')
    kv "pkg.$p" "$v"
done
[ -f "$R/opt/etc/opkg/nfqws2-keenetic.conf" ] && kv feed.nfqws2 1 || kv feed.nfqws2 0

# --- kernel modules nfqws2 needs (Keenetic: "Netfilter kernel modules" component)
for m in nfnetlink_queue xt_NFQUEUE xt_connbytes xt_multiport; do
    s=missing
    if lsmod 2>/dev/null | awk '{print $1}' | grep -qx "$m"; then
        s=loaded
    elif find "/lib/modules/$(uname -r)" -name "$m.ko*" 2>/dev/null | grep -q .; then
        s=available
    fi
    kv "kmod.$m" "$s"
done

# --- engines ------------------------------------------------------------------
for e in S51nfqws2 S51usque S52xray S99nuxk-core; do
    [ -x "$R/opt/etc/init.d/$e" ] && kv "init.$e" 1 || kv "init.$e" 0
done
# usque-keenetic speaks nuxk's contract only in our fork (feature/web-ui)
if [ -x "$R/opt/etc/init.d/S51usque" ] && "$R/opt/etc/init.d/S51usque" info 2>/dev/null | grep -q '^service.running '; then
    kv usque_contract 1
else
    kv usque_contract 0
fi

# --- nuxk itself ----------------------------------------------------------------
if [ -x "$R/opt/usr/bin/nuxk-core" ]; then
    kv nuxk_core "$("$R/opt/usr/bin/nuxk-core" -version 2>/dev/null | awk '{print $2}')"
else
    kv nuxk_core ""
fi
[ -f "$R/opt/etc/nuxk/nuxk.conf" ] && kv nuxk_conf 1 || kv nuxk_conf 0
kv nuxk_listen "$(sed -n 's/^LISTEN=["]\{0,1\}\([^"]*\)["]\{0,1\}$/\1/p' "$R/opt/etc/nuxk/nuxk.conf" 2>/dev/null | tail -n 1)"
if [ -x "$R/opt/etc/init.d/S99nuxk-core" ] && "$R/opt/etc/init.d/S99nuxk-core" status 2>/dev/null | grep -q 'is running'; then
    kv nuxk_running 1
else
    kv nuxk_running 0
fi
