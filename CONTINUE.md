# CONTINUE — где мы и что дальше

Рабочая копия форка **`side-effect-tm/usque-keenetic`** с добавленным веб-интерфейсом
и планом объединения в трёхслойный обход (nfqws2 + usque + VLESS). Этот файл — точка
входа, чтобы продолжить на другой машине.

---

## 0. Забрать на другой машине

```sh
git clone http://192.168.1.54:8418/admin/usque-keenetic.git
cd usque-keenetic
git checkout feature/web-ui          # вся работа здесь
```

Ветки:
- `main` — база форка (= upstream `side-effect-tm/usque-keenetic`, коммит `8071a4d`).
- `feature/web-ui` — **вся наша работа** (веб-интерфейс, OpenAPI, интеграционные патчи ядра, демо, план).

Remotes: `origin` → эта Гитея, `upstream` → github.com/side-effect-tm/usque-keenetic.

---

## 1. Что сделано (ветка `feature/web-ui`)

### Ядро `usque-keenetic` — патчи под интеграцию
`scripts/init.d/` (склеивается в `/opt/etc/init.d/S51usque` при сборке):

- Вывод демона → `/opt/var/log/usque.log` (кольцевой, лимит `LOG_MAX_BYTES=512K`) через
  сгенерированный wrapper `/opt/var/run/usque.run.sh`.
- Хуки `usque --on-connect` / `--on-disconnect` → `/opt/var/run/usque.state`
  (`STATE=connected|disconnected|unknown|stopped`, `SINCE=<epoch>`). Включаются только
  если установленный usque поддерживает флаги (`fn_supports_hooks` кэширует проверку).
- Новые подкоманды:
  - `S51usque info` — плоский `key value` дамп (сервис, туннель, iface, трафик из
    `/sys/class/net`, конфиг, маршруты). Контракт для веба.
  - `S51usque probe` — активная проверка через интерфейс: `curl --interface opkgtunN`
    на `cdn-cgi/trace` (egress IP, PoP, warp=), `ping -I` для RTT.
  - `S51usque reregister` — сброс device key + рестарт.
- Версии пишутся в `/opt/etc/usque/{version,usque-version}` при сборке.
- `scripts/ipk/postrm` чистит новые рантайм-файлы.

### Пакет `usque-keenetic-web` (новый, `Architecture: all`)
Модель как у nfqws: **ядро и веб — разные .ipk из одного репо**, веб опционален,
`Depends: usque-keenetic, php8-cgi, php8-mod-session, php8-mod-curl, curl, lighttpd, lighttpd-mod-cgi, lighttpd-mod-setenv`.

- `web/backend/index.php` — тонкий API, делегирует в `S51usque` (`cmd`-стиль как у nfqws).
- `web/public/` — SPA без сборки (vanilla JS): статус сервиса/туннеля, health-проба,
  интерфейс, трафик со спарклайном, маршруты, лог, start/stop/restart/reregister,
  правка `SNI`/`HTTP2_ENABLE`/`IFACE_IP`. RU/EN, тёмная/светлая.
- `web/openapi.yaml` — OpenAPI 3.1, весь контракт. Ставится в пакет, отдаётся на
  `http://<router>:91/openapi.yaml`, ссылка «API» в шапке.
- `web/lighttpd/81-usque.conf` — сокет `:91` (nfqws-web занимает `:90`), `cgi.assign .php`.
- `web/conf/usque_web.conf` — `[auth] enabled=true` (юзер Entware).
- `scripts/ipk-web/{conffiles,postinst,prerm,postrm}`.

### Сборка
- `make packages` = `pkg-all` (ядро mips/mipsel/aarch64) + `web`.
- `make repository` — раскладывает opkg-репо, веб под `/web/`.
- `scripts/build.sh web` — собирает веб-`.ipk` **без make** (Windows/git-bash).
- CI: `.github/workflows/{release,release-checks}.yml` обновлены под `make packages`.
- `.gitattributes` форсит LF — скрипты переживают checkout на Windows.

### Демо и план (для показа / разработки UX)
- `web/demo/index.html` — **интерактивное демо**, бэкенд имитируется в браузере. Все
  кнопки рабочие, сценарии (подключён / переподключение / туннель упал / остановлен),
  живой спарклайн, карточка **routing** с пресетами `itdoginfo/allow-domains` и правкой
  списка + настройки интерфейса. Открывать файлом.
