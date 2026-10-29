package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/store"
)

// TestConcurrentReadsLoginsAndDraftPUTs verifies the reader pool keeps reads
// (audit, status) fast and error-free under concurrent logins and draft writes.
// Logins must complete under 500 ms even with -race overhead.
func TestConcurrentReadsLoginsAndDraftPUTs(t *testing.T) {
	_, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	w := request(h, "GET", "/configuration/draft", nil, cookie, "", "")
	var d Draft
	json.Unmarshal(w.Body.Bytes(), &d)

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	start := make(chan struct{})

	// 8 reader goroutines: /audit and /status in a loop.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for j := 0; j < 10; j++ {
				if w := request(h, "GET", "/audit", nil, cookie, "", ""); w.Code == 503 {
					errs <- fmt.Errorf("reader %d: /audit returned 503", id)
					return
				}
				if w := request(h, "GET", "/status", nil, cookie, "", ""); w.Code == 503 {
					errs <- fmt.Errorf("reader %d: /status returned 503", id)
					return
				}
			}
		}(i)
	}

	// 2 login goroutines: full login flow (anonymous CSRF + POST /session).
	// loginSlots has capacity 2, so 2 concurrent logins test the reader pool
	// without tripping the rate limiter.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			t0 := time.Now()
			aw := request(h, "GET", "/session", nil, nil, "", "")
			var v map[string]any
			json.Unmarshal(aw.Body.Bytes(), &v)
			ac := aw.Result().Cookies()[0]
			lw := request(h, "POST", "/session", map[string]string{"username": "admin", "password": "test-password-only-123"}, ac, v["csrf_token"].(string), "https://panel.test")
			elapsed := time.Since(t0)
			if lw.Code != 200 {
				errs <- fmt.Errorf("login %d failed: %d %s", id, lw.Code, lw.Body.String())
				return
			}
			if elapsed > 500*time.Millisecond {
				errs <- fmt.Errorf("login %d too slow: %v", id, elapsed)
				return
			}
		}(i)
	}

	// 4 draft-PUT goroutines: conflicts (409) are expected and not errors.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			input := map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}
			w := request(h, "PUT", "/configuration/draft", input, cookie, csrf, "https://panel.test")
			if w.Code != 200 && w.Code != 409 {
				errs <- fmt.Errorf("draft PUT %d: unexpected %d %s", id, w.Code, w.Body.String())
			}
		}(i)
	}

	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestTouchThrottle verifies that last_seen_at is updated on the first
