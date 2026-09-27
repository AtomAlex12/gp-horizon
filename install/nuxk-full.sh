#!/bin/sh
# nuxk Horizon — full: the controller on a Raspberry Pi (or any 64-bit Linux
# with Docker) plus nuxk on the router.
#
#   curl -fsSLo nuxk-full.sh https://github.com/AtomAlex12/nuxk-horizon/releases/latest/download/nuxk-full.sh
#   sh nuxk-full.sh
#
#   sh nuxk-full.sh            install or update the controller, then (asks) the router
#   sh nuxk-full.sh router     install or update nuxk on the router, over SSH
#   sh nuxk-full.sh status     sh nuxk-full.sh uninstall [--purge]
#   sh ~/nuxk/nuxk-full.sh update    the latest release (the script keeps a copy there)
#
# The controller is a prebuilt image from GitHub (ghcr.io), pulled by the
# digest the release lists — nothing is compiled here. The release's
# SHA256SUMS is checked against the nuxk Horizon release signature
# (ssh-keygen, the key below). The router gets nuxk-lite from the same
# release, checked against that SHA256SUMS, run on the router over SSH: its
# password goes to ssh only, never through this script.
#
# Options: --yes (defaults without questions), --no-router (skip the router).
# Env: NUXK_VERSION, NUXK_REPO, NUXK_DIR (default ~/nuxk), NUXK_PORT (4200),
#      NUXK_IMAGE (another image), NUXK_PULL=0 (a local image: don't pull),
#      NUXK_BASE_URL (a mirror of the release), NO_COLOR.

VERSION="@VERSION@" # stamped by the release; unstamped = the latest release
REPO="${NUXK_REPO:-AtomAlex12/nuxk-horizon}"
DIR="${NUXK_DIR:-$HOME/nuxk}"
PORT="${NUXK_PORT:-4200}"
OLD_PROJECT="${NUXK_OLD_PROJECT:-nuxk-pi}" # deploy/pi (building from source, for development)

# The release key: the release workflow signs SHA256SUMS with it; nuxk-core
# carries the same (nuxk-core/internal/release/allowed_signers — a test
# checks they match). NUXK_SIGNERS: another allowed_signers file (tests).
RELEASE_KEY='release@nuxk-horizon namespaces="nuxk-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOFUe/OQavfYPddqeudJtzQnJ5ndibBW9foQnKtSXo0P'

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    B=$(printf '\033[1m') D=$(printf '\033[2m') G=$(printf '\033[32m') Y=$(printf '\033[33m')
    E=$(printf '\033[31m') A=$(printf '\033[36m') N=$(printf '\033[0m')
else
    B="" D="" G="" Y="" E="" A="" N=""
fi
line() { printf '  %s────────────────────────────────────────────────────%s\n' "$D" "$N"; }
banner() {
    printf '\n  %s◆ nuxk Horizon%s  %sфул · %s%s\n' "$A$B" "$N" "$D" "$1" "$N"
    printf '    %sпанель на Raspberry Pi + агент на роутере Keenetic%s\n' "$D" "$N"
    line
}
section() { printf '\n  %s%s%s\n' "$B" "$1" "$N"; }
row() {
    case "$1" in
    ok) m="$G✓" ;; warn) m="$Y!" ;; bad) m="$E✗" ;; do) m="$A•" ;; *) m="$D·" ;;
    esac
    printf '    %s%s %s%s%s' "$m" "$N" "$B" "$2" "$N"
    [ -n "${3:-}" ] && printf '  %s%s%s' "$D" "$3" "$N"
    printf '\n'
}
ok() { printf '    %s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '    %s!%s %s\n' "$Y" "$N" "$*"; }
note() { printf '    %s%s%s\n' "$D" "$*" "$N"; }
STEP=0 STEPS=0
step() {
    STEP=$((STEP + 1))
    printf '\n  %s▸%s %s%s%s  %s[%s/%s]%s\n' "$A" "$N" "$B" "$1" "$N" "$D" "$STEP" "$STEPS" "$N"
}
TMP=""
cleanup() { [ -n "$TMP" ] && rm -rf "$TMP"; }
trap cleanup EXIT INT TERM
die() {
    printf '\n  %s✗ %s%s\n' "$E" "$*" "$N" >&2
    cleanup
    exit 1
}
YES=""
ask() { # ask QUESTION y|n
    if [ -n "$YES" ] || ! { [ -r /dev/tty ] && : </dev/tty; } 2>/dev/null; then
        [ "$2" = y ]
        return
    fi
    hint="[y/N]"
    [ "$2" = y ] && hint="[Y/n]"
    printf '\n  %s?%s %s %s%s%s ' "$A" "$N" "$1" "$D" "$hint" "$N"
    read -r ans </dev/tty || ans=""
    case "$ans" in
    [YyДд]*) return 0 ;;
    [NnНн]*) return 1 ;;
    *) [ "$2" = y ] ;;
    esac
}
# confirm QUESTION — an action asked for by name (uninstall): --yes confirms it
confirm() { [ -n "$YES" ] || ask "$1" n; }
input() { # input QUESTION DEFAULT → the answer on stdout
    if [ -n "$YES" ] || ! { [ -r /dev/tty ] && : </dev/tty; } 2>/dev/null; then
        echo "$2"
        return
    fi
    printf '  %s?%s %s %s[%s]%s ' "$A" "$N" "$1" "$D" "$2" "$N" >/dev/tty
    read -r v </dev/tty || v=""
    echo "${v:-$2}"
}
have() { command -v "$1" >/dev/null 2>&1; }
# run CMD — its output indented and dimmed; its exit status
run() {
    : >"$TMP/.rc"
    { sh -c "$1" 2>&1 || echo "$?" >"$TMP/.rc"; } | while IFS= read -r l; do printf '      %s│ %s%s\n' "$D" "$l" "$N"; done
    [ ! -s "$TMP/.rc" ]
}

