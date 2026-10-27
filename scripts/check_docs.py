#!/usr/bin/env python3
"""Dependency-free checks for this repository's documentation, not full OAS lint."""

from __future__ import annotations

import ipaddress
import json
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
LINK = re.compile(r"!?\[[^\]\n]*\]\((<[^>]+>|[^\s)]+)(?:\s+\"[^\"]*\")?\)")
TEXT_SUFFIXES = {".md", ".json", ".py", ".yml", ".yaml", ".toml"}
TEXT_NAMES = {"Makefile", ".gitignore", ".gitattributes", ".editorconfig", ".env.example"}
HTTP_METHODS = {"get", "post", "put", "patch", "delete", "head", "options", "trace"}


def repo_files() -> list[Path]:
    result = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=ROOT, check=True, capture_output=True,
    )
    names = {name for name in result.stdout.decode().split("\0") if name}
    return [ROOT / name for name in sorted(names) if (ROOT / name).is_file()]


def check_refs(node: object, document: dict, label: str, errors: list[str]) -> None:
    if isinstance(node, dict):
        reference = node.get("$ref")
        if reference:
            if not reference.startswith("#/"):
                errors.append(f"{label}: unsupported external $ref: {reference}")
            else:
                target = document
                try:
                    for part in reference[2:].split("/"):
                        target = target[unquote(part).replace("~1", "/").replace("~0", "~")]
                except (KeyError, TypeError):
                    errors.append(f"{label}: unresolved $ref: {reference}")
        for value in node.values():
            check_refs(value, document, label, errors)
    elif isinstance(node, list):
        for value in node:
            check_refs(value, document, label, errors)


def check_api(api: dict, errors: list[str]) -> int:
    if api.get("openapi") != "3.1.1" or not api.get("info", {}).get("version"):
        errors.append("api/openapi.json: expected versioned OpenAPI 3.1.1 document")
    check_refs(api, api, "api/openapi.json", errors)
    seen = set()
    for path, item in api.get("paths", {}).items():
        for method, operation in item.items():
            if method not in HTTP_METHODS:
                continue
            label = f"{method.upper()} {path}"
            operation_id = operation.get("operationId")
            if not operation_id or operation_id in seen:
                errors.append(f"{label}: missing/duplicate operationId")
            seen.add(operation_id)
            if not operation.get("responses"):
                errors.append(f"{label}: missing responses")
            params = item.get("parameters", []) + operation.get("parameters", [])
            for name in re.findall(r"\{([^}]+)\}", path):
                if not any(p.get("name") == name and p.get("in") == "path" and p.get("required") for p in params):
                    errors.append(f"{label}: missing required path parameter {name}")
            if method in {"post", "put", "patch", "delete"}:
                if not any(p.get("name") == "X-CSRF-Token" and p.get("required") for p in params):
                    errors.append(f"{label}: missing CSRF header contract")
    return len(seen)


def check_policy_example(policy: dict, errors: list[str]) -> None:
    """Check key semantic invariants, not a general-purpose JSON Schema engine."""
    try:
        assert policy["schema_version"] == 1, "unknown schema version"
        clients, policies, profiles = (policy[k] for k in ("clients", "policies", "tls_profiles"))
        for collection in (clients, policies, profiles):
            assert len({item["id"] for item in collection}) == len(collection), "duplicate IDs"
        policy_ids = {item["id"] for item in policies}
        profile_ids = {item["id"] for item in profiles}
        networks = []
        for client in clients:
            assert client["policy_id"] in policy_ids, "missing policy"
            assert client["source_networks"], "empty source networks"
            for raw in client["source_networks"]:
                network = ipaddress.ip_network(raw, strict=True)
                assert network.prefixlen > 0, "default source route forbidden"
                if client["enabled"]:
                    for previous, owner in networks:
                        assert not (owner != client["id"] and network.version == previous.version and network.overlaps(previous)), "overlapping clients"
                    networks.append((network, client["id"]))
        for policy_item in policies:
            assert policy_item["mode"] in {"all_web", "allowlist"}, "invalid policy mode"
            assert policy_item["tls_profile_id"] in profile_ids, "missing TLS profile"
            if policy_item["mode"] == "all_web":
                assert not policy_item["destinations"], "ambiguous all_web destinations"
            for target in policy_item["destinations"]:
                assert re.fullmatch(r"[a-z0-9.-]+", target["domain"]), "noncanonical example domain"
                assert type(target["include_subdomains"]) is bool, "subdomain flag must be bool"
        for profile in profiles:
            assert profile["mode"] in {"tunnel", "inspect"}, "invalid TLS mode"
            assert profile["proxy_transport"] in {"http", "tls"}, "invalid proxy transport"
            if profile["proxy_transport"] == "tls":
                assert profile["cert_ref"], "TLS transport requires certificate reference"
            if profile["mode"] == "inspect":
                assert profile["ca_ref"], "inspection requires CA reference"
    except (AssertionError, KeyError, TypeError, ValueError) as exc:
        errors.append(f"examples/policy.json: invalid example: {exc}")


def main() -> int:
    errors: list[str] = []
    files = repo_files()
    markdown_count = link_count = json_count = 0
    documents = {}
    for path in files:
        label = str(path.relative_to(ROOT))
        if path.suffix not in TEXT_SUFFIXES and path.name not in TEXT_NAMES:
            continue
        content = path.read_text(encoding="utf-8")
        if content and not content.endswith("\n"):
            errors.append(f"{label}: missing final newline")
        for line_no, line in enumerate(content.splitlines(), 1):
            if line.rstrip() != line:
                errors.append(f"{label}:{line_no}: trailing whitespace")
        if path.suffix == ".json":
            try:
                documents[label] = json.loads(content)
                json_count += 1
            except json.JSONDecodeError as exc:
                errors.append(f"{label}: invalid JSON: {exc}")
        if path.suffix != ".md":
            continue
        markdown_count += 1
        for match in LINK.finditer(content):
            raw = match.group(1).strip("<>")
            parts = urlsplit(raw)
            if parts.scheme or parts.netloc or not parts.path:
                continue
            target = (path.parent / unquote(parts.path)).resolve()
            link_count += 1
            if not target.is_relative_to(ROOT):
                errors.append(f"{label}: local link escapes repository: {raw}")
            elif not target.exists():
                errors.append(f"{label}: broken local link: {raw}")
    api = documents.get("api/openapi.json")
    operation_count = check_api(api, errors) if isinstance(api, dict) else 0
    if not api:
        errors.append("api/openapi.json: missing contract")
    policy = documents.get("examples/policy.json")
    if isinstance(policy, dict):
        check_policy_example(policy, errors)
    else:
        errors.append("examples/policy.json: missing example")
    if errors:
        print("Documentation checks failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print(f"OK: {markdown_count} Markdown files, {link_count} local links, {json_count} JSON files, {operation_count} API operations; refs, example and whitespace checked.")
    print("Scope: local checks only; not full OpenAPI validation, remote link validation or runtime tests.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
