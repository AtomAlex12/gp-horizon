# nuxk Horizon

Управляющая платформа для стека обхода блокировок на роутерах **Keenetic + Entware**
(РФ, 2026). Демон-контроллер владеет состоянием и дёргает три движка; веб — отдельный
сервис; режим под каждый домен подбирается автоматически.

```
 Pi: nuxk-controller                    роутер: nuxk-core (агент)
 ├ полный веб (консоль)                 ├ движки nfqws2 / usque / xray
 ├ история, графики, логи               ├ плоскость: списки DPI/WARP/VLESS
 ├ подбор стратегий (дальше)            │  → маршрутизация Keenetic по доменам
 └ клиент агента ── REST + токен ─────► ├ /api/v1 — контракт OpenAPI
                   ◄── события SSE ──── └ /api/v1/events (лёгкий веб — запасной)
```

Контракт между ними — [`nuxk-core/api/openapi.yaml`](nuxk-core/api/openapi.yaml):
в CI проверяется, что спецификация совпадает с маршрутами агента, а типы для
веба генерируются из неё (`nuxk-web/src/lib/schema.d.ts`). Роутер работает и без
Pi: списки и намерения хранятся на нём.

## Раскладка

| Каталог | Что | Подход |
|---|---|---|
| `nuxk-core/` | агент на роутере (Go, stdlib, 1 бинарь/арх) | своё |
| `nuxk-controller/` | контроллер на Pi: веб, история, прокси к агенту | своё |
| `nuxk-web/` | UI (Svelte 5 + Vite) — сборки `full` \| `lite` | своё |
| `nuxk-plane/` | плоскость решений | форк HydraRoute, адаптация |
| `engines/nuxk-nfqws2/` | обёртка nfqws2 | форк nfqws2-keenetic |
| `engines/nuxk-usque/` | обёртка usque | наш форк (`feature/web-ui`) |
| `engines/nuxk-xray/` | обёртка xray | своя тонкая |
| `nuxk-installer/` | установка на роутер по SSH (веб-форма) | своё |
| `packaging/` | opkg-репо + Docker-образ | своё |

**Ядра движков** (`nfqws2`, `usque`, `xray-core`) — вендорим из upstream-релизов,
пинним версию. Не форкаем: гонка с ТСПУ, отставать нельзя.

## Быстрый старт (без роутера)

```sh
# контроллер + мок-движки usque и xray
cd nuxk-core
go test ./...
go run . -config testdata/nuxk.conf -debug        # :4141

# веб (в другом терминале)
cd nuxk-web
npm install
npm run dev                                        # :5173, /api проксируется на :4141
```

Открыть `http://localhost:5173` — карточки движков usque и xray, кнопки start/stop/restart;
на вкладке xray — задание сервера (ссылка `vless://` или подписка 3x-ui).
`echo down > nuxk-core/testdata/mock.state` — уронить туннель usque,
`echo down > nuxk-core/testdata/mock.xray.state` — xray.

## Бета: Raspberry Pi + роутер

Пошагово — [`docs/BETA.md`](docs/BETA.md). Коротко:

1. Роутер: компоненты «OPKG» и «Модули ядра подсистемы Netfilter», Entware, NTP.
2. Pi: `git clone … && sh deploy/pi/bootstrap.sh` — стенд на :4242, инсталлятор на :4300.
3. Форма инсталлятора: адрес роутера, SSH Entware, «Проверить роутер» →
   «Установить выбранное». Ставится только недостающее; в конце — адрес панели и токен.
4. Контроллер на Pi (:4200): токен роутера в `deploy/pi/.env`, профиль `controller`.

Инсталлятор есть и отдельным файлом (`make installer` или релиз) для Linux,
Windows и macOS — для роутера в другой сети.

## Тестовый стенд (Raspberry Pi 5)

Первый тест — `deploy/proto`: настоящие usque и nfqws2 в одном контейнере против
DPI провайдера, без роутера. Включается поэтапно.

```sh
# на Pi, один раз: модули ядра для nfqws2 (этап C)
sudo modprobe nfnetlink_queue xt_multiport xt_connbytes xt_NFQUEUE xt_CONNMARK xt_connmark nf_conntrack

git clone <repo> && cd nuxk-horizon
make proto                                   # сборка образа (версия из VERSION)
docker compose -f deploy/proto/docker-compose.yml run --rm \
  --entrypoint /opt/etc/init.d/S51usque-docker nuxk register   # один раз: регистрация WARP
docker compose -f deploy/proto/docker-compose.yml up -d
```

Дашборд — `http://<pi>:4242`. Этапы — переменные в `deploy/proto/docker-compose.yml`:

| Этап | Что включить | Что проверить |
|---|---|---|
| A | `NUXK_ENABLE_USQUE=1` (по умолчанию) | карточка usque «ok», проба `warp=on`; «Стоп» → через ≤10с ядро само не поднимает (намерение «остановлен»); `docker compose restart` → состояние восстановилось |
| B | `NUXK_ENABLE_DNS_GLUE=1` | DNS контейнера уходит в WARP: `docker compose exec nuxk /opt/etc/nuxk/routing-glue.sh show` |
| C | `NUXK_ENABLE_NFQWS2=1` | проба nfqws2 открывает заблокированный домен; в `endpoints.list` сам появился IP WARP |

Состояние ядра (`nuxk-state`) и списки nfqws2 (`nfqws2-lists`) лежат в томах и
переживают `up --build`. Сброс: `docker compose -f deploy/proto/docker-compose.yml down -v`
(удалит и регистрацию WARP).

Версия сборки видна в шапке дашборда и в `GET /api/v1/version` (с коммитом).

## Версии и релизы

- Единственный источник версии — файл [`VERSION`](VERSION) (SemVer). Сейчас
  `0.x`: API и формат `state/` ещё могут меняться. `-alpha.N` — стенд,
  `-beta.N` — роутер, `-rc.N` — кандидат.
- `scripts/version.sh set X.Y.Z` — поднять версию (VERSION + `nuxk-web/package.json`),
  затем раздел в [`CHANGELOG.md`](CHANGELOG.md).
- `make check` — всё, что гоняет CI; `make release` — бандл в `dist/`:
  ядро под mips/mipsel/aarch64/x86_64, веб full/lite, `SHA256SUMS`.
- Тег `vX.Y.Z` на `main` → GitHub Actions собирает релиз и прикладывает бандл.

## Статус — `0.2.0-beta.3`

- **Агент на роутере** — `nuxk-core`: движки с желаемым состоянием (автоперезапуск,
  повторное применение), плоскость «три списка» поверх маршрутизации Keenetic по
  доменам (только объекты `nuxk-*`, по умолчанию только план), метрики из `/proc`,
  лог в памяти и SSE.
- **Контроллер на Pi** — `nuxk-controller`: полный веб, час истории графиков, прокси
  к агенту; мастер первичной настройки (пароль admin → подключение роутера); плагины
  в том же контейнере.
- **Подбор стратегий nfqws2** — плагин GP на Pi: прогоны, результаты, применение
  стратегии на роутере кнопкой с копией конфига и откатом. Только в полной версии.
- **Вход** — на роутере `root` и пароль Entware, на контроллере `admin`; токен
  `API_TOKEN` только для программ.
- **Инсталлятор** — по SSH, ставит только недостающее; WARP по желанию.
- **Дальше** — список соединений (conntrack), пресеты antifilter / itdoginfo,
  xray на роутере, решения по доменам.

Полная презентация — артефакт «nuxk Horizon».

## Лицензия

MIT. См. [`LICENSE`](LICENSE).
