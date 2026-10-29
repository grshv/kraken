.PHONY: help docs-check check image integration web-build web-test test

help:
	@echo "check        Docs, frontend type/build, Go race tests, vet and patch checks"
	@echo "docs-check   Local documentation checks (Go: links, ADR, map, headings, OpenAPI, example)"
	@echo "image        Build single-container development image"
	@echo "integration  Go tests on real Squid: smoke, network, APT, ops (requires image; RUN=TestOps, PARALLEL=N)"

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

image:
	docker build -f deploy/Dockerfile -t kraken:dev .

integration:
	cd src && go test -tags integration -count=1 -timeout 60m -parallel $(or $(PARALLEL),4) $(if $(RUN),-run $(RUN),) ./tests/integration/...
