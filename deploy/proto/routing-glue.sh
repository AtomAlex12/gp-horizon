#!/bin/sh
# routing-glue.sh — TEMPORARY stand-in for nuxk-plane (see nuxk-plane/README.md,
# which is still an empty, unvendored fork of HydraRoute Neo). Reproduces
# ONLY the interface-based fwmark -> table -> route mechanism from HydraRoute
# Neo (Neo/source/src/routing.c: drm_allocate_fwmark / drm_install_route),
# self-assigned, using the SAME locked mark/table scheme nuxk-plane/README.md
# defines (Keenetic PBR = low 28 bits / 0x0fffffff mask, nfqws2 = bits 29-30
# / 0x20000000+0x40000000, bit 28 free) so results/config transfer to the
# real vendored plane later.
#
# HydraRoute's domain/policy-name-based path (Keenetic RCI 127.0.0.1:79,
# "IP policy" objects) is NOT reproduced here — it doesn't exist off-router
# and isn't needed for this prototype's fixed DNS-via-WARP case.
#
# DO NOT grow this into a general domain/CIDR router — that is nuxk-plane's
# job (see deploy/proto plan, "Маршрутизирующая склейка").
set -e

DNS_MARK=0x10000000 # bit 28 — the one bit nuxk-plane/README.md leaves free
DNS_TABLE=100
DNS_RULE_PRIO=100
TUN_IFACE="${TUN_IFACE:-opkgtun0}"

apply_dns_warp() {
    # Locally-generated traffic (dnsmasq's own upstream queries), not transit
    # traffic — a plain OUTPUT/MARK is correct here; no CONNMARK restore
    # dance is needed (that's for forwarded LAN-client traffic, which
    # nfqws2/HydraRoute handle — not this case).
    for ip in 1.1.1.1 1.0.0.1; do
        for proto in udp tcp; do
            iptables -t mangle -C OUTPUT -p "$proto" -d "$ip" --dport 53 \
                -j MARK --set-mark "$DNS_MARK" 2>/dev/null ||
                iptables -t mangle -A OUTPUT -p "$proto" -d "$ip" --dport 53 \
                    -j MARK --set-mark "$DNS_MARK"
        done
    done
    ip rule show | grep -q "fwmark $DNS_MARK lookup $DNS_TABLE" ||
        ip rule add priority "$DNS_RULE_PRIO" fwmark "$DNS_MARK" table "$DNS_TABLE"
    ip route replace default dev "$TUN_IFACE" table "$DNS_TABLE"
    ip route flush cache 2>/dev/null || true
}

case "$1" in
apply-dns-warp) apply_dns_warp ;;
show)
    ip rule show
    echo ---
    ip route show table "$DNS_TABLE"
    echo ---
    iptables -t mangle -S OUTPUT
    ;;
*)
    echo "usage: $0 {apply-dns-warp|show}"
    exit 1
    ;;
esac
