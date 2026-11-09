.PHONY: help docs-check check squid-image image integration web-build web-test test scan scan-go scan-sbom scan-secrets

help:
	@echo "check        Docs, frontend type/build, Go race tests, vet and patch checks"
	@echo "docs-check   Local documentation checks (Go: links, ADR, map, headings, OpenAPI, example)"
	@echo "squid-image  Build the Squid image locally (kraken-squid:local) from deploy/squid"
	@echo "image        Build the image kraken:dev (SQUID_IMAGE=kraken-squid:local for a local Squid)"
	@echo "integration  Go tests on real Squid: smoke, network, APT, ops (requires image; RUN=TestOps, PARALLEL=N)"
	@echo "scan         scan-go, scan-sbom and scan-secrets (requires image; results in dist/scan)"
	@echo "scan-go      govulncheck over the Go sources (reachable calls); no image needed"
	@echo "scan-sbom    pebble binary (govulncheck) and SBOM of image + web deps + Squid (CPE) under grype; fails on high"
	@echo "scan-secrets gitleaks over the whole git history"

docs-check:
	cd src && go run ./tools/docs-check

web-build:
	npm --prefix src/web ci --no-audit --no-fund
	npm --prefix src/web run build

web-test:
	npm --prefix src/web run test

test: web-build
	cd src && go test -race ./...

check: docs-check web-build web-test test
	cd src && go vet ./...
	@test -z "$$(gofmt -l src)" || { gofmt -l src; echo 'gofmt: files above need formatting'; exit 1; }
	git diff --check

squid-image:
	docker build -t kraken-squid:local deploy/squid

image:
	docker build -f deploy/Dockerfile -t kraken:dev $(if $(SQUID_IMAGE),--build-arg SQUID_IMAGE=$(SQUID_IMAGE)) .

integration:
	cd src && go test -tags integration -count=1 -timeout 60m -parallel $(or $(PARALLEL),4) $(if $(RUN),-run $(RUN),) ./tests/integration/...

# Scanners run from pinned images; nothing is installed on the host. The image
# goes to syft as an archive, so no Docker socket enters a container.
SCAN_DIR := dist/scan
IMAGE ?= kraken:dev
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
# go run pkg@version builds the tool with the Go its own module asks for; pin
# the toolchain of src/go.mod so govulncheck can load our packages.
GO_TOOLCHAIN := go$(shell awk '/^go /{print $$2}' src/go.mod)
SYFT := anchore/syft:v1.57.1@sha256:03215d0e98b661d0ecbc8575dbc14d65be1743b5de5df6410aecb7e80f95b060
GRYPE := anchore/grype:v0.121.3@sha256:13113eeceab017260370cf540e1d25442c59665fd07d1b09b66d63bb0c282887
GITLEAKS := zricethezav/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f

scan: scan-go scan-sbom scan-secrets

scan-go:
	cd src && GOTOOLCHAIN=$(GO_TOOLCHAIN) go run $(GOVULNCHECK) ./...

scan-sbom:
	mkdir -p $(SCAN_DIR)
	cid=$$(docker create $(IMAGE)) && docker cp -q $$cid:/usr/local/bin/pebble $(SCAN_DIR)/pebble; rc=$$?; docker rm $$cid >/dev/null; exit $$rc
	cd src && GOTOOLCHAIN=$(GO_TOOLCHAIN) go run $(GOVULNCHECK) -mode=binary ../$(SCAN_DIR)/pebble
	docker save $(IMAGE) -o $(SCAN_DIR)/image.tar && chmod 644 $(SCAN_DIR)/image.tar
	docker run --rm -v "$(CURDIR)/$(SCAN_DIR):/out" $(SYFT) scan docker-archive:/out/image.tar -o cyclonedx-json=/out/image.cdx.json
	docker run --rm -v "$(CURDIR)/src/web/package-lock.json:/in/package-lock.json:ro" -v "$(CURDIR)/$(SCAN_DIR):/out" $(SYFT) scan file:/in/package-lock.json -q -o cyclonedx-json=/out/web.cdx.json
	# The Squid version comes from the binary in the scanned image, not from a Dockerfile.
	squid=$$(docker run --rm --network none --entrypoint /usr/sbin/squid $(IMAGE) -v | sed -n 's/^Squid Cache: Version \([0-9.]*\)$$/\1/p') \
		&& cd src && go run ./tools/sbom -image ../$(SCAN_DIR)/image.cdx.json -web ../$(SCAN_DIR)/web.cdx.json -squid-version "$$squid" -out ../$(SCAN_DIR)/sbom.cdx.json
	rm -f $(SCAN_DIR)/image.tar
	docker run --rm -v "$(CURDIR)/$(SCAN_DIR):/out:ro" -v "$(CURDIR)/deploy/grype.yaml:/grype.yaml:ro" -v kraken-grype-db:/root/.cache/grype $(GRYPE) -c /grype.yaml sbom:/out/sbom.cdx.json -q --fail-on high

scan-secrets:
	docker run --rm -v "$(CURDIR):/repo:ro" $(GITLEAKS) git /repo --redact --no-banner
