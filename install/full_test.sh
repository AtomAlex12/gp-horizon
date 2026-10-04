#!/bin/sh
# Tests nuxk-full.sh on a fake Pi: a fake docker whose `compose up` starts a
# stand-in controller (a python web server answering /ctl/v1/healthz) — except
# for the broken release 0.9.9 —, releases signed with a throwaway key and laid
# out like GitHub's, GitHub's release list from a local server. Needs curl,
# sha256sum, ssh-keygen (OpenSSH 8.1+) and python3.
#   sh install/full_test.sh
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
FULL="$HERE/nuxk-full.sh"
ROOT=$(mktemp -d)
command -v python3 >/dev/null 2>&1 || { echo "need python3"; exit 1; }
command -v ssh-keygen >/dev/null 2>&1 || { echo "need ssh-keygen"; exit 1; }
BIN="$ROOT/bin" REL="$ROOT/releases" API="$ROOT/api" WWW="$ROOT/www"
mkdir -p "$BIN" "$REL" "$API/repos/me/nuxk" "$WWW/ctl/v1" "$ROOT/home"
echo '{"status":"ok"}' >"$WWW/ctl/v1/healthz"

free_port() { python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'; }
PORT=$(free_port)
APIPORT=$(free_port)
(cd "$API" && exec python3 -m http.server "$APIPORT" --bind 127.0.0.1) >/dev/null 2>&1 &
APIPID=$!
trap 'kill $APIPID 2>/dev/null; [ -f "$ROOT/web.pid" ] && kill "$(cat "$ROOT/web.pid")" 2>/dev/null; rm -rf "$ROOT"' EXIT

ssh-keygen -q -t ed25519 -N "" -C "test release key" -f "$ROOT/key"
printf 'release@nuxk-horizon namespaces="nuxk-release" %s\n' "$(cut -d' ' -f1,2 "$ROOT/key.pub")" >"$ROOT/signers"

# release VERSION — v$VERSION in $REL, as the release workflow lays it out
release() {
    d="$REL/v$1"
    mkdir -p "$d"
    printf 'version %s\ncommit abc123\n' "$1" >"$d/BUILD"
    echo "sha256:$(echo "$1" | sha256sum | cut -d' ' -f1)" >"$d/controller-image"
    sed "s/^VERSION=\"@VERSION@\"/VERSION=\"$1\"/" "$FULL" >"$d/nuxk-full.sh"
    (cd "$d" && sha256sum BUILD controller-image nuxk-full.sh >SHA256SUMS)
    ssh-keygen -q -Y sign -f "$ROOT/key" -n nuxk-release "$d/SHA256SUMS" >/dev/null 2>&1
}

# fake docker: compose up stands in a controller — not for the broken 0.9.9
cat >"$BIN/docker" <<EOF
#!/bin/sh
case "\$1" in
info | ps) exit 0 ;;
version) echo 27.0.0; exit 0 ;;
logs) echo "panic: the stand-in doesn't start"; exit 0 ;;
compose)
    shift
    [ "\$1" = version ] && { echo 2.29.0; exit 0; }
    f=\$2; shift 2
    case "\$1" in
    pull) echo "pulled \$(sed -n 's/^ *image: //p' "\$f")" >>"$ROOT/docker.log" ;;
    up)
        [ -f "$ROOT/web.pid" ] && kill "\$(cat "$ROOT/web.pid")" 2>/dev/null
        rm -f "$ROOT/web.pid"
        sleep 1
        echo "up \$(sed -n 's/^ *image: //p' "\$f")" >>"$ROOT/docker.log"
        grep -q 'controller:0.9.9@' "\$f" && exit 0
        (cd "$WWW" && exec python3 -m http.server $PORT --bind 127.0.0.1) >/dev/null 2>&1 &
        echo \$! >"$ROOT/web.pid"
        ;;
    down) [ -f "$ROOT/web.pid" ] && kill "\$(cat "$ROOT/web.pid")" 2>/dev/null; rm -f "$ROOT/web.pid" ;;
    esac
    ;;
esac
exit 0
EOF
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH" HOME="$ROOT/home" NUXK_DIR="$ROOT/home/nuxk" NUXK_PORT=$PORT NUXK_REPO=me/nuxk
export NUXK_SIGNERS="$ROOT/signers" NUXK_RELEASES="file://$REL" NUXK_API="http://127.0.0.1:$APIPORT"
export NUXK_HELPER=0 NUXK_HEALTH_WAIT=8 NO_COLOR=1
D="$NUXK_DIR"

