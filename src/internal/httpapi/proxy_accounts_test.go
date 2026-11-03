package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyAccountExportWrittenOnMutation(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	w := request(h, "POST", "/proxy-accounts", map[string]any{"username": "build", "password": "test-only-proxy-password"}, cookie, csrf, "https://panel.test")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	b, e := os.ReadFile(s.Runtime.Options.ProxyAccounts)
	if e != nil {
		t.Fatal("export not written after create", e)
	}
	if !strings.Contains(string(b), `"username":"build"`) || strings.Contains(string(b), "test-only-proxy-password") {
		t.Fatalf("unexpected export content: %s", b)
	}
	w = request(h, "PATCH", "/proxy-accounts/build", map[string]any{"enabled": false}, cookie, csrf, "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	b, _ = os.ReadFile(s.Runtime.Options.ProxyAccounts)
	if strings.Contains(string(b), `"username":"build"`) {
		t.Fatalf("disabled account remained in export: %s", b)
	}
}

func TestProxyAccountExportFailureReturns503(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	dir := filepath.Dir(s.Runtime.Options.ProxyAccounts)
	if e := os.Chmod(dir, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(dir, 0750)
	w := request(h, "POST", "/proxy-accounts", map[string]any{"username": "broken", "password": "test-only-proxy-password"}, cookie, csrf, "https://panel.test")
	if w.Code != 503 {
		t.Fatalf("expected 503 on export failure, got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "hash") {
		t.Fatalf("response leaked credential material: %s", w.Body.String())
	}
}

func TestProxyAccountAPI(t *testing.T) {
	_, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	w := request(h, "POST", "/proxy-accounts", map[string]any{"username": "build", "password": "test-only-proxy-password"}, cookie, csrf, "https://panel.test")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Fatal("password field exposed")
	}
	w = request(h, "GET", "/proxy-accounts", nil, cookie, "", "")
	var list struct {
		Items []struct {
			Username string `json:"username"`
			Enabled  bool   `json:"enabled"`
		} `json:"items"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &list); e != nil || len(list.Items) != 1 || list.Items[0].Username != "build" {
		t.Fatal(w.Body.String(), e)
	}
	w = request(h, "PATCH", "/proxy-accounts/build", map[string]any{"enabled": false, "password": "new-test-proxy-password"}, cookie, csrf, "https://panel.test")
	if w.Code != 200 || strings.Contains(w.Body.String(), "password") {
		t.Fatal(w.Body.String())
	}
	if w = request(h, "POST", "/proxy-accounts", map[string]any{"username": "build", "password": "test-only-proxy-password"}, cookie, csrf, "https://panel.test"); w.Code != 409 {
		t.Fatal("duplicate response", w.Code)
	}
	if w = request(h, "PATCH", "/proxy-accounts/unknown", map[string]any{"enabled": false}, cookie, csrf, "https://panel.test"); w.Code != 404 {
		t.Fatal("unknown account", w.Code)
	}
	if w = request(h, "POST", "/proxy-accounts", map[string]any{"username": "invalid\nname", "password": "test-only-proxy-password"}, cookie, csrf, "https://panel.test"); w.Code != 400 {
		t.Fatal("username injection accepted")
	}
	if w = request(h, "POST", "/proxy-accounts", map[string]any{"username": "other", "password": "test-only-password\n"}, cookie, csrf, "https://panel.test"); w.Code != 400 {
		t.Fatal("protocol control character accepted")
	}
	if w = request(h, "GET", "/proxy-accounts", nil, nil, "", ""); w.Code != 401 {
		t.Fatal("unauthenticated accounts list")
	}
	if w = request(h, "PATCH", "/proxy-accounts/build", map[string]any{"enabled": true}, cookie, "", "https://panel.test"); w.Code != 403 {
		t.Fatal("CSRF-free mutation")
	}
}
