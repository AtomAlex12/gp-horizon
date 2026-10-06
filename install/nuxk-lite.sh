#!/bin/sh
# nuxk Horizon — lite: installs nuxk on a Keenetic router, in Entware.
#
#   opkg update && opkg install curl ca-certificates
#   curl -fsSLo /opt/tmp/nuxk-lite.sh https://github.com/AtomAlex12/nuxk-horizon/releases/latest/download/nuxk-lite.sh
#   sh /opt/tmp/nuxk-lite.sh
#
# It stays on the router as `nuxk`:
#   nuxk              state of nuxk and its engines
#   nuxk update       the latest release: agent, web, adapters (and xray);
#                     the old version back by itself if the new one won't start
#   nuxk rollback     the version before the last update, back
#   nuxk warp         add WARP (usque)       nuxk vless   add VLESS (xray)
#   nuxk dns          add SmartDNS (beta): the panel's «DNS» can use it instead
#                     of nuxk's own DNS; nuxk nfqws2 — add nfqws2-keenetic
#   nuxk uninstall    remove nuxk; the router's own settings stay
#
# Everything comes prebuilt from the GitHub release and is checked against
# its SHA256SUMS, and SHA256SUMS against the release signature — by the agent
# already on the router; xray comes from XTLS's own release and SmartDNS from
# pymumu's, each pinned by hash below. Nothing is compiled here.
#
# Options: --yes (defaults without questions), --with-warp, --with-vless.
# Env: NUXK_VERSION, NUXK_REPO, NUXK_BASE_URL (a mirror of the release),
#      NO_COLOR; NUXK_ROOT prefixes every path (tests). The panel sets
#      NUXK_STATUS (progress for it), NUXK_FROM, NUXK_STARTED, NUXK_TASK.

VERSION="@VERSION@" # stamped by the release; unstamped = the latest release
REPO="${NUXK_REPO:-AtomAlex12/nuxk-horizon}"
R="${NUXK_ROOT:-}"

# xray: the official XTLS release, pinned by version and by each archive's
# SHA-256 (from its .dgst). MIPS routers take the soft-float build: most
# have no FPU (nuxk-core is built the same way). Since 26.3.23 XTLS builds
# with Go 1.26, whose runtime dies at start on the MIPS Keenetics' 3.4
# kernel (futexwakeup returned -89, ENOSYS): MIPS stays on 26.2.6 (Go 1.25),
# checked on a live mipsel router.
xray_asset() { # arch → "zip binary sha256 version"
    case "$1" in
    aarch64) echo "Xray-linux-arm64-v8a.zip xray 4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c 26.3.27" ;;
    mips) echo "Xray-linux-mips32.zip xray_softfloat 76a40ab665db109dad408abcdd01d8a6dabeaaac97f1b74d9f3b09429ff0bb30 26.2.6" ;;
    mipsel) echo "Xray-linux-mips32le.zip xray_softfloat 1590b2bcefe64fb0604f29c8c75219bfbe8f63daa3bc8e3956edfa6da97c5869 26.2.6" ;;
    x86_64) echo "Xray-linux-64.zip xray 23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae 26.3.27" ;;
    esac
}
xray_version() { set -- $(xray_asset "$1"); echo "${4:-}"; }
XRAY_VERSION="" # the one for this router's arch, set by survey
XRAY_BASE_URL="${XRAY_BASE_URL:-https://github.com/XTLS/Xray-core/releases/download}"
XRAY_MARK="nuxk-vless" # description of the OpkgTun nuxk creates

# SmartDNS (beta): the official release (pymumu/smartdns), its static binary
# per router arch, pinned by SHA-256. "none": no pinned build — not installed.
SMARTDNS_VERSION="Release48.4"
smartdns_asset() { # arch → "file sha256"
    case "$1" in
    aarch64) echo "smartdns-aarch64 1208c5562046c71913c3df88ad4e3ed0a3410161ff77ce4a60cdd63936bf1b27" ;;
    mips) echo "smartdns-mips 47ccd57e43fd8fe68e9b5a65cac865dff0ca8d4f1a09ab5d3a9b744190036b3d" ;;
    mipsel) echo "smartdns-mipsel 02a3390b43af092f6bbf9312988d5f5094858e24f08bca70fab63eec11f90d11" ;;
    x86_64) echo "smartdns-x86_64 none" ;;
    esac
}
SMARTDNS_BASE_URL="${SMARTDNS_BASE_URL:-https://github.com/pymumu/smartdns/releases/download}"

# router paths
P_BIN=/opt/usr/bin/nuxk-core
P_INIT=/opt/etc/init.d/S99nuxk-core
P_SHIM=/opt/etc/nuxk/engines/S51nfqws2-nuxk
P_CONF=/opt/etc/nuxk/nuxk.conf
P_PLANE=/opt/etc/nuxk/plane.json
P_WEB=/opt/share/www/nuxk
P_LOG=/opt/var/log/nuxk-core.log
P_SELF=/opt/bin/nuxk
P_NFQ_INIT=/opt/etc/init.d/S51nfqws2
P_NFQ_LIST=/opt/etc/nfqws2/lists/user.list
P_NFQ_FEED=/opt/etc/opkg/nfqws2-keenetic.conf
P_USQUE=/opt/etc/init.d/S51usque
P_XRAY=/opt/sbin/xray
P_XRAY_INIT=/opt/etc/init.d/S52xray-nuxk
P_SMARTDNS=/opt/sbin/smartdns
P_SMARTDNS_INIT=/opt/etc/init.d/S53smartdns-nuxk
P_SMARTDNS_DIR=/opt/etc/smartdns-nuxk
P_PREV=/opt/var/lib/nuxk/prev # the version before the last update: agent, init, adapter, web
NFQ_FEED="src/gz nfqws2-keenetic https://nfqws.github.io/nfqws2-keenetic/all"
WARP_HOSTS="cloudflareclient.com" # desynced before usque registers (plane.WarpDomains)
DEPS="curl ca-certificates ipset"
MIN_FREE_KB=20480  # agent (~8 MB), web and headroom
XRAY_FREE_KB=71680 # the archive (≤29 MB) and the binary (≤38 MB) at once

# --- look ------------------------------------------------------------------------------

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    B=$(printf '\033[1m') D=$(printf '\033[2m') G=$(printf '\033[32m') Y=$(printf '\033[33m')
    E=$(printf '\033[31m') A=$(printf '\033[36m') N=$(printf '\033[0m')
else
    B="" D="" G="" Y="" E="" A="" N=""
fi
line() { printf '  %s────────────────────────────────────────────────────%s\n' "$D" "$N"; }
banner() {
    printf '\n  %s◆ nuxk Horizon%s  %sлайт · %s%s\n' "$A$B" "$N" "$D" "$1" "$N"
    printf '    %sобход блокировок на роутере Keenetic%s\n' "$D" "$N"
    line
}
section() { printf '\n  %s%s%s\n' "$B" "$1" "$N"; }
row() { # row ok|warn|bad|do|opt|skip NAME [DETAIL]
    case "$1" in
    ok) m="$G✓" ;; warn) m="$Y!" ;; bad) m="$E✗" ;; do) m="$A•" ;; opt) m="$D○" ;; *) m="$D·" ;;
    esac
    printf '    %s%s %s%s%s' "$m" "$N" "$B" "$2" "$N"
    [ -n "${3:-}" ] && printf '  %s%s%s' "$D" "$3" "$N"
    printf '\n'
}
say() { printf '    %s\n' "$*"; }
note() { printf '    %s%s%s\n' "$D" "$*" "$N"; }
ok() { printf '    %s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '    %s!%s %s\n' "$Y" "$N" "$*"; }
STEP=0 STEPS=0
step() {
    STEP=$((STEP + 1))
    printf '\n  %s▸%s %s%s%s  %s[%s/%s]%s\n' "$A" "$N" "$B" "$1" "$N" "$D" "$STEP" "$STEPS" "$N"
}
die() {
    printf '\n  %s✗ %s%s\n' "$E" "$*" "$N" >&2
    report "${FAIL_STATE:-failed}" "$*"
    cleanup
    exit 1
}

# report STATE MESSAGE — progress for the panel, when it started this update
# (NUXK_STATUS): the agent restarts on the way and reads the file back
report() {
    [ -n "${NUXK_STATUS:-}" ] || return 0
    {
        echo "state $1"
        echo "from ${NUXK_FROM:-${CORE_VER:-}}"
        echo "to ${NUXK_VERSION:-$VERSION}"
        echo "pid $$"
        echo "started ${NUXK_STARTED:-}"
        [ -n "${NUXK_TASK:-}" ] && echo "task $NUXK_TASK"
        echo "at $(date +%s)"
        echo "message $(echo "$2" | tr '\n' ' ')"
    } >"$NUXK_STATUS.tmp" 2>/dev/null && mv -f "$NUXK_STATUS.tmp" "$NUXK_STATUS"
}

YES=""
# ask QUESTION y|n — the answer; without a terminal (or with --yes), the default
ask() {
    if [ -n "$YES" ] || ! { [ -r /dev/tty ] && : </dev/tty; } 2>/dev/null; then
        [ "$2" = y ]
        return
    fi
    hint="[y/N]"
    [ "$2" = y ] && hint="[Y/n]"
    drain
    printf '\n  %s?%s %s %s%s%s ' "$A" "$N" "$1" "$D" "$hint" "$N"
    read -r ans </dev/tty || ans=""
    case "$ans" in
    [YyДд]*) return 0 ;;
    [NnНн]*) return 1 ;;
    *) [ "$2" = y ] ;;
    esac
}

