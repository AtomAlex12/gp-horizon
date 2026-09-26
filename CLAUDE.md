# CLAUDE.md — nuxk Horizon

Платформа управления обходом блокировок на роутерах Keenetic + Entware.
Владелец — Александр, начинающий DevOps. **Отвечай по-русски**, объясняй шаги
понятно, без жаргона без нужды. Код, комментарии в коде и сообщения коммитов —
по-английски; всё, что видит пользователь (веб, инсталлятор, логи для людей,
документация в `docs/`), — по-русски.

## Архитектура

```
 Pi: nuxk-controller (:4200)            роутер: nuxk-core — агент (:4141)
 ├ полный веб, час истории графиков     ├ движки nfqws2 / usque / xray (init-скрипты)
 └ прокси /api/v1 → агент (свой токен)  ├ плоскость: списки DPI / WARP / VLESS
                                        │  → маршрутизация Keenetic по доменам (RCI)
                                        └ /api/v1 (OpenAPI) + /events (SSE), лёгкий веб
```

| Каталог | Что |
|---|---|
| `nuxk-core/` | агент (Go 1.24, **только stdlib**). `internal/api` — маршруты, `core` — контроллер движков, `plane` — списки → Keenetic, `engine` — адаптеры init-скриптов, `node` — метрики `/proc`, `logbuf` — лог |
| `nuxk-core/api/openapi.yaml` | **контракт** агента — источник правды для API |
| `nuxk-controller/` | контроллер на Pi (Go, stdlib): прокси, история `/ctl/v1/*`, хост плагинов (`supervise` → `serve` + плагины, `plugin.go`) |
| `nuxk-controller/plugins/<name>/` | рецепты плагинов: `plugin.json` + `install.sh` (GP — подбор стратегий) |
| `nuxk-web/` | Svelte 5 + Vite; типы API в `src/lib/schema.d.ts` генерируются |
| `nuxk-installer/` | установка на роутер по SSH (Go, `golang.org/x/crypto` v0.44.0 — не обновлять выше без Go 1.26) |
| `engines/nuxk-nfqws2/S51nfqws2-nuxk` | прослойка над штатным пакетом nfqws2-keenetic |
| `engines/nuxk-usque/` | форк usque-keenetic (ipk для mips/mipsel/aarch64) |
| `engines/nuxk-xray/S52xray-nuxk` | init-скрипт xray (VLESS): свой TUN `opkgtunN`, конфиг от агента, проверка и откат; xray — официальный релиз XTLS, версия и хеши в `nuxk-installer/xray.go` |
| `deploy/pi/`, `deploy/proto/` | стек для Raspberry Pi: стенд, инсталлятор, контроллер |
| `docs/BETA.md` | пошаговая установка и тест на роутере |

## Команды

```sh
make check                 # всё: версия, go test/vet/gofmt, svelte-check, check:api, прослойка
make release               # полный релиз в dist/ (нужен интернет: usque из GitHub)
make installer             # инсталлятор со встроенными файлами для роутера

cd nuxk-core && go run . -config testdata/nuxk.conf -debug   # агент с мок-движками, :4141
cd nuxk-web  && npm install && npm run dev                   # веб :5173, /api → :4141
cd nuxk-web  && npm run gen:api                              # типы из openapi.yaml
cd nuxk-core && go test -race ./...
sh engines/nuxk-nfqws2/shim_test.sh
scripts/version.sh set X.Y.Z   # версия — только так (VERSION + nuxk-web/package.json)
```

Контроллер против живого роутера (мастер настройки откроется на :4200; или сразу
`AGENT_URL`+`AGENT_TOKEN`): `cd nuxk-controller && DATA_DIR=./.data LISTEN=127.0.0.1:4200 WEB_ROOT=../nuxk-web/dist go run .`

Вход: на агенте — root из Entware (`AUTH_FILES`, для разработки `testdata/shadow`:
root / nuxk-dev), на контроллере — admin из мастера. `API_TOKEN` — только для программ
(контроллер получает его через `POST /api/v1/auth/pair`).

## Правила разработки

- **API меняется только вместе с контрактом.** Новый/изменённый эндпоинт агента →
  запись в `api.Routes` (`internal/api/api.go`) + `api/openapi.yaml` + `npm run gen:api`.
  CI проверяет: `TestOpenAPIMatchesRoutes`, `TestOpenAPISchemasCoverJSONFields`, `check:api`.
