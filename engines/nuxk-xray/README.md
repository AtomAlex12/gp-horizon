# nuxk-xray

VLESS client for the router: the official **xray-core** binary plus nuxk's own
init script, `S52xray-nuxk`. Not a fork of xkeen — our needs are narrow.

- **xray, not sing-box**: the server is 3x-ui = xray-core, so Reality (flow,
  fingerprint, shortIds, spiderX) lines up exactly.
- **Binary**: the official release of [XTLS/Xray-core](https://github.com/XTLS/Xray-core),
  checked against its `.dgst` SHA-256; only `xray` goes to the router (≈34 MB
  on arm64), not geoip/geosite — the config doesn't use them.
- **Traffic in**: xray's own TUN inbound brings up `opkgtun1`; KeeneticOS shows
  it as `OpkgTun1`, and its DNS routing sends the VLESS list there
  (`PLANE_IFACE_VLESS`). xray's own connection to the server takes the
  ordinary default route, so there is no loop.

## Who does what

| | |
|---|---|
| nuxk-core (`internal/engine/xray`) | parses the `vless://` link or the 3x-ui subscription (its first VLESS server), renders `config.json` (the VLESS outbound + a direct one), resolves the server to an IP for nfqws2's endpoints list |
| `S52xray-nuxk` | tests the new config with `xray run -test`, swaps it in, restarts xray; the old one comes back if xray won't start or `opkgtun1` doesn't appear in 10 s |

The TUN inbound is not in `config.json`: the script writes it to `tun.json`
and runs `xray run -config tun.json -config config.json`. `xray -test` on a
config with a TUN inbound opens the device — which the running xray holds —
so a new server could never be tested while the old one runs.

## Files on the router

| | |
|---|---|
| `/opt/etc/init.d/S52xray-nuxk` | this script (Entware starts it at boot) |
| `/opt/sbin/xray` | the binary |
| `/opt/etc/xray/config.json` | the server, 0600 in a 0700 dir (holds the user id) |
| `/opt/etc/xray/tun.json`, `nuxk.meta` | TUN inbound; what `info` reports (no secrets) |
| `/opt/var/run/xray.pid`, `/opt/var/log/xray.log` | pid, log (rotated at 512 KiB) |

## Contract

```
S52xray-nuxk start|stop|restart|status
S52xray-nuxk info      # service.*, tunnel.state, iface.name, config.{server,endpoint,security,network,sni,fingerprint,flow}, traffic.*
S52xray-nuxk probe     # curl --interface opkgtun1 https://www.cloudflare.com/cdn-cgi/trace → ok, egress_ip, rtt_ms
S52xray-nuxk set-config < meta lines, "---", config.json
```

`NUXK_ROOT`, `XRAY_BIN`, `SYSNET` override paths for tests:
`sh engines/nuxk-xray/shim_test.sh`.

## Checked

On the Pi (arm64), in a throwaway container: the official v26.3.27 binary,
a local VLESS + Reality (vision) server, the real nuxk-core — link through
`PUT /api/v1/engines/xray/config`, the tunnel up, a new server while the old
one runs, the probe through the tunnel, a broken link refused with 400.

On a mipsel Keenetic (KeeneticOS 5.01, kernel 3.4): v26.3.27 (Go 1.26) dies at
start (`futexwakeup … returned -89`), v26.2.6 (Go 1.25) runs — MIPS is pinned
to 26.2.6 in `install/nuxk-lite.sh`.
The address of `opkgtun1` comes from KeeneticOS (the `OpkgTun1` interface);
without one, `curl --interface` has no source address.
