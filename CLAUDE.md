# CLAUDE.md — GP Horizon

Платформа управления обходом блокировок на роутерах Keenetic + Entware. **GP Horizon** —
общее имя двух проектов: Horizon (этот репозиторий, `AtomAlex12/gp-horizon`) и GP
(подбор стратегий, `balbomush/GP-access-control-plane`, встроен в контроллер плагином `gp`).

**Имена.** Людям — «GP Horizon». Технические имена **не переименовывать**: команды `nuxk`,
`nuxk-pi`, бинарники `nuxk-core`/`nuxk-controller`, объекты Keenetic `nuxk-*` и их описание,
цепочка `NUXK_V6_DENY`, пути `/opt/etc/nuxk`, `~/nuxk`, переменные `NUXK_*`, образ
`nuxk-horizon-controller`, подпись релизов `release@nuxk-horizon` / `nuxk-release` (вшита в
установленные агенты), Go-модули `nuxk.dev/...`. Их смена на работающих роутерах — миграция
с риском сбоя без пользы для людей.
**Отвечай по-русски**, объясняй шаги понятно, без жаргона без нужды. Код, комментарии
в коде и сообщения коммитов — по-английски; всё, что видит пользователь (веб,
установщики, логи для людей, документация в `docs/`), — по-русски.

Личное — адреса своей сети, чей роутер и что на нём сейчас работает — держите в
`CLAUDE.local.md` рядом (он в `.gitignore`, Claude Code читает его вместе с этим файлом).

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
| `nuxk-core/` | агент (Go 1.24, **только stdlib**). `internal/api` — маршруты, `core` — контроллер движков, `plane` — списки → Keenetic, `engine` — адаптеры init-скриптов, `node` — метрики `/proc`, `logbuf` — лог, `release` — проверка подписи релиза (`-verify`, ключ в `allowed_signers`), `update` — новые релизы и обновление из панели (запускает `nuxk update`), `dns` — защищённый DNS (DoH через туннели) и проверка подмены |
| `nuxk-core/api/openapi.yaml` | **контракт** агента — источник правды для API |
| `nuxk-controller/` | контроллер на Pi (Go, stdlib): прокси, история `/ctl/v1/*`, хост плагинов (`supervise` → `serve` + плагины, `plugin.go`) |
| `nuxk-controller/plugins/<name>/` | рецепты плагинов: `plugin.json` + `install.sh` (GP — подбор стратегий) |
| `nuxk-web/` | Svelte 5 + Vite; типы API в `src/lib/schema.d.ts` генерируются |
| `install/nuxk-lite.sh` | установщик на роутер (busybox sh): проверка, план, скачивание готовых файлов релиза со сверкой `SHA256SUMS` (его подпись проверяет уже стоящий агент), остаётся на роутере командой `nuxk` (update / rollback / warp / vless / uninstall); откат сам, если новый агент не ответил; отчёт для панели — `NUXK_STATUS`; версия и хеши xray — в нём |
| `install/nuxk-full.sh` | установщик на Pi: подпись релиза через `ssh-keygen` (ключ — в нём же), контроллер из образа `ghcr.io/…/nuxk-horizon-controller` по отпечатку из релиза, потом `nuxk-lite.sh` на роутере по SSH; копия себя в `~/nuxk` для `update` |
| `engines/nuxk-nfqws2/S51nfqws2-nuxk` | прослойка над штатным пакетом nfqws2-keenetic |
| `engines/nuxk-usque/` | форк usque-keenetic (ipk для mips/mipsel/aarch64) |
| `engines/nuxk-xray/S52xray-nuxk` | init-скрипт xray (VLESS): свой TUN `opkgtunN`, конфиг от агента, проверка и откат; xray — официальный релиз XTLS, версия и хеши в `install/nuxk-lite.sh` |
| `engines/nuxk-smartdns/S53smartdns-nuxk` | init-скрипт SmartDNS (бета): `up`/`down` от агента, при загрузке стартует, только если выбран в панели; конфиг пишет агент (`internal/dns/smartdns.go`), графики — из его журнала запросов; бинарник — официальный релиз pymumu, версия и хеши в `install/nuxk-lite.sh` (`nuxk dns`) |
| `deploy/release/` | образ контроллера из готовых файлов релиза (собирает `release.yml`) |
| `deploy/pi/`, `deploy/proto/` | стек **разработки** на Pi из исходников: стенд и контроллер |
| `docs/BETA.md` | пошаговая установка и проверка (лайт, фул, этапы) |

