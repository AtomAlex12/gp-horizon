# nuxk-core

The nuxk Horizon control daemon. Go, stdlib-only, one static binary per arch.

> **MVP-1.** Not compiled where scaffolded (no Go toolchain) — `go vet ./...`,
> `go test ./...`, `go build ./...` first. Implemented: config, `/api/v1`
> (healthz/version/status/engines*), the **usque adapter**, the reconcile loop
> + hub. Stubbed (`TODO`): nfqws2/xray adapters, nuxk-plane driver, discovery,
> lists/decisions/apply endpoints, SSE.

## Run against the mock (no router needed)

```sh
go test ./...
go run . -config testdata/nuxk.conf -debug        # uses testdata/S51usque-mock

curl -s localhost:4141/api/v1/healthz
curl -s localhost:4141/api/v1/status | jq
curl -s -X POST localhost:4141/api/v1/engines/usque/stop
curl -s -X POST localhost:4141/api/v1/engines/usque/probe | jq
echo down > testdata/mock.state    # flip the mock tunnel state, watch /status
```

With the web build: `go run . -config testdata/nuxk.conf -web ../nuxk-web/dist -debug`

Config: `-config` (default `/opt/etc/nuxk/nuxk.conf`), shell-sourceable
`KEY="value"`. Keys: `LISTEN`, `API_TOKEN`, `STATE_DIR`, `WEB_ROOT`,
`ENGINE_NFQWS2`, `ENGINE_USQUE`, `ENGINE_XRAY`.

## Cross-compile (Keenetic)

```sh
make cross     # → dist/nuxk-core-{mips,mipsel,aarch64}  (CGO off, GOMIPS=softfloat)
```

## Layout

| Path | Role |
|---|---|
| `main.go` | daemon: flags, config, wire engines, start reconciler, HTTP server |
| `internal/config` | shell-sourceable `key=value` loader |
| `internal/api` | `/api/v1` router; `/status` + `/engines` read the Hub, `/engines/{kind}` is live |
| `internal/engine` | **the adapter contract** (`Engine`: `Info`/`Probe`/`Start`/`Stop`/`ApplyRouting`) + registry + `Exec` helper |
| `internal/engine/usque` | first real adapter — shells to `S51usque` |
| `internal/core` | `Hub` (latest snapshot) + `Reconciler` (Info every 5s, Probe every 60s) |
| `internal/state` | flat-file store: `decisions.json`, `lists/*.list`, `engines/*.json` |
| `fs/` | package payload: `S99nuxk-core` init + default `nuxk.conf` |
| `api/openapi.yaml` | draft v1 contract |

## The adapter contract

`internal/engine/engine.go` is the stability boundary. Everything above it
(the API, the web, the model) and everything below it (which desync tool, which
proxy core, which routing mechanism) can change without touching that file.