fail=0
check() { # check DESC ACTUAL EXPECTED
    if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got '$2', want '$3'"; fail=1; fi
}
has() { grep -c -- "$2" "$1" 2>/dev/null || true; }
image_of() { sed -n 's|^ *image: .*controller:\([^@]*\)@.*|\1|p' "$D/docker-compose.yml"; }
releases() { # releases TAG:prerelease … — GitHub's list
    printf '[' >"$API/repos/me/nuxk/releases"
    sep=""
    for r in "$@"; do
        printf '%s{"url":"x","author":{"login":"me","id":1},"tag_name":"%s","name":"%s","draft":%s,"prerelease":%s,"body":"a, b {c}"}' \
            "$sep" "${r%%:*}" "${r%%:*}" "$([ "${r#*:}" = draft ] && echo true || echo false)" "$([ "${r#*:}" = pre ] && echo true || echo false)" >>"$API/repos/me/nuxk/releases"
        sep=","
    done
    printf ']' >>"$API/repos/me/nuxk/releases"
}

# 1. a first install of a beta
release 0.5.0-beta.5
NUXK_VERSION=0.5.0-beta.5 sh "$FULL" install --yes --no-router >"$ROOT/out" 2>&1 || { cat "$ROOT/out"; exit 1; }
check "install: the image by digest" "$(image_of)" "0.5.0-beta.5"
check "install: the panel answers" "$(has "$ROOT/out" 'панель отвечает')" "1"
check "install: the update dir mounted" "$(has "$D/docker-compose.yml" './update:/var/lib/nuxk-update')" "1"
check "install: the inbox writable for the controller" "$(stat -c %a "$D/update/inbox")" "777"

# 2. update without a version: a beta takes the newest beta, drafts aside
releases v0.5.0:stable v0.6.0-beta.2:draft v0.6.0-beta.1:pre v0.5.0-beta.5:pre
release 0.6.0-beta.1
check "install: the script kept" "$([ -f "$D/nuxk-full.sh" ] && echo kept)" "kept"
sh "$D/nuxk-full.sh" update --yes --no-router >"$ROOT/out" 2>&1 || { cat "$ROOT/out"; exit 1; }
check "update: the newest beta" "$(image_of)" "0.6.0-beta.1"

# 3. nothing newer on the channel: nothing changes, never an older one
sh "$D/nuxk-full.sh" update --yes --no-router >"$ROOT/out" 2>&1 || { cat "$ROOT/out"; exit 1; }
check "update: nothing newer" "$(has "$ROOT/out" 'новее нет')/$(image_of)" "1/0.6.0-beta.1"

# 4. the panel's request: an older version or not a version — refused, said in status
printf 'version 0.5.0\nat 1\n' >"$D/update/inbox/request"
sh "$D/nuxk-full.sh" panel-update || true
check "panel: older refused" "$(has "$D/update/status" '^state failed')/$(has "$D/update/status" 'только новее')" "1/1"
check "panel: the request taken" "$([ -e "$D/update/inbox/request" ] && echo there || echo gone)" "gone"
printf 'version 1.0; rm -rf /\n' >"$D/update/inbox/request"
sh "$D/nuxk-full.sh" panel-update || true
check "panel: not a version refused" "$(has "$D/update/status" 'это не версия')/$(image_of)" "1/0.6.0-beta.1"
sh "$D/nuxk-full.sh" panel-update # no request: nothing
check "panel: no request, nothing" "$?" "0"

# 5. the panel asks for a release whose controller won't start: the old one back
release 0.9.9
printf 'version 0.9.9\nat 1\n' >"$D/update/inbox/request"
sh "$D/nuxk-full.sh" panel-update || true
check "panel: rolled back" "$(has "$D/update/status" '^state rolled_back')/$(image_of)" "1/0.6.0-beta.1"
check "panel: said which runs" "$(has "$D/update/status" 'работает прежний 0.6.0-beta.1')" "1"
check "panel: the output kept" "$([ "$(has "$D/update/update.log" 'не ответил')" -ge 1 ] && echo yes)" "yes"
check "panel: the old one answers" "$(curl -fsS -m 2 "http://127.0.0.1:$PORT/ctl/v1/healthz")" '{"status":"ok"}'

# 6. the panel asks for a good one
release 0.6.0
printf 'version 0.6.0\nat 1\n' >"$D/update/inbox/request"
sh "$D/nuxk-full.sh" panel-update || { cat "$D/update/update.log"; exit 1; }
check "panel: updated" "$(has "$D/update/status" '^state done')/$(image_of)" "1/0.6.0"
check "panel: from, to" "$(sed -n 's/^from //p;s/^to //p' "$D/update/status" | tr '\n' ' ')" "0.6.0-beta.1 0.6.0 "

[ "$fail" = 0 ] && echo "all full installer tests passed"
exit "$fail"
