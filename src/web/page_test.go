package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"kraken.local/kraken/internal/csp"
)

// index.html carries the response's nonce, never the placeholder, and the
// embedded build lists valid style hashes.
func TestPageNonceAndStyleHashes(t *testing.T) {
	for _, path := range []string{"/", "/index.html", "/clients/deep-link"} {
		r := httptest.NewRequest("GET", path, nil)
		r = r.WithContext(csp.WithNonce(r.Context(), "n0nce+TEST=="))
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, r)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `name="csp-nonce" content="n0nce+TEST=="`) || strings.Contains(body, "__CSP_NONCE__") {
			t.Fatalf("%s: %d %s", path, w.Code, body)
		}
	}
	hashes := StyleHashes()
	if len(hashes) == 0 {
		t.Fatal("no style hashes")
	}
	for _, h := range hashes {
		if !csp.ValidHash(h) {
			t.Fatalf("bad hash %q", h)
		}
	}
}
