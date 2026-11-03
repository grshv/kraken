// Package csp builds the panel's Content-Security-Policy. Scripts load only
// from the panel itself. Inline <style> is admitted only by an exact sha256 of
// a known component style (web/public/csp-style-hashes.json) or by the
// per-response nonce that index.html hands to the Radix scroll lock; arbitrary
// inline styles stay blocked.
package csp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"regexp"
	"strings"
)

type nonceKey struct{}

// NewNonce returns 128 random bits, base64 encoded.
func NewNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// WithNonce stores the response's nonce for the page handler.
func WithNonce(ctx context.Context, nonce string) context.Context {
	return context.WithValue(ctx, nonceKey{}, nonce)
}

// Nonce returns the nonce stored by WithNonce, or "".
func Nonce(ctx context.Context) string {
	n, _ := ctx.Value(nonceKey{}).(string)
	return n
}

var hashRE = regexp.MustCompile(`^sha256-[A-Za-z0-9+/]{43}=$`)

// ValidHash reports whether s is a CSP sha256 source value.
func ValidHash(s string) bool { return hashRE.MatchString(s) }

// Header is the policy for one response.
func Header(nonce string, styleHashes []string) string {
	style := []string{"'self'"}
	if nonce != "" {
		style = append(style, "'nonce-"+nonce+"'")
	}
	for _, h := range styleHashes {
		if ValidHash(h) {
			style = append(style, "'"+h+"'")
		}
	}
	return "default-src 'self'; script-src 'self'; style-src " + strings.Join(style, " ") +
		"; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
}
