# nuxk-plane

The routing plane — **fork of [Ground-Zerro/HydraRoute](https://github.com/Ground-Zerro/HydraRoute)**
(Neo), adapted for nuxk Horizon.

## Why fork, not just drive

HydraRoute Neo already solves the hard part on Keenetic: DNS interception →
ipset/nftset → `fwmark` → `ip rule` → routing table, and it coexists with
Keenetic PBR. But it targets **one tunnel at a time**. We need **three targets
at once**, resolved by decision:

| List / decision | Target |
|---|---|
| `desync` | no reroute — just an ipset nfqws2 reads (`endpoints.list` + domains) |
| `warp` | `fwmark` → table → `opkgtun0` |
| `vless` | `fwmark` → table → `tun-xray` |

## Constraints (verify on device — see the plan)

- Keenetic PBR mark = low 28 bits (`0x0fffffff` mask) — do not touch.
- nfqws2 marks = `0x20000000`, `0x40000000` (masked) — do not touch.
- Only bit 28 is clearly free → the two tunnel targets need distinct
  `ip rule` selectors (two ipsets + two rules) rather than two mark bits.
- Re-apply from `/opt/etc/ndm/netfilter.d/` — Keenetic rebuilds the firewall
  and wipes foreign rules.

## Adaptation plan

1. Vendor HydraRoute Neo, `upstream` remote.
2. Config surface driven by `nuxk-core` (not hand-edited): three ipsets, three
   rules, table numbers from a probed free range.
3. `nuxk-core` calls a reload entrypoint after `POST /api/v1/apply`.
4. Endpoint hardening: `nuxk-core` feeds tunnel upstream IPs into the `desync`
   ipset so nfqws2 desyncs the tunnels' own handshakes.

## Status

Empty — vendor HydraRoute here in Ф.1 (MVP-2). First: install stock HydraRoute
on the test router and observe coexistence with nfqws2 + Keenetic PBR.
