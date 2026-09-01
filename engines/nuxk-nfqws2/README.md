# nuxk-nfqws2

DPI-desync engine wrapper. **Fork of
[nfqws/nfqws2-keenetic](https://github.com/nfqws/nfqws2-keenetic)**.

- Core: `nfqws2` binary vendored from
  [bol-van/zapret2](https://github.com/bol-van/zapret2) releases. **Not forked** —
  strategies change monthly, can't fall behind.
- Wrapper: fork of `nfqws2-keenetic`, `upstream` remote, minimal adapter patches
  kept in separate files where possible.

## Adapter patches (keep small — try to upstream)

1. **`S51nfqws2 info --json`** — machine-readable state (running, pid, active
   strategies, list sizes). Candidate for upstream PR (Q6 — nfqws-keenetic-web
   would want it too). Until merged, carry as `S51nfqws2-adapter`.
2. **Two list scopes** via `--new`:
   - `desync.list` — domains/IPs the user wants desynced (own strategy).
   - `endpoints.list` — tunnel upstream IPs from `nuxk-core` (WARP / VLESS
     handshake hardening, `warp-quic` / `vless-reality-tcp` strategy snippets).
3. **Strategy snippet library** — `strategies/*.args`, community-tunable per ISP.

## Contract

nfqws2 does **not** reroute — it's a passive NFQUEUE hook on WAN egress. Its
`ApplyRouting`:
- `Domains` / `CIDRs` → `desync.list`
- `Endpoints` → `endpoints.list`
- `Strategy` → which snippet for the endpoints scope

## Coexistence

Marks `0x20000000` / `0x40000000` (masked), bits 29–30. Has `POLICY_EXCLUDE` to
leave Keenetic PBR alone. `nuxk-plane`'s marks must stay clear of these.

## Status

Empty — fork in Ф.1 (MVP-2).
