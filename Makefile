.PHONY: help docs-check check image integration web-build web-test test

help:
	@echo "check        Docs, frontend type/build, Go race tests, vet and patch checks"
	@echo "docs-check   Local documentation checks (Go: links, ADR, map, headings, OpenAPI, example)"
	@echo "image        Build single-container development image"
	@echo "integration  Run smoke, network, APT and ops tests on real Squid (requires image)"

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
	python3 src/tests/integration/smoke.py
	python3 src/tests/integration/network.py
	python3 src/tests/integration/apt.py
	python3 src/tests/integration/ops.py
