package csp

import (
	"context"
	"strings"
	"testing"
)

func TestHeader(t *testing.T) {
	h := "sha256-StEaX+se6YS7pqjzrzMIA0KaX9zF/8zAhvQXZAe5epY="
	got := Header("abc", []string{h, "'unsafe-inline'", "sha256-short", "sha256-" + strings.Repeat("A", 43) + "=; script-src *"})
	want := "style-src 'self' 'nonce-abc' '" + h + "';"
	if !strings.Contains(got, want) {
		t.Fatalf("header %q lacks %q", got, want)
	}
	for _, bad := range []string{"unsafe-inline", "sha256-short", "script-src *"} {
		if strings.Contains(got, bad) {
			t.Fatalf("header %q admits %q", got, bad)
		}
	}
	if !strings.Contains(got, "script-src 'self';") || strings.Contains(Header("", nil), "nonce") {
		t.Fatalf("scripts or empty nonce: %q", got)
	}
}

func TestNonce(t *testing.T) {
	a, b := NewNonce(), NewNonce()
	if a == b || len(a) != 24 {
		t.Fatalf("nonces %q %q", a, b)
	}
	if Nonce(WithNonce(context.Background(), a)) != a || Nonce(context.Background()) != "" {
		t.Fatal("context round trip")
	}
}
