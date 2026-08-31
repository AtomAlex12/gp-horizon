# usque-keenetic-web — веб-интерфейс

Мониторинг и управление сервисом `usque` (Cloudflare WARP / MASQUE) на роутерах
Keenetic / Netcraze с Entware.

Модель как у `nfqws`: **ядро и веб-интерфейс — разные opkg-пакеты** из одного
репозитория. Ядро (`usque-keenetic`) ставится и работает самостоятельно; веб
(`usque-keenetic-web`) — опционально, `Depends: usque-keenetic`.

```
opkg install usque-keenetic            # только сервис
opkg install usque-keenetic-web        # + дашборд (подтянет usque-keenetic)
```

## Архитектура

```
                    ┌──────────────────────────────────────────┐
   браузер ───:91──▶│ lighttpd  (/opt/etc/lighttpd/conf.d/81-usque.conf)
                    │   /              → web/public (статика, SPA)
                    │   /api/index.php → php8-cgi                │
                    └───────────────┬──────────────────────────┘
                                    │ exec()
                    ┌───────────────▼──────────────────────────┐
                    │ /opt/etc/init.d/S51usque                  │
                    │   info   → JSON: статус, туннель, iface,  │
                    │            трафик, конфиг, маршруты       │
                    │   probe  → JSON: egress IP, PoP, RTT,warp │
                    │   start|stop|restart|reregister           │
                    └───────────────┬──────────────────────────┘
                                    │
      ┌─────────────────────────────┼───────────────────────────────┐
      ▼                             ▼                               ▼
/opt/var/run/usque.state    /sys/class/net/opkgtunN/*      ndmc -c "show interface …"
(--on-connect / -disconnect)   statistics/*                 (состояние NDM-интерфейса)
/opt/var/log/usque.log
```

Вся логика сбора состояния — в `S51usque` (shell). PHP — тонкая прослойка:
парсит `cmd`, дергает init-скрипт, отдаёт JSON. Один источник правды, что бы
`S51usque info` в консоли и дашборд показывали одно и то же.

## Что изменяется в ядре (интеграция)

| Изменение | Зачем |
|---|---|
| stdout/stderr демона → `/opt/var/log/usque.log` (кольцевой, лимит `LOG_MAX_BYTES`) | сейчас лог-файл объявлен, но пуст — писать некуда |
| `usque --on-connect` / `--on-disconnect` → `/opt/var/run/usque.state` | достоверное состояние туннеля, а не «процесс жив» |
| `S51usque info` — JSON-состояние | машиночитаемо, общий контракт с вебом |
| `S51usque probe` — активная проверка через интерфейс | `warp=on`, egress IP, PoP Cloudflare, RTT |
| `S51usque reregister` | сброс device key из UI (с подтверждением) |

Формат `/opt/var/run/usque.state`:
```sh
STATE=connected        # connected | disconnected | unknown
SINCE=1756668000       # epoch, момент последней смены STATE
EVENT=on-connect       # последнее событие от usque
```

## Контракт API

`POST /api/index.php`, тело `application/x-www-form-urlencoded`, поле `cmd`.
Ответ — `application/json`. При `auth.enabled=true` все команды кроме `login`
требуют сессию (логин/пароль пользователя Entware, как в nfqws-keenetic-web).

| cmd | тело | ответ |
|---|---|---|
| `status` | — | `info` + `web_version`, `anonym` |
| `probe` | — | `{ok, egress_ip, warp, colo, loc, rtt_ms, ts}` |
| `log` | `lines?` | `{status, content}` (последние N строк, новые сверху) |
| `config_get` | — | `{sni, http2, iface_ip, iface}` |
| `config_set` | `sni?, http2?, iface_ip?` | `{status, output}` (валидация + restart) |
| `start` / `stop` / `restart` | — | `{status, output[]}` |
| `reregister` | — | `{status, output[]}` |
| `login` | `user, password` | `{status}` |
| `logout` | — | `{status}` |

Ответ `status`:
```jsonc
{
  "status": 0,
  "anonym": false,
  "web_version": "0.1.0",
  "name": "USQUE",
  "version": { "pkg": "0.4.0", "usque": "4.2.0", "config": 1 },
  "service": { "running": true, "pid": 1234, "uptime_s": 3600 },
  "tunnel":  { "state": "connected", "since": 1756668000, "endpoint": "162.159.198.2" },
  "iface":   { "name": "opkgtun0", "ndm_name": "OpkgTun0", "label": "usque",
               "ip": "172.16.1.100", "mtu": 1280, "link": "up", "ndm_state": "up" },
  "traffic": { "rx_bytes": 0, "tx_bytes": 0, "rx_packets": 0, "tx_packets": 0,
               "rx_errors": 0, "tx_errors": 0, "rx_dropped": 0, "tx_dropped": 0 },
  "config":  { "sni": "ozon.ru", "http2": 0, "iface_ip": "" },
  "routes":  { "count": 12, "list": ["104.16.0.0/13", "..."] }
}
```

## Экраны (MVP)

1. **Статус** — сервис up/down, PID, uptime, версии usque/пакета/конфига,
   состояние туннеля («connected since …»).
2. **Здоровье** — кнопка/автопробинг: egress IP, PoP Cloudflare (colo/loc),
   `warp=on/off`, RTT. Доступность endpoint `162.159.198.1/2`.
3. **Интерфейс** — имя `opkgtunN` / метка `usque`, IP, MTU, link, состояние NDM.
4. **Трафик** — rx/tx всего, текущая скорость, спарклайн (~2 мин), errors/drops.
5. **Маршруты** — сколько префиксов направлено в интерфейс, список
   (`ip route show dev opkgtunN`).
6. **Управление** — start / stop / restart / re-register (с подтверждением).
7. **Логи** — хвост `usque.log`, автообновление.
8. **Конфиг** — правка `SNI`, `HTTP2_ENABLE`, `IFACE_IP` → save + restart.

Фаза 2: соединения через туннель (`conntrack -L`, top talkers), cron-watchdog с
авто-рестартом по провалу пробинга, несколько `opkgtunN`, история графиков.

## Frontend

Без сборки: `index.html` + `app.js` + `style.css` (vanilla JS, ~1 canvas для
спарклайна). Поллинг `status` раз в 2 с, `probe` раз в 15 с, `log` раз в 5 с при
открытой панели. Тёмная/светлая тема по `prefers-color-scheme`. i18n ru/en.

Причина отказа от React/Vite (в отличие от nfqws-keenetic-web): MVP — 8 виджетов
на одном опросе, сборочный тулчейн в CI не оправдан. Точка расширения оставлена:
если UI разрастётся — заменяется на Preact без изменения бэкенда.

## Пакет `usque-keenetic-web`

```
Architecture: all
Depends: usque-keenetic, php8-cgi, php8-mod-session, php8-mod-curl,
         lighttpd, lighttpd-mod-cgi, lighttpd-mod-setenv, lighttpd-mod-rewrite
```

Раскладка на устройстве:
```
/opt/share/www/usque/                 index.html, app.js, style.css, api/index.php
/opt/etc/lighttpd/conf.d/81-usque.conf socket :91, cgi.assign .php
/opt/etc/usque_web.conf                [auth] enabled=true
```

`postinst`: патч `PROCS` в `S80lighttpd` (конфликт с webdav Keenetic, как в
nfqws), рестарт lighttpd, печать `http://<router_ip>:91`.

Порт `:91` — что бы не конфликтовать с `nfqws-keenetic-web` (`:90`).
