#!/bin/sh
# E0c — read-only survey of Keenetic's built-in DNS-based routing
# ("маршрутизация по доменам": object-group fqdn + dns-proxy route), which nuxk
# plans to drive instead of running its own resolver. Runs ON the router.
#
#   from the Pi:  sh tools/e0/run.sh 192.168.1.1 222 keenetic-dnsrt.sh
#
# Prints structure, not secrets: group names and sizes, route lines, ipset
# headers, the DNSRT iptables rules, fwmark → table mapping. One group is
# shown in full (at most 15 lines) so the exact CLI syntax is on record.

export LC_ALL=C
have() { command -v "$1" >/dev/null 2>&1; }
sec() { printf '\n===== %s =====\n' "$1"; }
SUM=""
put() { SUM="$SUM$1 $2
"; }
ndm() { have ndmc && ndmc -c "$1" 2>/dev/null; }
RC=$(ndm 'show running-config')

# -------------------------------------------------------------------- firmware API
sec "API"
ndm 'show version' | grep -E '^ *(release|title|model):'
put keenetic_os "$(ndm 'show version' | sed -n 's/^ *release: *//p' | head -n 1)"
if have curl; then
    code=$(curl -s -o /dev/null -w '%{http_code}' -m 3 http://127.0.0.1:79/rci/show/version 2>/dev/null)
    echo "RCI http://127.0.0.1:79/rci/show/version -> ${code:-000}"
    put rci_http "${code:-000}"
else
    put rci_http no-curl
fi
put ndmc "$(have ndmc && echo 1 || echo 0)"

# -------------------------------------------------------------------- domain groups
sec "object-group fqdn: names and sizes"
echo "$RC" | awk '
    /^object-group fqdn / { name=$3; n[name]=0; order[++k]=name; ingroup=1; next }
    ingroup && /^ +include / { n[name]++; next }
    ingroup && /^ +exclude / { x[name]++; next }
    /^[^ ]/ { ingroup=0 }
    END { for (i=1;i<=k;i++) printf "%s include=%d exclude=%d\n", order[i], n[order[i]], x[order[i]] }'
put fqdn_groups "$(echo "$RC" | grep -c '^object-group fqdn ')"
put fqdn_entries_total "$(echo "$RC" | awk '/^object-group fqdn /{g=1;next} g&&/^ +include /{c++} /^[^ ]/{g=0} END{print c+0}')"

sec "object-group fqdn: one group in full (syntax reference)"
echo "$RC" | awk '/^object-group fqdn /{p=1} p{print; if (++n>=15) exit} p&&/^!/{exit}'

sec "SYNTAX (verbatim, for the agent generator)"
echo "$RC" | awk '/^object-group fqdn /{print; c=0; g=1; next} g&&/^ +include /&&c<2{print; c++} /^[^ ]/{g=0}' | head -n 12
echo "$RC" | grep -E '^ +route object-group|^ip route .*object-group' | head -n 10

sec "entry forms used (domain / wildcard / ip / cidr)"
echo "$RC" | awk '/^object-group fqdn /{g=1;next} g&&/^ +include /{print $2} /^[^ ]/{g=0}' |
    awk '{ if ($0 ~ /^[0-9.]+\/[0-9]+$/) c++; else if ($0 ~ /^[0-9.]+$/) ip++; else if ($0 ~ /^\*\./) w++; else d++ }
         END { printf "domain=%d wildcard=%d ip=%d cidr=%d\n", d, w, ip, c }'

# -------------------------------------------------------------------- routes
sec "dns-proxy routes (object-group -> interface)"
echo "$RC" | awk '/^dns-proxy/{p=1;print;next} p&&/^ /{ if ($0 ~ /route|object-group/) print; next } p&&/^[^ ]/{p=0}'
put dnsrt_routes "$(echo "$RC" | grep -cE '^ +route object-group')"
echo "$RC" | grep -E '^ip route .*object-group|^ ?route object-group' | head -n 20

sec "route target interfaces"
for i in $(echo "$RC" | grep -oE 'route object-group [^ ]+ [^ ]+' | awk '{print $4}' | sort -u); do
    echo "--- $i"
    ndm "show interface $i" | grep -E '^ *(id|index|type|description|state|link|connected|address|mtu|global|defaultgw):' | head -n 12
done

sec "OpkgTun interfaces (usque/xray targets)"
echo "$RC" | grep -E '^interface OpkgTun' || echo "(none in config)"
ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | grep -i opkgtun || echo "(no opkgtun link)"
put opkgtun_config "$(echo "$RC" | grep -cE '^interface OpkgTun')"

# -------------------------------------------------------------------- kernel side
sec "ipsets created by DNS routing (_NDM_OGDN_*: one per group, v4 and v6)"
for s in $(ipset list -n 2>/dev/null | grep -E '^_NDM_OGDN_'); do
    ipset list "$s" -t 2>/dev/null | awk -v s="$s" '
        /^Type:/{t=$2} /^Header:/{sub(/^Header: /,""); h=$0} /^Number of entries:/{e=$4}
        END { printf "%s type=%s entries=%s header=[%s]\n", s, t, e, h }'
done
put ogdn_ipsets "$(ipset list -n 2>/dev/null | grep -cE '^_NDM_OGDN_')"
put ogdn_ipsets_v4 "$(ipset list -n 2>/dev/null | grep -cE '^_NDM_OGDN_4_')"
put ogdn_ipsets_v6 "$(ipset list -n 2>/dev/null | grep -cE '^_NDM_OGDN_6_')"
echo "other ndm ipsets: $(ipset list -n 2>/dev/null | grep -iE 'ndm' | grep -vcE '^_NDM_OGDN_')"

sec "iptables: DNSRT/OGDN rules (v4)"
iptables-save 2>/dev/null | grep -iE 'DNSRT|OGDN' | head -n 60
sec "iptables: DNSRT/OGDN rules (v6)"
ip6tables-save 2>/dev/null | grep -iE 'DNSRT|OGDN' | head -n 30
put dnsrt_v4_rules "$(iptables-save 2>/dev/null | grep -iE 'DNSRT|OGDN' | grep -ciE 'MARK')"
# v6 chains may exist without MARK rules: then IPv6 to listed domains is NOT steered
put dnsrt_v6_mark_rules "$(ip6tables-save 2>/dev/null | grep -iE 'DNSRT|OGDN' | grep -ciE 'MARK')"

sec "where DNSRT sits in mangle PREROUTING (order matters for marks)"
iptables -t mangle -S PREROUTING 2>/dev/null | head -n 30

sec "fwmark -> table"
ip rule show 2>/dev/null | grep -E 'fwmark|lookup (4[0-9]{3}|16386)'
ip -6 rule show 2>/dev/null | grep -E 'fwmark' | sed 's/^/v6 /'
for t in $(ip rule show 2>/dev/null | grep -oE 'lookup [0-9]+' | awk '$2>=1000{print $2}' | sort -u); do
    echo "--- table $t"
    ip route show table "$t" 2>/dev/null | head -n 5
done

# -------------------------------------------------------------------- neighbour
sec "S99aiway-manager (what is it)"
[ -f /opt/etc/init.d/S99aiway-manager ] && grep -vE '^\s*$' /opt/etc/init.d/S99aiway-manager | head -n 25
for p in $(pgrep -f aiway 2>/dev/null | head -n 5); do
    tr '\0' ' ' </proc/"$p"/cmdline 2>/dev/null; echo
done

printf '\n===== SUMMARY =====\n%s' "$SUM"
echo "===== END ====="
