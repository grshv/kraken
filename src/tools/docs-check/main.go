// Command docs-check runs dependency-free checks for this repository's
// documentation: local Markdown links, ADR registry, docs map, dated headings,
// orphan documents, nested AGENTS.md, OpenAPI structure, the policy example,
// help-article front matter and whitespace. It is not a full OpenAPI linter and
// does not validate remote links.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

func run() error {
	root, e := repoRoot()
	if e != nil {
		return e
	}
	files, e := listFiles(root)
	if e != nil {
		return e
	}
	errs, md, links, jsons, ops := check(root, files)
	if len(errs) != 0 {
		fmt.Fprintln(os.Stderr, "Documentation checks failed:")
		for _, msg := range errs {
			fmt.Fprintln(os.Stderr, "- "+msg)
		}
		return errors.New("documentation checks failed")
	}
	fmt.Printf("OK: %d Markdown files, %d local links, %d JSON files, %d API operations; refs, ADR registry, docs map, headings, example and whitespace checked.\n", md, links, jsons, ops)
	fmt.Println("Scope: local checks only; not full OpenAPI validation, remote link validation or runtime tests.")
	return nil
}

// repoRoot returns the repository root: the argument if given, otherwise the
// nearest ancestor of the working directory that contains both docs/ and src/.
func repoRoot() (string, error) {
	if arg := rootFromArgs(); arg != "" {
		return arg, nil
	}
	dir, e := os.Getwd()
	if e != nil {
		return "", e
	}
	for {
		if isRepoRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root not found; pass it as an argument")
		}
		dir = parent
	}
}

func rootFromArgs() string {
	if len(os.Args) < 2 || os.Args[1] == "" {
		return ""
	}
	abs, e := filepath.Abs(os.Args[1])
	if e != nil {
		return ""
	}
	return abs
}

func isRepoRoot(dir string) bool {
	for _, name := range []string{"docs", "src"} {
		if info, e := os.Stat(filepath.Join(dir, name)); e != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// listFiles mirrors the Python tool's `git ls-files --cached --others
// --exclude-standard` enumeration so the checked set is identical.
func listFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	cmd.Dir = root
	out, e := cmd.Output()
	if e != nil {
		return nil, fmt.Errorf("git ls-files: %w", e)
	}
	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	return files, nil
}

var (
	linkRe       = regexp.MustCompile(`!?\[[^\]\n]*\]\((<[^>]+>|[^\s)]+)(?:\s+"[^"]*")?\)`)
	textSuffixes = map[string]bool{".md": true, ".json": true, ".py": true, ".yml": true, ".yaml": true, ".toml": true}
	textNames    = map[string]bool{"Makefile": true, ".gitignore": true, ".gitattributes": true, ".editorconfig": true, ".env.example": true}
	httpMethods  = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true, "trace": true}

	adrStatus    = regexp.MustCompile(`Статус:\s*\**(Proposed|Accepted|Superseded|Deprecated)\b`)
	datedHeading = regexp.MustCompile(`^#+ .*(\d{2}\.\d{2}\.\d{4}|\d{4}-\d{2}-\d{2}|\(\d+\.\d+\.\d+\))`)
	// Descriptive documents state the current behaviour; history belongs in git log.
	undatedPrefixes = []string{"docs/architecture/", "docs/operations/", "docs/development/", "docs/security/"}
	undatedFiles    = map[string]bool{"docs/status.md": true, "docs/README.md": true, "docs/product/requirements.md": true, "docs/product/interface.md": true}

	helpArticles    = "src/internal/help/articles"
	helpFrontMatter = regexp.MustCompile(`(?s)\A---\n(.*?\n)---\n`)
	helpLink        = regexp.MustCompile(`\[([^\]]*)\]\(help:([^\s)]+)\)`)
	helpKeys        = []string{"slug", "title", "section", "order"}
)

const (
	helpArticlesDir = "src/internal/help/articles/"
)

