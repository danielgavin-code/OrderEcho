GO      ?= go
BIN     := bin/orderecho
PKG     := ./...

.PHONY: build test interop lint clean

build:
	$(GO) build -o $(BIN) ./cmd/orderecho

# Unit tests: pure core, codec, stores, logs, config. No network.
test:
	$(GO) test -count=1 $(PKG)

# Interop tests against the real Python emulator in ../OrderEchoFixEmulator.
interop: build
	$(GO) test -count=1 -tags interop -v ./internal/interop/

lint:
	$(GO) vet $(PKG)
	$(GO) vet -tags interop $(PKG)

clean:
	rm -rf bin
