# nuxk Horizon

Управляющая платформа для стека обхода блокировок на роутерах **Keenetic + Entware**
(РФ, 2026). Демон-контроллер владеет состоянием и дёргает три движка; веб — отдельный
сервис; режим под каждый домен подбирается автоматически.

```
┌── nuxk-web ── Svelte, full в Docker / lite на роутере ──┐
│                     HTTP/JSON · /api/v1                  │
├── nuxk-core ── Go-демон · «нерушимая платформа» ─────────┤
│   состояние · кэш решений · автоподбор · health · плоскость
├── контракт адаптера: Info · Probe · Start/Stop · ApplyRouting
│                                                          │
│   nuxk-nfqws2      nuxk-usque       nuxk-xray            │
│   десинк           MASQUE/WARP      VLESS-Reality        │
└── nuxk-plane ── форк HydraRoute · fwmark / table / ipset ┘
```

## Раскладка

| Каталог | Что | Подход |
|---|---|---|
| `nuxk-core/` | контроллер (Go, 1 бинарь/арх) | своё |
| `nuxk-web/` | UI (Svelte 5 + Vite) — сборки `full` \| `lite` | своё |
| `nuxk-plane/` | плоскость решений | форк HydraRoute, адаптация |
| `engines/nuxk-nfqws2/` | обёртка nfqws2 | форк nfqws2-keenetic |
| `engines/nuxk-usque/` | обёртка usque | наш форк (`feature/web-ui`) |
| `engines/nuxk-xray/` | обёртка xray | своя тонкая |
| `packaging/` | opkg-репо + Docker-образ | своё |

**Ядра движков** (`nfqws2`, `usque`, `xray-core`) — вендорим из upstream-релизов,
пинним версию. Не форкаем: гонка с ТСПУ, отставать нельзя.

## Быстрый старт (без роутера)

```sh
# контроллер + мок-движок usque
cd nuxk-core
chmod +x testdata/S51usque-mock
go test ./...
go run . -config testdata/nuxk.conf -debug        # :4141

# веб (в другом терминале)
cd nuxk-web
npm install
npm run dev                                        # :5173, /api проксируется на :4141
```

Открыть `http://localhost:5173` — карточка движка usque, кнопки start/stop/restart.
`echo down > nuxk-core/testdata/mock.state` — переключить состояние туннеля.

## Статус

- **Ф.0** — `engines/nuxk-usque` готов (`info`/`probe`/`reregister`, логи, hooks).
- **MVP-1 ✅** — `nuxk-core` (демон + `/api/v1` + адаптер usque + reconcile-loop),
  `nuxk-web` (Svelte-дашборд). Не собрано на этой машине — `go vet/test/build` первым делом.
- **MVP-2** — + адаптер nfqws2 + `nuxk-plane` + пресеты + укрепление транспорта. ← дальше
- Ф.3 VLESS · Ф.4 автоподбор · Ф.5 lite-веб.

Полная презентация — артефакт «nuxk Horizon».

## Лицензия

MIT. См. [`LICENSE`](LICENSE).
