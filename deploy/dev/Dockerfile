# nuxk Horizon — dev / e2e image.
#
# Builds nuxk-core + nuxk-web and runs them together against the S51usque mock,
# so the full stack (reconcile loop, /api/v1, dashboard) can be exercised without
# a router. NOT a production image — the router ships an opkg package, not this.
#
#   docker compose -f deploy/dev/docker-compose.yml up --build
#   open http://localhost:4141

# --- stage: web -------------------------------------------------------------
FROM node:22-alpine AS web
WORKDIR /src
# package-lock.json is deferred from the repo until first release — use `npm
# install`, not `npm ci`. The lockfile is copied when present (reproducible),
# ignored when not.
COPY nuxk-web/package.json nuxk-web/package-lock.jso[n] ./
RUN npm install --no-audit --no-fund
COPY nuxk-web/ ./
ARG VITE_API_TOKEN=dev-nuxk-token
RUN npm run build            # -> /src/dist

# --- stage: core ----------------------------------------------------------
FROM golang:1.24-alpine AS core
WORKDIR /src
COPY nuxk-core/go.mod ./
RUN go mod download
COPY nuxk-core/ ./
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /nuxk-core .

# --- stage: runtime -------------------------------------------------------
FROM alpine:3.20
RUN apk add --no-cache curl                     # curl: healthcheck only
COPY --from=core /nuxk-core /usr/bin/nuxk-core
COPY --from=web  /src/dist  /web
COPY nuxk-core/testdata/S51usque-mock /opt/mock/S51usque-mock
COPY deploy/dev/nuxk.conf              /etc/nuxk/nuxk.conf
RUN chmod +x /opt/mock/S51usque-mock && mkdir -p /var/lib/nuxk

EXPOSE 4141
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s \
  CMD curl -fsS http://localhost:4141/api/v1/healthz || exit 1

CMD ["nuxk-core", "-config", "/etc/nuxk/nuxk.conf", "-web", "/web", "-listen", "0.0.0.0:4141", "-debug"]