# drain — drops what was typed before a question was asked: a blank line
# pasted along with the command (or a CRLF line end turned into two) would
# otherwise answer it with the default. Without stty: nothing to do.
drain() {
    command -v stty >/dev/null 2>&1 || return 0
    tty_mode=$(stty -g </dev/tty 2>/dev/null) || return 0
    stty -icanon min 0 time 0 </dev/tty 2>/dev/null || return 0
    dd bs=512 count=16 </dev/tty >/dev/null 2>&1
    stty "$tty_mode" </dev/tty 2>/dev/null || stty sane </dev/tty 2>/dev/null
    return 0
}

# confirm QUESTION — for an action the person asked for by name (nuxk warp,
# nuxk uninstall): --yes confirms it, otherwise ask, "no" by default
confirm() { [ -n "$YES" ] || ask "$1" n; }

# run CMD — its output indented and dimmed; its exit status
run() {
    : >"$TMP/.rc"
    { sh -c "$1" 2>&1 || echo "$?" >"$TMP/.rc"; } | while IFS= read -r l; do printf '      %s│ %s%s\n' "$D" "$l" "$N"; done
    [ ! -s "$TMP/.rc" ]
}

# --- downloads -------------------------------------------------------------------------

TMP=""
cleanup() {
    [ -n "$TMP" ] && rm -rf "$TMP"
    # after `nuxk update` handed over to the new release's script: its folder
    case "${NUXK_OLD_TMP:-}" in */nuxk-setup.*) rm -rf "$NUXK_OLD_TMP" ;; esac
}
trap cleanup EXIT INT TERM

base_url() {
    if [ -n "${NUXK_BASE_URL:-}" ]; then
        echo "$NUXK_BASE_URL"
    elif [ "$VERSION" = "@""VERSION@" ]; then
        echo "https://github.com/$REPO/releases/latest/download"
    else
        echo "https://github.com/$REPO/releases/download/v$VERSION"
    fi
}

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        openssl dgst -sha256 "$1" 2>/dev/null | awk '{print $NF}'
    fi
}

size_mb() { awk -v b="$(wc -c <"$1")" 'BEGIN { printf "%.1f МБ", b / 1048576 }'; }

get() { # get URL FILE
    curl -fsSL --retry 2 --connect-timeout 15 --max-time 600 -o "$2" "$1" 2>"$TMP/.curl" && return 0
    err=$(tr '\n' ' ' <"$TMP/.curl")
    case "$err" in *certificate* | *SSL*) err="$err — проверьте время на роутере (NTP) и пакет ca-certificates" ;; esac
    die "не скачался $1: $err"
}

# fetch NAME — a release file into $TMP, checked against its SHA256SUMS
# (once: what prefetch already checked isn't downloaded again)
fetch() {
    [ -f "$TMP/.ok.$1" ] && return 0
    get "$BASE/$1" "$TMP/$1"
    sum_ok "$1"
    note "↓ $1 · $(size_mb "$TMP/$1") · sha256 ✓"
}

sum_ok() { # sum_ok NAME — $TMP/NAME is the release's, or the run stops
    want=$(awk -v f="$1" '$2 == f || $2 == "*" f { print $1 }' "$TMP/SHA256SUMS")
    [ -n "$want" ] || die "$1 нет в SHA256SUMS релиза"
    [ "$(sha256 "$TMP/$1")" = "$want" ] || die "хеш $1 не совпал с SHA256SUMS — файл не тот, что в релизе; ставить не буду"
    : >"$TMP/.ok.$1"
}

# verify_sums — SHA256SUMS carries the nuxk Horizon release signature
# (SHA256SUMS.sig, made by the release workflow). The agent already on the
# router checks it: a newly downloaded one can't vouch for itself. A first
# install has no agent yet — its files are checked against SHA256SUMS only,
# as they come over HTTPS from GitHub.
SIG_NOTE=""
verify_sums() {
    if [ ! -x "$R$P_BIN" ]; then
        SIG_NOTE="подпись релиза проверит уже установленный агент при следующих обновлениях"
        return 0
    fi
    # a missing .sig isn't a download error: the agent refuses it
    curl -fsSL --retry 2 --connect-timeout 15 --max-time 60 -o "$TMP/SHA256SUMS.sig" "$BASE/SHA256SUMS.sig" 2>/dev/null || rm -f "$TMP/SHA256SUMS.sig"
    out=$("$R$P_BIN" -verify "$TMP/SHA256SUMS" 2>&1)
    case $? in
    0) SIG_NOTE="подпись релиза ✓ ${out##* }" ;;
    2) SIG_NOTE="установленный агент старше проверки подписей — сверяю хеши; со следующего обновления подпись обязательна" ;;
    *) die "SHA256SUMS релиза не подписан ключом nuxk Horizon ($out) — файлы не от проекта; ставить не буду" ;;
    esac
}

# release_version — the release's own version, from its BUILD (checked like
# any file, so it's the signed one): a tag that points at other files stops here
release_version() {
    get "$BASE/BUILD" "$TMP/BUILD"
    sum_ok BUILD
    rel=$(sed -n 's/^version //p' "$TMP/BUILD")
    [ -n "$rel" ] || die "не понял версию релиза"
    if [ "$VERSION" = "@""VERSION@" ]; then
        VERSION=$rel
    elif [ "$VERSION" != "$rel" ]; then
        die "в релизе $VERSION лежит версия $rel — не тот релиз; ставить не буду"
    fi
}

# newer A B — version A is later than B (a release is later than its pre-releases)
newer() {
    awk -v a="$1" -v b="$2" '
    function key(v,   i, n, p, c, x, s) {
        sub(/^v/, "", v); p = ""
        i = index(v, "-"); if (i) { p = substr(v, i + 1); v = substr(v, 1, i - 1) }
        split(v, c, "."); s = sprintf("%09d.%09d.%09d", c[1], c[2], c[3])
        if (p == "") return s "~"
        n = split(p, x, "."); s = s "-"
        for (i = 1; i <= n; i++) s = s (x[i] ~ /^[0-9]+$/ ? sprintf("0%09d", x[i]) : "1" x[i]) "."
        return s
    }
    BEGIN { exit !(key(a) > key(b)) }'
}