base_url() {
    if [ -n "${NUXK_BASE_URL:-}" ]; then
        echo "$NUXK_BASE_URL"
    elif [ "$VERSION" = "@""VERSION@" ]; then
        echo "https://github.com/$REPO/releases/latest/download"
    else
        echo "https://github.com/$REPO/releases/download/v$VERSION"
    fi
}
get() { # get URL FILE
    curl -fsSL --retry 2 --connect-timeout 15 -o "$2" "$1" 2>"$TMP/.curl" ||
        die "не скачался $1: $(tr '\n' ' ' <"$TMP/.curl")"
}

sum_of() { awk -v f="$1" '$2 == f || $2 == "*" f { print $1 }' "$TMP/SHA256SUMS"; }

# fetch NAME — a release file into $TMP, checked against the signed SHA256SUMS
fetch() {
    get "$BASE/$1" "$TMP/$1"
    want=$(sum_of "$1")
    [ -n "$want" ] || die "$1 нет в SHA256SUMS релиза"
    [ "$(sha256sum "$TMP/$1" | cut -d' ' -f1)" = "$want" ] || die "хеш $1 не совпал с SHA256SUMS — файл не тот, что в релизе; ставить не буду"
}

# verify_sums — SHA256SUMS carries the nuxk Horizon release signature: checked
# with ssh-keygen (OpenSSH 8.1+) against RELEASE_KEY
SIG_NOTE=""
verify_sums() {
    v=$(ssh -V 2>&1 | sed -n 's/^OpenSSH_\([0-9]*\)\.\([0-9]*\).*/\1 \2/p')
    if ! have ssh-keygen || [ -z "$v" ] || [ "$(echo "$v" | awk '{ print ($1 * 100 + $2 >= 801) }')" != 1 ]; then
        SIG_NOTE="нет ssh-keygen из OpenSSH 8.1+ — подпись релиза не проверена, только хеши (sudo apt install -y openssh-client)"
        return 0
    fi
    curl -fsSL --retry 2 --connect-timeout 15 -o "$TMP/SHA256SUMS.sig" "$BASE/SHA256SUMS.sig" 2>/dev/null ||
        die "у релиза нет подписи (SHA256SUMS.sig) — ставить не буду"
    if [ -n "${NUXK_SIGNERS:-}" ]; then cp "$NUXK_SIGNERS" "$TMP/allowed_signers"; else printf '%s\n' "$RELEASE_KEY" >"$TMP/allowed_signers"; fi
    ssh-keygen -Y verify -f "$TMP/allowed_signers" -I release@nuxk-horizon -n nuxk-release \
        -s "$TMP/SHA256SUMS.sig" <"$TMP/SHA256SUMS" >"$TMP/.sig" 2>&1 ||
        die "SHA256SUMS релиза не подписан ключом nuxk Horizon — файлы не от проекта; ставить не буду ($(tr '\n' ' ' <"$TMP/.sig"))"
    SIG_NOTE="подпись релиза ✓ $(sed -n 's/.* key \(SHA256:[^ ]*\).*/\1/p' "$TMP/.sig")"
}

