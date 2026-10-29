package help

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

var helpLink = regexp.MustCompile(`\[([^\]]*)\]\(help:([^\s)]+)\)`)

func TestIndexNonEmptyAndSorted(t *testing.T) {
	index := Index()
	if len(index) == 0 {
		t.Fatal("index is empty")
	}
	want := make([]Entry, len(index))
	copy(want, index)
	sort.SliceStable(want, func(i, j int) bool {
		ri, rj := sectionRank(want[i].Section), sectionRank(want[j].Section)
		if ri != rj {
			return ri < rj
		}
		return want[i].Order < want[j].Order
	})
	for i, e := range index {
		if e != want[i] {
			t.Fatalf("index not sorted at %d: got %+v, want %+v", i, e, want[i])
		}
	}
}

func TestArticleBySlug(t *testing.T) {
	for _, e := range Index() {
		a, ok := Article(e.Slug)
		if !ok {
			t.Fatalf("Article(%q) not found", e.Slug)
		}
		if a.Entry != e {
			t.Fatalf("Article(%q) entry mismatch: got %+v, want %+v", e.Slug, a.Entry, e)
		}
		if strings.TrimSpace(a.Markdown) == "" {
			t.Fatalf("Article(%q) has empty body", e.Slug)
		}
	}
}

func TestArticleUnknownSlug(t *testing.T) {
	if _, ok := Article("does-not-exist"); ok {
		t.Fatal("unknown slug returned an article")
	}
}

func TestSlugsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Index() {
		if seen[e.Slug] {
			t.Fatalf("duplicate slug %q", e.Slug)
		}
		seen[e.Slug] = true
	}
}

// TestHelpLinksResolve guards the same invariant as scripts/check_docs.py:
// every [text](help:slug) reference points to a real article. A broken
// link would render as a dead end in the help UI.
func TestHelpLinksResolve(t *testing.T) {
	slugs := map[string]bool{}
	for _, e := range Index() {
		slugs[e.Slug] = true
	}
	for _, e := range Index() {
		a, _ := Article(e.Slug)
		for _, m := range helpLink.FindAllStringSubmatch(a.Markdown, -1) {
			target := m[2]
			if !slugs[target] {
				t.Fatalf("article %q references unknown slug %q", e.Slug, target)
			}
		}
	}
}