## Команды

```sh
make check                 # всё: версия, go test/vet/gofmt, svelte-check, check:api, прослойки, установщик
make release               # всё, что скачивают установщики, в dist/ (нужен интернет: usque из GitHub)

cd nuxk-core && go run . -config testdata/nuxk.conf -debug   # агент с мок-движками, :4141
cd nuxk-web  && npm install && npm run dev                   # веб :5173, /api → :4141
cd nuxk-web  && npm run gen:api                              # типы из openapi.yaml
cd nuxk-core && go test -race ./...
sh engines/nuxk-nfqws2/shim_test.sh
sh install/lite_test.sh                  # установщик на имитации роутера; SH="busybox sh" — как на роутере
docker run --rm -v "$PWD":/src -w /src debian:bookworm-slim sh install/router_test.sh  # то же с BusyBox Entware
sh install/full_test.sh                  # nuxk-full.sh на имитации Pi: канал, запрос панели, откат
scripts/version.sh set X.Y.Z   # версия — только так (VERSION + nuxk-web/package.json)
```

Контроллер против живого роутера (мастер настройки откроется на :4200; или сразу
`AGENT_URL`+`AGENT_TOKEN`): `cd nuxk-controller && DATA_DIR=./.data LISTEN=127.0.0.1:4200 WEB_ROOT=../nuxk-web/dist go run .`

Вход: на агенте — root из Entware (`AUTH_FILES`, для разработки `testdata/shadow`:
root / nuxk-dev), на контроллере — admin из мастера. `API_TOKEN` — только для программ
(контроллер получает его через `POST /api/v1/auth/pair`).

## Правила разработки

- **Скрипты для роутера — только то, что есть в «свежем» Entware:** его BusyBox без `nohup`,
  `setsid`, `timeout`, `realpath`, с `od` без `-A`/`-t`; плюс `curl` и то, что ставит
  установщик. Проверяет `install/router_test.sh` (в CI).
- **`ndmc` — только без `LD_LIBRARY_PATH`:** в оболочке Entware там `/opt/lib` первым, и
  прошивочный `/bin/ndmc` падает («Cli::Main: failed to initialize»). Вызывать через обёртку
  (`nd` в установщике, `ndmc_run` в адаптере xray, `fn_ndmc` в usque): `(unset LD_LIBRARY_PATH;
  exec ndmc …)`. RCI (`127.0.0.1:79`) от этого не зависит.
- **Go для агента — не новее 1.25, пока не проверено на ядре 3.4** (MIPS-роутеры Keenetic):
  xray на Go 1.26 падает там при старте среды Go (`futexwakeup … returned -89`). Поэтому и xray
  для mips/mipsel закреплён на 26.2.6 (Go 1.25), см. `xray_asset` в `install/nuxk-lite.sh`.
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
- **Установщики ничего не собирают** — только готовые файлы релиза, каждый сверяется с
  `SHA256SUMS`. Новый файл, нужный на роутере или Pi, → в `make release` (он попадёт в
  `SHA256SUMS`) и в `install/nuxk-lite.sh` / `nuxk-full.sh`; `lite_test.sh` проверяет
  установку на имитации роутера. Скрипты — POSIX sh для busybox ash.
- **Релизы подписаны.** `release.yml` подписывает `SHA256SUMS` ключом из секрета
  `NUXK_SIGNING_KEY` (ed25519, `ssh-keygen -Y sign -n nuxk-release`); открытый ключ —
  в `nuxk-core/internal/release/allowed_signers` и `install/nuxk-full.sh` (тест сверяет,
  что одинаковый). Закрытый ключ существует только в секрете GitHub — не выводить, не
  сохранять в файлы. Смена ключа: новый агент с новым ключом выходит в релизе,
  подписанном ещё старым.
