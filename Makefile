# nuxk Horizon — top-level build. Component Makefiles/npm scripts do the work;
# this ties them to the single VERSION file and produces release bundles.
#
#   make check      version + gofmt + vet + tests + web typecheck (what CI runs)
#   make release    dist/nuxk-horizon-<ver>/: core per arch, web full/lite, SHA256SUMS
#   make proto      build the real-engine prototype image (deploy/proto, arm64 Pi)
#   make dev        run the mock stack in Docker (deploy/dev)
#   make version    print the version;  scripts/version.sh set X.Y.Z to bump

VERSION := $(shell tr -d ' \n\r' < VERSION)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell git diff --quiet 2>/dev/null || echo -dirty)
OUT     := dist/nuxk-horizon-$(VERSION)

.PHONY: version version-check check core-check web-check release proto dev clean

version:
	@echo $(VERSION)

version-check:
	@scripts/version.sh check

core-check:
	$(MAKE) -C nuxk-core fmt-check vet test

web-check:
	cd nuxk-web && npm install --no-audit --no-fund && npm run check

check: version-check core-check web-check

release: version-check
	rm -rf $(OUT) && mkdir -p $(OUT)
	$(MAKE) -C nuxk-core cross VERSION=$(VERSION) COMMIT=$(COMMIT)
	cp nuxk-core/dist/nuxk-core-* $(OUT)/
	cd nuxk-web && npm install --no-audit --no-fund && npm run build && npm run build:lite
	tar -C nuxk-web/dist      -czf $(OUT)/nuxk-web-full-$(VERSION).tar.gz .
	tar -C nuxk-web/dist-lite -czf $(OUT)/nuxk-web-lite-$(VERSION).tar.gz .
	cp CHANGELOG.md $(OUT)/
	printf 'version %s\ncommit %s\n' '$(VERSION)' '$(COMMIT)' > $(OUT)/BUILD
	cd $(OUT) && sha256sum nuxk-core-* *.tar.gz BUILD > SHA256SUMS
	tar -C dist -czf dist/nuxk-horizon-$(VERSION).tar.gz nuxk-horizon-$(VERSION)
	@ls -lh $(OUT) dist/nuxk-horizon-$(VERSION).tar.gz

proto:
	NUXK_COMMIT=$(COMMIT) docker compose -f deploy/proto/docker-compose.yml build

dev:
	NUXK_COMMIT=$(COMMIT) docker compose -f deploy/dev/docker-compose.yml up --build

clean:
	rm -rf dist
	$(MAKE) -C nuxk-core clean