// check runs every check over files (paths relative to root) and returns the
// error list plus summary counts. It is split from run() so tests can feed a
// synthetic file list without invoking git.
func check(root string, files []string) (errs []string, mdCount, linkCount, jsonCount, opCount int) {
	documents := map[string]any{}
	for _, rel := range files {
		path := filepath.Join(root, rel)
		info, e := os.Stat(path)
		if e != nil || !info.Mode().IsRegular() {
			// git ls-files may list a path that is a broken symlink or was
			// removed before read; match Python's is_file() by skipping it.
			continue
		}
		if !textSuffixes[strings.ToLower(filepath.Ext(rel))] && !textNames[filepath.Base(rel)] {
			continue
		}
		content, e := os.ReadFile(path)
		if e != nil {
			errs = append(errs, rel+": "+e.Error())
			continue
		}
		s := string(content)
		if len(s) > 0 && !strings.HasSuffix(s, "\n") {
			errs = append(errs, rel+": missing final newline")
		}
		for i, line := range strings.Split(s, "\n") {
			if strings.TrimRight(line, " \t") != line {
				errs = append(errs, rel+":"+strconv.Itoa(i+1)+": trailing whitespace")
			}
		}
		if strings.ToLower(filepath.Ext(rel)) == ".json" {
			var v any
			if e := json.Unmarshal(content, &v); e != nil {
				errs = append(errs, rel+": invalid JSON: "+e.Error())
			} else {
				documents[rel] = v
				jsonCount++
			}
		}
		if strings.ToLower(filepath.Ext(rel)) != ".md" {
			continue
		}
		mdCount++
		for _, m := range linkRe.FindAllStringSubmatch(s, -1) {
			raw := strings.Trim(m[1], "<>")
			u, e := url.Parse(raw)
			if e != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
				continue
			}
			target := absLink(root, filepath.Dir(rel), u.Path)
			linkCount++
			rel2, e := filepath.Rel(root, target)
			if e != nil || strings.HasPrefix(filepath.ToSlash(rel2), "../") {
				errs = append(errs, rel+": local link escapes repository: "+raw)
				continue
			}
			if _, e := os.Stat(target); errors.Is(e, fs.ErrNotExist) {
				errs = append(errs, rel+": broken local link: "+raw)
			}
		}
	}
	checkStructure(root, files, &errs)
	if api, ok := documents["api/openapi.json"].(map[string]any); ok {
		opCount = checkAPI(api, &errs)
	} else {
		errs = append(errs, "api/openapi.json: missing contract")
	}
	if policy, ok := documents["examples/policy.json"].(map[string]any); ok {
		checkPolicyExample(policy, &errs)
	} else {
		errs = append(errs, "examples/policy.json: missing example")
	}
	checkHelpArticles(root, files, &errs)
	return errs, mdCount, linkCount, jsonCount, opCount
}

// absLink resolves a markdown link target relative to the linking file's
// directory, then to an absolute path inside root.
func absLink(root, fileDir, raw string) string {
	decoded, e := url.PathUnescape(raw)
	if e != nil {
		decoded = raw
	}
	clean := filepath.Clean(filepath.Join(fileDir, decoded))
	abs := filepath.Join(root, clean)
	return abs
}

func linkedTargets(root, rel, content string) map[string]bool {
	targets := map[string]bool{}
	for _, m := range linkRe.FindAllStringSubmatch(content, -1) {
		raw := strings.Trim(m[1], "<>")
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
			continue
		}
		targets[absLink(root, filepath.Dir(rel), u.Path)] = true
	}
	return targets
}