# release_version — the release's version from its BUILD (checked, so the
# signed one), and the controller image's digest it lists
IMAGE_DIGEST=""
release_version() {
    fetch BUILD
    rel=$(sed -n 's/^version //p' "$TMP/BUILD")
    [ -n "$rel" ] || die "не понял версию релиза"
    if [ "$VERSION" = "@""VERSION@" ]; then
        VERSION=$rel
    elif [ "$VERSION" != "$rel" ]; then
        die "в релизе $VERSION лежит версия $rel — не тот релиз; ставить не буду"
    fi
    if [ -n "$(sum_of controller-image)" ]; then
        fetch controller-image
        IMAGE_DIGEST=$(tr -d ' \r\n' <"$TMP/controller-image")
        case "$IMAGE_DIGEST" in sha256:*) ;; *) die "controller-image релиза — не отпечаток образа" ;; esac
    fi
}

# --- the host ------------------------------------------------------------------------

DOCKER=""
check_host() {
    section "Этот компьютер"
    case "$(uname -m)" in
    aarch64 | arm64) row ok "Архитектура" "$(uname -m)" ;;
    x86_64 | amd64) row ok "Архитектура" "$(uname -m)" ;;
    *) row bad "Архитектура" "$(uname -m) — нужен 64-битный Linux (Raspberry Pi 4/5 с 64-bit OS или x86_64)"; die "не тот компьютер" ;;
    esac
    have docker || {
        row bad "Docker" "не установлен"
        note "Raspberry Pi OS / Debian:  curl -fsSL https://get.docker.com | sh  и  sudo usermod -aG docker \$USER, затем перелогиньтесь"
        die "нужен Docker"
    }
    if docker info >/dev/null 2>&1; then
        DOCKER="docker"
    elif have sudo && sudo -n docker info >/dev/null 2>&1; then
        DOCKER="sudo docker"
    else
        row bad "Docker" "нет доступа у пользователя $(id -un)"
        note "sudo usermod -aG docker $(id -un), затем перелогиньтесь (или запустите через sudo)"
        die "нет доступа к Docker"
    fi
    $DOCKER compose version >/dev/null 2>&1 || die "нет docker compose v2: sudo apt install -y docker-compose-plugin"
    row ok "Docker" "$($DOCKER version --format '{{.Server.Version}}' 2>/dev/null) · compose $($DOCKER compose version --short 2>/dev/null)"
    IP=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')
    [ -n "$IP" ] || IP=$(hostname -I 2>/dev/null | awk '{ print $1 }')
    row ok "Адрес" "${IP:-?}"
}

# the image: by tag and by the digest the signed release lists — Docker
# refuses anything else under that tag
image() { echo "${NUXK_IMAGE:-ghcr.io/$(echo "$REPO" | cut -d/ -f1 | tr 'A-Z' 'a-z')/nuxk-horizon-controller:$VERSION${IMAGE_DIGEST:+@$IMAGE_DIGEST}}"; }

# keep_self — this script into $DIR: `sh ~/nuxk/nuxk-full.sh update` later
keep_self() {
    case "$0" in */nuxk-full.sh | nuxk-full.sh) ;; *) return 0 ;; esac
    [ -f "$0" ] && [ "$0" != "$DIR/nuxk-full.sh" ] && cp -f "$0" "$DIR/nuxk-full.sh" 2>/dev/null
    return 0
}

