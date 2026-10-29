.PHONY: help docs-check web-build build test check image integration integration-network integration-apt dev-cert bootstrap password-reset up down

help:
	@echo "build        Build frontend and Go binary in bin/"
	@echo "check        Docs, frontend type/build, Go race tests, vet and patch checks"
	@echo "image        Build single-container development image"
	@echo "integration  Run smoke, network and APT tests on real Squid (requires image)"
	@echo "dev-cert     Create localhost development certificate in Compose volume"
	@echo "bootstrap    Interactively create initial administrator"
	@echo "password-reset  Reset an existing password locally and revoke sessions"
	@echo "up / down    Start/stop local Compose deployment"

docs-check:
	python3 scripts/check_docs.py

web-build:
	npm --prefix src/web ci --no-audit --no-fund
	npm --prefix src/web run build

build: web-build
	cd src && go build -trimpath -o ../bin/kraken ./cmd/control

test: web-build
	cd src && go test -race ./...

check: docs-check web-build test
	cd src && go vet ./...
	@test -z "$$(gofmt -l src)" || { gofmt -l src; echo 'gofmt: files above need formatting'; exit 1; }
	git diff --check

image:
	docker build -f deploy/Dockerfile -t kraken:dev .

integration:
	python3 src/tests/integration/smoke.py
	python3 src/tests/integration/network.py
	python3 src/tests/integration/apt.py

integration-network:
	python3 src/tests/integration/network.py

integration-apt:
	python3 src/tests/integration/apt.py

dev-cert:
	docker compose -f deploy/compose.yaml run --rm --no-deps --user 10013:10013 --entrypoint /usr/local/bin/kraken kraken dev-cert

bootstrap:
	python3 src/tools/bootstrap.py

password-reset:
	python3 src/tools/password_reset.py

up:
	docker compose -f deploy/compose.yaml up -d

down:
	docker compose -f deploy/compose.yaml down