# prefetch NAME… — every release file a run needs, downloaded and checked
# before the router is touched: a file that isn't the release's stops the
# run with nothing changed
prefetch() {
    [ $# -gt 0 ] || return 0
    section "Загрузка"
    for n in "$@"; do fetch "$n"; done
}

# domains N — "1 домен", "3 домена", "5 доменов"
domains() {
    n=$1
    if [ $((n % 10)) = 1 ] && [ $((n % 100)) != 11 ]; then w=домен
    elif [ $((n % 10)) -ge 2 ] && [ $((n % 10)) -le 4 ] && { [ $((n % 100)) -lt 12 ] || [ $((n % 100)) -gt 14 ]; }; then w=домена
    else w=доменов; fi
    echo "$n $w"
}

in_release() { awk -v f="$1" '$2 == f || $2 == "*" f { found = 1 } END { exit !found }' "$TMP/SHA256SUMS"; }

# --- the router, read only -------------------------------------------------------------

have() { command -v "$1" >/dev/null 2>&1; }
pkg_ver() { opkg list-installed "$1" 2>/dev/null | awk -v p="$1" '$1 == p { print $3 }'; }
conf_get() { sed -n "s/^$1=[\"']\{0,1\}\([^\"']*\)[\"']\{0,1\}\$/\1/p" "$R$P_CONF" 2>/dev/null | tail -n 1; }

survey() {
    ENTWARE=0
    have opkg && ENTWARE=1
    ARCH_RAW=$(opkg print-architecture 2>/dev/null | awk '$2 != "all" && $2 != "noarch" { if ($3 + 0 >= p) { p = $3 + 0; a = $2 } } END { print a }')
    [ -n "$ARCH_RAW" ] || ARCH_RAW=$(uname -m 2>/dev/null)
    case "$ARCH_RAW" in
    *aarch64* | *arm64*) ARCH=aarch64 ;;
    *mips64*) ARCH="" ;;
    *mipsel* | *mipsle*) ARCH=mipsel ;;
    mips)
        # uname says "mips" on little-endian MT7621 too: ELF EI_DATA, 1 = LE.
        # dd, not od: KeeneticOS's BusyBox od has no -A/-t/-j/-N
        case "$(dd if=/bin/busybox bs=1 skip=5 count=1 2>/dev/null | tr '\001\002' 'lb')" in
        l) ARCH=mipsel ;; b) ARCH=mips ;; *) ARCH="" ;;
        esac
        ;;
    *mips*) ARCH=mips ;;
    *x86_64* | *x64* | *amd64*) ARCH=x86_64 ;;
    *) ARCH="" ;;
    esac
    XRAY_VERSION=$(xray_version "$ARCH")
    KOS="" MODEL=""
    if have ndmc; then
        KOS=$(ndmc -c 'show version' 2>/dev/null | sed -n 's/^ *release: *//p' | head -n 1)
        MODEL=$(ndmc -c 'show version' 2>/dev/null | sed -n 's/^ *model: *//p' | head -n 1)
    fi
    FREE_KB=$(df -k "$R/opt" 2>/dev/null | awk 'NR == 2 { print $4 }')
    MEM_KB=$(awk '/MemAvailable/ { print $2 }' /proc/meminfo 2>/dev/null)
    LAN_IP=$(ip -4 -o addr show br0 2>/dev/null | awk '{ sub(/\/.*/, "", $4); print $4; exit }')
    MISSING_DEPS=""
    for p in $DEPS; do [ -n "$(pkg_ver "$p")" ] || MISSING_DEPS="$MISSING_DEPS $p"; done
    NFQ_VER=$(pkg_ver nfqws2-keenetic)
    MISSING_KMODS=""
    for m in nfnetlink_queue xt_NFQUEUE xt_connbytes xt_multiport; do
        lsmod 2>/dev/null | awk '{ print $1 }' | grep -qx "$m" && continue
        find "/lib/modules/$(uname -r)" -name "$m.ko*" 2>/dev/null | grep -q . && continue
        MISSING_KMODS="$MISSING_KMODS $m"
    done
    CORE_VER=""
    [ -x "$R$P_BIN" ] && CORE_VER=$("$R$P_BIN" -version 2>/dev/null | awk '{ print $2 }')
    HAVE_CONF=0
    [ -f "$R$P_CONF" ] && HAVE_CONF=1
    USQUE_READY=0
    [ -x "$R$P_USQUE" ] && "$R$P_USQUE" info 2>/dev/null | grep -q '^service.running ' && USQUE_READY=1
    USQUE_IFACE=$(sed -n 's/^IFACE="\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$R/opt/etc/usque/usque.conf" 2>/dev/null | tail -n 1 | sed 's/^opkg/Opkg/; s/tun/Tun/')
    XRAY_VER=""
    [ -x "$R$P_XRAY" ] && XRAY_VER=$("$R$P_XRAY" version 2>/dev/null | sed -n '1s/^Xray \([^ ]*\).*/\1/p')
    XRAY_READY=0
    [ -n "$XRAY_VER" ] && [ -x "$R$P_XRAY_INIT" ] && XRAY_READY=1
    XRAY_IFACE=""
    SMARTDNS_VER=""
    [ -x "$R$P_SMARTDNS" ] && [ -x "$R$P_SMARTDNS_INIT" ] && SMARTDNS_VER=$(cat "$R$P_SMARTDNS_DIR/.release" 2>/dev/null)
    [ -x "$R$P_SMARTDNS" ] && [ -x "$R$P_SMARTDNS_INIT" ] && [ -z "$SMARTDNS_VER" ] && SMARTDNS_VER="?"
}

# smartdns_ok — a pinned SmartDNS build for this arch (tests bring their own)
smartdns_ok() {
    set -- $(smartdns_asset "$ARCH")
    [ -n "${1:-}" ] || return 1
    [ "${2:-none}" != none ] && return 0
    [ -n "$R" ] && [ -n "${SMARTDNS_TEST_SUM:-}" ]
}

# xray_iface — the OpkgTun for VLESS: the one nuxk made before (description
# nuxk-vless), else the first free one from OpkgTun1 — never someone else's,
# never usque's. Empty = none free.
xray_iface() {
    have ndmc || { echo OpkgTun1; return; }
    free=""
    for n in 0 1 2 3 4 5 6 7 8 9; do
        i="OpkgTun$n"
        if out=$(ndmc -c "show interface $i" 2>/dev/null); then
            echo "$out" | grep -q "description: *$XRAY_MARK *\$" && { echo "$i"; return; }
        elif [ "$n" -gt 0 ] && [ -z "$free" ] && [ "$i" != "$USQUE_IFACE" ]; then
            free=$i
        fi
    done
    echo "$free"
}

# --- steps -----------------------------------------------------------------------------

do_deps() {
    step "Пакеты Entware"
    run "opkg update >/dev/null && opkg install $MISSING_DEPS" || die "opkg не поставил:$MISSING_DEPS"
}

do_nfqws2() {
    step "nfqws2-keenetic"
    note "репозиторий nfqws2-keenetic → $P_NFQ_FEED"
    run "mkdir -p '$(dirname "$R$P_NFQ_FEED")' && echo '$NFQ_FEED' >'$R$P_NFQ_FEED' && opkg update >/dev/null && opkg install nfqws2-keenetic" ||
        die "nfqws2-keenetic не установился"
    warn "штатный nfqws2 сразу обрабатывает трафик по своему конфигу; если что-то перестало открываться: $P_NFQ_INIT stop"
}

# core_files — what do_core puts on the router: the agent's files, and the
# xray adapter when xray is there (the adapter moves with the agent that
# drives it; xray itself only when its pinned version changes)
core_files() {
    echo "nuxk-core-$ARCH nuxk-web-lite-$VERSION.tar.gz S99nuxk-core S51nfqws2-nuxk nuxk-lite.sh"
    [ -x "$R$P_XRAY_INIT" ] && echo S52xray-nuxk
    [ -x "$R$P_SMARTDNS_INIT" ] && echo S53smartdns-nuxk
    return 0
}

# keep_prev — the running version (agent, its init, the adapters, web) into
# $P_PREV. The `nuxk` command isn't kept: a newer one runs older agents.
keep_prev() {
    [ -x "$R$P_BIN" ] || return 0
    rm -rf "$R$P_PREV" && mkdir -p "$R$P_PREV" || return 0
    cp -f "$R$P_BIN" "$R$P_PREV/nuxk-core"
    [ -f "$R$P_INIT" ] && cp -f "$R$P_INIT" "$R$P_PREV/S99nuxk-core"
    [ -f "$R$P_SHIM" ] && cp -f "$R$P_SHIM" "$R$P_PREV/S51nfqws2-nuxk"
    [ -f "$R$P_XRAY_INIT" ] && cp -f "$R$P_XRAY_INIT" "$R$P_PREV/S52xray-nuxk"
    [ -f "$R$P_SMARTDNS_INIT" ] && cp -f "$R$P_SMARTDNS_INIT" "$R$P_PREV/S53smartdns-nuxk"
    [ -d "$R$P_WEB" ] && cp -R "$R$P_WEB" "$R$P_PREV/web"
    return 0
}

prev_ver() { "$R$P_PREV/nuxk-core" -version 2>/dev/null | awk '{ print $2 }'; }

