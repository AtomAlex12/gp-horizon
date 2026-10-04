# nuxk-smartdns

SmartDNS (beta) for the router: the official **SmartDNS** static binary from
[pymumu/smartdns](https://github.com/pymumu/smartdns) plus nuxk's own init
script, `S53smartdns-nuxk`. It answers where nuxk's built-in DNS forwarder
would — the router's LAN address, port 53053, already in Keenetic's DNS proxy
— once it's chosen in the panel («DNS» → «Настройки» → «Чем отвечать»).

- **Binary**: `smartdns-<arch>` of the pinned release (aarch64, mips, mipsel),
  checked against the SHA-256 in `install/nuxk-lite.sh`; installed by
  `nuxk dns`. SmartDNS's web UI plugin isn't used (its releases don't ship it
  for routers).
- **Way out**: DoH to the catalog's resolvers at fixed addresses (SmartDNS
  never asks DNS itself), through WARP's interface (`-interface opkgtun0`,
  SO_BINDTODEVICE); the same servers straight as `-fallback`. VLESS isn't
  used: a far server is too slow for DNS.
- **Only the router asks**: `acl-enable` + `client-rules` for the router's
  own addresses — devices ask Keenetic's DNS proxy, where domain routing is.

## Who does what

| | |
|---|---|
| nuxk-core (`internal/dns/smartdns.go`) | renders `smartdns.conf` from the panel's settings; switches between itself and SmartDNS on the same address (the old one back if the new one doesn't answer); checks it every 30 s; reads its audit log (`/tmp/smartdns-nuxk/audit.log`) for the charts |
| `S53smartdns-nuxk` | `set-config`: the new configuration swapped in, SmartDNS restarted, the old one back if it won't start; `up` / `down` from the agent; Entware's `start` at boot only while it's on (`/opt/etc/smartdns-nuxk/on`) |

## What the charts can't tell

SmartDNS's audit log has no «from the cache» flag: an answer within 1 ms is
counted as cached, and an expired answer handed out during a failure looks
the same. The panel says so.

## Tests

`sh engines/nuxk-smartdns/shim_test.sh` — the script against a fake smartdns
under a temp root (part of `make check`).
