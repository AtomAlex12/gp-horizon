#!/bin/sh
# E0 — read-only survey of a Keenetic router for the nuxk data plane (agent,
# ipset steering, soft DNS mode). Runs ON the router under busybox ash.
# Changes nothing: only `show`, listings, /proc reads and help output.
#
#   from the Pi:  sh tools/e0/run.sh 192.168.2.1        (wraps ssh + saves report)
#   by hand:      ssh -p 222 root@192.168.2.1 'sh -s' < tools/e0/router-check.sh
#
# Output: human-readable sections, then a SUMMARY block of "key value" lines.
# Deliberately never prints the full running-config (it holds credentials):
# only DNS / PPE / policy lines are grepped out of it.

export LC_ALL=C
have() { command -v "$1" >/dev/null 2>&1; }
sec() { printf '\n===== %s =====\n' "$1"; }
SUM=""
put() { SUM="$SUM$1 $2
"; }

# ndm: Keenetic CLI from Entware (read-only "show" commands only)
ndm() { have ndmc && ndmc -c "$1" 2>/dev/null; }
RC=$(ndm 'show running-config')

# -------------------------------------------------------------------- system
sec "system"
uname -a 2>/dev/null
ndm 'show version' | grep -E '^ *(release|model|device|hw_version|arch|sandbox):'
put kernel "$(uname -r 2>/dev/null)"
put keenetic_os "$(ndm 'show version' | sed -n 's/^ *release: *//p' | head -n 1)"
put model "$(ndm 'show version' | sed -n 's/^ *model: *//p' | head -n 1)"
put opkg_arch "$(opkg print-architecture 2>/dev/null | awk '$2!="all"&&$2!="noarch"{if($3+0>=p){p=$3+0;a=$2}}END{print a}')"
put mem_total_kb "$(awk '/MemTotal/ {print $2}' /proc/meminfo)"
put mem_avail_kb "$(awk '/MemAvailable/ {print $2}' /proc/meminfo)"
put cpu_count "$(grep -c ^processor /proc/cpuinfo)"
grep -m1 -iE 'system type|model name|cpu model' /proc/cpuinfo
put opt_free_kb "$(df -k /opt 2>/dev/null | awk 'NR==2 {print $4}')"
cat /proc/loadavg

# -------------------------------------------------------------------- fwmark usage
# Which of the high bits 28..31 does anything on this box already touch?
# (Keenetic PBR is believed to use the low 28 bits; nfqws2 uses 29 and 30.)
sec "fwmark: ip rule"
ip rule show 2>/dev/null
ip -6 rule show 2>/dev/null | sed 's/^/v6 /'

sec "fwmark: iptables lines mentioning marks"
MARKS=$( { iptables-save 2>/dev/null; ip6tables-save 2>/dev/null; } | grep -iE 'mark' )
echo "$MARKS" | head -n 80
[ "$(echo "$MARKS" | grep -c .)" -gt 80 ] && echo "... ($(echo "$MARKS" | grep -c .) lines total)"

sec "fwmark: Keenetic policies (running-config)"
echo "$RC" | grep -iE '^ *(ip policy|ip hotspot|policy|permit|mark|dns-proxy|ppe|opkg)' | head -n 60

# bit_used N — does any mark value or mask (ip rule + iptables) have bit N set?
# Pure string work on the first hex digit: no 32-bit arithmetic on MIPS ash.
HEX=$( { ip rule show 2>/dev/null; echo "$MARKS"; } | grep -oiE '0x[0-9a-f]+' | tr 'A-F' 'a-f' | sort -u)
bit_used() { # $1 = 28|29|30|31
    for h in $HEX; do
        d=${h#0x}
        while [ ${#d} -lt 8 ]; do d="0$d"; done
        [ ${#d} -gt 8 ] && continue
        f=$(printf '%s' "$d" | cut -c1)
        case "$f" in
        0) n=0 ;; 1) n=1 ;; 2) n=2 ;; 3) n=3 ;; 4) n=4 ;; 5) n=5 ;; 6) n=6 ;; 7) n=7 ;;
        8) n=8 ;; 9) n=9 ;; a) n=10 ;; b) n=11 ;; c) n=12 ;; d) n=13 ;; e) n=14 ;; f) n=15 ;;
        esac
        case "$1" in 28) b=1 ;; 29) b=2 ;; 30) b=4 ;; 31) b=8 ;; esac
        [ $((n / b % 2)) -eq 1 ] && { echo "$h"; return 0; }
    done
    return 1
}
for b in 28 29 30 31; do
    if w=$(bit_used $b); then put "mark_bit$b" "used($w)"; else put "mark_bit$b" free; fi