# dns_detach — nuxk's DNS forwarder out of the router's DNS proxy (it lives in
# the running config only). When nuxk goes, and before an older agent comes
# back that may not know it; one that does adds it again within seconds.
dns_detach() {
    # where the agent put it: DNS_LISTEN, else LISTEN's address on port 53053
    a=$(conf_get DNS_LISTEN)
    if [ -z "$a" ]; then
        h=$(conf_get LISTEN)
        h=${h%:*}
        case "$h" in "" | 0.0.0.0 | 127.* | *[!0-9.]*) h=127.0.0.1 ;; esac
        a="$h:53053"
    fi
    case "$a" in *[!0-9.:]* | *:*:*) return 0 ;; esac # an IPv4:port, nothing else
    curl -fsS -m 5 -X POST http://127.0.0.1:79/rci/ -d "{\"parse\":\"no ip name-server $a\"}" >/dev/null 2>&1
    return 0
}

# AGENT_ENV — this run's own variables, kept away from the agent it starts: a
# daemon started from here would carry them into its next update from the
# panel (a mirror's URL long gone, «already self-updated»)
AGENT_ENV="-u NUXK_BASE_URL -u NUXK_VERSION -u NUXK_REPO -u NUXK_SELF_UPDATED -u NUXK_OLD_TMP -u NUXK_STATUS -u NUXK_FROM -u NUXK_STARTED -u NUXK_TASK"

# rollback — $P_PREV back in place, the agent restarted and asked whether it
# answers. The engines run on their own meanwhile: only the panel blinks.
rollback() {
    [ -x "$R$P_PREV/nuxk-core" ] || return 1
    "$R$P_INIT" stop >/dev/null 2>&1
    dns_detach
    cp -f "$R$P_PREV/nuxk-core" "$R$P_BIN" && chmod 755 "$R$P_BIN" || return 1
    for f in "S99nuxk-core:$P_INIT" "S51nfqws2-nuxk:$P_SHIM" "S52xray-nuxk:$P_XRAY_INIT" "S53smartdns-nuxk:$P_SMARTDNS_INIT"; do
        [ -f "$R$P_PREV/${f%%:*}" ] && cp -f "$R$P_PREV/${f%%:*}" "$R${f#*:}" && chmod 755 "$R${f#*:}"
    done
    [ -d "$R$P_PREV/web" ] && rm -rf "$R$P_WEB" && cp -R "$R$P_PREV/web" "$R$P_WEB"
    # shellcheck disable=SC2086
    env $AGENT_ENV "$R$P_INIT" restart >/dev/null 2>&1
    healthy
}

# undo MESSAGE — an update broke half way: the previous version back, then stop
undo() {
    if [ -n "$UPDATING" ] && [ -x "$R$P_PREV/nuxk-core" ]; then
        warn "$1 — возвращаю $(prev_ver)"
        report running "$1 — возвращаю прежнюю версию"
        if rollback; then
            FAIL_STATE=rolled_back
            die "$1. Вернул прежнюю версию $(prev_ver) — она работает"
        fi
        die "$1, и прежняя версия тоже не поднялась — посмотрите $P_LOG"
    fi
    die "$1"
}

# healthy — the agent answers on its address within HEALTH_WAIT seconds
healthy() {
    listen=$(conf_get LISTEN)
    host=${listen%:*} port=${listen##*:}
    case "$host" in "" | 0.0.0.0 | "[::]") host=127.0.0.1 ;; esac
    i=0
    while [ "$i" -lt "$HEALTH_WAIT" ]; do
        curl -fsS -m 2 "http://$host:${port:-4141}/api/v1/healthz" >/dev/null 2>&1 && return 0
        sleep 1
        i=$((i + 1))
    done
    return 1
}

do_core() {
    step "nuxk-core $VERSION и веб"
    report running "Ставлю агент и веб $VERSION"
    for f in $(core_files); do fetch "$f"; done
    chmod 755 "$TMP/nuxk-core-$ARCH"
    "$TMP/nuxk-core-$ARCH" -version >/dev/null 2>&1 || die "nuxk-core-$ARCH не запускается на этом роутере ($ARCH_RAW)"
    mkdir -p "$TMP/web" && tar -xzf "$TMP/nuxk-web-lite-$VERSION.tar.gz" -C "$TMP/web" || die "веб не распаковался"
    # what runs now goes aside first: back by itself if the new one won't
    # start, or by hand with `nuxk rollback` (the same version again keeps
    # the older one there)
    [ -n "$CORE_VER" ] && [ "$CORE_VER" != "$VERSION" ] && keep_prev
    # stop first: a running binary can't be overwritten on some filesystems
    [ -x "$R$P_INIT" ] && "$R$P_INIT" stop >/dev/null 2>&1
    mkdir -p "$R$(dirname $P_BIN)" "$R$(dirname $P_INIT)" "$R$(dirname $P_SHIM)" "$R$(dirname $P_SELF)" "$R$(dirname $P_WEB)"
    cp -f "$TMP/nuxk-core-$ARCH" "$R$P_BIN" && chmod 755 "$R$P_BIN" || undo "не записался $P_BIN"
    set -- "S99nuxk-core:$P_INIT" "S51nfqws2-nuxk:$P_SHIM" "nuxk-lite.sh:$P_SELF"
    xa=""
    [ -x "$R$P_XRAY_INIT" ] && [ -f "$TMP/S52xray-nuxk" ] && set -- "$@" "S52xray-nuxk:$P_XRAY_INIT" && xa=" · адаптер xray"
    [ -x "$R$P_SMARTDNS_INIT" ] && [ -f "$TMP/S53smartdns-nuxk" ] && set -- "$@" "S53smartdns-nuxk:$P_SMARTDNS_INIT" && xa="$xa · адаптер SmartDNS"
    for f in "$@"; do
        cp -f "$TMP/${f%%:*}" "$R${f#*:}" && chmod 755 "$R${f#*:}" || undo "не записался ${f#*:}"
    done
    rm -rf "$R$P_WEB.new" && cp -R "$TMP/web" "$R$P_WEB.new" && rm -rf "$R$P_WEB" && mv "$R$P_WEB.new" "$R$P_WEB" || undo "веб не записался в $P_WEB"
    ok "$("$R$P_BIN" -version 2>/dev/null) · веб · адаптер nfqws2$xa · команда nuxk"
    [ -n "$CORE_VER" ] && [ "$CORE_VER" != "$VERSION" ] && note "прежняя версия $CORE_VER — в $P_PREV (вернуть: nuxk rollback)"
    CORE_VER=$VERSION
    return 0
}

do_usque() {
    step "WARP (usque)"
    fetch "usque-keenetic-$ARCH.ipk"
    # additive only: the user's own entries stay; nfqws2 re-reads on reload
    if [ -f "$R$P_NFQ_LIST" ] && ! grep -qx "$WARP_HOSTS" "$R$P_NFQ_LIST"; then
        echo "$WARP_HOSTS" >>"$R$P_NFQ_LIST"
        [ -x "$R$P_NFQ_INIT" ] && "$R$P_NFQ_INIT" reload >/dev/null 2>&1
        note "$WARP_HOSTS → список nfqws2 (рукопожатие WARP через десинк)"
    fi
    # postinst: picks a free opkgtun, creates the ndm interface, saves the
    # router's config, starts the service (the first start registers with WARP)
    run "opkg install --force-reinstall '$TMP/usque-keenetic-$ARCH.ipk'" || die "usque-keenetic не установился"
    survey
    [ "$USQUE_READY" = 1 ] || die "usque установлен, но S51usque не отвечает на info — посмотрите /opt/var/log/usque.log"
    ok "WARP · интерфейс ${USQUE_IFACE:-?}"
    "$R$P_USQUE" info 2>/dev/null | grep -q '^service.running 1' ||
        warn "usque пока не запущен (часто — не прошла регистрация в WARP): после запуска nuxk нажмите «Рестарт» у WARP в панели"
    wire "ENGINE_USQUE" "$P_USQUE"
    [ -n "$USQUE_IFACE" ] && wire "PLANE_IFACE_WARP" "$USQUE_IFACE"
    return 0
}

