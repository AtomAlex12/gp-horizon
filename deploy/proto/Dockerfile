# GP Horizon — REAL-engine prototype image (deploy/proto). NOT deploy/dev
# (which stays mock-only and untouched) and NOT a production image — the
# router ships an opkg package, not this. Runs real usque + real nfqws2
# inside one container so the whole reconcile/routing chain can be exercised
# against real MGTS DPI on an isolated Pi/Docker box. See deploy/proto plan.
#
#   docker compose -f deploy/proto/docker-compose.yml build
#   docker compose -f deploy/proto/docker-compose.yml run --rm \
#     --entrypoint /opt/etc/init.d/S51usque-docker nuxk register   # checkpoint 2, once
#   docker compose -f deploy/proto/docker-compose.yml up -d
#
# debian:bookworm-slim (not deploy/dev's alpine) is a deliberate deviation:
# the vendored usque/nfqws2 binaries are third-party glibc-toolchain
# releases of unknown musl-compatibility, and this combination is already
# smoke-tested working on the target Pi (Docker 29.8.0, aarch64).

# --- stage: web (same pattern as deploy/dev) --------------------------------
FROM node:22-alpine AS web
WORKDIR /src
COPY nuxk-web/package.json nuxk-web/package-lock.jso[n] ./
RUN npm install --no-audit --no-fund
COPY nuxk-web/ ./
ARG VITE_API_TOKEN=proto-nuxk-token
RUN npm run build

# --- stage: core -------------------------------------------------------------
FROM golang:1.24-alpine AS core
WORKDIR /src
COPY nuxk-core/go.mod ./
RUN go mod download
COPY nuxk-core/ ./
COPY VERSION /VERSION
# Version comes from the repo-root VERSION file (single source of truth);
# COMMIT is passed by `make proto` (git isn't in the build context).
ARG COMMIT=unknown
RUN CGO_ENABLED=0 GOARCH=arm64 go build \
    -ldflags "-s -w -X main.version=$(cat /VERSION)+proto -X main.commit=${COMMIT}" -o /nuxk-core .

# --- stage: usque-fetch -------------------------------------------------------
FROM debian:bookworm-slim AS usque-fetch
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates unzip \
    && rm -rf /var/lib/apt/lists/*
# Pinned to the same version usque-keenetic (feature/web-ui, USQUE_VERSION
# file) currently vendors — do NOT float "latest".
ARG USQUE_VERSION=4.2.0
RUN url=$(curl -s "https://api.github.com/repos/Diniboy1123/usque/releases/tags/v${USQUE_VERSION}" \
    | grep browser_download_url | grep -E 'linux_arm64\.' | cut -d '"' -f4) \
    && test -n "$url" \
    && curl -sSL "$url" -o /tmp/usque.zip \
    && mkdir -p /out && unzip -o /tmp/usque.zip -d /out usque && chmod +x /out/usque

# --- stage: nfqws2-fetch -------------------------------------------------------
FROM debian:bookworm-slim AS nfqws2-fetch
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# Pinned like usque above — "latest" made two builds a day apart behave
# differently against the same DPI, so test results weren't comparable.
# Bump deliberately, note it in CHANGELOG.md.
# Escape hatch: --build-arg ZAPRET2_VERSION=latest (then record the version used).
ARG ZAPRET2_VERSION=1.0.5.2
RUN if [ "$ZAPRET2_VERSION" = latest ]; then rel=latest; else rel="tags/v${ZAPRET2_VERSION}"; fi \
    && url=$(curl -s "https://api.github.com/repos/bol-van/zapret2/releases/${rel}" \
    | grep browser_download_url | grep 'embedded.tar.gz' | cut -d '"' -f4) \
    && { test -n "$url" || { echo "zapret2 ${ZAPRET2_VERSION}: no *embedded.tar.gz asset" >&2; exit 1; }; } \
    && curl -sSL "$url" -o /tmp/zapret2.tar.gz \
    && mkdir -p /tmp/zapret2 /out/bin /out/lua \
    && tar -C /tmp/zapret2 -xzf /tmp/zapret2.tar.gz \
    && cp /tmp/zapret2/zapret2-*/binaries/linux-arm64/nfqws2 /out/bin/nfqws2 \
    && cp -r /tmp/zapret2/zapret2-*/lua/* /out/lua/ \
    && chmod +x /out/bin/nfqws2

# --- stage: runtime -------------------------------------------------------------
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    iptables iproute2 dnsmasq dnsutils curl iputils-ping tcpdump procps kmod ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=core        /nuxk-core       /usr/bin/nuxk-core
COPY --from=web          /src/dist       /web
COPY --from=usque-fetch  /out/usque      /opt/usr/bin/usque
COPY --from=nfqws2-fetch /out/bin/nfqws2 /opt/usr/bin/nfqws2
COPY --from=nfqws2-fetch /out/lua        /opt/etc/nfqws2/lua

COPY deploy/proto/engine/S51usque-docker  /opt/etc/init.d/S51usque-docker
COPY deploy/proto/engine/S51nfqws2-docker /opt/etc/init.d/S51nfqws2-docker
COPY deploy/proto/engine/usque.conf       /opt/etc/usque/usque.conf
COPY deploy/proto/engine/nfqws2.conf      /opt/etc/nfqws2/nfqws2.conf
COPY deploy/proto/engine/user.list        /opt/etc/nfqws2/lists/user.list
COPY deploy/proto/engine/exclude.list     /opt/etc/nfqws2/lists/exclude.list
# Blobs vendored from nfqws2-keenetic as base64 text (deploy/proto/plan §8 —
# the Gitea file-write tool used to vendor them isn't binary-safe), decoded
# back to real bytes here at build time.
COPY deploy/proto/engine/blobs/quic_initial.bin.b64     /tmp/quic_initial.bin.b64
COPY deploy/proto/engine/blobs/tls_clienthello.bin.b64  /tmp/tls_clienthello.bin.b64
RUN mkdir -p /opt/etc/nfqws2/blobs \
    && base64 -d /tmp/quic_initial.bin.b64    > /opt/etc/nfqws2/blobs/quic_initial.bin \
    && base64 -d /tmp/tls_clienthello.bin.b64 > /opt/etc/nfqws2/blobs/tls_clienthello.bin \
    && rm -f /tmp/quic_initial.bin.b64 /tmp/tls_clienthello.bin.b64
COPY deploy/proto/routing-glue.sh         /opt/etc/nuxk/routing-glue.sh
COPY deploy/proto/dnsmasq.conf            /etc/dnsmasq.d/nuxk-warp.conf
COPY deploy/proto/nuxk.conf               /etc/nuxk/nuxk.conf
COPY deploy/proto/entrypoint.sh           /entrypoint.sh

RUN touch /opt/etc/nfqws2/lists/auto.list \
    && chmod +x /opt/etc/init.d/S51usque-docker /opt/etc/init.d/S51nfqws2-docker \
       /opt/etc/nuxk/routing-glue.sh /entrypoint.sh \
    && mkdir -p /var/lib/nuxk /opt/var/run /opt/var/log /opt/etc/usque/hooks

EXPOSE 4141
ENTRYPOINT ["/entrypoint.sh"]
CMD ["nuxk-core", "-config", "/etc/nuxk/nuxk.conf", "-web", "/web", "-listen", "0.0.0.0:4141", "-debug"]
