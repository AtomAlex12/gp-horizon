# nuxk Horizon

Обход блокировок на роутерах **Keenetic + Entware**: у каждого сайта свой путь —
**DPI** (nfqws2), **WARP** (Cloudflare) или **ваш VLESS-сервер** (xray), — и одна
понятная панель, в которой видно, что работает и почему сайт не открывается.

- **Три пути, три списка.** Домены раскладываются по спискам DPI / WARP / VLESS, а
  маршрутизацию делает сам Keenetic (его маршрутизация по доменам). nuxk трогает только
  свои объекты `nuxk-*` и по умолчанию работает в режиме плана: показывает, что сделал бы.
- **Проба по вашим сайтам.** Роутер регулярно открывает ваши сайты и говорит, где
  обрывается: блокировка по имени (нужна стратегия nfqws2) или по IP (нужен туннель).
- **Подбор стратегий nfqws2** (полная версия) — прогоны на Raspberry Pi и применение
  найденной стратегии на роутере кнопкой, с копией конфига и откатом.
- **Вход** — на роутере `root` и пароль Entware; на Pi — свой пароль администратора.

## Установка

| | **Лайт** | **Фул** |
|---|---|---|
| Где | только роутер | роутер + Raspberry Pi 4/5 (или любой 64-битный Linux с Docker) |
| Панель | лёгкая, на роутере (`:4141`) | полная, на Pi (`:4200`): история графиков, подбор стратегий, плагины |
| Ставится | одной командой на роутере | одной командой на Pi — она же ставит лайт на роутер |