do_xray() {
    step "VLESS (xray $XRAY_VERSION)"
    set -- $(xray_asset "$ARCH")
    zip=$1 xbin=$2 xsum=$3
    # tests serve their own archive; only under a test root
    [ -n "$R" ] && [ -n "${XRAY_TEST_SUM:-}" ] && xsum=$XRAY_TEST_SUM
    tun=$(xray_iface)
    [ -n "$tun" ] || die "все интерфейсы OpkgTun1–9 заняты — освободите один в Keenetic"
    fetch S52xray-nuxk
    if have unzip; then
        UNZIP=unzip
    elif busybox --list 2>/dev/null | grep -qx unzip; then
        UNZIP="busybox unzip"
    else
        run "opkg install unzip" || die "нет unzip: opkg install unzip"
        UNZIP=unzip
    fi
    get "$XRAY_BASE_URL/v$XRAY_VERSION/$zip" "$TMP/$zip"
    [ "$(sha256 "$TMP/$zip")" = "$xsum" ] || die "хеш архива xray не совпал с закреплённым — файл не тот, что выпустили XTLS; ставить не буду"
    note "↓ $zip · $(size_mb "$TMP/$zip") · sha256 ✓ (XTLS)"
    [ -x "$R$P_XRAY_INIT" ] && "$R$P_XRAY_INIT" stop >/dev/null 2>&1
    mkdir -p "$R$(dirname $P_XRAY)" "$R$(dirname $P_XRAY_INIT)"
    $UNZIP -p "$TMP/$zip" "$xbin" >"$R$P_XRAY.new"
    rm -f "$TMP/$zip" # the archive (≈30 MB) goes before anything else takes room
    chmod 755 "$R$P_XRAY.new"
    if ! "$R$P_XRAY.new" version 2>/dev/null | grep -q '^Xray '; then
        rm -f "$R$P_XRAY.new"
        [ -n "$XRAY_SOFT" ] || die "xray $XRAY_VERSION не запускается на этом роутере ($ARCH_RAW)"
        # a whole install or update doesn't fall over one optional engine:
        # the old xray (if any) is started again, the rest goes on
        [ -x "$R$P_XRAY" ] && [ -x "$R$P_XRAY_INIT" ] && "$R$P_XRAY_INIT" start >/dev/null 2>&1
        warn "xray $XRAY_VERSION не запускается на этом роутере ($ARCH_RAW) — VLESS пропускаю, остальное ставлю дальше"
        return 1
    fi
    mv -f "$R$P_XRAY.new" "$R$P_XRAY"
    cp -f "$TMP/S52xray-nuxk" "$R$P_XRAY_INIT" && chmod 755 "$R$P_XRAY_INIT"
    ok "$("$R$P_XRAY" version | head -n 1 | cut -d' ' -f1-2) → $P_XRAY ($xbin)"
    # the interface is created once and saved; S52xray-nuxk gives it its
    # address at every start, like usque does for OpkgTun0. An update finds
    # it made: the router's configuration isn't saved again.
    if have ndmc && ! ndmc -c "show interface $tun" 2>/dev/null | grep -q "description: *$XRAY_MARK *\$"; then
        if ! ndmc -c "show interface $tun" >/dev/null 2>&1 && ! nerr=$(ndmc -c "interface $tun" 2>&1); then
            nerr=$(echo "$nerr" | tr -s '\n' ' ' | sed 's/ *$//')
            [ -n "$XRAY_SOFT" ] || die "интерфейс $tun в Keenetic не создался${nerr:+: $nerr}"
            # xray stays in /opt; without its adapter it isn't counted as
            # installed, so the next run asks again
            rm -f "$R$P_XRAY_INIT"
            warn "интерфейс $tun в Keenetic не создался${nerr:+: $nerr} — VLESS пропускаю, остальное ставлю дальше"
            return 1
        fi
        ndmc -c "interface $tun description $XRAY_MARK" >/dev/null 2>&1 && ndmc -c "system configuration save" >/dev/null 2>&1 ||
            die "не удалось подписать $tun и сохранить конфигурацию роутера"
        ok "Keenetic: интерфейс $tun ($XRAY_MARK), конфигурация роутера сохранена"
    fi
    XRAY_VER=$XRAY_VERSION XRAY_READY=1 XRAY_IFACE=$tun
    wire "ENGINE_XRAY" "$P_XRAY_INIT"
    wire "PLANE_IFACE_VLESS" "$tun"
    note "сервер задаётся в панели: xray (VLESS) → «Сервер VLESS» — ссылка vless:// или подписка 3x-ui"
    return 0
}

do_smartdns() {
    step "SmartDNS ($SMARTDNS_VERSION, бета)"
    set -- $(smartdns_asset "$ARCH")
    file=$1 ssum=$2
    # tests serve their own binary; only under a test root
    [ -n "$R" ] && [ -n "${SMARTDNS_TEST_SUM:-}" ] && ssum=$SMARTDNS_TEST_SUM
    [ "$ssum" != none ] || die "для $ARCH нет закреплённой сборки SmartDNS"
    fetch S53smartdns-nuxk
    get "$SMARTDNS_BASE_URL/$SMARTDNS_VERSION/$file" "$TMP/$file"
    [ "$(sha256 "$TMP/$file")" = "$ssum" ] || die "хеш SmartDNS не совпал с закреплённым — файл не тот, что выпустил автор SmartDNS; ставить не буду"
    note "↓ $file · $(size_mb "$TMP/$file") · sha256 ✓ (pymumu/smartdns)"
    chmod 755 "$TMP/$file"
    "$TMP/$file" -v 2>/dev/null | grep -q '^smartdns' || die "SmartDNS не запускается на этом роутере ($ARCH_RAW)"
    # running (chosen in the panel): stopped for the swap, back after
    was_on=""
    [ -f "$R$P_SMARTDNS_DIR/on" ] && was_on=1
    [ -x "$R$P_SMARTDNS_INIT" ] && "$R$P_SMARTDNS_INIT" stop >/dev/null 2>&1
    mkdir -p "$R$(dirname $P_SMARTDNS)" "$R$(dirname $P_SMARTDNS_INIT)" "$R$P_SMARTDNS_DIR"
    mv -f "$TMP/$file" "$R$P_SMARTDNS" || die "не записался $P_SMARTDNS"
    cp -f "$TMP/S53smartdns-nuxk" "$R$P_SMARTDNS_INIT" && chmod 755 "$R$P_SMARTDNS_INIT" || die "не записался $P_SMARTDNS_INIT"
    echo "$SMARTDNS_VERSION" >"$R$P_SMARTDNS_DIR/.release"
    [ -n "$was_on" ] && "$R$P_SMARTDNS_INIT" start >/dev/null 2>&1
    ok "SmartDNS $SMARTDNS_VERSION → $P_SMARTDNS"
    SMARTDNS_VER=$SMARTDNS_VERSION
    note "включается в панели: «DNS» → «Чем отвечать» → SmartDNS. Пока не выбран — не запускается"
    return 0
}

# wire KEY VALUE — set one key in an existing nuxk.conf (the rest stays as edited)
wire() {
    f="$R$P_CONF"
    [ -f "$f" ] || return 0
    if grep -q "^$1=" "$f"; then
        sed -i "s|^$1=.*|$1=\"$2\"|" "$f"
    else
        echo "$1=\"$2\"" >>"$f"
    fi
}

