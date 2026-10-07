# nuxk-core

The GP Horizon control daemon. Go, stdlib-only, one static binary per arch.

> **0.1.0-alpha.1.** Implemented: config, `/api/v1` (healthz/version/status/
> engines*, apply, config), adapters for **usque, nfqws2, xray**, the
> **desired-state controller** + hub. Stubbed (`TODO`): nuxk-plane driver,
> discovery, lists/decisions endpoints, SSE.

## Run against the mock (no router needed)

```sh
go test ./...
go run . -config testdata/nuxk.conf -debug        # usque + xray mocks, nfqws2 off

curl -s localhost:4141/api/v1/healthz
curl -s localhost:4141/api/v1/status | jq
curl -s -X POST localhost:4141/api/v1/engines/usque/stop
curl -s -X POST localhost:4141/api/v1/engines/usque/probe | jq
echo down > testdata/mock.state    # flip the mock tunnel state, watch /status
# after a /start the engine is "managed": knock it down and the controller
# restarts it (backoff 10s → 5m) — echo down > testdata/mock.xray.state
```

With the web build: `go run . -config testdata/nuxk.conf -web ../nuxk-web/dist -debug`

Config: `-config` (default `/opt/etc/nuxk/nuxk.conf`), shell-sourceable
`KEY="value"`. Keys: `LISTEN`, `API_TOKEN` (bearer token for programs — the
controller gets it by pairing), `AUTH_USER` / `AUTH_FILES` (browser login with
the box's own account, default `root` from `/opt/etc/shadow /opt/etc/passwd`;
`AUTH_USER=off` disables it), `STATE_DIR`, `WEB_ROOT`,
`ENGINE_NFQWS2`, `ENGINE_USQUE`, `ENGINE_XRAY` (empty = disabled; a missing
script = engine not wired), `INFO_EVERY`, `PROBE_EVERY` (seconds; defaults
5 / 60 — raise `INFO_EVERY` on a slow MIPS router).

## Cross-compile (Keenetic)

```sh
make cross     # → dist/nuxk-core-{mips,mipsel,aarch64,x86_64}  (CGO off, GOMIPS=softfloat)
               #   version from ../VERSION, commit from git (-ldflags)
```

## Layout

| Path | Role |
|---|---|
| `main.go` | daemon: flags, config, wire engines, start reconciler, HTTP server |
| `internal/config` | shell-sourceable `key=value` loader |
| `internal/api` | `/api/v1` router; `/status` + `/engines` read the Hub, `/engines/{kind}` is live; state changes go through the Controller |
| `internal/engine` | **the adapter contract** (`Engine`, `Configurable`) + registry (serialises calls per engine) + `Exec` helper |
| `internal/engine/{usque,nfqws2,xray}` | adapters — shell to `S51usque` / `S51nfqws2` / `S52xray` |
| `internal/core` | `Controller` (desired state → engines: Info 5s, Probe 60s, auto-restart, restore, endpoint hardening) + `Hub` (latest snapshot) |
| `internal/state` | flat-file store: `engines/<kind>.json` (desired state, 0600), `decisions.json`, `lists/*.list` |
| `fs/` | package payload: `S99nuxk-core` init + default `nuxk.conf` |
| `api/openapi.yaml` | draft v1 contract |

## Routing plane (`internal/plane`)

nuxk doesn't run its own resolver or marks on a Keenetic: the firmware already
binds DNS answers to routes (object-group fqdn + dns-proxy route → ipset →
mark → table). The plane keeps the firmware's **`nuxk-*` objects** equal to the
desired lists through RCI (`http://127.0.0.1:79/rci/`, no password from the
router itself) and never touches anything else.

```
state/plane.json (lists: name, mode warp|vless, domains)
        │ Build: one group per routed mode, subdomains folded into parents
        ▼
nuxk-warp  → dns-proxy route → PLANE_IFACE_WARP  (OpkgTun0, usque)
nuxk-vless → dns-proxy route → PLANE_IFACE_VLESS (OpkgTun1, xray)
        + ip6tables NUXK_V6_DENY: REJECT v6 to _NDM_OGDN_6_@nuxk-*
          (the firmware routes only IPv4; clients fall back to it at once)
```

- **Plan-only by default.** `PLANE_APPLY=1` lets it change the router;
  otherwise `GET /api/v1/plane` shows the pending ops.
- **Migration from the user's own lists without overlap.** A domain still in a
  routed user group is held back as a *conflict* until removed there.
- **Order is the safety:** create/extend → routes → v6 rules → drop stale
  routes/domains → delete stale groups. Unknown firmware (not 4.x/5.x) is
  refused. A missing target interface skips that mode with a warning.

Config: `PLANE` (off | keenetic), `PLANE_APPLY` (0/1), `PLANE_V6` (deny | off),
`PLANE_IFACE_WARP`, `PLANE_IFACE_VLESS`, `PLANE_RCI`, `PLANE_EVERY` (s, 60).
API: `GET /api/v1/plane`, `GET|PUT /api/v1/plane/lists`,
`POST /api/v1/plane/import {"groups":[…],"mode":"vless"}`.

## Desired state

```
web ──PUT/POST──▶ api ──▶ Controller ──▶ state/engines/<kind>.json   (intent)
                               │
                               └─ reconcile tick ──▶ engines        (reality)
```

The API never changes an engine behind the controller's back. `start`/`stop`
record `run: true|false`, `apply` records the routing, `config` records the
engine config (after the engine accepted it). Each tick the controller:
observes all engines in parallel → on the first tick re-applies stored
routing/config the engine lost → starts/stops engines whose state differs
from the intent (backoff) → pushes the tunnels' upstream IPs into nfqws2's
endpoints list when they change.

## The adapter contract

`internal/engine/engine.go` is the stability boundary. Everything above it
(the API, the web, the model) and everything below it (which desync tool, which
proxy core, which routing mechanism) can change without touching that file.