done
put mark_rules_count "$(echo "$MARKS" | grep -c .)"
put ip_rules_count "$(ip rule show 2>/dev/null | grep -c .)"

sec "routing tables in use"
TABLES=$( { ip rule show; ip route show table all; } 2>/dev/null | grep -oE '(lookup|table) [0-9a-z_]+' | awk '{print $2}' | sort -u | tr '\n' ' ')
echo "$TABLES"
put tables_in_use "$TABLES"

# -------------------------------------------------------------------- DNS
sec "DNS: who listens on :53"
if have netstat; then netstat -lnup 2>/dev/null | grep -E ':(53|5300|5353) '; netstat -lntp 2>/dev/null | grep -E ':(53|853|5300) '; fi
have ss && ss -lnup 2>/dev/null | grep -E ':(53|5300|5353) '
put dns53 "$( { netstat -lnup 2>/dev/null || ss -lnup 2>/dev/null; } | grep -E '[:.]53 ' | awk '{print $NF}' | sort -u | tr '\n' ' ')"
# the agent's planned port (5300) must be free; 5353 is usually mDNS
put port5300_busy "$( { netstat -lnup 2>/dev/null || ss -lnup 2>/dev/null; } | grep -cE '[:.]5300 ')"
put port5353_busy "$( { netstat -lnup 2>/dev/null || ss -lnup 2>/dev/null; } | grep -cE '[:.]5353 ')"

sec "DNS: Keenetic config (name-server / dns-proxy / dns-override lines only)"
echo "$RC" | grep -iE 'name-server|dns-proxy|dns-override|opkg dns|tls upstream|https upstream|dot|doh' | head -n 40
put dns_override "$(echo "$RC" | grep -ciE 'opkg dns-override')"
put dns_upstreams "$(echo "$RC" | grep -iE '^ *ip name-server' | awk '{print $3}' | tr '\n' ' ')"
put dns_proxy_encrypted "$(echo "$RC" | grep -ciE 'tls upstream|https upstream')"

sec "DNS: CLI syntax for ip name-server (help only, nothing is set)"
ndm 'help ip name-server' | head -n 20 || echo "(no help output)"

sec "DNS: resolver of the router itself"
cat /etc/resolv.conf 2>/dev/null
cat /opt/etc/resolv.conf 2>/dev/null | sed 's/^/opt: /'

# -------------------------------------------------------------------- kernel netfilter features
sec "netfilter: loaded matches / targets"
echo "matches: $(tr '\n' ' ' < /proc/net/ip_tables_matches 2>/dev/null)"
echo "targets: $(tr '\n' ' ' < /proc/net/ip_tables_targets 2>/dev/null)"
for f in set connbytes connmark mark conntrack statistic; do
    grep -qx "$f" /proc/net/ip_tables_matches 2>/dev/null && put "match_$f" 1 || put "match_$f" 0
done
for f in CONNMARK MARK NFQUEUE NFLOG REJECT; do
    grep -qx "$f" /proc/net/ip_tables_targets 2>/dev/null && put "target_$f" 1 || put "target_$f" 0
done

