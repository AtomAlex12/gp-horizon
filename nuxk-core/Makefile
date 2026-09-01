VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GO      := go

.PHONY: build run test vet cross clean

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o nuxk-core .

run:
	$(GO) run . -config testdata/nuxk.conf -web ../nuxk-web/dist -debug

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

cross: clean
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=mips   GOMIPS=softfloat $(GO) build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-mips   .
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat $(GO) build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-mipsel .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64                   $(GO) build -ldflags "$(LDFLAGS)" -o dist/nuxk-core-aarch64 .
	@ls -lh dist/

clean:
	rm -rf dist nuxk-core testdata/state testdata/mock.state