Оба установщика ничего не собирают: берут готовые файлы из
[релиза на GitHub](https://github.com/AtomAlex12/nuxk-horizon/releases/latest) и сверяют
каждый с его `SHA256SUMS`, а сам `SHA256SUMS` — с подписью релиза; xray — из
официального релиза XTLS с закреплённым хешем.

### Перед установкой — роутер

1. В веб-интерфейсе Keenetic, «Управление → Параметры системы → Изменить набор
   компонентов»: **«Поддержка открытых пакетов OPKG»** и **«Модули ядра подсистемы
   Netfilter»** (без них nfqws2 не запустится).
2. [Entware](https://help.keenetic.com/hc/ru/articles/360021214160) — на USB или во
   внутреннюю память; вход по SSH именно в Entware (порт 22 или 222).
3. Синхронизация времени (NTP) — без неё не работает HTTPS.

### Лайт — на роутере

В SSH-сессии Entware:

```sh
opkg update && opkg install curl ca-certificates
curl -fsSLo /opt/tmp/nuxk-lite.sh https://github.com/AtomAlex12/nuxk-horizon/releases/latest/download/nuxk-lite.sh
sh /opt/tmp/nuxk-lite.sh
```

Установщик проверит роутер, покажет план и спросит про WARP и VLESS (оба — по желанию:
они создают интерфейс в Keenetic и сохраняют его конфигурацию). В конце — адрес панели:
`http://<роутер>:4141`, вход `root` и пароль Entware.

Дальше на роутере работает команда `nuxk`:

| | |
|---|---|
| `nuxk` | состояние nuxk и движков |
| `nuxk update` | обновиться до последнего релиза (то же делает кнопка в панели) |
| `nuxk rollback` | вернуть версию, стоявшую до последнего обновления |
| `nuxk warp` · `nuxk vless` | добавить WARP или VLESS |
| `nuxk uninstall` | удалить nuxk (настройки Keenetic остаются) |

**Обновления.** Раз в сутки роутер смотрит, нет ли нового релиза; если есть — в панели
появится «доступна X», а в «Системе» — что нового и кнопка «Обновить». Обновление
проверяет подпись релиза, перезапускает агент на несколько секунд (обход и туннели
работают дальше) и, если новая версия не ответила, само возвращает прежнюю. Проверку
можно выключить там же.

### Фул — на Raspberry Pi

Нужен Docker ([get.docker.com](https://get.docker.com)). На Pi:

```sh
curl -fsSLo nuxk-full.sh https://github.com/AtomAlex12/nuxk-horizon/releases/latest/download/nuxk-full.sh
sh nuxk-full.sh
```

Скрипт запустит контроллер из готового образа (`ghcr.io`) и предложит сразу поставить
nuxk на роутер — по SSH, пароль вы вводите самому ssh. Потом откройте
`http://<pi>:4200`: мастер попросит пароль администратора и подключит роутер.
Обновление контроллера — `sh ~/nuxk/nuxk-full.sh update` (роутер — кнопкой в панели
или той же командой); ещё `sh ~/nuxk/nuxk-full.sh router | status | uninstall`.

Подробно, с проверками и откатом на каждом шаге, — [`docs/BETA.md`](docs/BETA.md).

### Подпись релизов

`SHA256SUMS` каждого релиза подписан ключом nuxk Horizon при сборке на GitHub, и в нём
же — точный отпечаток образа контроллера. Обновление на роутере проверяет подпись ещё
старым, уже работающим агентом: подменённый релиз не встанет. Проверить релиз вручную
(OpenSSH 8.1+; файл `allowed_signers` — в `nuxk-core/internal/release/`):

```sh
ssh-keygen -Y verify -f allowed_signers -I release@nuxk-horizon -n nuxk-release -s SHA256SUMS.sig < SHA256SUMS
sha256sum -c SHA256SUMS
```

Отпечаток ключа: `SHA256:T2omf5hHIbtKjT4l5WOoKb4dj2syNWjHDDy+5j1xSr4`.

## Как это устроено

```
 Pi: nuxk-controller (фул)              роутер: nuxk-core — агент
 ├ полный веб, история графиков         ├ движки nfqws2 / usque (WARP) / xray (VLESS)
 ├ плагины (подбор стратегий — GP)      ├ списки DPI / WARP / VLESS
 └ клиент агента ── REST + ключ ──────► │  → маршрутизация Keenetic по доменам (RCI)
                   ◄── события SSE ──── └ /api/v1 (OpenAPI) + лёгкий веб
```

Контракт между ними — [`nuxk-core/api/openapi.yaml`](nuxk-core/api/openapi.yaml): в CI
проверяется, что спецификация совпадает с маршрутами агента, а типы для веба
генерируются из неё. Роутер работает и без Pi: списки и намерения хранятся на нём.

| Каталог | Что |
|---|---|
| `nuxk-core/` | агент на роутере (Go, только stdlib, один бинарник на архитектуру) |
| `nuxk-controller/` | контроллер на Pi: полный веб, история, прокси к агенту, хост плагинов |
| `nuxk-web/` | веб (Svelte 5 + Vite) — сборки `full` и `lite` |
| `engines/` | адаптеры движков: nfqws2 (над штатным nfqws2-keenetic), usque (форк usque-keenetic), xray (свой init-скрипт) |
| `install/` | установщики: `nuxk-lite.sh` (роутер), `nuxk-full.sh` (Pi) |
| `deploy/` | Docker: образ контроллера для релиза, стенд разработки на Pi, мок-стек |

Ядра движков (`nfqws2`, `usque`, `xray-core`) берутся из их релизов с закреплённой
версией, не форкаются.

## Разработка

```sh
cd nuxk-core && go run . -config testdata/nuxk.conf -debug   # агент с мок-движками, :4141
cd nuxk-web  && npm install && npm run dev                   # веб :5173, /api → :4141
make check                                                   # всё, что гоняет CI
```

`http://localhost:5173` — карточки движков, запуск и остановка, сервер xray (ссылка
`vless://` или подписка 3x-ui). `echo down > nuxk-core/testdata/mock.state` роняет
туннель usque, `mock.xray.state` — xray.

- **Стенд на Pi** из исходников (настоящие usque и nfqws2 в контейнере + контроллер):
  `sh deploy/pi/bootstrap.sh`, этапы — в `deploy/proto/docker-compose.yml`.
- **Установщики** проверяются на имитации роутера: `sh install/lite_test.sh`
  (`SH="busybox sh"` — как на роутере).

### Версии и релизы

- Версия — только в [`VERSION`](VERSION) (SemVer); `scripts/version.sh set X.Y.Z`
  поднимает её и в `nuxk-web/package.json`; изменения — в [`CHANGELOG.md`](CHANGELOG.md).
- Тег `vX.Y.Z` на `main` → GitHub Actions: `make check`, `make release` (всё, что
  скачивают установщики, и `SHA256SUMS`), образ контроллера на `ghcr.io` под arm64 и
  amd64, его отпечаток — в релиз, `SHA256SUMS` подписывается ключом из секрета
  `NUXK_SIGNING_KEY` (без него релиз не соберётся). Тег с `-` — предварительный релиз.
- Форк со своими релизами: свой ключ (`ssh-keygen -t ed25519`) — открытую часть в
  `nuxk-core/internal/release/allowed_signers` и `install/nuxk-full.sh`, закрытую — в
  секрет `NUXK_SIGNING_KEY`.

## Лицензия

MIT. См. [`LICENSE`](LICENSE).
