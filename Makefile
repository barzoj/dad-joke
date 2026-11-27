# Local build helpers mirroring scripts/build.ps1 ldflags.
# Canonical link package for metadata:
MODULE := github.com/barzoj/dad-joke
PKG := $(MODULE)/internal/buildinfo

VERSION ?= 0.0.1-dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
RELEASE_API_URL ?= https://api.github.com/repos/barzoj/dad-joke/releases/latest

LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).ReleaseAPIURL=$(RELEASE_API_URL)

.PHONY: test build build-windows vet

test:
	CGO_ENABLED=0 go test ./...

vet:
	go vet ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dad-joke ./cmd/dad-joke

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dad-joke.exe ./cmd/dad-joke
