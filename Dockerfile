# syntax=docker/dockerfile:1

# --- Frontend -------------------------------------------------------------
FROM node:26-trixie-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- Go binary ------------------------------------------------------------
# CGO stays enabled: the Flow Store (DuckDB) needs it, which is also why the
# runtime image is glibc-based Debian rather than Alpine or scratch.
FROM golang:1.27-trixie AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/flowmon ./cmd/flowmon

# --- Runtime --------------------------------------------------------------
FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 10001 flowmon \
 && useradd --system --uid 10001 --gid 10001 --home-dir /data --no-create-home --shell /usr/sbin/nologin flowmon \
 && mkdir -p /data \
 && chown 10001:10001 /data

COPY --from=build /out/flowmon /usr/local/bin/flowmon

USER 10001:10001
ENV FLOWMON_DATA_DIR=/data
VOLUME ["/data"]
# 8080: web UI and API. 2055/udp: NetFlow. 4739: IPFIX.
EXPOSE 8080 2055/udp 4739/udp 4739/tcp

# Docker honours this; Podman ignores it for OCI images, so run configs also
# declare `flowmon healthcheck` explicitly.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["flowmon", "healthcheck"]

ENTRYPOINT ["flowmon"]
CMD ["serve"]