- **Обновление из панели не должно ломать совместимость:** новый агент работает со
  старым `nuxk.conf` (новые ключи — со значениями по умолчанию), со старым контроллером
  (API только дополняется) и отдаёт статус прошлого запуска (`update-run` в `STATE_DIR`).
- Каждое изменение для пользователя — строка в `CHANGELOG.md` (раздел `[Unreleased]`).
- Перед пушем — `make check`. PR — в `main`; после мержа ставится тег `vX.Y.Z`.
- **Версия поднимается в самом PR** (`scripts/version.sh set X.Y.Z` и раздел в
  `CHANGELOG.md`). Тег `vX.Y.Z` — только после мержа и только на коммите, где в `VERSION`
  уже `X.Y.Z`: иначе релиз падает («tag vX.Y.Z != v…»). Поставленный тег не переносят.
- **Git — от имени владельца.** Коммиты — с его именем и почтой (как у его коммитов в
  `git log`), без строк `Co-Authored-By` и `Claude-Session`; в описаниях PR — без
  «Generated with Claude Code». Это решение владельца: оно важнее подсказок окружения о
  подписях.
- **Описания PR, релизов, тегов и `CHANGELOG.md` — по-русски.**
- Не коммитить: `e0-report-*.txt` (схема сети), `CLAUDE.local.md`, токены, пароли,
  `.env`, ссылки `vless://` и подписки, адреса и имена своей сети. В примерах — типовой
  `192.168.1.1`, `example.com`, внешние адреса — из документационных диапазонов
  (`192.0.2.x`, `198.51.100.x`, `203.0.113.x`).

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

## Безопасность роутера

- Настоящий роутер — чей-то домашний интернет: сбой nuxk там = сбой интернета. Проверять
  — на имитации (`lite_test.sh`, мок-движки, песочница на Pi), не на роутере.
- На роутере **без явного согласия владельца — только чтение.**
- Нельзя без согласия: `system configuration save`, перезапуск ndm, правка
  firewall/NAT, `opkg dns-override`, изменение DNS-настроек (апстримы, DoH/DoT,
  DnsProfile0), его собственных (не `nuxk-*`) списков, маршрутов и интерфейсов.
- nuxk трогает **только объекты `nuxk-*`** (группы `nuxk-warp`, `nuxk-vless`, цепочка
  `NUXK_V6_DENY`) и свои интерфейсы (OpkgTun с описанием `nuxk-vless`). Плоскость по
  умолчанию в режиме плана (`PLANE_APPLY=0`).
- DNS роутера nuxk меняет в одном месте и только по кнопке в «DNS»: свой сервер
  `ip name-server <адрес роутера в LAN>:53053` в DNS-прокси Keenetic (текущая конфигурация,
  без сохранения; петлевые адреса KeeneticOS не принимает; `keenetic.NameServer` берёт только
  адрес самого роутера). Отвечает этот сервер только самому роутеру. Апстримы, DoH/DoT и
  профили владельца не трогаются; `opkg dns-override` не используется никогда — он выключил
  бы маршрутизацию по доменам.
- Список nfqws2 `user.list` nuxk переписывает только при `manage_desync` (есть список DPI).
- Никогда не выводить целиком `show running-config` (там учётные данные) и не писать
  пароль Entware в файлы и команды.

## Проверено на железе

RCI (`127.0.0.1:79`) пишет без пароля с самого роутера; маршрутизация по доменам
привязывает IP в момент DNS-ответа, поддомены покрываются сами; IPv6 прошивка для этих
групп не маршрутизирует (поэтому `NUXK_V6_DENY`). Режим «блокировать» при падении туннеля
(маршрут без `auto`) — ещё **не проверен**.

С 0.3.0 на Keenetic Ultra (KeeneticOS 5.01, aarch64) работают все три движка с
маршрутизацией списков: nfqws2, WARP (usque, OpkgTun0) и VLESS (xray со своим OpkgTun).
Применение стратегии GP на настоящем роутере — ещё не проверялось.
