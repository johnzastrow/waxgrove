# Waxgrove — one container, one process, no sidecars.
#
# Three stages: build the PWA, build the Go binary with that PWA embedded, ship
# the binary alone. The final image carries no toolchain, no shell, and no
# package manager, so its attack surface is the binary plus the CA bundle.

# --- 1. the PWA --------------------------------------------------------------
FROM node:24-alpine AS web

WORKDIR /src/app
# Copy the manifests first so a dependency-free source change reuses the layer.
COPY app/package.json app/package-lock.json ./
# `npm ci` installs exactly the lockfile — never a resolved-at-build-time tree.
RUN npm ci

COPY app/ ./
# The build output lands where the Go module expects it, replacing whatever was
# committed: a release always ships a frontend built from this source tree.
RUN npm run build

# --- 2. the binary -----------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist

ARG VERSION=dev
# CGO off: the pure-Go sqlite driver, and a genuinely static binary that can run
# on a base image with no libc at all.
ENV CGO_ENABLED=0
RUN go build -trimpath \
      -ldflags "-s -w -X github.com/johnzastrow/waxgrove/internal/httpapi.Version=${VERSION}" \
      -o /out/waxgrove ./cmd/waxgrove

# Fail the build rather than ship an image that starts and serves nothing.
RUN go test ./internal/webui/...

# --- 3. the image ------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# TLS roots, needed to reach MusicBrainz over HTTPS.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/waxgrove /usr/local/bin/waxgrove

# distroless :nonroot runs as uid 65532. The database lives on a mounted
# volume, which must be writable by that uid.
USER nonroot:nonroot
WORKDIR /data
VOLUME ["/data"]

ENV WAXGROVE_ADDR=0.0.0.0:8080 \
    WAXGROVE_DB=/data/waxgrove.db
EXPOSE 8080

# No shell in the image, so this is the exec form against the binary itself.
# /health checks the database too, not just that the process is alive.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/waxgrove", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/waxgrove"]
