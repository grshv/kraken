.PHONY: docs-check check help

help:
	@echo "docs-check  Check local documentation and JSON contracts"
	@echo "check       Run documentation checks and git diff --check"

docs-check:
	python3 scripts/check_docs.py

check: docs-check
	git diff --check
