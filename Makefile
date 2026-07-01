BINARY  := adb_exporter
PKG     := ./cmd/exporter
MODULE  := github.com/vvervolv/adb_exporter

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

# Release target platforms (SPEC §Release).
PLATFORMS := \
	windows/amd64 \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64

.PHONY: all fmt fmt-check vet test build release clean

all: fmt vet test build

fmt:
	gofmt -w .

# Used by CI: fail if any file is not gofmt-clean.
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

vet:
	go vet ./...

test:
	go test -race ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

# Cross-compile every platform into dist/ and package the archives.
release: clean
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		bin=$(BINARY); ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		out=dist/$(BINARY)-$${os}-$${arch}; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags '$(LDFLAGS)' -o $$out/$$bin$$ext $(PKG) || exit 1; \
		cp README.md LICENSE config.example.yaml $$out/; \
		if [ "$$os" = "windows" ]; then \
			(cd dist && zip -qr $(BINARY)-$${os}-$${arch}.zip $(BINARY)-$${os}-$${arch}); \
		else \
			tar -czf dist/$(BINARY)-$${os}-$${arch}.tar.gz -C dist $(BINARY)-$${os}-$${arch}; \
		fi; \
	done
	@echo "artifacts:" && ls -1 dist/*.tar.gz dist/*.zip 2>/dev/null

clean:
	rm -rf dist $(BINARY) $(BINARY).exe
