# nuxk-core

The nuxk Horizon control daemon. Go, stdlib-only, one static binary per arch.

> **Skeleton.** No Go toolchain was available where this was scaffolded — run
> `go build ./...` and `go vet ./...` first. `main.go` serves the API and
> (optionally) the web build; engine adapters, the reconcile loop, discovery
> and the plane driver are stubbed with `TODO`.

## Run

```sh
go run . -listen 127.0.0.1:4141 -debug
# or with the web build:
go run . -web ../nuxk-web/dist -debug

curl -s localhost:4141/api/v1/healthz
curl -s localhost:4141/api/v1/status | jq
```

Config: `-config` (default `/opt/etc/nuxk/nuxk.conf`), shell-sourceable
`KEY="value"`. Keys: `LISTEN`, `API_TOKEN`, `STATE_DIR`, `WEB_ROOT`,
`ENGINE_NFQWS2`, `ENGINE_USQUE`, `ENGINE_XRAY`.

## Cross-compile (Keenetic)

```sh
make cross     # → dist/nuxk-core-{mips,mipsel,aarch64}
```

```makefile
# Makefile (to add)
LDFLAGS := -s -w -X main.version=$(VERSION)
cross:
	CGO_ENABLED=0 GOOS=linux GOARCH=mips   GOMIPS=softfloat go build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-mips   .
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-mipsel .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64                   go build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-aarch64 .
```

## Layout

| Path | Role |
|---|---|
| `main.go` | daemon: flags, config, HTTP server, graceful shutdown |
| `internal/config` | shell-sourceable `key=value` loader |
| `internal/api` | `/api/v1` router, handlers, auth, SPA fallback |
| `internal/engine` | **the adapter contract** (`Engine`: `Info`/`Probe`/`Start`/`Stop`/`ApplyRouting`) + registry + `Exec` helper |
| `internal/state` | flat-file store: `decisions.json`, `lists/*.list`, `engines/*.json` |
| `api/openapi.yaml` | draft v1 contract |

## The adapter contract

`internal/engine/engine.go` is the stability boundary. Everything above it
(the API, the web, the model) and everything below it (which desync tool, which
proxy core, which routing mechanism) can change without touching that file.

Adapters live in the `engines/nuxk-*` packages and are thin: shell to the
engine's `S51*` init script (`Exec` helper), parse its `info`/`probe` output,
write its list files. Wired in `main.go`'s registry as they land
(MVP-1: usque → MVP-2: + nfqws2 → Ф.3: + xray).