# token — 128 random bits as 32 hex digits. KeeneticOS 5.01's BusyBox od has
# no -A/-t: then the kernel's random UUID (122 random bits), then md5sum.
token() {
    t=$(head -c 16 /dev/urandom 2>/dev/null | od -An -tx1 2>/dev/null | tr -d ' \n')
    hex32 "$t" || t=$(tr -d '\n-' </proc/sys/kernel/random/uuid 2>/dev/null)
    hex32 "$t" || t=$(head -c 32 /dev/urandom 2>/dev/null | md5sum 2>/dev/null | cut -c1-32)
    hex32 "$t" && echo "$t"
}
hex32() { [ ${#1} -eq 32 ] && case "$1" in *[!0-9a-f]*) false ;; esac; }

do_config() {
    step "Конфиг $P_CONF"
    listen="${LAN_IP:-127.0.0.1}:4141"
    usque="" xray="" nfq=""
    [ "$USQUE_READY" = 1 ] && usque=$P_USQUE
    [ "$XRAY_READY" = 1 ] && xray=$P_XRAY_INIT
    [ -x "$R$P_NFQ_INIT" ] && nfq=$P_SHIM
    vless_if=$XRAY_IFACE
    [ -z "$vless_if" ] && [ "$XRAY_READY" = 1 ] && vless_if=$(xray_iface)
    vless_if=${vless_if:-OpkgTun1}
    mkdir -p "$R$(dirname $P_CONF)"
    (
        umask 077
        cat >"$R$P_CONF" <<EOF
# nuxk-core — written by nuxk-lite $VERSION on $(date +%Y-%m-%d).
# Shell-sourceable KEY="value". Edits are kept: nuxk update never rewrites it.

# LAN address only: reachable from the home network, never bound on WAN.
LISTEN="$listen"

# Bearer token for programs: nuxk-controller on a Pi gets it by pairing with
# the root password. People log in to the web UI as root (Entware's password,
# the same as for SSH) — no need to copy this anywhere.
API_TOKEN="$(token)"

STATE_DIR="/opt/etc/nuxk"
WEB_ROOT="$P_WEB"

# Engines: empty = off. nfqws2 goes through the nuxk adapter over the stock
# nfqws2-keenetic package.
ENGINE_NFQWS2="$nfq"
ENGINE_USQUE="$usque"
ENGINE_XRAY="$xray"

# A router CPU is slow: poll engines every 10 s, probe every 2 min.
INFO_EVERY="10"
PROBE_EVERY="120"

# Routing plane: KeeneticOS's own DNS routing (object-group fqdn + dns-proxy
# route) through RCI on 127.0.0.1:79, only ever nuxk-* objects.
# PLANE_APPLY="0" = plan only: the panel shows what it would do.
PLANE="keenetic"
PLANE_APPLY="0"
PLANE_V6="deny"
PLANE_IFACE_WARP="${USQUE_IFACE:-OpkgTun0}"
PLANE_IFACE_VLESS="$vless_if"
EOF
    )
    ok "адрес $listen · режим плана (PLANE_APPLY=\"0\") · права 0600"
    HAVE_CONF=1
}

# seed_plane — on the very first start, nfqws2's own user.list becomes the
# «DPI» list: nuxk owns that file from then on and would otherwise empty it.
seed_plane() {
    [ -f "$R$P_PLANE" ] && return 0
    doms=""
    n=0
    if [ -f "$R$P_NFQ_LIST" ]; then
        for d in $(grep -v '^#' "$R$P_NFQ_LIST" | tr -d '\r' | grep -E '^[A-Za-z0-9._-]+$' | grep -vx "$WARP_HOSTS"); do
            doms="$doms${doms:+, }\"$d\""
            n=$((n + 1))
        done
    fi
    (
        umask 077
        if [ "$n" -gt 0 ]; then
            printf '{\n  "on_down": "direct",\n  "manage_desync": true,\n  "lists": [\n    { "name": "DPI", "mode": "desync", "source": "imported:nfqws2", "domains": [%s] }\n  ]\n}\n' "$doms" >"$R$P_PLANE"
        else
            printf '{\n  "on_down": "direct",\n  "lists": []\n}\n' >"$R$P_PLANE"
        fi
    )
    note "список «DPI» из user.list nfqws2: $(domains "$n")"
}

do_start() {
    step "Запуск и проверка"
    seed_plane
    if [ -z "$(conf_get API_TOKEN)" ]; then
        tok=$(token)
        if [ -n "$tok" ]; then wire API_TOKEN "$tok"
        else warn "не получилось создать API_TOKEN: контроллер на Pi не подключится, вход в панель работает"; fi
    fi
    report running "Перезапускаю агент и проверяю, что он отвечает"
    if run "env $AGENT_ENV '$R$P_INIT' restart" && healthy; then
        ok "nuxk-core отвечает на http://$host:${port:-4141}"
        return 0
    fi
    tail -n 15 "$R$P_LOG" 2>/dev/null | while IFS= read -r l; do printf '      %s│ %s%s\n' "$D" "$l" "$N"; done
    undo "nuxk-core $VERSION не ответил за $HEALTH_WAIT секунд"
}

# --- modes -----------------------------------------------------------------------------

show_router() {
    section "Роутер"
    if [ "$ENTWARE" = 1 ]; then row ok "Entware" "${KOS:+KeeneticOS $KOS}${MODEL:+ · $MODEL}"; else row bad "Entware" "нет opkg — установите Entware и зайдите по SSH именно в него"; fi
    if [ -n "$ARCH" ]; then row ok "Архитектура" "$ARCH_RAW → $ARCH"; else row bad "Архитектура" "$ARCH_RAW не поддерживается (mips, mipsel, aarch64, x86_64)"; fi
    if [ -n "$FREE_KB" ] && [ "$FREE_KB" -lt "$MIN_FREE_KB" ]; then row bad "Место в /opt" "свободно $((FREE_KB / 1024)) МБ, нужно не меньше $((MIN_FREE_KB / 1024)) МБ"; else row ok "Место в /opt" "свободно $((${FREE_KB:-0} / 1024)) МБ"; fi
    if [ -n "$MISSING_KMODS" ]; then
        row warn "Модули Netfilter" "нет$MISSING_KMODS: Keenetic → Параметры системы → Изменить набор компонентов → «Модули ядра подсистемы Netfilter». Без них nfqws2 не запустится"
    else
        row ok "Модули Netfilter" "NFQUEUE, connbytes, multiport"
    fi
    [ -n "$LAN_IP" ] && row ok "Адрес в сети" "$LAN_IP"
}

blocked() { [ "$ENTWARE" = 1 ] && [ -n "$ARCH" ] && { [ -z "$FREE_KB" ] || [ "$FREE_KB" -ge "$MIN_FREE_KB" ]; } && return 1; return 0; }

finish() {
    printf '\n'
    line
    listen=$(conf_get LISTEN)
    printf '  %s✓ nuxk Horizon %s на роутере%s\n' "$G$B" "$VERSION" "$N"
    printf '    %sПанель%s   http://%s\n' "$B" "$N" "${listen:-${LAN_IP:-роутер}:4141}"
    printf '    %sВход%s     root и пароль Entware (как для SSH)\n' "$B" "$N"
    printf '    %sКоманды%s  nuxk · nuxk update · nuxk rollback · nuxk warp · nuxk vless · nuxk dns · nuxk uninstall\n' "$B" "$N"
    [ "$(conf_get PLANE_APPLY)" = 1 ] || note "Маршрутизация списков выключена (режим плана), пока в $P_CONF не поставить PLANE_APPLY=\"1\"."
    note "Есть Raspberry Pi? Полная версия (история, подбор стратегий): nuxk-full.sh — см. README."
    printf '\n'
    report done "${DONE_MSG:-nuxk Horizon $VERSION работает}"
}

mode_install() {
    survey
    show_router
    blocked && die "установка невозможна: исправьте пункты с ✗ и запустите снова"

    section "План"
    want_deps="" want_nfq="" want_core="" want_usque="" want_xray="" want_conf=""
    if [ -n "$MISSING_DEPS" ]; then row do "Пакеты Entware" "opkg install$MISSING_DEPS"; want_deps=1; else row ok "Пакеты Entware" "$DEPS"; fi
    if [ -n "$NFQ_VER" ]; then row ok "nfqws2-keenetic" "$NFQ_VER · обновляется через opkg upgrade"; else row do "nfqws2-keenetic" "обход DPI: репозиторий nfqws2-keenetic + opkg install"; want_nfq=1; fi
    if [ -z "$CORE_VER" ]; then row do "nuxk-core $VERSION" "агент, веб, адаптер nfqws2"; want_core=1
    elif [ "$CORE_VER" != "$VERSION" ]; then row do "nuxk-core" "$CORE_VER → $VERSION"; want_core=1
    else row ok "nuxk-core" "уже $VERSION"; fi
    if [ "$USQUE_READY" = 1 ]; then row ok "WARP (usque)" "установлен${USQUE_IFACE:+ · $USQUE_IFACE}"
    elif in_release "usque-keenetic-$ARCH.ipk"; then row opt "WARP (usque)" "по желанию"
    else row skip "WARP (usque)" "нет сборки для $ARCH"; fi
    if [ "$XRAY_READY" = 1 ]; then row ok "VLESS (xray)" "xray $XRAY_VER"
    elif [ -n "$(xray_asset "$ARCH")" ]; then row opt "VLESS (xray)" "по желанию"
    else row skip "VLESS (xray)" "нет сборки для $ARCH"; fi
    if [ "$HAVE_CONF" = 1 ]; then row ok "Конфиг" "$P_CONF сохраняется как есть"; else row do "Конфиг" "$P_CONF: адрес в LAN, режим плана"; want_conf=1; fi

    if [ -n "$want_nfq" ] && ! ask "Поставить nfqws2-keenetic (обход DPI — основа nuxk)?" y; then want_nfq=""; fi
    if [ "$USQUE_READY" != 1 ] && in_release "usque-keenetic-$ARCH.ipk"; then
        [ -n "$WITH_WARP" ] && want_usque=1
        [ -z "$want_usque" ] && ask "Поставить WARP? Регистрирует устройство в Cloudflare WARP (вы принимаете их условия), создаёт интерфейс OpkgTun и сохраняет конфигурацию роутера." n && want_usque=1
    fi
    if [ "$XRAY_READY" != 1 ] && [ -n "$(xray_asset "$ARCH")" ]; then
        [ -n "$WITH_VLESS" ] && want_xray=1
        [ -z "$want_xray" ] && ask "Поставить VLESS (xray)? Нужен свой сервер VLESS (например, 3x-ui). Скачает xray $XRAY_VERSION с GitHub XTLS со сверкой хеша (≈35 МБ в /opt, 30–60 МБ памяти), создаст в Keenetic интерфейс $(xray_iface) и сохранит конфигурацию роутера." n && want_xray=1
    fi
    if [ -n "$want_xray" ] && [ -n "$FREE_KB" ] && [ "$FREE_KB" -lt "$XRAY_FREE_KB" ]; then
        warn "для xray мало места: свободно $((FREE_KB / 1024)) МБ, нужно около $((XRAY_FREE_KB / 1024)) МБ на время установки — пропускаю"
        want_xray=""
    fi

    [ -n "$want_deps$want_nfq$want_core$want_usque$want_xray$want_conf" ] || {
        ok "всё уже стоит — перезапускаю и проверяю"
        # a router set up before this installer has no `nuxk` command yet
        if [ ! -x "$R$P_SELF" ]; then
            fetch nuxk-lite.sh
            mkdir -p "$R$(dirname $P_SELF)" && cp -f "$TMP/nuxk-lite.sh" "$R$P_SELF" && chmod 755 "$R$P_SELF" && ok "команда nuxk"
        fi
        STEPS=1
        do_start
        finish
        return
    }
    ask "Начать установку?" y || die "отменено — на роутере ничего не изменилось"

    # over an older nuxk: the same as an update — if the new agent doesn't
    # answer, the one that ran comes back by itself
    [ -n "$want_core" ] && [ -n "$CORE_VER" ] && UPDATING=1
    files=""
    [ -n "$want_core" ] && files=$(core_files)
    [ -n "$want_usque" ] && files="$files usque-keenetic-$ARCH.ipk"
    [ -n "$want_xray" ] && files="$files S52xray-nuxk"
    # shellcheck disable=SC2086
    prefetch $files

    STEPS=1
    for w in "$want_deps" "$want_nfq" "$want_core" "$want_usque" "$want_xray" "$want_conf"; do [ -n "$w" ] && STEPS=$((STEPS + 1)); done
    [ -n "$want_deps" ] && do_deps
    [ -n "$want_nfq" ] && do_nfqws2
    [ -n "$want_core" ] && do_core
    [ -n "$want_usque" ] && do_usque
    [ -n "$want_xray" ] && XRAY_SOFT=1 && { do_xray || :; }
    [ -n "$want_conf" ] && do_config
    do_start
    finish
}

mode_update() {
    survey
    [ -n "$ARCH" ] || die "архитектура $ARCH_RAW не поддерживается"
    [ -n "$CORE_VER" ] || die "nuxk не установлен — запустите без аргументов"
    # the latest release older than what runs (a beta, or a release pulled
    # back): nothing to do; an older one only when named, NUXK_VERSION=…
    if [ -z "${NUXK_VERSION:-}" ] && newer "$CORE_VER" "$VERSION"; then
        ok "на роутере $CORE_VER — новее последнего релиза $VERSION; ничего не меняю"
        report done "на роутере $CORE_VER — новее последнего релиза"
        return 0
    fi
    UPDATING=1
    report running "Скачиваю и проверяю файлы $VERSION"
    STEPS=2
    want_x="" want_s=""
    [ "$XRAY_READY" = 1 ] && [ "$XRAY_VER" != "$XRAY_VERSION" ] && want_x=1 && STEPS=$((STEPS + 1))
    [ -n "$SMARTDNS_VER" ] && [ "$SMARTDNS_VER" != "$SMARTDNS_VERSION" ] && smartdns_ok && want_s=1 && STEPS=$((STEPS + 1))
    printf '\n'
    row do "nuxk-core" "$CORE_VER → $VERSION"
    # shellcheck disable=SC2046
    prefetch $(core_files)
    do_core
    if [ -n "$want_x" ]; then
        report running "Обновляю xray до $XRAY_VERSION"
        XRAY_SOFT=1
        do_xray || :
    fi
    if [ -n "$want_s" ]; then
        report running "Обновляю SmartDNS до $SMARTDNS_VERSION"
        do_smartdns
    fi
    do_start
    finish
}

mode_rollback() {
    survey
    [ -x "$R$P_PREV/nuxk-core" ] || die "прежней версии нет ($P_PREV) — откатывать не на что"
    pv=$(prev_ver)
    printf '\n'
    row do "nuxk-core" "${CORE_VER:-?} → $pv · агент, веб, адаптер nfqws2"
    note "движки и списки не трогаются; команда nuxk остаётся новой"
    confirm "Вернуть nuxk $pv?" || die "отменено"
    STEPS=1
    step "Откат на $pv"
    rollback || die "nuxk-core $pv не ответил за $HEALTH_WAIT секунд — посмотрите $P_LOG"
    ok "nuxk-core $pv отвечает; обновиться снова — nuxk update"
}

mode_add() { # add nfqws2|warp|vless|dns
    survey
    [ -n "$CORE_VER" ] || die "сначала поставьте nuxk: запустите без аргументов"
    STEPS=2
    case "$1" in
    nfqws2)
        if [ -n "$NFQ_VER" ] && [ "$(conf_get ENGINE_NFQWS2)" = "$P_SHIM" ]; then already "nfqws2-keenetic $NFQ_VER уже стоит"; return; fi
        if [ -z "$NFQ_VER" ]; then
            confirm "Поставить nfqws2-keenetic (обход DPI)? Подключит его репозиторий и поставит пакет; штатный nfqws2 сразу начнёт обрабатывать трафик по своему конфигу." || die "отменено"
            report running "Ставлю nfqws2-keenetic"
            do_nfqws2
        fi
        [ -x "$R$P_NFQ_INIT" ] || die "nfqws2-keenetic не установился: нет $P_NFQ_INIT"
        wire "ENGINE_NFQWS2" "$P_SHIM"
        DONE_MSG="nfqws2 установлен"
        ;;
    warp)
        [ "$USQUE_READY" = 1 ] && { already "WARP уже стоит"; return; }
        in_release "usque-keenetic-$ARCH.ipk" || die "в релизе нет usque для $ARCH"
        confirm "Поставить WARP? Регистрирует устройство в Cloudflare WARP (вы принимаете их условия), создаёт интерфейс OpkgTun и сохраняет конфигурацию роутера." || die "отменено"
        report running "Ставлю WARP (usque)"
        do_usque
        DONE_MSG="WARP установлен"
        ;;
    vless)
        [ -n "$(xray_asset "$ARCH")" ] || die "нет сборки xray для $ARCH"
        [ -z "$FREE_KB" ] || [ "$FREE_KB" -ge "$XRAY_FREE_KB" ] || die "для xray мало места: свободно $((FREE_KB / 1024)) МБ, нужно около $((XRAY_FREE_KB / 1024)) МБ"
        confirm "Поставить VLESS (xray $XRAY_VERSION)? Создаст в Keenetic интерфейс $(xray_iface) и сохранит конфигурацию роутера." || die "отменено"
        report running "Ставлю VLESS (xray $XRAY_VERSION)"
        do_xray
        DONE_MSG="VLESS установлен — задайте сервер на странице «xray (VLESS)»"
        ;;
    dns)
        smartdns_ok || die "для $ARCH нет сборки SmartDNS"
        [ "$SMARTDNS_VER" = "$SMARTDNS_VERSION" ] && { already "SmartDNS $SMARTDNS_VERSION уже стоит — включается в панели, «DNS»"; return; }
        confirm "Поставить SmartDNS $SMARTDNS_VERSION (бета)? Скачает его с GitHub автора со сверкой хеша в /opt. Сам не запустится: его выбирают в панели, «DNS», вместо встроенного DNS nuxk." || die "отменено"
        report running "Ставлю SmartDNS $SMARTDNS_VERSION"
        do_smartdns
        DONE_MSG="SmartDNS установлен — включается в «DNS» → «Настройки»"
        ;;
    esac
    do_start
    finish
}