func checkStructure(root string, files []string, errs *[]string) {
	markdown := map[string]string{}
	for _, rel := range files {
		if strings.HasSuffix(rel, ".md") {
			markdown[rel] = filepath.Join(root, rel)
		}
	}
	for rel := range markdown {
		if filepath.Base(rel) == "AGENTS.md" && rel != "AGENTS.md" {
			*errs = append(*errs, rel+": nested AGENTS.md is not allowed; use root AGENTS.md")
		}
	}
	docsMap := filepath.Join(root, "docs/README.md")
	adrIndex := filepath.Join(root, "docs/adr/README.md")
	mapped := readFileTargets(root, "docs/README.md", docsMap)
	adrText := ""
	if b, e := os.ReadFile(adrIndex); e == nil {
		adrText = string(b)
	}
	indexed := readFileTargets(root, "docs/adr/README.md", adrIndex)
	for rel, path := range markdown {
		if !strings.HasPrefix(rel, "docs/") || path == docsMap || path == adrIndex {
			continue
		}
		if strings.HasPrefix(rel, "docs/adr/") {
			if !indexed[path] {
				*errs = append(*errs, rel+": ADR is missing from docs/adr/README.md")
				continue
			}
			body, e := os.ReadFile(path)
			if e != nil {
				continue
			}
			status := adrStatus.FindSubmatch(body)
			if status == nil {
				*errs = append(*errs, rel+": missing 'Статус: <Proposed|Accepted|Superseded|Deprecated>'")
				continue
			}
			row := ""
			for _, line := range strings.Split(adrText, "\n") {
				if strings.Contains(line, "]("+filepath.Base(rel)+")") {
					row = line
					break
				}
			}
			want := strings.TrimRight(strings.TrimRight(row, " "), "|")
			want = strings.TrimSpace(strings.TrimRight(want, "|"))
			if !strings.HasSuffix(strings.TrimSpace(want), string(status[1])) {
				*errs = append(*errs, rel+": registry status differs from file status "+string(status[1]))
			}
			continue
		}
		if !mapped[path] {
			*errs = append(*errs, rel+": document is not listed in docs/README.md")
		}
		if undatedFiles[rel] || hasPrefix(rel, undatedPrefixes) {
			lines := strings.Split(string(mustRead(path)), "\n")
			for i, line := range lines {
				if datedHeading.MatchString(line) {
					*errs = append(*errs, rel+":"+strconv.Itoa(i+1)+": dated heading looks like a session log; describe current state")
				}
			}
		}
	}
}

func readFileTargets(root, rel, path string) map[string]bool {
	targets := map[string]bool{}
	if b, e := os.ReadFile(path); e == nil {
		for t := range linkedTargets(root, rel, string(b)) {
			targets[t] = true
		}
	}
	return targets
}

