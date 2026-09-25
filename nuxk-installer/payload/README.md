# payload

Filled by `make installer` at build time and embedded into the binary:

| File | What |
|---|---|
| `VERSION` | nuxk version these files belong to |
| `nuxk-core-{mips,mipsel,aarch64,x86_64}` | controller binary per router arch |
| `S99nuxk-core` | Entware init script |
| `S51nfqws2-nuxk` | adapter shim over the stock nfqws2-keenetic package |
| `web/` | nuxk-web lite build |
| `usque-keenetic-{mips,mipsel,aarch64}.ipk` | WARP engine (engines/nuxk-usque), optional: without it the WARP step is skipped |

Only this README is committed; the rest is build output (see `.gitignore`).