# self_update — `update`: the latest release's own script, once its SHA256SUMS
# signature and its hash check out here, installs that release
self_update() {
    VERSION="@""VERSION@"
    [ -n "${NUXK_VERSION:-}" ] && VERSION=${NUXK_VERSION#v}
    BASE=$(base_url)
    get "$BASE/SHA256SUMS" "$TMP/SHA256SUMS"
    verify_sums
    fetch nuxk-full.sh
    NUXK_SELF_UPDATED=1 exec sh "$TMP/nuxk-full.sh" install ${YES:+--yes} ${NO_ROUTER:+--no-router}
}

# --- the controller ------------------------------------------------------------------

write_compose() { # write_compose VOLUME-KEY
    mkdir -p "$DIR" || die "не создать $DIR"
    if [ "$1" = old ]; then
        vol="  controller-data:
    external: true
    name: ${OLD_PROJECT}_controller-data"
    else
        vol="  controller-data: {}"
    fi
    cat >"$DIR/docker-compose.yml" <<EOF
# nuxk Horizon — full, written by nuxk-full.sh $VERSION. Re-run it to update;
# edits here are overwritten then.
name: nuxk
services:
  controller:
    image: $(image)
    container_name: nuxk-controller
    ports: ["$PORT:4200"]
    volumes:
      - controller-data:/var/lib/nuxk-controller
    read_only: true
    tmpfs: [/run, /tmp]
    init: true   # reaps what plugins leave behind
    # supervise hands out less: the web/API runs as nobody with no rights, a
    # plugin gets only the ones in its plugin.json, inside this container
    cap_drop: [ALL]
    cap_add: [CHOWN, DAC_OVERRIDE, FOWNER, SETUID, SETGID, SETPCAP, KILL, NET_ADMIN, NET_RAW]
    security_opt: ["no-new-privileges:true"]
    restart: unless-stopped
volumes:
$vol
EOF
}

do_controller() {
    uses_old=""
    [ -f "$DIR/docker-compose.yml" ] && grep -q "name: ${OLD_PROJECT}_controller-data" "$DIR/docker-compose.yml" && uses_old=1
    old=$($DOCKER ps -a --filter "label=com.docker.compose.project=$OLD_PROJECT" --filter "label=com.docker.compose.service=controller" --format '{{.Names}}' 2>/dev/null | head -n 1)
    if [ -n "$old" ]; then
        warn "здесь уже работает контроллер из deploy/pi ($old) — сборка из исходников для разработки"
        if ask "Перенести его настройки (пароль admin, роутер, плагины) в эту установку и остановить старый?" y; then
            uses_old=1
            $DOCKER stop "$old" >/dev/null && $DOCKER rm "$old" >/dev/null || die "не остановился $old"
            ok "$old остановлен, его данные — том ${OLD_PROJECT}_controller-data — переходят сюда"
            note "остальное из deploy/pi не тронуто; не нужно — docker compose -f ~/nuxk-horizon/deploy/pi/docker-compose.yml down"
        else
            die "порт $PORT занят старым контроллером — остановите его или выберите NUXK_PORT"
        fi
    fi
    busy=$($DOCKER ps --format '{{.Names}} {{.Ports}}' | grep -E ":$PORT->" | grep -v '^nuxk-controller ' || true)
    [ -z "$busy" ] || die "порт $PORT занят: $busy (другой порт: NUXK_PORT=4201 sh nuxk-full.sh)"

    step "Контроллер $VERSION"
    write_compose "${uses_old:+old}"
    note "$DIR/docker-compose.yml · образ $(image)"
    if [ "${NUXK_PULL:-1}" != 0 ]; then
        run "$DOCKER compose -f '$DIR/docker-compose.yml' pull" ||
            die "образ не скачался — если репозиторий ещё приватный, сначала: docker login ghcr.io"
    fi
    run "$DOCKER compose -f '$DIR/docker-compose.yml' up -d" || die "контейнер не запустился — вывод выше"
    i=0
    while [ "$i" -lt 30 ]; do
        curl -fsS -m 2 "http://127.0.0.1:$PORT/ctl/v1/setup" >/dev/null 2>&1 && { ok "панель отвечает на :$PORT"; return 0; }
        sleep 1
        i=$((i + 1))
    done
    run "$DOCKER logs --tail 20 nuxk-controller"
    die "контроллер не ответил за 30 секунд — строки его журнала выше"
}

# --- the router ------------------------------------------------------------------------

do_router() {
    step "nuxk на роутере"
    have ssh || die "нет ssh-клиента: sudo apt install -y openssh-client"
    gw=$(ip -4 route show default 2>/dev/null | awk '{ print $3; exit }')
    host=$(input "Адрес роутера" "${gw:-192.168.1.1}")
    port=$(input "Порт SSH Entware (обычно 22 или 222)" "22")
    want=$(awk '$2 == "nuxk-lite.sh" || $2 == "*nuxk-lite.sh" { print $1 }' "$TMP/SHA256SUMS")
    [ -n "$want" ] || die "в релизе нет nuxk-lite.sh"
    note "ssh root@$host -p $port — пароль Entware вводится в ssh, этот скрипт его не видит"
    # on the router: curl, the lite script from this very release, its hash,
    # then the script itself — interactive, so its questions reach you
    ssh -t -p "$port" -o StrictHostKeyChecking=accept-new "root@$host" \
        "opkg update >/dev/null 2>&1; opkg install curl ca-certificates >/dev/null 2>&1; mkdir -p /opt/tmp &&
         curl -fsSLo /opt/tmp/nuxk-lite.sh '$BASE/nuxk-lite.sh' &&
         [ \"\$(sha256sum /opt/tmp/nuxk-lite.sh | cut -d' ' -f1)\" = '$want' ] || { echo 'nuxk-lite.sh: хеш не совпал'; exit 1; };
         sh /opt/tmp/nuxk-lite.sh; rc=\$?; rm -f /opt/tmp/nuxk-lite.sh; exit \$rc" ||
        die "установка на роутере не завершилась — вывод выше; повторить: sh nuxk-full.sh router"
    ROUTER=$host
}

finish() {
    printf '\n'
    line
    printf '  %s✓ nuxk Horizon %s%s\n' "$G$B" "$VERSION" "$N"
    printf '    %sПанель%s   http://%s:%s\n' "$B" "$N" "${IP:-<адрес Pi>}" "$PORT"
    printf '    %sДальше%s   мастер в панели: пароль admin → роутер%s (root и пароль Entware)\n' "$B" "$N" "${ROUTER:+ $ROUTER}"
    printf '    %sКоманды%s  sh %s/nuxk-full.sh update · router · status · uninstall\n' "$B" "$N" "$DIR"
    note "Обновление: sh $DIR/nuxk-full.sh update — новый образ контроллера, затем (спросит) роутер."
    note "Роутер обновляется и сам: кнопкой в панели («Система» → «Обновления») или nuxk update по SSH."
    printf '\n'
}

# --- modes -----------------------------------------------------------------------------

MODE=install PURGE="" NO_ROUTER=""
for a in "$@"; do
    case "$a" in
    --yes | -y) YES=1 ;;
    --purge) PURGE=1 ;;
    --no-router) NO_ROUTER=1 ;;
    install | update | router | status | uninstall) MODE=$a ;;
    -h | --help)
        sed -n '2,24p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'
        exit 0
        ;;
    *) die "не понимаю «$a»: sh nuxk-full.sh [install|update|router|status|uninstall] [--yes]" ;;
    esac