- **Агент — только stdlib.** Он работает на MIPS-роутере: без сторонних зависимостей,
  без тяжёлых циклов. Опрос `/proc` дешёвый; не добавлять вызовы, которые форкают
  процессы на каждый запрос.
- **Интерфейс не показывает выдуманных данных.** Нет данных в агенте — честный пустой
  экран «появится позже». Стенд всегда подписан как стенд.
- Цвета графиков — токены `--s1/--s2/--s3` в `app.css` (проверены на различимость в
  обеих темах); новые серии — через валидатор палитры, не на глаз.
- Каждое изменение для пользователя — строка в `CHANGELOG.md` (раздел `[Unreleased]`).
- Перед пушем — `make check`. PR — в `main`; после мержа ставится тег `vX.Y.Z`.
- Не коммитить: `e0-report-*.txt` (схема сети), токены, пароли, `.env`.

## Плагины (контроллер на Pi)

- Один контейнер: `supervise` (root, без сети) → `serve` (nobody, без прав) + плагины
  (uid 0 только с правами из `plugin.json`, без `CAP_DAC_*`/`SYS_PTRACE`). Новых
  контейнеров под плагины не заводим.
- Код плагина не лежит в репозитории: `install.sh` скачивает его на устройстве с его
  релизов, с проверкой хешей. Разрешённые права — `allowedCaps` в `plugin.go`.
- В панель — только нужные вызовы API плагина (белый список, как `gpAllowed`).
- Лёгкий веб для роутера (`build:lite`, `__LITE__`) плагинов не содержит: вкладки
  только для Pi — в `PI_ONLY` в `App.svelte`, их ветки под `!__LITE__`, чтобы код
  не попал в бандл (проверка: `grep strategy-discovery dist-lite/assets/*.js` пусто).
- Стратегии на роутер — только кнопкой, через `PUT /engines/nfqws2/strategies`
  (профиль в `NFQWS_ARGS_CUSTOM`, копия конфига, откат при сбое).
- Тесты хоста плагинов — Linux-only (`supervisor_linux_test.go`); на Windows их
  собирают `GOOS=linux go test -c` и гоняют на Pi из `~` (`/tmp` там noexec).

## Безопасность роутера (основной роутер в работе!)

- На роутере **без явного согласия Александра — только чтение.**
- Нельзя без согласия: `system configuration save`, перезапуск ndm, правка
  firewall/NAT, `opkg dns-override`, изменение DNS-настроек (апстримы, DoH/DoT,
  DnsProfile0) и его собственных списков/маршрутов (`domain-list0` «AI»,
  `domain-list1` «Meta» → Wireguard1).
- nuxk трогает **только объекты `nuxk-*`** (группы `nuxk-warp`, `nuxk-vless`, цепочка
  `NUXK_V6_DENY`). Плоскость по умолчанию в режиме плана (`PLANE_APPLY=0`).
- Список nfqws2 `user.list` nuxk переписывает только при `manage_desync` (есть список DPI).
- Никогда не выводить целиком `show running-config` (там учётные данные) и не писать
  пароль Entware в файлы и команды.

## Инфраструктура

| Узел | Адрес | Заметки |
|---|---|---|
| Роутер квартира | 192.168.2.1 | Keenetic Ultra NC-1812, KeeneticOS 5.01, aarch64; SSH Entware — **порт 22**, root; RCI `127.0.0.1:79` без пароля изнутри |
| Роутер дом | 192.168.1.1 | Entware; из сети квартиры не виден |
| Raspberry Pi 5 | 192.168.2.10 | стенд :4242, инсталлятор :4300, контроллер :4200; `~/nuxk-horizon` |
| Docker-хост | 192.168.1.100 | |
| Proxmox | 192.168.1.27 | |
| NAS Synology | 192.168.1.54 | Gitea :8418 |

## Проверено на железе

RCI пишет без пароля с самого роутера; маршрутизация по доменам привязывает IP в момент
DNS-ответа, поддомены покрываются сами; IPv6 прошивка для этих групп не маршрутизирует
(поэтому `NUXK_V6_DENY`). Режим «блокировать» при падении туннеля (маршрут без `auto`) —
ещё **не проверен**.