- `web/dev/mock-server.js` — `node web/dev/mock-server.js` поднимает `web/public/`
  (реальный фронт) + мок-API на `:8419`.
- `doc/plan-3-engine.html` / `.md` — план объединения nfqws2 + usque + VLESS: оценка
  сети (авг 2026), матрица слоёв, архитектура, конфликты fwmark, план веб-интерфейса,
  «строить или взять готовое», дорожная карта, открытые решения.
- `doc/web-ui.md` — дизайн-док именно `usque-keenetic-web` (архитектура, контракт API).

### Коммиты
```
feat: add usque-keenetic-web (monitoring & control UI)       (dbdb7c8)
feat(web): add OpenAPI 3.1 spec for the dashboard API        (960b3ee)
chore: handoff — demo, 3-engine plan, dev mock, CONTINUE.md  (этот)
```

---

## 2. Локальная проверка (без роутера)

```sh
# интерактивное демо — всё работает, ничего не нужно
start web/demo/index.html          # или открыть двойным кликом

# реальный фронт против мок-бэкенда
node web/dev/mock-server.js         # → http://localhost:8419

# сборка веб-пакета
sh scripts/build.sh web             # → out/tmp/usque-keenetic-web_*.ipk

# синтаксис склеенного init-скрипта
awk 'FNR==1 && NR!=1 {next} {print}' \
  scripts/init.d/entware-head scripts/network scripts/usque \
  scripts/init.d/_shared scripts/init.d/entware-tail | sh -n -
```

---

## 3. Что НЕ проверено (нужен живой Keenetic + Entware)

- Флаги `--on-connect` / `--on-disconnect` у usque 4.2.0 (рантайм-гард есть).
- `include conf.d/*.conf` в основном `lighttpd.conf` (postinst дописывает `include_shell`).
- `php-cgi` в `/opt/bin/php-cgi`; lighttpd под root (фрагмент ставит `server.username := ""`).
- Парсинг `ndmc -c "show interface OpkgTunN"` — поля `state`/`address`.
- Сосуществование веб-стека с `nfqws-keenetic-web` (порты :90/:91 — ок; правки в коде:
  убрать `server.modules +=` из фрагмента, добавить `session_name()` в PHP — **ещё не сделано**).

Полный чек-лист — в `doc/web-ui.md`.

---

## 4. Следующий шаг — решения за владельцем

Из `doc/plan-3-engine.md`, раздел 7:

| | варианты |
|---|---|
| Плоскость решений | **HydraRoute** (реком.) / podkop / своя на nftables |
| Ядро vless | xray (xkeen) / sing-box / mihomo |
| Панель | отдельный пакет / PR в `web4static` с движком usque / растить `usque-keenetic-web` до 3 вкладок |
| VPS для vless | свой / арендный |

После выбора — Фаза 1 из дорожной карты (плоскость на HydraRoute, три списка → три цели,
проверка на роутере).

---

## 5. Зеркала референсов на этой Гитее

Если github недоступен — всё нужное уже зеркалировано (приватные, `admin/*`):

| Гитея | Оригинал | Зачем |
|---|---|---|
| `admin/nfqws2-keenetic` | github.com/nfqws/nfqws2-keenetic | движок десинка, модель split-пакетов |
| `admin/nfqws-keenetic` | github.com/nfqws/nfqws-keenetic | v1, для истории |
| `admin/nfqws-keenetic-web` | github.com/nfqws/nfqws-keenetic-web | стек веб-UI (lighttpd+PHP+React), с которого копируем |
| `admin/HydraRoute` | github.com/Ground-Zerro/HydraRoute | плоскость решений для Ф.1 |
| `admin/web4static` | github.com/spatiumstas/web4static | мульти-движковый веб-UI (прецедент) |

Зеркала — снимок на момент старта; обновить: `git remote add gh <github-url> && git fetch gh && git push origin 'refs/remotes/gh/*:refs/heads/*'`.

## 6. Полезные ссылки

- upstream: <https://github.com/side-effect-tm/usque-keenetic>
- usque: <https://github.com/Diniboy1123/usque>
- пресеты доменов: <https://github.com/itdoginfo/allow-domains>
- xkeen: <https://github.com/Corvus-Malus/XKeen>
