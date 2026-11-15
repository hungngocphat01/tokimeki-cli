BINARY      := tokimeki
PKG         := ./cmd/tokimeki
ENGINE      := github.com/hungngocphat01/tokimeki
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo unknown)
COMMIT      := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BRANCH      := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS_VER := -X main.versionCLI=$(VERSION) -X main.versionCommit=$(COMMIT) -X main.versionBranch=$(BRANCH) -X main.versionBuildTime=$(BUILD_TIME)

# Static binary, no cgo → faster link, portable.
export CGO_ENABLED := 0

# Release builds ignore any go.work so the binary always links the engine
# version pinned in go.mod.
RELEASE_GO  := GOWORK=off go

.PHONY: all dev fast release install clean linux-amd64 linux-arm64 darwin-arm64 cross check-engine bump-engine

all: dev

# Dev: no version stamping, no stripping. Fastest incremental build.
# Use this while iterating; warm rebuilds in ~50ms.
dev:
	go build -buildvcs=false -o $(BINARY) $(PKG)

# Fast: full version stamping, no stripping. Skip VCS auto-stamp.
fast:
	go build -buildvcs=false -ldflags "$(LDFLAGS_VER)" -o $(BINARY) $(PKG)

# Fail unless go.mod pins the engine to a release tag with no local override.
check-engine:
	@./scripts/check-engine.sh

# Adopt a new engine release: make bump-engine V=v0.2.0
bump-engine:
	@test -n "$(V)" || { echo "usage: make bump-engine V=vX.Y.Z" >&2; exit 1; }
	GOWORK=off go get $(ENGINE)@$(V)
	GOWORK=off go mod tidy
	@./scripts/check-engine.sh

# Release: trimpath + stripped symbols. Smallest, slowest cold build.
release: check-engine
	$(RELEASE_GO) build -trimpath -buildvcs=false \
		-ldflags "-s -w $(LDFLAGS_VER)" \
		-o $(BINARY) $(PKG)

install: check-engine
	$(RELEASE_GO) install -ldflags "$(LDFLAGS_VER)" $(PKG)

# Cross-compile for shared-FS clusters. Output is suffixed with GOOS-GOARCH so
# multiple artifacts coexist side-by-side. Stripped + trimpath like `release`.
linux-amd64: check-engine
	GOWORK=off GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
		-ldflags "-s -w $(LDFLAGS_VER)" \
		-o $(BINARY)-linux-amd64 $(PKG)

linux-arm64: check-engine
	GOWORK=off GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false \
		-ldflags "-s -w $(LDFLAGS_VER)" \
		-o $(BINARY)-linux-arm64 $(PKG)

darwin-arm64: check-engine
	GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath -buildvcs=false \
		-ldflags "-s -w $(LDFLAGS_VER)" \
		-o $(BINARY)-darwin-arm64 $(PKG)

cross: linux-amd64 linux-arm64 darwin-arm64

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-linux-arm64 $(BINARY)-darwin-arm64
