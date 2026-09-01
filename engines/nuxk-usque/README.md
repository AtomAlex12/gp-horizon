# nuxk-usque

WARP / MASQUE engine wrapper. **Our fork of
[side-effect-tm/usque-keenetic](https://github.com/side-effect-tm/usque-keenetic)**
— already adapted (branch `feature/web-ui`).

- Core: `usque` binary vendored from [Diniboy1123/usque](https://github.com/Diniboy1123/usque) releases.
- Wrapper: `S51usque` with `info` / `probe` / `reregister`, log capture,
  `--on-connect` / `--on-disconnect` state → `/opt/var/run/usque.state`.
- Interface: `opkgtun0` (nativetun).
- Speaks the adapter contract already — `nuxk-core`'s usque adapter is a thin
  `Exec{Script: "/opt/etc/init.d/S51usque"}` mapping `info`/`probe`/actions.

## Migration

Move the `feature/web-ui` tree here. The old `usque-keenetic-web` (lighttpd +
PHP + vanilla JS) becomes **reference** — `nuxk-web` (Svelte) + `nuxk-core` (Go)
replace it. Keep the core patches, the OpenAPI spec, the demo.

```sh
git clone -b feature/web-ui <gitea>/admin/usque-keenetic.git nuxk-usque
# then: reparent history / subtree merge into the monorepo
```

## Endpoint for hardening

MASQUE endpoint `162.159.198.0/24` (h3) — `nuxk-core` feeds this into nfqws2's
`endpoints.list` when WARP is enabled, so the QUIC handshake survives ТСПУ.
