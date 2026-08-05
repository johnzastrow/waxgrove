# Waxgrove — one static binary, no external services to boot (N1/N2).
BINARY  := waxgrove
PKG     := ./cmd/waxgrove
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/johnzastrow/waxgrove/internal/httpapi.Version=$(VERSION)

# CGO off keeps the pure-Go sqlite driver and a static binary (§7).
export CGO_ENABLED := 0

.PHONY: all build test vet fmt check clean run genkey pi web web-dev web-check docker

all: check build

# `build` deliberately does NOT depend on `web`. internal/webui/dist is
# committed, so a checkout with no Node installed still produces a complete
# binary. Run `make web` when the frontend changes; `make web-check` in CI
# catches a stale commit of it.
build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

# --- frontend ---------------------------------------------------------------
# The PWA compiles into internal/webui/dist and is embedded by the Go build.

web:
	npm --prefix app ci
	npm --prefix app run build

# Live reload against a locally running binary (see app/vite.config.ts proxy).
web-dev:
	npm --prefix app install
	npm --prefix app run dev

# Fails if the committed build output does not match the current sources.
web-check: web
	@git diff --stat --exit-code -- internal/webui/dist \
		|| (echo "internal/webui/dist is stale — run 'make web' and commit the result"; exit 1)

# --- container --------------------------------------------------------------
# amd64 only: that is the deployment target. Building on an arm64 machine
# emulates, which is slow but produces the image that actually gets deployed.

docker:
	docker build --platform linux/amd64 --build-arg VERSION=$(VERSION) -t waxgrove:$(VERSION) .
	@docker image inspect waxgrove:$(VERSION) --format 'built {{.Os}}/{{.Architecture}}'

# Cross-compile for a Raspberry Pi — the N1 target.
pi:
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY)-arm64 $(PKG)

# The race detector needs cgo, so it is re-enabled for this target only.
# Shipped binaries stay CGO_ENABLED=0 (pure-Go sqlite, static, §7).
test:
	CGO_ENABLED=1 go test -race ./...

# Test exactly as the binary ships: no cgo, pure-Go sqlite driver.
test-nocgo:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

check: vet test
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "run: make fmt"; exit 1)

run: build
	./bin/$(BINARY)

genkey: build
	@./bin/$(BINARY) genkey

clean:
	rm -rf bin
