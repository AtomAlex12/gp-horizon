#!/bin/sh
# GP Horizon — the DEVELOPMENT stack on a Raspberry Pi, built from source
# (the real-engine stand and the controller). People install with
# install/nuxk-full.sh instead: prebuilt, nothing compiled on the Pi.
# Safe to re-run: it checks, fixes what's missing, updates the checkout and
# restarts the stack.
#
#   git clone https://github.com/AtomAlex12/gp-horizon.git ~/nuxk-horizon
#   cd ~/nuxk-horizon && sh deploy/pi/bootstrap.sh
#
# Env: NUXK_REF (branch/tag, default main), NUXK_DIR (checkout, default: the
# one this script lives in, else ~/nuxk-horizon), NUXK_REPO (clone URL).
set -eu

REPO="${NUXK_REPO:-git@github.com:AtomAlex12/gp-horizon.git}"
REF="${NUXK_REF:-main}"
HERE=$(cd "$(dirname "$0")/../.." 2>/dev/null && pwd || true)
if [ -z "${NUXK_DIR:-}" ] && [ -f "$HERE/deploy/pi/docker-compose.yml" ]; then DIR="$HERE"; else DIR="${NUXK_DIR:-$HOME/nuxk-horizon}"; fi
COMPOSE="deploy/pi/docker-compose.yml"
KMODS="nfnetlink_queue xt_multiport xt_connbytes xt_NFQUEUE xt_CONNMARK xt_connmark nf_conntrack"

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }
SUDO=""; [ "$(id -u)" = 0 ] || SUDO="sudo"

# --- 1. host checks -------------------------------------------------------------
say "Проверка Raspberry Pi"
case "$(uname -m)" in
aarch64 | arm64) ;;
*) die "нужен 64-битный ARM (Raspberry Pi 4/5, 64-bit OS), а здесь $(uname -m)" ;;
esac
command -v git >/dev/null || die "нет git: $SUDO apt install -y git"
command -v docker >/dev/null || die "нет Docker: https://docs.docker.com/engine/install/debian/"
docker compose version >/dev/null 2>&1 || die "нет docker compose v2: $SUDO apt install -y docker-compose-plugin"
docker info >/dev/null 2>&1 || die "Docker недоступен текущему пользователю: $SUDO usermod -aG docker $USER, затем перелогиньтесь"
v=$(docker compose version --short 2>/dev/null | sed 's/^v//')
case "$v" in 1.* | 2.[0-9].* | 2.1[0-9].*) die "docker compose $v слишком старый (нужен 2.20+ для include)" ;; esac

# --- 2. kernel modules for nfqws2 (host kernel; a container can't load them) -----
say "Модули ядра для nfqws2"
for m in $KMODS; do $SUDO modprobe "$m" 2>/dev/null || warn "не загрузился модуль $m"; done
if [ ! -f /etc/modules-load.d/nuxk.conf ]; then
    printf '%s\n' $KMODS | $SUDO tee /etc/modules-load.d/nuxk.conf >/dev/null
    say "модули будут грузиться при загрузке (/etc/modules-load.d/nuxk.conf)"
fi
[ -c /dev/net/tun ] || die "нет /dev/net/tun — нужен для туннеля usque"

# --- 3. checkout ----------------------------------------------------------------------
if [ -d "$DIR/.git" ]; then
    say "Обновление $DIR до $REF"
    git -C "$DIR" fetch --tags --quiet origin || die "git fetch не прошёл: нужен доступ Pi к приватному репозиторию (SSH-ключ или токен)"
    git -C "$DIR" checkout --quiet "$REF"
    git -C "$DIR" pull --ff-only --quiet origin "$REF" 2>/dev/null || true # a tag has nothing to pull
else
    say "Клонирование в $DIR ($REF)"
    git clone --quiet --branch "$REF" "$REPO" "$DIR" || die "git clone не прошёл: репозиторий приватный — добавьте SSH-ключ Pi в GitHub или клонируйте по https с токеном"
fi
cd "$DIR"
VERSION=$(cat VERSION)
NUXK_COMMIT=$(git rev-parse --short HEAD)
export NUXK_COMMIT
say "nuxk $VERSION ($NUXK_COMMIT)"

if [ ! -f deploy/pi/.env ]; then
    cp deploy/pi/env.example deploy/pi/.env
    say "создан deploy/pi/.env (этап A: только WARP) — этапы B и C включаются там"
fi

# --- 4. an older stand from deploy/proto (project "proto") holds :4242 --------------
OLD=$(docker ps --filter "label=com.docker.compose.project=proto" --format '{{.Names}}' | head -n 1)
if [ -n "$OLD" ]; then
    say "Останавливаю старый стенд deploy/proto ($OLD) — его тома сохраняются"
    docker ps -q --filter "label=com.docker.compose.project=proto" | xargs docker stop >/dev/null
fi
BUSY=$(docker ps --format '{{.Names}} {{.Ports}}' | grep -E ':(4242)->' | grep -v '^nuxk-pi-' || true)
[ -z "$BUSY" ] || die "порт 4242 занят другим контейнером: $BUSY"

# --- 5. build ---------------------------------------------------------------------------
say "Сборка образов (первый раз 5–10 минут)"
docker compose -f "$COMPOSE" build

# --- 6. WARP registration, once (reads Cloudflare's ToS — keep it interactive) ---
# Carry over the registration of an older deploy/proto stand instead of
# registering a second device.
if docker volume inspect proto_usque-session >/dev/null 2>&1 &&
    ! docker volume inspect nuxk-pi_usque-session >/dev/null 2>&1; then
    say "Переношу регистрацию WARP из старого стенда (том proto_usque-session)"
    docker volume create nuxk-pi_usque-session >/dev/null
    docker run --rm -v proto_usque-session:/from:ro -v nuxk-pi_usque-session:/to \
        --entrypoint sh nuxk-horizon-proto -c 'cp -a /from/. /to/'
fi
if ! docker compose -f "$COMPOSE" run --rm --entrypoint sh nuxk -c 'test -f /opt/etc/usque/session.conf' 2>/dev/null; then
    say "Регистрация устройства WARP (один раз)"
    if [ -t 0 ]; then
        docker compose -f "$COMPOSE" run --rm --entrypoint /opt/etc/init.d/S51usque-docker nuxk register
    else
        warn "нет терминала для подтверждения условий Cloudflare. Выполните вручную и перезапустите скрипт:"
        warn "  cd $DIR && docker compose -f $COMPOSE run --rm --entrypoint /opt/etc/init.d/S51usque-docker nuxk register"
        exit 1
    fi
fi

# --- 7. start ------------------------------------------------------------------------------
say "Запуск стека"
docker compose -f "$COMPOSE" up -d
sleep 3
IP=$(hostname -I 2>/dev/null | awk '{print $1}')
HEALTH=$(curl -fsS -m 5 "http://127.0.0.1:4242/api/v1/healthz" 2>/dev/null || echo "не отвечает")

cat <<EOF

  nuxk $VERSION на Raspberry Pi
  ───────────────────────────────────────────────
  Дашборд стенда:   http://$IP:4242/      (healthz: $HEALTH)
  Контроллер:       http://$IP:4200/      (профиль controller в deploy/pi/.env)
  Роутер:           install/nuxk-lite.sh — установщик лайт, из этого checkout или релиза

  Логи:  docker compose -f $DIR/$COMPOSE logs -f
  Стоп:  docker compose -f $DIR/$COMPOSE down

EOF