# already MESSAGE — nothing to add: said here and to the panel
already() {
    ok "$1"
    report done "$1"
}

mode_status() {
    survey
    banner "${CORE_VER:-не установлен}"
    show_router
    section "nuxk"
    if [ -z "$CORE_VER" ]; then row bad "nuxk-core" "не установлен"
    elif "$R$P_INIT" status 2>/dev/null | grep -q 'is running'; then row ok "nuxk-core" "$CORE_VER · работает · http://$(conf_get LISTEN)"
    else row warn "nuxk-core" "$CORE_VER · остановлен: $P_INIT start"; fi
    for e in "nfqws2:$P_SHIM" "WARP:$P_USQUE" "VLESS:$P_XRAY_INIT" "SmartDNS:$P_SMARTDNS_INIT"; do
        s="${e#*:}"
        [ -x "$R$s" ] || { row skip "${e%%:*}" "не установлен"; continue; }
        st=$("$R$s" info 2>/dev/null)
        if echo "$st" | grep -q '^service.running 1'; then row ok "${e%%:*}" "работает"; else row warn "${e%%:*}" "не запущен"; fi
    done
    plane=$(conf_get PLANE_APPLY)
    [ "$plane" = 1 ] && row ok "Маршрутизация" "применяется" || row opt "Маршрутизация" "режим плана (PLANE_APPLY=\"0\")"
    printf '\n'
}

