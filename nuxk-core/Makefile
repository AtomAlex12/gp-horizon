# Version: repo-root VERSION file (see scripts/version.sh). Commit: git short sha.
VERSION ?= $(shell cat ../VERSION 2>/dev/null || echo 0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell git diff --quiet 2>/dev/null || echo -dirty)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
GO      := go

.PHONY: build run test vet fmt-check cross clean

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o nuxk-core .

run:
	$(GO) run . -config testdata/nuxk.conf -web ../nuxk-web/dist -debug

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt-check:
	@out=$$(gofmt -l .); [ -z "$$out" ] || { echo "gofmt needed:"; echo "$$out"; exit 1; }

cross: clean
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=mips   GOMIPS=softfloat $(GO) build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-mips   .
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat $(GO) build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-mipsel .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64                   $(GO) build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-aarch64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64                   $(GO) build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-x86_64  .
	@ls -lh dist/

clean:
	rm -rf dist nuxk-core testdata/state testdata/mock.state testdata/mock.xray.state testdata/mock.xray-config