sec "netfilter: modules on disk (loaded or loadable)"
KDIR="/lib/modules/$(uname -r)"
for m in ip_set ip_set_hash_ip ip_set_hash_net xt_set xt_connbytes xt_connmark xt_NFQUEUE nfnetlink_queue xt_NFLOG nfnetlink_log nf_conntrack_netlink; do
    if lsmod 2>/dev/null | awk '{print $1}' | grep -qx "$m"; then s=loaded
    elif find "$KDIR" -name "$m.ko*" 2>/dev/null | grep -q .; then s=available
    else s=missing; fi
    echo "$m: $s"
    put "kmod_$m" "$s"
done

sec "ipset"
if have ipset; then
    ipset --version 2>/dev/null | head -n 1
    echo "sets: $(ipset list -n 2>/dev/null | tr '\n' ' ')"
    ipset help 2>/dev/null | grep -iE 'hash:ip |hash:net |timeout' | head -n 10
    put ipset 1
    put ipset_sets "$(ipset list -n 2>/dev/null | grep -c .)"
else
    echo "ipset not installed (opkg install ipset)"
    put ipset 0
fi

sec "conntrack"
put conntrack_count "$(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null)"
put conntrack_max "$(cat /proc/sys/net/netfilter/nf_conntrack_max 2>/dev/null)"
[ -r /proc/net/nf_conntrack ] && put conntrack_proc 1 || put conntrack_proc 0
echo "count/max: $(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null)/$(cat /proc/sys/net/netfilter/nf_conntrack_max 2>/dev/null)"

sec "iptables flavour"
iptables -V 2>/dev/null
put iptables "$(iptables -V 2>/dev/null)"

# -------------------------------------------------------------------- hardware offload
sec "offload (PPE / HW NAT / fastpath)"
echo "$RC" | grep -iE '^ *ppe|hw_nat|hwnat|fastnat|swnat|offload' | head -n 10
lsmod 2>/dev/null | grep -iE 'hw_nat|hwnat|fastnat|swnat|ppe|mtk_ppe|nf_flow' | awk '{print "module: "$1}'
put ppe "$(echo "$RC" | grep -iE '^ *ppe' | tr '\n' ';')"
put offload_modules "$(lsmod 2>/dev/null | grep -iE 'hw_nat|hwnat|fastnat|swnat|ppe|nf_flow' | awk '{print $1}' | tr '\n' ' ')"

# -------------------------------------------------------------------- IPv6
sec "IPv6"
ip -6 addr show scope global 2>/dev/null | grep -E 'inet6' | head -n 5
put ipv6_global "$(ip -6 addr show scope global 2>/dev/null | grep -c inet6)"
have ip6tables && put ip6tables 1 || put ip6tables 0

# -------------------------------------------------------------------- engines & neighbours
sec "Entware init scripts"
ls /opt/etc/init.d/ 2>/dev/null
put init_scripts "$(ls /opt/etc/init.d/ 2>/dev/null | tr '\n' ' ')"
for x in nfqws nfqws2 hydra hrneo xkeen xray sing-box AdGuardHome adguard dnsmasq stubby dnscrypt https-dns-proxy; do
    ls /opt/etc/init.d/ 2>/dev/null | grep -qi "$x" && put "has_$x" 1
done

sec "tunnel interfaces"
ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | grep -iE 'opkgtun|tun|wg|nwg|ovpn|ipsec|ppp' | tr '\n' ' '
echo
put tun_ifaces "$(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | grep -iE 'opkgtun|tun' | tr '\n' ' ')"

sec "netfilter hook dir"
ls -la /opt/etc/ndm/netfilter.d/ 2>/dev/null || echo "(none)"
put ndm_netfilter_hooks "$(ls /opt/etc/ndm/netfilter.d/ 2>/dev/null | tr '\n' ' ')"

sec "time"
date -u
put clock "$(date +%s)"

# -------------------------------------------------------------------- summary
printf '\n===== SUMMARY =====\n%s' "$SUM"
echo "===== END ====="