mode_uninstall() {
    survey
    [ -n "$CORE_VER" ] || [ -x "$R$P_INIT" ] || die "nuxk не установлен"
    section "Удаление nuxk"
    say "Уберу: агент, веб, адаптеры, команду nuxk, свои объекты маршрутизации nuxk-*."
    say "Останутся: /opt/etc/nuxk.removed (конфиг и списки), nfqws2-keenetic с его списком, ваши настройки Keenetic."
    confirm "Удалить nuxk?" || die "отменено"
    STEPS=2
    [ "$XRAY_READY" = 1 ] && STEPS=3
    step "nuxk-core"
    [ -x "$R$P_INIT" ] && "$R$P_INIT" stop >/dev/null 2>&1
    # nuxk's routing objects live only in the running config (never saved):
    # dropped here, and gone after a reboot anyway
    for g in "nuxk-warp:$(conf_get PLANE_IFACE_WARP)" "nuxk-vless:$(conf_get PLANE_IFACE_VLESS)"; do
        grp=${g%%:*} ifc=${g#*:}
        [ -n "$ifc" ] && curl -fsS -m 5 -X POST http://127.0.0.1:79/rci/ -d "{\"dns-proxy\":{\"route\":{\"group\":\"$grp\",\"interface\":\"$ifc\",\"no\":true}}}" >/dev/null 2>&1
        curl -fsS -m 5 -X POST http://127.0.0.1:79/rci/ -d "{\"object-group\":{\"fqdn\":{\"$grp\":{\"no\":true}}}}" >/dev/null 2>&1
    done
    dns_detach
    if have ip6tables; then
        ip6tables -w -D FORWARD -j NUXK_V6_DENY 2>/dev/null
        ip6tables -w -F NUXK_V6_DENY 2>/dev/null
        ip6tables -w -X NUXK_V6_DENY 2>/dev/null
    fi
    # the config, lists and state stay aside for a reinstall; the adapter
    # (engines/) goes with the agent
    rm -rf "$R/opt/etc/nuxk/engines" "$R/opt/etc/nuxk.removed"
    [ -d "$R/opt/etc/nuxk" ] && mv -f "$R/opt/etc/nuxk" "$R/opt/etc/nuxk.removed"
    # SmartDNS answered only for nuxk: it goes with it
    if [ -x "$R$P_SMARTDNS_INIT" ]; then
        "$R$P_SMARTDNS_INIT" down >/dev/null 2>&1
        rm -rf "$R$P_SMARTDNS" "$R$P_SMARTDNS_INIT" "$R$P_SMARTDNS_DIR" "$R/tmp/smartdns-nuxk"
    fi
    rm -rf "$R$P_BIN" "$R$P_BIN.prev" "$R$P_PREV" "$R$P_INIT" "$R$P_WEB" "$R$P_LOG"* "$R/opt/var/log/nuxk-core.crash"
    ok "агент, веб, SmartDNS и объекты nuxk-* убраны; конфиг и списки — /opt/etc/nuxk.removed"
    if [ "$XRAY_READY" = 1 ]; then
        step "xray (VLESS)"
        tun=$(conf_get PLANE_IFACE_VLESS)
        if ask "Удалить и xray${tun:+ с интерфейсом $tun} (конфигурация роутера сохранится)?" n; then
            "$R$P_XRAY_INIT" stop >/dev/null 2>&1
            rm -rf "$R$P_XRAY" "$R$P_XRAY_INIT" "$R/opt/etc/xray" "$R/opt/var/log/xray.log"
            if [ -n "$tun" ] && have ndmc && ndmc -c "show interface $tun" 2>/dev/null | grep -q "description: *$XRAY_MARK"; then
                ndmc -c "no interface $tun" >/dev/null 2>&1 && ndmc -c "system configuration save" >/dev/null 2>&1 && ok "интерфейс $tun убран"
            fi
            ok "xray удалён"
        else
            note "xray оставлен"
        fi
    fi
    step "Команда nuxk"
    rm -f "$R$P_SELF"
    ok "готово. WARP (opkg remove usque-keenetic) и nfqws2-keenetic — отдельно, если нужно"
}

# nuxk update (the command on the router): the target release's own script
# does the update, so what's new in it applies too — once its SHA256SUMS
# signature and hash check out here
self_update() {
    [ -n "${NUXK_SELF_UPDATED:-}" ] && return 1
    VERSION="@""VERSION@"
    [ -n "${NUXK_VERSION:-}" ] && VERSION=${NUXK_VERSION#v}
    BASE=$(base_url)
    report running "Проверяю релиз ${NUXK_VERSION:-}"
    get "$BASE/SHA256SUMS" "$TMP/SHA256SUMS"
    verify_sums
    get "$BASE/nuxk-lite.sh" "$TMP/nuxk-lite.sh"
    sum_ok nuxk-lite.sh
    NUXK_SELF_UPDATED=1 NUXK_OLD_TMP=$TMP exec sh "$TMP/nuxk-lite.sh" update ${YES:+--yes}
}

# --- main ------------------------------------------------------------------------------

MODE=""
WITH_WARP="" WITH_VLESS="" XRAY_SOFT=""
for a in "$@"; do
    case "$a" in
    --yes | -y) YES=1 ;;
    --with-warp) WITH_WARP=1 ;;
    --with-vless) WITH_VLESS=1 ;;
    install | update | rollback | nfqws2 | warp | vless | dns | status | uninstall) MODE=$a ;;
    -h | --help)
        sed -n '2,27p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'
        exit 0
        ;;
    *) die "не понимаю «$a»: nuxk [install|update|rollback|nfqws2|warp|vless|dns|status|uninstall] [--yes]" ;;
    esac
done
# the downloaded script installs; the installed command shows the state
if [ -z "$MODE" ]; then
    case "$0" in */bin/nuxk | nuxk) MODE=status ;; *) MODE=install ;; esac
fi

[ "$(id -u 2>/dev/null)" = 0 ] || [ -n "$R" ] || die "запустите от root (Entware по SSH)"
have curl || die "нет curl: opkg update && opkg install curl ca-certificates"
mkdir -p "$R/opt/tmp" 2>/dev/null
TMP=$(mktemp -d "$R/opt/tmp/nuxk-setup.XXXXXX" 2>/dev/null) || TMP="$R/opt/tmp/nuxk-setup.$$"
mkdir -p "$TMP" || die "не создать $TMP"
UPDATING="" FAIL_STATE=""
HEALTH_WAIT=15
[ -n "$R" ] && HEALTH_WAIT=${NUXK_TEST_WAIT:-15} # tests don't wait out a dead agent

# without the network: what's on the router
case "$MODE" in
status)
    mode_status
    exit 0
    ;;
rollback)
    banner "откат"
    mode_rollback
    exit 0
    ;;
esac
if [ "$MODE" = update ] && [ -z "${NUXK_SELF_UPDATED:-}" ]; then
    self_update
fi

[ -n "${NUXK_VERSION:-}" ] && VERSION=${NUXK_VERSION#v}
BASE=$(base_url)
get "$BASE/SHA256SUMS" "$TMP/SHA256SUMS"
verify_sums
release_version

banner "$VERSION"
[ -n "$SIG_NOTE" ] && note "$SIG_NOTE"
case "$MODE" in
install) mode_install ;;
update) mode_update ;;
nfqws2 | warp | vless | dns) mode_add "$MODE" ;;
uninstall) mode_uninstall ;;
esac
