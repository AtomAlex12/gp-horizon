#!/bin/sh
# Run the E0 read-only router survey from the Pi and save the report.
#
#   sh tools/e0/run.sh [router-ip] [ssh-port] [script]
#     defaults: 192.168.2.1 222 router-check.sh (E0); keenetic-dnsrt.sh = E0c
#
# Asks for the Entware root password (or uses your SSH key). The report lands
# in ./e0-report-<router>-<date>.txt — it contains the router's LAN layout,
# so keep it out of git (e0-report-*.txt is ignored).
set -eu
HOST="${1:-192.168.2.1}"
PORT="${2:-222}"
SCRIPT="${3:-router-check.sh}"
HERE=$(cd "$(dirname "$0")" && pwd)
[ -f "$HERE/$SCRIPT" ] || { echo "нет такого скрипта: $HERE/$SCRIPT" >&2; exit 1; }
OUT="e0-report-$HOST-${SCRIPT%.sh}-$(date +%Y%m%d-%H%M).txt"

echo "E0: $HOST:$PORT (Entware SSH, только чтение) → $OUT"
ssh -p "$PORT" -o ConnectTimeout=10 "root@$HOST" 'sh -s' <"$HERE/$SCRIPT" >"$OUT"
echo
sed -n '/===== SUMMARY =====/,/===== END =====/p' "$OUT"
echo
echo "Полный отчёт: $OUT"