func mustRead(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func checkAPI(api map[string]any, errs *[]string) int {
	if v, _ := api["openapi"]; v != "3.1.1" {
		*errs = append(*errs, "api/openapi.json: expected versioned OpenAPI 3.1.1 document")
	}
	info, _ := api["info"].(map[string]any)
	if _, ok := info["version"]; !ok {
		*errs = append(*errs, "api/openapi.json: expected versioned OpenAPI 3.1.1 document")
	}
	checkRefs(api, api, "api/openapi.json", errs)
	seen := map[string]bool{}
	paths, _ := api["paths"].(map[string]any)
	for path, item := range paths {
		itemMap, _ := item.(map[string]any)
		for method, op := range itemMap {
			if !httpMethods[method] {
				continue
			}
			label := strings.ToUpper(method) + " " + path
			opMap, _ := op.(map[string]any)
			operationID, _ := opMap["operationId"].(string)
			if operationID == "" || seen[operationID] {
				*errs = append(*errs, label+": missing/duplicate operationId")
			}
			seen[operationID] = true
			if _, ok := opMap["responses"]; !ok {
				*errs = append(*errs, label+": missing responses")
			}
			params := []map[string]any{}
			if ip, ok := itemMap["parameters"].([]any); ok {
				for _, p := range ip {
					if pm, ok := p.(map[string]any); ok {
						params = append(params, pm)
					}
				}
			}
			if op, ok := opMap["parameters"].([]any); ok {
				for _, p := range op {
					if pm, ok := p.(map[string]any); ok {
						params = append(params, pm)
					}
				}
			}
			for _, name := range pathParamNames(path) {
				if !hasPathParam(params, name) {
					*errs = append(*errs, label+": missing required path parameter "+name)
				}
			}
			if method == "post" || method == "put" || method == "patch" || method == "delete" {
				if !hasCSRFHeader(params) {
					*errs = append(*errs, label+": missing CSRF header contract")
				}
			}
		}
	}
	return len(seen)
}

func pathParamNames(path string) []string {
	var names []string
	start := -1
	for i := 0; i < len(path); i++ {
		if path[i] == '{' {
			start = i + 1
		} else if path[i] == '}' && start >= 0 {
			names = append(names, path[start:i])
			start = -1
		}
	}
	return names
}

func hasPathParam(params []map[string]any, name string) bool {
	for _, p := range params {
		if pn, _ := p["name"].(string); pn == name {
			if pin, _ := p["in"].(string); pin == "path" {
				if req, _ := p["required"].(bool); req {
					return true
				}
			}
		}
	}
	return false
}

func hasCSRFHeader(params []map[string]any) bool {
	for _, p := range params {
		if pn, _ := p["name"].(string); pn == "X-CSRF-Token" {
			if pin, _ := p["in"].(string); pin == "header" {
				if req, _ := p["required"].(bool); req {
					return true
				}
			}
		}
	}
	return false
}

func checkRefs(node any, document map[string]any, label string, errs *[]string) {
	switch v := node.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok && ref != "" {
			if !strings.HasPrefix(ref, "#/") {
				*errs = append(*errs, label+": unsupported external $ref: "+ref)
			} else if !resolveRef(document, ref[2:]) {
				*errs = append(*errs, label+": unresolved $ref: "+ref)
			}
		}
		for _, value := range v {
			checkRefs(value, document, label, errs)
		}
	case []any:
		for _, value := range v {
			checkRefs(value, document, label, errs)
		}
	}
}

func resolveRef(document map[string]any, ptr string) bool {
	target := document
	for _, part := range strings.Split(ptr, "/") {
		decoded, e := url.PathUnescape(part)
		if e != nil {
			return false
		}
		decoded = strings.ReplaceAll(decoded, "~1", "/")
		decoded = strings.ReplaceAll(decoded, "~0", "~")
		next, ok := target[decoded]
		if !ok {
			return false
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return false
		}
		target = nextMap
	}
	return true
}

// checkPolicyExample checks key semantic invariants, not general JSON Schema.
func checkPolicyExample(policy map[string]any, errs *[]string) {
	fail := func(msg string) { *errs = append(*errs, "examples/policy.json: invalid example: "+msg) }
	if policy["schema_version"] != float64(1) {
		fail("unknown schema version")
		return
	}
	clients, _ := policy["clients"].([]any)
	policies, _ := policy["policies"].([]any)
	profiles, _ := policy["tls_profiles"].([]any)
	if len(ids(clients)) != len(clients) {
		fail("duplicate IDs")
	}
	policyIDs := set(ids(policies))
	profileIDs := set(ids(profiles))
	networks := []netOwner{}
	for _, c := range clients {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		cid, _ := cm["id"].(string)
		if !policyIDs[asStr(cm["policy_id"])] {
			fail("missing policy")
		}
		nets, _ := cm["source_networks"].([]any)
		if len(nets) == 0 {
			fail("empty source networks")
		}
		enabled, _ := cm["enabled"].(bool)
		for _, raw := range nets {
			s, _ := raw.(string)
			net, e := parseNetwork(s)
			if e != nil {
				fail(e.Error())
				continue
			}
			if net.prefixLen() == 0 {
				fail("default source route forbidden")
			}
			if enabled {
				for _, prev := range networks {
					if prev.owner != cid && net.sameVersion(prev.net) && net.overlaps(prev.net) {
						fail("overlapping clients")
					}
				}
				networks = append(networks, netOwner{net: net, owner: cid})
			}
		}
	}
	for _, p := range policies {
		pm, _ := p.(map[string]any)
		if pm == nil {
			continue
		}
		mode, _ := pm["mode"].(string)
		if mode != "all_web" && mode != "allowlist" {
			fail("invalid policy mode")
		}
		if !profileIDs[asStr(pm["tls_profile_id"])] {
			fail("missing TLS profile")
		}
		dests, _ := pm["destinations"].([]any)
		if mode == "all_web" && len(dests) != 0 {
			fail("ambiguous all_web destinations")
		}
		for _, d := range dests {
			dm, _ := d.(map[string]any)
			if dm == nil {
				continue
			}
			domain, _ := dm["domain"].(string)
			if !canonicalDomainRe.MatchString(domain) {
				fail("noncanonical example domain")
			}
			if _, ok := dm["include_subdomains"].(bool); !ok {
				fail("subdomain flag must be bool")
			}
		}
	}
	for _, p := range profiles {
		pm, _ := p.(map[string]any)
		if pm == nil {
			continue
		}
		if pm["mode"] != "tunnel" {
			fail("invalid TLS mode")
		}
		transport, _ := pm["proxy_transport"].(string)
		if transport != "http" && transport != "tls" {
			fail("invalid proxy transport")
		}
		if transport == "tls" {
			if asStr(pm["cert_ref"]) == "" {
				fail("TLS transport requires certificate reference")
			}
		}
	}
}

