BINARY      := tokimeki
PKG         := ./cmd/tokimeki
COMMIT      := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BRANCH      := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS_VER := -X main.versionCommit=$(COMMIT) -X main.versionBranch=$(BRANCH) -X main.versionBuildTime=$(BUILD_TIME)

# Static binary, no cgo → faster link, portable.
export CGO_ENABLED := 0

.PHONY: all dev fast release install clean linux-amd64 linux-arm64 darwin-arm64 cross

all: dev

# Dev: no version stamping, no stripping. Fastest incremental build.
# Use this while iterating; warm rebuilds in ~50ms.
dev:
	go build -buildvcs=false -o $(BINARY) $(PKG)

# Fast: full version stamping, no stripping. Skip VCS auto-stamp.
fast:
	go build -buildvcs=false -ldflags "$(LDFLAGS_VER)" -o $(BINARY) $(PKG)

# Release: trimpath + stripped symbols. Smallest, slowest cold build.
release:
	go build -trimpath -buildvcs=false \
		-ldflags "-s -w $(LDFLAGS_VER)" \
		-o $(BINARY) $(PKG)

install:
	go install -ldflags "$(LDFLAGS_VER)" $(PKG)

# Cross-compile for shared-FS clusters. Output is suffixed with GOOS-GOARCH so
# multiple artifacts coexist side-by-side. Stripped + trimpath like `release`.
linux-amd64:
	GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
		-ldflags "-s -w $(LDFLAGS_VER)" \
		-o $(BINARY)-linux-amd64 $(PKG)

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false \
		-ldflags "-s -w $(LDFLAGS_VER)" \
		-o $(BINARY)-linux-arm64 $(PKG)

darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -trimpath -buildvcs=false \
		-ldflags "-s -w $(LDFLAGS_VER)" \
		-o $(BINARY)-darwin-arm64 $(PKG)

cross: linux-amd64 linux-arm64 darwin-arm64

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-linux-arm64 $(BINARY)-darwin-arm64
