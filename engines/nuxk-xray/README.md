# nuxk-xray

VLESS-Reality engine wrapper. **Own, thin** (~200 lines) — not a fork of xkeen
(too opinionated; our needs are narrow).

- Core: `xray-core` binary vendored from
  [XTLS/Xray-core](https://github.com/XTLS/Xray-core) releases.
  **xray, not sing-box** (Q2): the server is 3x-ui = xray-core → zero Reality
  drift (flow, fingerprint, shortIds, spiderX line up exactly). sing-box is the
  documented fallback.
- Interface: `tun-xray` (TUN or tproxy — decide on device).

## Client only (Q3)

The user already runs a **3x-ui** server. No server-side work. `nuxk-xray`:

1. Takes a `vless://…` link **or** a 3x-ui subscription URL (`nuxk-core`
   `PUT /api/v1/engines/xray/config`).
2. Parses it → generates `config.json` (outbound VLESS-Reality + `tun`/`dokodemo`
   inbound + minimal routing — the real routing is `nuxk-plane`).
3. `S52xray start|stop|restart|info|probe` — same flat `key value` contract as
   `S51usque`.
4. `probe` = `curl --interface tun-xray https://…/cdn-cgi/trace` + ping to the
   server IP.

## Config surface

```
VLESS_URI="vless://uuid@host:443?type=tcp&security=reality&..."
# or
SUB_URL="https://panel.example/sub/xxxx"        # 3x-ui subscription, auto-refresh
```

## Endpoint for hardening

The server's IP → `nuxk-core` feeds it into nfqws2's `endpoints.list` with the
`vless-reality-tcp` strategy, so the Reality handshake to a fresh IP survives
behavioural ТСПУ.

## Status

Empty — Ф.3.
