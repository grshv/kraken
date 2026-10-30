package httpapi

import (
	"testing"

	"kraken.local/kraken/internal/events"
)

// A domain that resolved to a protected or internal address is explained by
// the reason Squid logged, not by the static domain check.
func TestExplainUsesLoggedReason(t *testing.T) {
	snap := inspectorSnapshot()
	cid := "c-a"
	reason := func(r string) *string { return &r }
	base := events.Event{SourceIP: "10.1.0.5", ClientID: &cid, Destination: "archive.ubuntu.com:443", Method: "CONNECT", ProxyResult: "TCP_DENIED/403"}
	if ex := explainEvent(base, snap); ex.reason != "allowed_by_policy" {
		t.Fatalf("static: %s", ex.reason)
	}
	for _, r := range []string{"protected", "private"} {
		v := base
		v.DenyReason = reason(r)
		if ex := explainEvent(v, snap); ex.decision != "denied" || ex.reason != "protected_destination" {
			t.Fatalf("%s: %+v", r, ex)
		}
	}
	// With internal destinations opened in the evaluated snapshot, "private"
	// no longer explains the denial.
	open := inspectorSnapshot()
	open.Settings.AllowPrivateDestinations = true
	v := base
	v.DenyReason = reason("private")
	if ex := explainEvent(v, open); ex.reason != "allowed_by_policy" {
		t.Fatalf("private opened: %+v", ex)
	}
	v.DenyReason = reason("no_rule")
	if ex := explainEvent(v, snap); ex.reason != "allowed_by_policy" {
		t.Fatalf("no_rule keeps the static answer: %+v", ex)
	}
}
