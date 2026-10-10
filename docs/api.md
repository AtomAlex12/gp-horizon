# API

GP Horizon состоит из двух программ с HTTP API:

- **агент `nuxk-core`** на роутере — `http://<роутер>:4141/api/v1/*`. Это публичный контракт
  проекта: [`nuxk-core/api/openapi.yaml`](../nuxk-core/api/openapi.yaml) (OpenAPI 3).
  CI проверяет, что спецификация совпадает с маршрутами агента
  (`TestOpenAPIMatchesRoutes`) и описывает все поля ответов
  (`TestOpenAPISchemasCoverJSONFields`), а типы для веб-интерфейса генерируются из неё.
- **контроллер `nuxk-controller`** на Pi — `http://<pi>:4200`. Собственные маршруты
  `/ctl/v1/*` и прокси `/api/v1/*` к агенту.

Все ответы — JSON. Ошибка — `{"error": {"code": "...", "message": "..."}}` с кодом HTTP
4xx/5xx; `message` написан для человека.

---

## Авторизация

### Агент

| Способ | Кто | Как |
|---|---|---|
| Сессия браузера | человек | `POST /api/v1/auth/login` с `{"user": "root", "password": "..."}` — пароль Entware. Ответ ставит cookie сессии (`HttpOnly`). Сессия хранится в памяти агента и истекает через 12 ч без обращений. |
| Токен | программы | Заголовок `Authorization: Bearer <API_TOKEN>` из `nuxk.conf`. |
| Сопряжение | контроллер | `POST /api/v1/auth/pair` с паролем root возвращает `API_TOKEN`. Пароль не сохраняется. |

Без авторизации доступен только `GET /api/v1/healthz`. После 5 неудачных попыток входа
подряд вход с этого адреса блокируется: от 30 с, с удвоением до 15 мин.

Пример на самом роутере:

```sh
TOKEN=$(sed -n 's/^API_TOKEN="\(.*\)"$/\1/p' /opt/etc/nuxk/nuxk.conf)
curl -s -H "Authorization: Bearer $TOKEN" http://192.168.1.1:4141/api/v1/status
```

### Контроллер

Браузер входит как `admin`: `POST /ctl/v1/auth/login` с `{"user": "admin", "password": "..."}`.
Все маршруты, кроме `GET /ctl/v1/healthz`, `GET /ctl/v1/setup`, `POST /ctl/v1/setup/admin` и
входа, требуют сессии. Запросы, изменяющие состояние, принимаются только со страницы самого
контроллера (проверка `Origin`). Прокси `/api/v1/*` подставляет токен агента сам — браузер
его не получает.

---

## Агент: `/api/v1`

### Узел и журнал

| Метод и путь | Назначение |
|---|---|
| `GET /healthz` | Проверка доступности, без авторизации. |
| `POST /auth/login` · `POST /auth/logout` | Вход и выход браузера. |
| `POST /auth/pair` | Выдать токен API контроллеру, знающему пароль root. |
| `GET /version` | Версия и коммит агента. |
| `GET /info` | Узел: роутер или стенд, модель, прошивка, архитектура, аптайм; расхождение часов с эталоном (`clock_skew_s`, `clock_checked`). |
| `GET /status` | Полное состояние: все движки и маршрутизация. |
| `GET /metrics` | Сырые счётчики из `/proc` (интерфейсы, NFQUEUE, conntrack, нагрузка); скорости вычисляет клиент. |
| `GET /logs` | Журнал агента в памяти (последние 2000 записей), `?after=<seq>&limit=<n>`. |
| `GET /logs/debug` · `PUT /logs/debug` | Переключатель отладочного журнала: `{"on": true, "minutes": 30}`; выключается сам по истечении времени. |
| `GET /events` | Поток Server-Sent Events: `status` — снимок состояния при подключении и при каждом изменении, `log` — каждая новая запись журнала. |

### Движки

`{kind}` — `nfqws2`, `usque` или `xray`.

