// Package help exposes the in-binary help articles embedded from articles/*.md.
//
// The article set is fixed at build time: each Markdown file under articles/
// carries front matter (slug, title, section, order, optional screen) and a body. Parsing happens
// once at init; a malformed article is a build-time defect and panics rather
// than degrading into a half-empty help index.
package help

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed articles/*.md
var files embed.FS

// sectionOrder fixes the sequence of sections in the index. Front matter carries
// only the per-article order field; the section sequence is a property of the
// help tree, not an authored value, so it is owned by code rather than metadata.
var sectionOrder = []string{
	"Начало работы",
	"Подключение машин",
	"Сценарии",
	"Как это работает",
}

// screens are the UI sections an article may link to with "Открыть в
// интерфейсе"; they mirror the web NavId values (help itself excluded).
var screens = map[string]bool{
	"clients": true, "policies": true, "log": true, "audit": true, "accounts": true, "settings": true,
}

// Entry is one row in the help index. Screen is the UI section the article
// describes, empty when it is conceptual.
type Entry struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Section string `json:"section"`
	Order   int    `json:"order"`
	Screen  string `json:"screen,omitempty"`
}

// ArticleBody is an entry plus its markdown body. The type name differs from
// the constructor Article below because Go forbids a type and func sharing one
// name in the same block; the JSON shape is unchanged.
type ArticleBody struct {
	Entry
	Markdown string `json:"markdown"`
}

var (
	articles []Entry
	bodies   = map[string]string{}
)

func init() {
	entries, e := files.ReadDir("articles")
	if e != nil {
		panic(fmt.Sprintf("help: cannot read embedded articles: %v", e))
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		raw, e := files.ReadFile(path.Join("articles", ent.Name()))
		if e != nil {
			panic(fmt.Sprintf("help: cannot read %s: %v", ent.Name(), e))
		}
		entry, body, e := parse(ent.Name(), string(raw))
		if e != nil {
			panic(fmt.Sprintf("help: %s: %v", ent.Name(), e))
		}
		if _, dup := bodies[entry.Slug]; dup {
			panic(fmt.Sprintf("help: duplicate slug %q", entry.Slug))
		}
		articles = append(articles, entry)
		bodies[entry.Slug] = body
	}
	if len(articles) == 0 {
		panic("help: no articles embedded")
	}
	sort.Slice(articles, func(i, j int) bool {
		ri, rj := sectionRank(articles[i].Section), sectionRank(articles[j].Section)
		if ri != rj {
			return ri < rj
		}
		return articles[i].Order < articles[j].Order
	})
}

func sectionRank(s string) int {
	for i, name := range sectionOrder {
		if name == s {
			return i
		}
	}
	return len(sectionOrder)
}

// Index returns the article index sorted by section order, then by article order.
// The returned slice is a copy and may be mutated by callers.
func Index() []Entry {
	out := make([]Entry, len(articles))
	copy(out, articles)
	return out
}

// Article returns the entry plus markdown body for the given slug. The second
// value is false for an unknown slug.
func Article(slug string) (ArticleBody, bool) {
	body, ok := bodies[slug]
	if !ok {
		return ArticleBody{}, false
	}
	for _, e := range articles {
		if e.Slug == slug {
			return ArticleBody{Entry: e, Markdown: body}, true
		}
	}
	return ArticleBody{}, false
}

// parse reads front matter and body. Front matter is a YAML-like block delimited
// by lines containing exactly "---"; the four keys slug, title, section and
// order are required, screen is optional and must name a UI section, no other
// keys are accepted, and slug must equal the file stem so a renamed file never
// silently breaks a help: link.
func parse(filename, raw string) (Entry, string, error) {
	const sep = "---"
	lines := strings.Split(raw, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != sep {
		return Entry{}, "", fmt.Errorf("missing opening front matter delimiter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == sep {
			end = i
			break
		}
	}
	if end == -1 {
		return Entry{}, "", fmt.Errorf("missing closing front matter delimiter")
	}
	var (
		slug, title, section string
		screen               string
		order                int
		hasSlug, hasTitle    bool
		hasSection, hasOrder bool
	)
	for _, line := range lines[1:end] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return Entry{}, "", fmt.Errorf("invalid front matter line: %q", line)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "slug":
			slug = val
			hasSlug = true
		case "title":
			title = val
			hasTitle = true
		case "section":
			section = val
			hasSection = true
		case "order":
			n, e := strconv.Atoi(val)
			if e != nil {
				return Entry{}, "", fmt.Errorf("invalid order %q: %v", val, e)
			}
			order = n
			hasOrder = true
		case "screen":
			if !screens[val] {
				return Entry{}, "", fmt.Errorf("unknown screen %q", val)
			}
			screen = val
		default:
			return Entry{}, "", fmt.Errorf("unknown front matter key %q", key)
		}
	}
	if !hasSlug || !hasTitle || !hasSection || !hasOrder {
		return Entry{}, "", fmt.Errorf("front matter must define slug, title, section and order")
	}
	stem := strings.TrimSuffix(filename, ".md")
	if slug != stem {
		return Entry{}, "", fmt.Errorf("slug %q does not match file stem %q", slug, stem)
	}
	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")
	return Entry{Slug: slug, Title: title, Section: section, Order: order, Screen: screen}, body, nil
}
