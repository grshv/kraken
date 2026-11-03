package httpapi

import "testing"

// HSTS must pin only the panel host: includeSubDomains would force HTTPS on
// sibling hosts (every *.localhost or *.corp name) and preload is meaningless
// for internal names.
func TestSecurityHeadersHSTSScope(t *testing.T) {
	_, h := fixtureServer(t)
	w := request(h, "GET", "/session", nil, nil, "", "")
	got := w.Header().Get("Strict-Transport-Security")
	if got != "max-age=31536000" {
		t.Fatalf("Strict-Transport-Security = %q", got)
	}
	if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("baseline security headers missing")
	}
}
