# packaging

Builds the distributables from the monorepo.

## opkg (router)

| Package | Contents | Depends |
|---|---|---|
| `nuxk-core` | daemon binary (per arch) + `S99nuxk-core` init + default `nuxk.conf` | — |
| `nuxk-web` | `dist-lite/` → `/opt/share/www/nuxk` (served by `nuxk-core -web`) | `nuxk-core` |
| `nuxk-nfqws2` | fork of nfqws2-keenetic ipk (own repo path) | — |
| `nuxk-usque` | our usque-keenetic ipk | — |
| `nuxk-xray` | xray wrapper + vendored xray-core | — |
| `nuxk-plane` | HydraRoute fork ipk | `nuxk-nfqws2` \| tunnel engines |

opkg repo layout mirrors nfqws2-keenetic: `Packages` / `Packages.gz` per arch
dir, published to Gitea Pages or a static host.

```
make opkg              # all packages, all arches → out/opkg/
make opkg-repo         # + Packages index
```

## Docker (full web)

```
make docker            # → ghcr / gitea registry: nuxk-web:full
```

```dockerfile
# nuxk-web/Dockerfile (to add) — static SPA + tiny server, or nginx
FROM node:22-alpine AS build
WORKDIR /app
COPY nuxk-web/ .
RUN npm ci && npm run build
FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
# runtime: point at a controller
ENV NUXK_CONTROLLERS="http://192.168.1.1:4141"
```

Multi-controller: the full web reads `NUXK_CONTROLLERS` (comma-separated), one
pane over several routers (home Keenetic + other sites via Tailscale).

## CI

`.gitea/workflows/ci.yml` — cross-compile matrix on tag, build web, assemble
packages, publish.