done

have curl || die "нет curl: sudo apt install -y curl"
TMP=$(mktemp -d 2>/dev/null) || die "не создать временный каталог"
[ "$MODE" = update ] && [ -z "${NUXK_SELF_UPDATED:-}" ] && self_update
[ -n "${NUXK_VERSION:-}" ] && VERSION=${NUXK_VERSION#v}
BASE=$(base_url)
if [ "$MODE" = install ] || [ "$MODE" = router ]; then
    get "$BASE/SHA256SUMS" "$TMP/SHA256SUMS"
    verify_sums
    release_version
fi
[ "$VERSION" = "@""VERSION@" ] && VERSION="последняя"
banner "$VERSION"
[ -n "$SIG_NOTE" ] && note "$SIG_NOTE"

case "$MODE" in
install)
    check_host
    STEPS=2
    do_controller
    keep_self
    if [ -z "$NO_ROUTER" ] && ask "Поставить или обновить nuxk на роутере сейчас (по SSH)?" y; then
        do_router
    else
        note "позже: sh nuxk-full.sh router — или на роутере: nuxk update"
    fi
    finish
    ;;
router)
    check_host
    STEPS=1
    do_router
    finish
    ;;
status)
    check_host
    section "nuxk"
    if $DOCKER ps --format '{{.Names}}' | grep -qx nuxk-controller; then
        row ok "Контроллер" "$($DOCKER inspect --format '{{.Config.Image}}' nuxk-controller) · http://${IP:-?}:$PORT"
    else
        row warn "Контроллер" "не запущен: sh nuxk-full.sh"
    fi
    printf '\n'
    ;;
uninstall)
    check_host
    [ -f "$DIR/docker-compose.yml" ] || die "в $DIR нет установки"
    confirm "Удалить контроллер${PURGE:+ вместе с его данными (пароль, роутер, плагины)}? На роутере nuxk останется (удаление там: nuxk uninstall)." || die "отменено"
    ext=$(sed -n 's/^    name: \(.*_controller-data\)$/\1/p' "$DIR/docker-compose.yml")
    run "$DOCKER compose -f '$DIR/docker-compose.yml' down ${PURGE:+-v}" || die "не удалось остановить — вывод выше"
    [ -n "$PURGE" ] && rm -rf "$DIR"
    if [ -n "$PURGE" ] && [ -n "$ext" ]; then
        ok "контроллер удалён; его данные — перенесённый том $ext — оставлены: docker volume rm $ext"
    else
        ok "контроллер удалён${PURGE:+ с данными}"
    fi
    ;;
esac
