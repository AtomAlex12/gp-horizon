#!/bin/sh
# deploy/proto entrypoint — process order mirrors Entware's own S51/S99 boot
# order (nfqws2 and usque are both real init.d scripts, same as on the
# router; nuxk-core is what would be S99nuxk-core). See deploy/proto plan,
# "Ключевое архитектурное решение".
set -e

NUXK_ENABLE_NFQWS2="${NUXK_ENABLE_NFQWS2:-1}"
NUXK_ENABLE_DNS_GLUE="${NUXK_ENABLE_DNS_GLUE:-1}"
NUXK_ENABLE_USQUE="${NUXK_ENABLE_USQUE:-1}"
log() { echo "[entrypoint] $*"; }

if [ "$NUXK_ENABLE_NFQWS2" = "1" ] && ! lsmod | grep -q '^nfnetlink_queue'; then
    log "FATAL: nfnetlink_queue not loaded in the HOST kernel."
    log "Run on the Pi HOST (not in this container) before 'docker compose up':"
    log "  sudo modprobe nfnetlink_queue xt_multiport xt_connbytes xt_NFQUEUE xt_CONNMARK xt_connmark nf_conntrack"
    exit 1
fi

if [ "$NUXK_ENABLE_NFQWS2" = "1" ]; then
    log "starting nfqws2 ..."
    /opt/etc/init.d/S51nfqws2-docker start
fi

if [ "$NUXK_ENABLE_USQUE" = "1" ]; then
    if [ ! -f /opt/etc/usque/session.conf ]; then
        log "FATAL: no /opt/etc/usque/session.conf — WARP device not registered."
        log "Run once, explicitly, and read the ToS prompt yourself:"
        log "  docker compose -f deploy/proto/docker-compose.yml run --rm \\"
        log "    --entrypoint /opt/etc/init.d/S51usque-docker nuxk register"
        exit 1
    fi
    log "starting usque ..."
    /opt/etc/init.d/S51usque-docker start
    log "waiting for opkgtun0 ..."
    i=0
    while ! ip link show opkgtun0 >/dev/null 2>&1; do
        i=$((i + 1))
        if [ "$i" -ge 30 ]; then
            log "FATAL: opkgtun0 did not come up in 30s"
            exit 1
        fi
        sleep 1
    done
fi

if [ "$NUXK_ENABLE_DNS_GLUE" = "1" ]; then
    log "applying routing glue ..."
    /opt/etc/nuxk/routing-glue.sh apply-dns-warp
    log "starting dnsmasq ..."
    dnsmasq --conf-file=/etc/dnsmasq.d/nuxk-warp.conf
fi

log "starting nuxk-core ..."
exec "$@"