var canonicalDomainRe = regexp.MustCompile(`^[a-z0-9.-]+$`)

type netOwner struct {
	net   *ipNet
	owner string
}

func ids(items []any) []string {
	out := []string{}
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if id, _ := m["id"].(string); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

func set(items []string) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		out[it] = true
	}
	return out
}

func asStr(v any) string {
	s, _ := v.(string)
	return s
}

func checkHelpArticles(root string, files []string, errs *[]string) {
	var articles []string
	for _, rel := range files {
		if strings.HasPrefix(rel, helpArticlesDir) && strings.HasSuffix(rel, ".md") {
			articles = append(articles, rel)
		}
	}
	if len(articles) == 0 {
		// An empty tree is a build defect caught by the Go package init panic;
		// docs-check only validates the contract of present articles.
		return
	}
	slugs := map[string]string{}
	for _, rel := range articles {
		path := filepath.Join(root, rel)
		body, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		content := string(body)
		m := helpFrontMatter.FindStringSubmatch(content)
		if m == nil {
			*errs = append(*errs, rel+": missing front matter block delimited by '---'")
			continue
		}
		fields := map[string]string{}
		for _, line := range strings.Split(m[1], "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			key, val, found := strings.Cut(line, ":")
			if !found {
				*errs = append(*errs, rel+": invalid front matter line: "+strconv.Quote(line))
				continue
			}
			fields[strings.TrimSpace(key)] = strings.TrimSpace(val)
		}
		for _, key := range helpKeys {
			if _, ok := fields[key]; !ok {
				*errs = append(*errs, rel+": front matter missing "+strconv.Quote(key))
			}
		}
		for key := range fields {
			known := false
			for _, k := range helpKeys {
				if k == key {
					known = true
					break
				}
			}
			if !known {
				*errs = append(*errs, rel+": unknown front matter key "+strconv.Quote(key))
			}
		}
		slug := fields["slug"]
		if slug != "" {
			stem := strings.TrimSuffix(filepath.Base(rel), ".md")
			if slug != stem {
				*errs = append(*errs, rel+": slug "+strconv.Quote(slug)+" does not match file stem "+strconv.Quote(stem))
			}
			if other, ok := slugs[slug]; ok {
				*errs = append(*errs, rel+": duplicate slug "+strconv.Quote(slug)+" (also in "+other+")")
			} else {
				slugs[slug] = rel
			}
		}
	}
	for _, rel := range articles {
		path := filepath.Join(root, rel)
		body, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		for _, m := range helpLink.FindAllStringSubmatch(string(body), -1) {
			target := m[2]
			if _, ok := slugs[target]; !ok {
				*errs = append(*errs, rel+": help link to unknown slug "+strconv.Quote(target))
			}
		}
	}
}