| Метод и путь | Назначение |
|---|---|
| `GET /engines` | Все движки. |
| `GET /engines/{kind}` | Один движок, прочитанный сейчас. |
| `POST /engines/{kind}/{action}` | `start`, `stop`, `restart`, `probe`, `apply`. |
| `PUT /engines/{kind}/config` | Конфигурация во время работы (xray: ссылка `vless_uri` или подписка `sub_url`). Хранится с правами `0600` и обратно не отдаётся. |
| `GET /engines/{kind}/upstream` | Куда указывает конфигурация (xray), без секретов. |
| `POST /engines/{kind}/upstream/pick` | Переключиться на другой сервер сохранённой подписки. |
| `POST /engines/{kind}/upstream/refresh` | Перечитать подписку сейчас. |
| `GET /engines/{kind}/strategies` · `PUT …` | Стратегии nfqws2 по доменам, которые поставил nuxk. Пустой список удаляет их. |
| `GET /engines/{kind}/probe-targets` · `PUT …` | Сайты пробы nfqws2; пустой список — выбор автоматически. |

### Маршрутизация

| Метод и путь | Назначение |
|---|---|
| `GET /plane` | Состояние: режим (план или применение), группы, ожидающие операции, конфликты, чужие списки. |
| `GET /plane/lists` · `PUT /plane/lists` | Списки DPI / WARP / VLESS и поведение при падении туннеля. После сохранения сверка выполняется сразу. |
| `POST /plane/import` | Скопировать собственные списки Keenetic с маршрутом в список nuxk. |

### Обновления и компоненты

| Метод и путь | Назначение |
|---|---|
| `GET /update` | Текущая версия, новейший релиз, последнее обновление из панели. |
| `POST /update` | Обновить роутер до найденного релиза (запускает `nuxk update`). |
| `POST /update/check` | Проверить наличие релиза сейчас. |
| `PUT /update/settings` | Ежедневная проверка и канал (релизы или с бетами). |
| `GET /components` | WARP, VLESS, SmartDNS, nfqws2: что установлено и что можно добавить. |
| `POST /components/{id}/install` | Добавить компонент (`nuxk <компонент> --yes` в фоне). |

### Защищённый DNS

| Метод и путь | Назначение |
|---|---|
| `GET /dns` | Включён ли, каким путём идут запросы, статистика по путям. |
| `PUT /dns/settings` | Включение, путь (`auto`, `vless`, `warp`, `direct`), серверы DoH, кэш, движок (`nuxk` или `smartdns`). |
| `POST /dns/check` | Проверка подмены: ответ роутера и обычного DNS против DoH через туннель. |
| `POST /dns/cache/flush` | Очистить кэш ответов. |
| `GET /dns/stats` | Последний час по минутам: из кэша, от сервера, устаревшие, без ответа. |
| `GET /dns/log` | Последние запросы и ответы. |

---

## Контроллер: `/ctl/v1`

| Метод и путь | Назначение |
|---|---|
| `GET /healthz` | Проверка доступности. |
| `GET /setup` · `POST /setup/admin` · `POST /setup/agent` | Мастер настройки: пароль администратора, подключение роутера. |
| `POST /auth/login` · `POST /auth/logout` | Вход и выход. |
| `GET /agent` | Подключённый роутер: адрес, доступность, сведения об узле, версия контроллера. |
| `GET /history` | История графиков за последний час (шаг 5 с). |
| `GET /logs` | Журнал контроллера и хоста плагинов, `?after=<seq>`. |
| `GET /debug` · `PUT /debug` | Отладка для всех частей сразу: контроллера, хоста плагинов и агента. |
| `GET /update` · `POST /update` · `POST /update/all` | Обновление контроллера и «Обновить всё» (сначала роутер, затем контроллер). |
| `GET /plugins` · `POST /plugins/{name}/{op}` | Плагины: `install`, `enable`, `disable`, `restart`, `rollback`. |
| `GET /plugins/{name}/log` · `GET /plugins/{name}/releases` | Журналы установки и работы плагина, доступные версии. |
| `/gp/{path}` | Прокси к API ядра GP — только разрешённые вызовы (`gpAllowed` в `gp.go`). |
| `GET /vless` · `POST /vless/sources` · `DELETE /vless/sources/{id}` · `POST /vless/sources/{id}/refresh` · `POST /vless/use` | Серверы VLESS на Pi: ссылки и подписки, выбор сервера для роутера. |

Контракт ядра GP — [`nuxk-controller/plugins/gp/openapi.json`](../nuxk-controller/plugins/gp/openapi.json)
(версия, на которой закреплён плагин).
