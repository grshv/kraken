package httpapi

import (
	"encoding/json"
	"testing"

	"kraken.local/kraken/internal/help"
)

func TestHelpIndexAndArticle(t *testing.T) {
	_, h := fixtureServer(t)
	// Anonymous: read permission required.
	if w := request(h, "GET", "/help", nil, nil, "", ""); w.Code != 401 {
		t.Fatalf("anonymous help index: got %d, want 401", w.Code)
	}
	cookie, _ := loginTest(t, h)
	w := request(h, "GET", "/help", nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatalf("help index: got %d, want 200", w.Code)
	}
	var got struct {
		Articles []help.Entry `json:"articles"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil {
		t.Fatal(e)
	}
	if len(got.Articles) == 0 {
		t.Fatal("help index is empty")
	}
	// The HTTP index mirrors the package index ordering and content.
	want := help.Index()
	if len(got.Articles) != len(want) {
		t.Fatalf("help index length: got %d, want %d", len(got.Articles), len(want))
	}
	for i, e := range got.Articles {
		if e != want[i] {
			t.Fatalf("help index[%d]: got %+v, want %+v", i, e, want[i])
		}
	}
	first := got.Articles[0]
	w = request(h, "GET", "/help/"+first.Slug, nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatalf("help article %s: got %d, want 200", first.Slug, w.Code)
	}
	var a help.ArticleBody
	if e := json.Unmarshal(w.Body.Bytes(), &a); e != nil {
		t.Fatal(e)
	}
	if a.Slug != first.Slug || a.Markdown == "" {
		t.Fatalf("help article %s body mismatch: %+v", first.Slug, a)
	}
}

func TestHelpArticleNotFound(t *testing.T) {
	_, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	w := request(h, "GET", "/help/does-not-exist", nil, cookie, "", "")
	if w.Code != 404 {
		t.Fatalf("unknown help article: got %d, want 404", w.Code)
	}
	var v map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if v["code"] != "not_found" {
		t.Fatalf("unknown help article code: got %v, want not_found", v["code"])
	}
}

func TestHelpAnonymousArticle(t *testing.T) {
	_, h := fixtureServer(t)
	// Authentication runs before slug lookup, so an anonymous request never
	// reveals whether an article exists.
	if w := request(h, "GET", "/help/getting-started", nil, nil, "", ""); w.Code != 401 {
		t.Fatalf("anonymous help article: got %d, want 401", w.Code)
	}
	if w := request(h, "GET", "/help/does-not-exist", nil, nil, "", ""); w.Code != 401 {
		t.Fatalf("anonymous unknown help article should still be 401 (auth before lookup): got %d", w.Code)
	}
}
