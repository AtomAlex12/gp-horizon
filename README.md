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
| `nuxk-web/` | UI (Svelte) — сборки `full` \| `lite` | своё |
| `nuxk-plane/` | плоскость решений | форк HydraRoute, адаптация |
| `engines/nuxk-nfqws2/` | обёртка nfqws2 | форк nfqws2-keenetic |
| `engines/nuxk-usque/` | обёртка usque | наш форк (`feature/web-ui`) |
| `engines/nuxk-xray/` | обёртка xray | своя тонкая |
| `packaging/` | opkg-репо + Docker-образ | своё |

**Ядра движков** (`nfqws2`, `usque`, `xray-core`) — вендорим из upstream-релизов,
пинним версию. Не форкаем: гонка с ТСПУ, отставать нельзя.

## Сборка

```sh
# контроллер под роутер
cd nuxk-core && make cross          # → dist/nuxk-core-{mips,mipsel,aarch64}

# веб
cd nuxk-web && npm i
npm run build                        # full  → dist/
npm run build -- --mode lite         # lite  → dist-lite/  (~30–40 КБ)

# пакеты
cd packaging && make opkg docker
```

CI (`.gitea/workflows/ci.yml`) собирает кросс-компиляцию и пакеты по тегу.

## Статус

- **Ф.0** — `engines/nuxk-usque` готов (`info`/`probe`/`reregister`, логи, hooks).
- **MVP-1** — `nuxk-core` + адаптер usque + full-`nuxk-web` в Docker. ← сейчас
- **MVP-2** — + адаптер nfqws2 + `nuxk-plane` + пресеты + укрепление транспорта.
- Ф.3 VLESS · Ф.4 автоподбор · Ф.5 lite-веб.

Полная презентация — артефакт «nuxk Horizon».

## Лицензия

MIT. См. [`LICENSE`](LICENSE).
