# nuxk Horizon — top-level build. Component Makefiles/npm scripts do the work;
# this ties them to the single VERSION file and produces release bundles.
#
#   make check      version + gofmt + vet + tests + web typecheck (what CI runs)
#   make release    dist/nuxk-horizon-<ver>/: core per arch, web full/lite, SHA256SUMS
#   make installer  nuxk-installer for linux-arm64/amd64, windows, macOS (router files embedded)
#   make proto      build the real-engine prototype image (deploy/proto, arm64 Pi)
#   make dev        run the mock stack in Docker (deploy/dev)
#   make version    print the version;  scripts/version.sh set X.Y.Z to bump

VERSION := $(shell tr -d ' \n\r' < VERSION)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell git diff --quiet 2>/dev/null || echo -dirty)
OUT     := dist/nuxk-horizon-$(VERSION)

.PHONY: version version-check check core-check web-check installer-check installer-payload installer release proto pi dev clean

version:
	@echo $(VERSION)

version-check:
	@scripts/version.sh check

core-check:
	$(MAKE) -C nuxk-core fmt-check vet test

web-check:
	cd nuxk-web && npm install --no-audit --no-fund && npm run check

installer-check:
	cd nuxk-installer && gofmt -l . | (! grep .) && go vet ./... && go test ./...
	sh engines/nuxk-nfqws2/shim_test.sh

check: version-check core-check web-check installer-check

# Router files the installer embeds and pushes over SSH.
PAYLOAD := nuxk-installer/payload
installer-payload:
	$(MAKE) -C nuxk-core cross VERSION=$(VERSION) COMMIT=$(COMMIT)
	cd nuxk-web && npm install --no-audit --no-fund && npm run build:lite
	find $(PAYLOAD) -mindepth 1 ! -name README.md -exec rm -rf {} +
	cp nuxk-core/dist/nuxk-core-mips nuxk-core/dist/nuxk-core-mipsel nuxk-core/dist/nuxk-core-aarch64 nuxk-core/dist/nuxk-core-x86_64 $(PAYLOAD)/
	cp nuxk-core/fs/opt/etc/init.d/S99nuxk-core engines/nuxk-nfqws2/S51nfqws2-nuxk $(PAYLOAD)/
	cp -r nuxk-web/dist-lite $(PAYLOAD)/web
	echo $(VERSION) > $(PAYLOAD)/VERSION

INST_LDFLAGS := -s -w
installer: installer-payload
	mkdir -p dist
	cd nuxk-installer && for t in linux/arm64 linux/amd64 windows/amd64 darwin/arm64; do \
	  os=$${t%/*}; arch=$${t#*/}; ext=$$( [ $$os = windows ] && echo .exe ); \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(INST_LDFLAGS)" -o ../dist/nuxk-installer-$$os-$$arch$$ext . || exit 1; \
	done
	@ls -lh dist/nuxk-installer-*

release: version-check installer
	rm -rf $(OUT) && mkdir -p $(OUT)
	cp nuxk-core/dist/nuxk-core-* $(OUT)/
	cp dist/nuxk-installer-* $(OUT)/
	cd nuxk-web && npm install --no-audit --no-fund && npm run build && npm run build:lite
	tar -C nuxk-web/dist      -czf $(OUT)/nuxk-web-full-$(VERSION).tar.gz .
	tar -C nuxk-web/dist-lite -czf $(OUT)/nuxk-web-lite-$(VERSION).tar.gz .
	cp CHANGELOG.md $(OUT)/
	printf 'version %s\ncommit %s\n' '$(VERSION)' '$(COMMIT)' > $(OUT)/BUILD
	cd $(OUT) && sha256sum nuxk-core-* nuxk-installer-* *.tar.gz BUILD > SHA256SUMS
	tar -C dist -czf dist/nuxk-horizon-$(VERSION).tar.gz nuxk-horizon-$(VERSION)
	@ls -lh $(OUT) dist/nuxk-horizon-$(VERSION).tar.gz

proto:
	NUXK_COMMIT=$(COMMIT) docker compose -f deploy/proto/docker-compose.yml build

# Raspberry Pi beta stack: prototype + installer (see deploy/pi/README.md)
pi:
	NUXK_COMMIT=$(COMMIT) docker compose -f deploy/pi/docker-compose.yml up -d --build

dev:
	NUXK_COMMIT=$(COMMIT) docker compose -f deploy/dev/docker-compose.yml up --build

clean:
	rm -rf dist
	find $(PAYLOAD) -mindepth 1 ! -name README.md -exec rm -rf {} +
	$(MAKE) -C nuxk-core clean
