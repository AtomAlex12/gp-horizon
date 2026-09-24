#!/bin/sh
# Run the E0 read-only router survey from the Pi and save the report.
#
#   sh tools/e0/run.sh [router-ip] [ssh-port]      default 192.168.2.1 222
#
# Asks for the Entware root password (or uses your SSH key). The report lands
# in ./e0-report-<router>-<date>.txt — it contains the router's LAN layout,
# so keep it out of git (e0-report-*.txt is ignored).
set -eu
HOST="${1:-192.168.2.1}"
PORT="${2:-222}"
HERE=$(cd "$(dirname "$0")" && pwd)
OUT="e0-report-$HOST-$(date +%Y%m%d-%H%M).txt"

echo "E0: $HOST:$PORT (Entware SSH, только чтение) → $OUT"
ssh -p "$PORT" -o ConnectTimeout=10 "root@$HOST" 'sh -s' <"$HERE/router-check.sh" >"$OUT"
echo
sed -n '/===== SUMMARY =====/,/===== END =====/p' "$OUT"
echo
echo "Полный отчёт: $OUT"
