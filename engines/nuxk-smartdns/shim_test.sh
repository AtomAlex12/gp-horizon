#!/bin/sh
# Tests S53smartdns-nuxk against a fake smartdns under a temp root.
#   sh engines/nuxk-smartdns/shim_test.sh
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
SHIM="$HERE/S53smartdns-nuxk"
ROOT=$(mktemp -d)
export NUXK_ROOT="$ROOT"
export SMARTDNS_BIN="$ROOT/bin/smartdns"
export SMARTDNS_RUN="$ROOT/run"
export SMARTDNS_WAIT=3
trap 'sh "$SHIM" down >/dev/null 2>&1 || true; rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin"

# fake smartdns: -v prints its version; otherwise it daemonizes like the real
# one (the child writes the pid file); a config with "dies" makes it exit
# right after, "nostart" refuses at once
cat >"$SMARTDNS_BIN" <<'FAKE'
#!/bin/sh
[ "$1" = -v ] && { echo "smartdns 1.2026.08.05-0921"; exit 0; }
conf="" pid=""
while [ $# -gt 0 ]; do
    case "$1" in
    -c) conf=$2; shift ;;
    -p) pid=$2; shift ;;
    esac
    shift
done
grep -q nostart "$conf" && { echo "unknown option nostart"; exit 1; }
(
    trap 'rm -f "$pid"; exit 0' TERM INT
    echo $$ >/dev/null
    sh -c 'echo $PPID' >"$pid"
    grep -q dies "$conf" && { sleep 0.3; rm -f "$pid"; exit 1; }
    while :; do sleep 1; done
) </dev/null >/dev/null 2>&1 &
exit 0
FAKE
chmod +x "$SMARTDNS_BIN"

fail=0
check() { # check NAME COND...
    name=$1
    shift
    if "$@"; then echo "ok   $name"; else
        echo "FAIL $name"
        fail=1
    fi
}
running() { sh "$SHIM" info | grep -q '^service.running 1'; }
DIR="$ROOT/opt/etc/smartdns-nuxk"

# off: Entware's rc start at boot does nothing
sh "$SHIM" start
check "boot start while off: nothing" sh -c "! sh '$SHIM' info | grep -q '^service.running 1'"

# up without a configuration: a plain error
if sh "$SHIM" up 2>"$ROOT/err"; then check "up without config fails" false; else
    check "up without config fails" grep -q "нет конфигурации" "$ROOT/err"
fi

# set-config: written, started, on
printf 'bind 192.168.1.1:53053\nserver-https https://1.1.1.1/dns-query -interface opkgtun0\n' | sh "$SHIM" set-config >"$ROOT/out"
check "set-config applied" grep -qx applied "$ROOT/out"
check "running" running
check "on for the next boot" test -f "$DIR/on"
check "version" sh -c "sh '$SHIM' info | grep -qx 'version 1.2026.08.05-0921'"
check "audit file reported" sh -c "sh '$SHIM' info | grep -qx 'audit.file $ROOT/run/audit.log'"

# the same again: nothing restarted
pid1=$(cat "$ROOT/opt/var/run/smartdns-nuxk.pid")
printf 'bind 192.168.1.1:53053\nserver-https https://1.1.1.1/dns-query -interface opkgtun0\n' | sh "$SHIM" set-config >"$ROOT/out"
check "same config: unchanged" grep -qx unchanged "$ROOT/out"
check "same pid" test "$pid1" = "$(cat "$ROOT/opt/var/run/smartdns-nuxk.pid")"

# a configuration it dies with: the old one back, running
if printf 'bind 192.168.1.1:53053\ndies\n' | sh "$SHIM" set-config 2>"$ROOT/err"; then check "bad config refused" false; else
    check "bad config refused" grep -q "вернул прежнюю" "$ROOT/err"
fi
check "old config back" grep -q opkgtun0 "$DIR/smartdns.conf"
check "running again" running
if printf 'bind 192.168.1.1:53053\nnostart\n' | sh "$SHIM" set-config 2>"$ROOT/err"; then check "refused at start" false; else
    check "refused at start" grep -q "вернул прежнюю" "$ROOT/err"
fi
check "running after refusal" running

# shell in a configuration: refused before anything
if printf 'bind 1.2.3.4:53053\nconf-file $(reboot)\n' | sh "$SHIM" set-config 2>"$ROOT/err"; then check "shell refused" false; else
    check "shell refused" grep -q "недопустимые" "$ROOT/err"
fi
check "config untouched" grep -q opkgtun0 "$DIR/smartdns.conf"

# rc stop at shutdown keeps it on; the next boot starts it
sh "$SHIM" stop
check "stopped" sh -c "! sh '$SHIM' info | grep -q '^service.running 1'"
check "still on" test -f "$DIR/on"
sh "$SHIM" start
check "boot start while on" running

# down: off, and not at the next boot
sh "$SHIM" down
check "down stops" sh -c "! sh '$SHIM' info | grep -q '^service.running 1'"
check "down: off" test ! -f "$DIR/on"
sh "$SHIM" start
check "boot after down: nothing" sh -c "! sh '$SHIM' info | grep -q '^service.running 1'"

# up: on again with the kept configuration
sh "$SHIM" up
check "up" running

exit $fail