// authenticated request but NOT on a second one within 60 seconds.
func TestTouchThrottle(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	// The login itself already touched last_seen_at; verify the value.
	hash := tokenHash(cookie.Value)
	var first int64
	if e := s.Store.Read.QueryRow("SELECT last_seen_at FROM sessions WHERE token_hash=?", hash).Scan(&first); e != nil {
		t.Fatal(e)
	}
	// Second request within the throttle window — should NOT touch.
	if w := request(h, "GET", "/status", nil, cookie, "", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var second int64
	s.Store.Read.QueryRow("SELECT last_seen_at FROM sessions WHERE token_hash=?", hash).Scan(&second)
	if second != first {
		t.Fatalf("last_seen_at touched within 60s throttle window: %d -> %d", first, second)
	}
}

// TestSessionNotRevivedAfterDeletion verifies a deleted session cannot be
// revived by a concurrent touch: readSession reads the row, a reset deletes
// it, and the conditional touch + existence recheck rejects the stale session.
func TestSessionNotRevivedAfterDeletion(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	hash := tokenHash(cookie.Value)
	// Age last_seen_at so the next readSession will attempt a conditional touch.
	if _, e := s.Store.DB.Exec("UPDATE sessions SET last_seen_at=? WHERE token_hash=?", time.Now().Add(-120*time.Second).Unix(), hash); e != nil {
		t.Fatal(e)
	}
	// Delete the session row directly (simulating a concurrent password reset
	// that runs between the readSession reader lookup and its conditional touch).
	if _, e := s.Store.DB.Exec("DELETE FROM sessions WHERE token_hash=?", hash); e != nil {
		t.Fatal(e)
	}
	if w := request(h, "GET", "/status", nil, cookie, "", ""); w.Code != 401 {
		t.Fatalf("deleted session was accepted: %d", w.Code)
	}
	// Verify the touch noop did not re-create the row.
	var n int
	s.Store.Read.QueryRow("SELECT COUNT(*) FROM sessions WHERE token_hash=?", hash).Scan(&n)
	if n != 0 {
		t.Fatal("deleted session row was revived by touch noop")
	}
}

// TestStorageFullRejectsMutations verifies each mutating endpoint returns 507
// storage_full when the data volume is below the full threshold.
func TestStorageFullRejectsMutations(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	// Create a revision (needed for POST /operations) and a proxy account
	// (needed for PATCH /proxy-accounts/{username}) while disk is still OK.
	w := request(h, "GET", "/configuration/draft", nil, cookie, "", "")
	var d Draft
	json.Unmarshal(w.Body.Bytes(), &d)
	w = request(h, "POST", "/revisions", map[string]any{"expected_draft_version": d.Version}, cookie, csrf, "https://panel.test")
	var rev Revision
	json.Unmarshal(w.Body.Bytes(), &rev)
	request(h, "POST", "/proxy-accounts", map[string]string{"username": "pa", "password": "test-password-only-123"}, cookie, csrf, "https://panel.test")

	// Simulate a full disk.
	s.freeBytes = func() (uint64, error) { return storageFullBytes - 1, nil }

	// PUT/DELETE /configuration/draft, POST /revisions, POST /operations,
	// PATCH /users, PUT /users/scope, POST /proxy-accounts, PATCH
	// /proxy-accounts. Login and logout stay available.
	mutations := []struct {
		method, path string
		body         any
		needsCSRF    bool
	}{
		{"PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, true},
		{"DELETE", "/configuration/draft", map[string]any{"expected_version": d.Version + 1}, true},
		{"POST", "/revisions", map[string]any{"expected_draft_version": d.Version + 1}, true},
		{"PATCH", "/users/admin", map[string]any{"enabled": false}, true},
		{"PUT", "/users/admin/scope", map[string]any{"networks": []string{}, "client_ids": []string{}}, true},
		{"POST", "/proxy-accounts", map[string]string{"username": "pa2", "password": "test-password-only-123"}, true},
	}
	for _, m := range mutations {
		w := request(h, m.method, m.path, m.body, cookie, csrf, "https://panel.test")
		if w.Code != 507 {
			t.Errorf("%s %s: expected 507 storage_full, got %d %s", m.method, m.path, w.Code, w.Body.String())
		}
	}

	// POST /operations needs an Idempotency-Key header.
	body := fmt.Sprintf(`{"action":"validate","revision_id":"%s"}`, rev.ID)
	r := httptest.NewRequest("POST", "https://panel.test/api/v1/operations", bytes.NewBufferString(body))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://panel.test")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Idempotency-Key", "test-storage-full-operations-0001")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 507 {
		t.Errorf("POST /operations: expected 507, got %d %s", rec.Code, rec.Body.String())
	}

	// Login stays available on a full disk so the operator can see the state.
	aw := request(h, "GET", "/session", nil, nil, "", "")
	var v map[string]any
	json.Unmarshal(aw.Body.Bytes(), &v)
	ac := aw.Result().Cookies()[0]
	lw := request(h, "POST", "/session", map[string]string{"username": "admin", "password": "test-password-only-123"}, ac, v["csrf_token"].(string), "https://panel.test")
	if lw.Code != 200 {
		t.Errorf("POST /session (login) on full disk: expected 200, got %d %s", lw.Code, lw.Body.String())
	}

	// PATCH /proxy-accounts/pa.
	w = request(h, "PATCH", "/proxy-accounts/pa", map[string]any{"enabled": false}, cookie, csrf, "https://panel.test")
	if w.Code != 507 {
		t.Errorf("PATCH /proxy-accounts/pa: expected 507, got %d %s", w.Code, w.Body.String())
	}
}

// TestStorageFullAllowsReads verifies read endpoints still work below the full
// threshold — only mutations are rejected.
func TestStorageFullAllowsReads(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	s.freeBytes = func() (uint64, error) { return storageFullBytes - 1, nil }
	for _, p := range []string{"/status", "/audit", "/configuration/draft"} {
		if w := request(h, "GET", p, nil, cookie, "", ""); w.Code != 200 {
			t.Errorf("GET %s: expected 200 under storage_full, got %d", p, w.Code)
		}
	}
}

// TestStorageLowPausesEventCollection verifies CollectEvents pauses (returns
// nil, sets ingestion state to "storage_low") when the disk is below the low
// threshold, and resumes when space is restored.
func TestStorageLowPausesEventCollection(t *testing.T) {
	s, _ := fixtureServer(t)
	// Below storageLow but above storageFull: reads work, collection pauses.
	var low uint64 = storageFullBytes + 1
	s.freeBytes = func() (uint64, error) { return low, nil }
	if _, e := s.CollectEvents(); e != nil {
		t.Fatalf("CollectEvents under storage_low should return nil, got %v", e)
	}
	if state := s.ingestion()["state"]; state != "storage_low" {
		t.Fatalf("expected ingestion state storage_low, got %v", state)
	}
	// Restore space — collection resumes. It may still error (no log file) but
	// the state must not stay "storage_low".
	s.freeBytes = func() (uint64, error) { return 1 << 40, nil }
	_, _ = s.CollectEvents()
	state := s.ingestion()["state"]
	if state == "storage_low" {
		t.Fatal("ingestion state stuck at storage_low after disk recovered")
	}
}

// TestStorageBusyOnDeadline verifies a handler returns 503 storage_busy when
// the request context is already cancelled (deadline exceeded).
func TestStorageBusyOnDeadline(t *testing.T) {
	_, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	// Create a request with an already-cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "https://panel.test/api/v1/configuration/draft", nil)
	r = r.WithContext(ctx)
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 503 {
		t.Fatalf("expected 503, got %d %s", rec.Code, rec.Body.String())
	}
	var v map[string]any
	json.Unmarshal(rec.Body.Bytes(), &v)
	if v["code"] != "storage_busy" {
		t.Fatalf("expected storage_busy, got %v", v["code"])
	}
	if rec.Result().Header.Get("Retry-After") != "2" {
		t.Fatal("missing Retry-After: 2 header")
	}
}

// TestIngestionInStatus verifies /status includes the ingestion object.
func TestIngestionInStatus(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	s.setIngestion("ok")
	w := request(h, "GET", "/status", nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var v map[string]any
	json.Unmarshal(w.Body.Bytes(), &v)
	ing, ok := v["ingestion"].(map[string]any)
	if !ok {
		t.Fatal("status response missing ingestion object")
	}
	if ing["state"] != "ok" {
		t.Fatalf("expected ingestion state ok, got %v", ing["state"])
	}
}

// satisfy unused import check when policy import is needed elsewhere
var _ = policy.Empty
var _ = store.AuditRetention
