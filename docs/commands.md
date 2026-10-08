# Справочник команд

Все команды, которые GP Horizon устанавливает на роутер и на Raspberry Pi, их параметры и
переменные окружения, а также команды разработки. Описание соответствует коду ветки `main`;
при расхождении источником правды считается код (`install/nuxk-lite.sh`,
`install/nuxk-full.sh`, `nuxk-core/main.go`, `nuxk-controller/main.go`).

- [Роутер: команда `nuxk`](#роутер-команда-nuxk)
- [Роутер: службы (init-скрипты)](#роутер-службы-init-скрипты)
- [Роутер: агент `nuxk-core`](#роутер-агент-nuxk-core)
- [Роутер: диагностика](#роутер-диагностика)
- [Raspberry Pi: `nuxk-full.sh` и `nuxk-pi`](#raspberry-pi-nuxk-fullsh-и-nuxk-pi)
- [Raspberry Pi: контейнер и служба обновления](#raspberry-pi-контейнер-и-служба-обновления)
- [Контроллер `nuxk-controller`](#контроллер-nuxk-controller)
- [Разработка](#разработка)

> Технические имена (`nuxk`, `nuxk-pi`, `nuxk-core`, `nuxk-controller`, пути `/opt/etc/nuxk`,
> `~/nuxk`, переменные `NUXK_*`) сохранены после переименования проекта из nuxk Horizon в
> GP Horizon: их смена на работающих установках не даёт пользы и несёт риск сбоя.

---

## Роутер: команда `nuxk`

Устанавливается установщиком лайт (`nuxk-lite.sh`) в `/opt/bin/nuxk` — это копия того же
скрипта. Запускается в оболочке Entware от `root`.

```
nuxk [install | update | rollback | nfqws2 | warp | vless | dns | status | uninstall] [--yes] [--with-warp] [--with-vless]
```

| Команда | Действие |
|---|---|
| `nuxk` или `nuxk status` | Состояние: версия `nuxk-core` и работает ли агент (с адресом панели), состояние движков nfqws2 / WARP / VLESS, режим маршрутизации («применяется» или «режим плана»). Сеть не требуется. |
| `nuxk install` | Установка или повторная установка. Скачанный `nuxk-lite.sh` без аргументов выполняет именно её. Проверяет роутер (Entware, архитектуру, свободное место, модули Netfilter), показывает план и спрашивает про nfqws2, WARP и VLESS. |
| `nuxk update` | Обновление до последнего **стабильного** релиза: агент, веб-интерфейс, адаптеры движков и — если сменилась закреплённая версия — xray и SmartDNS. `nuxk.conf` не изменяется. Если установленная версия новее последнего стабильного релиза (например, стоит бета), ничего не меняет. Если новая версия агента не ответила за 15 с, возвращается прежняя. Конкретную версию, в том числе бету, задаёт `NUXK_VERSION`. |
| `nuxk rollback` | Возврат версии, стоявшей до последнего обновления: агента, init-скрипта, адаптера nfqws2 и веб-интерфейса из `/opt/var/lib/nuxk/prev`. Сама команда `nuxk` остаётся новой. Сеть не требуется. |
| `nuxk nfqws2` | Установка пакета nfqws2-keenetic из его репозитория и подключение к агенту. |
| `nuxk warp` | Установка WARP (`usque-keenetic` из релиза GP Horizon): регистрация в Cloudflare WARP, создание интерфейса OpkgTun и сохранение конфигурации роутера. |
| `nuxk vless` | Установка xray-core из официального релиза XTLS (с проверкой закреплённого хеша), создание интерфейса OpkgTun с описанием `nuxk-vless` и сохранение конфигурации роутера. |
| `nuxk dns` | Установка SmartDNS (бета) из релиза pymumu/smartdns с проверкой хеша. Сам SmartDNS не запускается — его выбирают в панели: «DNS» → «Настройки». |
| `nuxk uninstall` | Удаление агента, веб-интерфейса, адаптеров и объектов `nuxk-*` в Keenetic. Конфигурация и списки переносятся в `/opt/etc/nuxk.removed`. Спрашивает, удалять ли xray и его интерфейс. Пакеты `usque-keenetic` и `nfqws2-keenetic` удаляются отдельно (`opkg remove …`). |

`nuxk warp`, `nuxk vless`, `nuxk dns` и `nuxk nfqws2` выполняют то же, что кнопки «Система» →
«Компоненты» в панели. Панель запускает команду с ключом `--yes` в фоне и показывает её ход.

### Параметры

| Параметр | Значение |
|---|---|
| `--yes`, `-y` | Не задавать вопросов, принимать ответы по умолчанию. |
| `--with-warp` | При установке сразу поставить WARP. |
| `--with-vless` | При установке сразу поставить VLESS (xray). |
| `-h`, `--help` | Краткая справка. |

### Переменные окружения

| Переменная | Значение |
|---|---|
| `NUXK_VERSION` | Устанавливаемая версия, например `NUXK_VERSION=0.5.0-beta.11 nuxk update`. Допускается и более старая версия, чем установленная. |
| `NUXK_REPO` | Репозиторий релизов на GitHub, по умолчанию `AtomAlex12/gp-horizon`. |
| `NUXK_BASE_URL` | Зеркало файлов релиза вместо GitHub. Файлы по-прежнему сверяются с `SHA256SUMS` и подписью. |
| `NO_COLOR` | Вывод без цвета. |

Переменные `NUXK_STATUS`, `NUXK_FROM`, `NUXK_STARTED` и `NUXK_TASK` устанавливает панель при
запуске обновления или установки компонента; вручную их задавать не нужно.

### Проверки при установке и обновлении

- Каждый скачанный файл сверяется с `SHA256SUMS` релиза; при несовпадении установка
  прекращается до записи файлов.
- `SHA256SUMS` проверяется по подписи релиза (`SHA256SUMS.sig`). Проверку выполняет агент,
  уже установленный на роутере (`nuxk-core -verify`), поэтому подменённый релиз не будет
  установлен. Агенты старше механизма подписей сверяют только хеши, о чём установщик
  сообщает отдельно.
- xray и SmartDNS скачиваются из релизов их авторов и проверяются по хешам, закреплённым в
  установщике.

---

## Роутер: службы (init-скрипты)

| Скрипт | Подкоманды | Назначение |
|---|---|---|
| `/opt/etc/init.d/S99nuxk-core` | `start` `stop` `restart` `status` | Агент `nuxk-core`. Ограничение кучи — `GOMEMLIMIT=48MiB` (переопределяется переменной окружения). |
| `/opt/etc/init.d/S51nfqws2` | `start` `stop` `restart` `reload` `status` | Штатный init-скрипт пакета nfqws2-keenetic. `stop` полностью отключает обход DPI. |
| `/opt/etc/nuxk/engines/S51nfqws2-nuxk` | `start` `stop` `restart` `reload` `status` — передаются штатному скрипту; `info` `probe` `apply-desync` `apply-endpoints` `apply-strategies` — для агента | Адаптер nuxk поверх штатного пакета: не изменяет пакет, работает с его файлами. |
| `/opt/etc/init.d/S51usque` | `start` `stop` `restart` `status` `reregister` `info` `probe` | WARP-клиент usque (пакет `usque-keenetic`). `reregister` — новая регистрация в WARP. |
| `/opt/etc/init.d/S52xray-nuxk` | `start` `stop` `restart` `status` `info` `probe` `set-config` | xray-core как клиент VLESS. `set-config` проверяет новую конфигурацию самим xray и при неудачном запуске возвращает прежнюю. |
| `/opt/etc/init.d/S53smartdns-nuxk` | `up` `down` `start` `stop` `restart` `status` `info` `set-config` | SmartDNS (бета). `up`/`down` — включение и выключение агентом; при загрузке роутера `start` запускает SmartDNS только если он включён. |

Подкоманды `info`, `probe`, `apply-*` и `set-config` — служебный интерфейс между агентом и
движком (строки вида `ключ значение`). Вызывать их вручную допустимо для диагностики, но
изменять конфигурацию через них не следует: агент восстановит желаемое состояние.

---

## Роутер: агент `nuxk-core`

Исполняемый файл — `/opt/usr/bin/nuxk-core`, запускается init-скриптом с параметрами
`-config /opt/etc/nuxk/nuxk.conf -log /opt/var/log/nuxk-core.log`.

| Флаг | Значение |
|---|---|
| `-config PATH` | Файл конфигурации, по умолчанию `/opt/etc/nuxk/nuxk.conf` ([описание ключей](configuration.md#роутер-nuxkconf)). |
| `-listen ADDR` | Переопределить адрес API (`LISTEN`). |
| `-web DIR` | Каталог веб-интерфейса (`WEB_ROOT`); пусто — только API. |
| `-log PATH` | Файл журнала с ротацией внутри процесса: не более 2 × 512 КиБ. Пусто — stderr. |
| `-debug` | Отладочный журнал на всё время работы процесса, в том числе в файл. Для временной отладки используйте переключатель «Отладка» в панели. |
| `-version` | Вывести `nuxk-core <версия> <коммит>` и завершиться. |
| `-verify FILE` | Проверить подпись `FILE` по `FILE.sig` ключом релизов GP Horizon. Код выхода 0 — подпись верна, 1 — нет. |

---

## Роутер: диагностика

| Команда | Что показывает |
|---|---|
| `nuxk` | Сводное состояние nuxk, движков и маршрутизации. |
| `tail -f /opt/var/log/nuxk-core.log` | Журнал агента (info и выше). Подробный журнал — «Логи» → «Отладка» в панели. |
| `cat /opt/var/log/nuxk-core.crash` | Аварийный вывод агента (stderr при падении), если он есть. |
| `/opt/etc/init.d/S99nuxk-core status` | Запущен ли агент и его PID. |
| `curl -s http://<адрес-LAN>:4141/api/v1/healthz` | Отвечает ли API агента (`{"status":"ok"}`, без авторизации). |
| `/opt/etc/nuxk/engines/S51nfqws2-nuxk info` | Состояние nfqws2 так, как его видит агент. |

Конфигурационный файл `/opt/etc/nuxk/nuxk.conf` содержит токен API (`API_TOKEN`). Не
публикуйте его содержимое целиком.

---

## Raspberry Pi: `nuxk-full.sh` и `nuxk-pi`

Установщик полной версии. После первой установки его копия хранится в `~/nuxk/nuxk-full.sh`,
а команда `/usr/local/bin/nuxk-pi` (ставится вместе со службой обновления, требует `sudo`
один раз) вызывает эту копию.

```
sh nuxk-full.sh [install | update | router | status | uninstall] [--yes] [--no-router] [--purge]
nuxk-pi        [install | update | router | status | uninstall] [--yes] [--no-router] [--purge]
```

| Команда | Действие |
|---|---|
| `sh nuxk-full.sh` (`install`) | Установка или обновление контроллера: проверка Pi (64-битный Linux, Docker), запуск образа `ghcr.io/atomalex12/nuxk-horizon-controller` по отпечатку из релиза, установка службы обновления и команды `nuxk-pi`. Затем предлагает поставить или обновить nuxk на роутере. |
| `nuxk-pi update` | Обновление до новейшей версии **того же канала**, что установлена: стоит бета — учитываются и беты, стоит стабильная — только стабильные. Более старую версию не устанавливает. Если новый контроллер не ответил за 60 с, возвращается прежний. Конкретная версия — `NUXK_VERSION=X nuxk-pi update`. |
| `nuxk-pi router` | Установка или обновление nuxk на роутере по SSH: на роутере запускается `nuxk-lite.sh` того же релиза после проверки его хеша. Пароль Entware вводится в `ssh` и скрипту недоступен. |
| `nuxk-pi status` | Работает ли контроллер, какой образ и адрес панели. |
| `nuxk-pi uninstall` | Остановка и удаление контроллера и службы обновления. Данные (пароль администратора, подключение к роутеру, плагины) сохраняются. На роутере nuxk остаётся — удаляется командой `nuxk uninstall`. |
| `nuxk-pi uninstall --purge` | То же вместе с данными контроллера и каталогом `~/nuxk`. |

Подкоманду `panel-update` вызывает служба `nuxk-update.service` по запросу из панели
(«Обновить» / «Обновить всё»); вручную её не используют.

### Параметры

| Параметр | Значение |
|---|---|
| `--yes`, `-y` | Не задавать вопросов, принимать ответы по умолчанию. |
| `--no-router` | Не предлагать установку на роутер. |
| `--purge` | С `uninstall`: удалить и данные контроллера. |
| `-h`, `--help` | Краткая справка. |

### Переменные окружения

| Переменная | По умолчанию | Значение |
|---|---|---|
| `NUXK_VERSION` | — | Устанавливаемая версия. |
| `NUXK_CHANNEL` | по установленной версии | `stable` или `beta` — канал для `update`. |
| `NUXK_REPO` | `AtomAlex12/gp-horizon` | Репозиторий релизов. |
| `NUXK_DIR` | `~/nuxk` | Каталог установки. |
| `NUXK_PORT` | `4200` | Порт панели на Pi. |
| `NUXK_IMAGE` | из релиза | Другой образ контроллера. |
| `NUXK_PULL` | `1` | `0` — не скачивать образ (используется локальный). |
| `NUXK_BASE_URL` | GitHub | Зеркало файлов релиза. |
| `NUXK_API` | `https://api.github.com` | API GitHub (или зеркало) для списка релизов. |
| `NO_COLOR` | — | Вывод без цвета. |

---

## Raspberry Pi: контейнер и служба обновления

| Объект | Назначение |
|---|---|
| `~/nuxk/docker-compose.yml` | Описание контейнера `nuxk-controller`. Перезаписывается установщиком при каждом обновлении — правки в нём не сохраняются. |
| `~/nuxk/nuxk-full.sh` | Копия установщика, которую вызывает `nuxk-pi`. |
| `~/nuxk/update/` | Запросы панели на обновление (`inbox/`), статус и журнал последнего обновления. |
| `/etc/systemd/system/nuxk-update.path`, `nuxk-update.service` | Служба, которая по запросу панели выполняет `nuxk-full.sh panel-update`. Сам контроллер доступа к Docker не имеет. |
| `/usr/local/bin/nuxk-pi` | Команда `nuxk-pi`. |

Полезные команды:

```sh
docker logs --tail 100 nuxk-controller            # журнал контроллера (info и выше)
docker compose -f ~/nuxk/docker-compose.yml ps     # состояние контейнера
docker compose -f ~/nuxk/docker-compose.yml restart
systemctl status nuxk-update.path                  # служба обновления
journalctl -u nuxk-update.service -n 50            # журнал последних обновлений из панели
```

Контейнер запускается с файловой системой только для чтения, без всех привилегий, кроме
перечисленных в `cap_add`, и с `no-new-privileges`. Подробнее — в
[модели безопасности](security.md#контроллер-на-pi).

---

## Контроллер `nuxk-controller`

Исполняемый файл в образе — `/usr/bin/nuxk-controller`.

| Режим | Назначение |
|---|---|
| `nuxk-controller supervise` | Режим контейнера (по умолчанию). Процесс с правами root без сетевого API: запускает `serve` от пользователя `nobody` без привилегий и плагины — каждый только с правами из своего `plugin.json`. |
| `nuxk-controller` / `nuxk-controller serve` | Веб-интерфейс и API без хоста плагинов. |
| `nuxk-controller -version` | Версия и коммит. |

Переменные окружения — в [справочнике конфигурации](configuration.md#контроллер-переменные-окружения).

---

## Разработка

### Make

| Цель | Действие |
|---|---|
| `make check` | Всё, что выполняет CI: версия, `gofmt`, `go vet`, тесты агента и контроллера, проверка типов веба, `check:api`, тесты установщиков и адаптеров. |
| `make release` | Сборка релиза в `dist/nuxk-horizon-<версия>/`: агент под mips, mipsel, aarch64 и x86_64, веб `full` и `lite`, init-скрипты и адаптеры, пакеты `usque-keenetic` под три архитектуры роутеров, контроллер под arm64 и amd64, установщики, `SHA256SUMS`. Требуется доступ в интернет. |
| `make dev` | Мок-стек в Docker (`deploy/dev`): агент с движками-заглушками и веб на `:4141`. |
| `make pi` | Стек разработки на Pi из исходников (`deploy/pi`). |
| `make proto` | Образ прототипа с настоящими usque и nfqws2 (`deploy/proto`, arm64). |
| `make version` | Текущая версия. |
| `make clean` | Удалить `dist/`. |

### Агент и контроллер

```sh
cd nuxk-core && go run . -config testdata/nuxk.conf -debug   # агент с движками-заглушками на :4141
cd nuxk-core && go test -race ./...
cd nuxk-controller && go test ./...                          # тесты хоста плагинов — только Linux
```

Под Windows тесты, запускающие shell-скрипты, и тесты хоста плагинов не выполняются; их
собирают `GOOS=linux go test -c` и запускают на Linux.

### Веб

| Команда (в `nuxk-web/`) | Действие |
|---|---|
| `npm install` | Зависимости. |
| `npm run dev` | Сервер разработки на `:5173`, запросы `/api` проксируются на `:4141`. |
| `npm run build` | Проверка типов и сборка полной версии в `dist/`. |
| `npm run build:lite` | Сборка лёгкой версии для роутера в `dist-lite/` (без вкладок и кода плагинов). |
| `npm run check` | Проверка типов (`svelte-check`). |
| `npm run gen:api` | Типы из контракта агента `nuxk-core/api/openapi.yaml` → `src/lib/schema.d.ts`. |
| `npm run gen:gp` | Типы из контракта GP `nuxk-controller/plugins/gp/openapi.json` → `src/lib/gp-schema.d.ts`. |
| `npm run check:api` | Перегенерация типов и проверка, что они совпадают с закоммиченными. |

### Установщики и адаптеры

```sh
sh install/lite_test.sh          # nuxk-lite.sh на имитации роутера
sh install/full_test.sh          # nuxk-full.sh на имитации Pi
sh engines/nuxk-nfqws2/shim_test.sh
sh engines/nuxk-xray/shim_test.sh
sh engines/nuxk-smartdns/shim_test.sh
# скрипты роутера с настоящим BusyBox из Entware — в одноразовом контейнере:
docker run --rm -v "$PWD":/src -w /src debian:bookworm-slim sh install/router_test.sh
```

### Версия

```sh
scripts/version.sh set X.Y.Z     # VERSION и nuxk-web/package.json
```

Тег `vX.Y.Z` на `main` запускает сборку релиза в GitHub Actions. Подробнее — в
[README](../README.md#версии-и-релизы).
