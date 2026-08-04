# Waxgrove — one static binary, no external services to boot (N1/N2).
BINARY  := waxgrove
PKG     := ./cmd/waxgrove
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/johnzastrow/waxgrove/internal/httpapi.Version=$(VERSION)

# CGO off keeps the pure-Go sqlite driver and a static binary (§7).
export CGO_ENABLED := 0

.PHONY: all build test vet fmt check clean run genkey pi

all: check build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

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
