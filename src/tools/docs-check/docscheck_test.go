package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// walkFiles enumerates regular files under root as slash-relative paths,
// standing in for `git ls-files` so tests do not need a git repository.
func walkFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files
}

func runCheck(t *testing.T, root string) []string {
	t.Helper()
	errs, _, _, _, _ := check(root, walkFiles(root))
	return errs
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}

func hasErr(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

func mustHave(t *testing.T, errs []string, substr string) {
	t.Helper()
	if !hasErr(errs, substr) {
		t.Fatalf("expected error containing %q, got %v", substr, errs)
	}
}

// baseTree is a minimal tree that check() reports clean. Each test mutates it.
func baseTree(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "docs/README.md", "# Карта документации\n")
	write(t, root, "docs/adr/README.md", "# Реестр ADR\n")
	write(t, root, "api/openapi.json", `{
  "openapi": "3.1.1",
  "info": {"version": "1.0"},
  "paths": {
    "/things": {
      "get": {"operationId": "listThings", "responses": {"200": {"description": "ok"}}}
    }
  }
}
`)
	write(t, root, "examples/policy.json", `{
  "schema_version": 1,
  "clients": [{"id":"c1","policy_id":"p1","source_networks":["10.0.0.0/24"],"enabled":true}],
  "policies": [{"id":"p1","mode":"all_web","tls_profile_id":"t1","destinations":[]}],
  "tls_profiles": [{"id":"t1","mode":"tunnel","proxy_transport":"http"}]
}
`)
	if errs := runCheck(t, root); len(errs) != 0 {
		t.Fatalf("base tree not clean: %v", errs)
	}
	return root
}

func TestBaseTreeClean(t *testing.T) { baseTree(t) }

func TestBrokenLocalLink(t *testing.T) {
	root := baseTree(t)
	write(t, root, "notes.md", "see [missing](nope.md)\n")
	mustHave(t, runCheck(t, root), "broken local link")
}

func TestLinkEscapesRepo(t *testing.T) {
	root := baseTree(t)
	write(t, root, "notes.md", "see [out](../../etc/hosts)\n")
	mustHave(t, runCheck(t, root), "escapes repository")
}

func TestMissingFinalNewline(t *testing.T) {
	root := baseTree(t)
	write(t, root, "notes.md", "no newline")
	mustHave(t, runCheck(t, root), "missing final newline")
}

func TestTrailingWhitespace(t *testing.T) {
	root := baseTree(t)
	write(t, root, "notes.md", "trailing   \n")
	mustHave(t, runCheck(t, root), "trailing whitespace")
}

func TestInvalidJSON(t *testing.T) {
	root := baseTree(t)
	write(t, root, "bad.json", "{not json")
	mustHave(t, runCheck(t, root), "invalid JSON")
}

func TestNestedAgents(t *testing.T) {
	root := baseTree(t)
	write(t, root, "sub/AGENTS.md", "# nested\n")
	mustHave(t, runCheck(t, root), "nested AGENTS.md")
}

func TestOrphanDoc(t *testing.T) {
	root := baseTree(t)
	write(t, root, "docs/orphan.md", "# Orphan\n")
	mustHave(t, runCheck(t, root), "not listed in docs/README.md")
}

func TestDatedHeadingInDescriptiveDoc(t *testing.T) {
	root := baseTree(t)
	write(t, root, "docs/operations/run.md", "# Изменения 2026-01-01\n")
	mustHave(t, runCheck(t, root), "dated heading")
}

func TestADRMissingFromRegistry(t *testing.T) {
	root := baseTree(t)
	write(t, root, "docs/adr/0099-x.md", "# ADR 0099\n\nСтатус: Accepted\n")
	mustHave(t, runCheck(t, root), "missing from docs/adr/README.md")
}

func TestADRStatusMismatch(t *testing.T) {
	root := baseTree(t)
	write(t, root, "docs/adr/README.md", "# Реестр ADR\n\n| ADR | Статус |\n| --- | --- |\n| [0099](0099-x.md) | Proposed |\n")
	write(t, root, "docs/adr/0099-x.md", "# ADR 0099\n\nСтатус: Accepted\n")
	mustHave(t, runCheck(t, root), "registry status differs from file status")
}

func TestADRMissingStatus(t *testing.T) {
	root := baseTree(t)
	write(t, root, "docs/adr/README.md", "# Реестр ADR\n\n| [0099](0099-x.md) | Accepted |\n")
	write(t, root, "docs/adr/0099-x.md", "# ADR 0099\nno status line\n")
	mustHave(t, runCheck(t, root), "missing 'Статус:")
}

func TestOpenAPIMissingContract(t *testing.T) {
	root := baseTree(t)
	if e := os.Remove(filepath.Join(root, "api/openapi.json")); e != nil {
		t.Fatal(e)
	}
	mustHave(t, runCheck(t, root), "missing contract")
}

func TestOpenAPIWrongVersion(t *testing.T) {
	root := baseTree(t)
	write(t, root, "api/openapi.json", `{"openapi":"3.0.0","info":{"version":"1.0"},"paths":{}}`)
	mustHave(t, runCheck(t, root), "expected versioned OpenAPI 3.1.1")
}

func TestOpenAPIUnresolvedRef(t *testing.T) {
	root := baseTree(t)
	write(t, root, "api/openapi.json", `{"openapi":"3.1.1","info":{"version":"1.0"},"paths":{},"components":{"x":{"$ref":"#/missing"}}}`)
	mustHave(t, runCheck(t, root), "unresolved $ref")
}

