# nuxk Horizon — top-level build. Component Makefiles/npm scripts do the work;
# this ties them to the single VERSION file and produces release bundles.
#
#   make check      version + gofmt + vet + tests + web typecheck + installers (what CI runs)
#   make release    dist/nuxk-horizon-<ver>/: everything the installers download —
#                   nuxk-core per arch, web full/lite, router init + engine adapters,
#                   usque ipk per arch, nuxk-controller, nuxk-lite.sh / nuxk-full.sh,
#                   SHA256SUMS; release.yml builds the controller image from it,
#                   adds the image's digest and signs SHA256SUMS
#   make proto      build the real-engine prototype image (deploy/proto, arm64 Pi)
#   make pi         the development stack on a Pi, from source (deploy/pi)
#   make dev        run the mock stack in Docker (deploy/dev)
#   make version    print the version;  scripts/version.sh set X.Y.Z to bump

VERSION := $(shell tr -d ' \n\r' < VERSION)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell git diff --quiet 2>/dev/null || echo -dirty)
OUT     := dist/nuxk-horizon-$(VERSION)

.PHONY: version version-check check core-check web-check install-check controller-check release sums proto pi dev clean

version:
	@echo $(VERSION)

version-check:
	@scripts/version.sh check

core-check:
	$(MAKE) -C nuxk-core fmt-check vet test

web-check:
	cd nuxk-web && npm install --no-audit --no-fund && npm run check:api && npm run check

# the router side: engine adapters and the lite installer, on a fake router
install-check:
	sh engines/nuxk-nfqws2/shim_test.sh
	sh engines/nuxk-xray/shim_test.sh
	sh install/lite_test.sh

controller-check:
	cd nuxk-controller && gofmt -l . | (! grep .) && go vet ./... && go test ./...

check: version-check core-check web-check install-check controller-check

release: version-check
	$(MAKE) -C nuxk-core cross VERSION=$(VERSION) COMMIT=$(COMMIT)
	rm -rf $(OUT) && mkdir -p $(OUT)
	cp nuxk-core/dist/nuxk-core-* $(OUT)/
	# router files next to the agent: its init script and the engine adapters
	cp nuxk-core/fs/opt/etc/init.d/S99nuxk-core engines/nuxk-nfqws2/S51nfqws2-nuxk engines/nuxk-xray/S52xray-nuxk $(OUT)/
	# the installers, stamped with this release (they download from it)
	for s in nuxk-lite nuxk-full; do \
	  sed 's/^VERSION="@VERSION@"/VERSION="$(VERSION)"/' install/$$s.sh > $(OUT)/$$s.sh || exit 1; \
	  grep -q '^VERSION="$(VERSION)"' $(OUT)/$$s.sh || { echo "$$s.sh: version not stamped"; exit 1; }; \
	done
	# WARP: the usque-keenetic fork's ipk per router arch (downloads usque
	# from its GitHub release)
	$(MAKE) -C engines/nuxk-usque pkg-mips pkg-mipsel pkg-aarch64
	for a in mips mipsel aarch64; do \
	  cp engines/nuxk-usque/out/tmp/usque-keenetic_$$(cat engines/nuxk-usque/VERSION)_$$a-3.*.ipk $(OUT)/usque-keenetic-$$a.ipk || exit 1; \
	done
	cd nuxk-controller && for arch in arm64 amd64; do \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o ../$(OUT)/nuxk-controller-linux-$$arch . || exit 1; \
	done
	cd nuxk-web && npm install --no-audit --no-fund && npm run build && npm run build:lite
	tar -C nuxk-web/dist      -czf $(OUT)/nuxk-web-full-$(VERSION).tar.gz .
	tar -C nuxk-web/dist-lite -czf $(OUT)/nuxk-web-lite-$(VERSION).tar.gz .
	cp CHANGELOG.md $(OUT)/
	printf 'version %s\ncommit %s\n' '$(VERSION)' '$(COMMIT)' > $(OUT)/BUILD
	$(MAKE) sums
	@ls -lh $(OUT) dist/nuxk-horizon-$(VERSION).tar.gz

# SHA256SUMS over the release's files, and its tarball. release.yml runs it
# again after adding the controller image's digest (controller-image), then
# signs SHA256SUMS with the release key (SHA256SUMS.sig).
sums:
	cd $(OUT) && rm -f SHA256SUMS SHA256SUMS.sig && sha256sum -- * > ../SHA256SUMS.tmp && mv ../SHA256SUMS.tmp SHA256SUMS
	tar -C dist -czf dist/nuxk-horizon-$(VERSION).tar.gz nuxk-horizon-$(VERSION)

proto:
	NUXK_COMMIT=$(COMMIT) docker compose -f deploy/proto/docker-compose.yml build

# development stack on a Pi, built from source: the real-engine stand and the
# controller (people install with install/nuxk-full.sh instead)
pi:
	NUXK_COMMIT=$(COMMIT) docker compose -f deploy/pi/docker-compose.yml up -d --build

dev:
	NUXK_COMMIT=$(COMMIT) docker compose -f deploy/dev/docker-compose.yml up --build

clean:
	rm -rf dist
	$(MAKE) -C nuxk-core clean