func TestOpenAPIMissingOperationId(t *testing.T) {
	root := baseTree(t)
	write(t, root, "api/openapi.json", `{"openapi":"3.1.1","info":{"version":"1.0"},"paths":{"/things":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	mustHave(t, runCheck(t, root), "missing/duplicate operationId")
}

func TestOpenAPIMissingPathParam(t *testing.T) {
	root := baseTree(t)
	write(t, root, "api/openapi.json", `{"openapi":"3.1.1","info":{"version":"1.0"},"paths":{"/things/{id}":{"get":{"operationId":"one","responses":{"200":{"description":"ok"}}}}}}`)
	mustHave(t, runCheck(t, root), "missing required path parameter id")
}

func TestOpenAPIMissingCSRF(t *testing.T) {
	root := baseTree(t)
	write(t, root, "api/openapi.json", `{"openapi":"3.1.1","info":{"version":"1.0"},"paths":{"/things":{"post":{"operationId":"make","responses":{"200":{"description":"ok"}}}}}}`)
	mustHave(t, runCheck(t, root), "missing CSRF header contract")
}

func TestPolicyMissingExample(t *testing.T) {
	root := baseTree(t)
	if e := os.Remove(filepath.Join(root, "examples/policy.json")); e != nil {
		t.Fatal(e)
	}
	mustHave(t, runCheck(t, root), "missing example")
}

func TestPolicyBadMode(t *testing.T) {
	root := baseTree(t)
	write(t, root, "examples/policy.json", `{"schema_version":1,"clients":[],"policies":[{"id":"p1","mode":"open","tls_profile_id":"t1","destinations":[]}],"tls_profiles":[{"id":"t1","mode":"tunnel","proxy_transport":"http"}]}`)
	mustHave(t, runCheck(t, root), "invalid policy mode")
}

func TestPolicyOverlappingClients(t *testing.T) {
	root := baseTree(t)
	write(t, root, "examples/policy.json", `{"schema_version":1,"clients":[{"id":"c1","policy_id":"p1","source_networks":["10.0.0.0/24"],"enabled":true},{"id":"c2","policy_id":"p1","source_networks":["10.0.0.0/24"],"enabled":true}],"policies":[{"id":"p1","mode":"all_web","tls_profile_id":"t1","destinations":[]}],"tls_profiles":[{"id":"t1","mode":"tunnel","proxy_transport":"http"}]}`)
	mustHave(t, runCheck(t, root), "overlapping clients")
}

func TestPolicyDefaultRouteForbidden(t *testing.T) {
	root := baseTree(t)
	write(t, root, "examples/policy.json", `{"schema_version":1,"clients":[{"id":"c1","policy_id":"p1","source_networks":["0.0.0.0/0"],"enabled":true}],"policies":[{"id":"p1","mode":"all_web","tls_profile_id":"t1","destinations":[]}],"tls_profiles":[{"id":"t1","mode":"tunnel","proxy_transport":"http"}]}`)
	mustHave(t, runCheck(t, root), "default source route forbidden")
}

func TestPolicyNoncanonicalDomain(t *testing.T) {
	root := baseTree(t)
	write(t, root, "examples/policy.json", `{"schema_version":1,"clients":[{"id":"c1","policy_id":"p1","source_networks":["10.0.0.0/24"],"enabled":true}],"policies":[{"id":"p1","mode":"allowlist","tls_profile_id":"t1","destinations":[{"domain":"UPPER","include_subdomains":false}]}],"tls_profiles":[{"id":"t1","mode":"tunnel","proxy_transport":"http"}]}`)
	mustHave(t, runCheck(t, root), "noncanonical example domain")
}

func TestPolicyTLSWithoutCert(t *testing.T) {
	root := baseTree(t)
	write(t, root, "examples/policy.json", `{"schema_version":1,"clients":[{"id":"c1","policy_id":"p1","source_networks":["10.0.0.0/24"],"enabled":true}],"policies":[{"id":"p1","mode":"all_web","tls_profile_id":"t1","destinations":[]}],"tls_profiles":[{"id":"t1","mode":"tunnel","proxy_transport":"tls"}]}`)
	mustHave(t, runCheck(t, root), "TLS transport requires certificate reference")
}

func TestHelpMissingFrontMatter(t *testing.T) {
	root := baseTree(t)
	write(t, root, "src/internal/help/articles/no-fm.md", "body only\n")
	mustHave(t, runCheck(t, root), "missing front matter block")
}

func TestHelpDuplicateSlug(t *testing.T) {
	root := baseTree(t)
	write(t, root, "src/internal/help/articles/a.md", "---\nslug: a\ntitle: A\nsection: S\norder: 1\n---\nbody\n")
	write(t, root, "src/internal/help/articles/b.md", "---\nslug: a\ntitle: B\nsection: S\norder: 2\n---\nbody\n")
	mustHave(t, runCheck(t, root), "duplicate slug")
}

func TestHelpSlugStemMismatch(t *testing.T) {
	root := baseTree(t)
	write(t, root, "src/internal/help/articles/article.md", "---\nslug: other\ntitle: A\nsection: S\norder: 1\n---\nbody\n")
	mustHave(t, runCheck(t, root), "does not match file stem")
}

func TestHelpLinkUnknownSlug(t *testing.T) {
	root := baseTree(t)
	write(t, root, "src/internal/help/articles/article.md", "---\nslug: article\ntitle: A\nsection: S\norder: 1\n---\nsee [x](help:missing)\n")
	mustHave(t, runCheck(t, root), "help link to unknown slug")
}

func TestHelpUnknownFrontMatterKey(t *testing.T) {
	root := baseTree(t)
	write(t, root, "src/internal/help/articles/article.md", "---\nslug: article\ntitle: A\nsection: S\norder: 1\ngarbage: yes\n---\nbody\n")
	mustHave(t, runCheck(t, root), "unknown front matter key")
}
