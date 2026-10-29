package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// holdControlWriteLock opens a second *sql.DB on the control DB file and holds
// a BEGIN IMMEDIATE transaction until release is called. The server's writer
// pool (MaxOpenConns=1, _txlock=immediate) cannot acquire the lock while this
// is held, so writes surface as deadline errors (503 storage_busy) within
// OpTimeout rather than waiting out busy_timeout.
func holdControlWriteLock(t *testing.T, s *Server) func() {
	t.Helper()
	dir := s.Store.DataDir()
	if dir == "" {
		t.Fatal("test store has no data dir (in-memory)")
	}
	path := filepath.Join(dir, "control.db")
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_txlock=immediate", path)
	db, e := sql.Open("sqlite", dsn)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		db.Close()
		t.Fatal(e)
	}
	if _, e := tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES('lock','hold','',0)"); e != nil {
		tx.Rollback()
		db.Close()
		t.Fatal(e)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			tx.Rollback()
			db.Close()
		})
	}
}

// TestGETsUnderWriteLockStayFast verifies admin and manager GET endpoints that
// read the control DB or telemetry keep returning 200 quickly while a write
// lock is held elsewhere. The reader pool isolates reads from the writer.
func TestGETsUnderWriteLockStayFast(t *testing.T) {
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	// Create a manager + scope so the manager GET path is exercised.
	createManagerUser(t, h, admin, csrf, "mgr", nil, nil)
	managerCookie, _ := loginAs(t, h, "mgr", "manager-password-123")
	// Age the admin session's last_seen_at past 60 s so readSession attempts a
	// conditional touch against the locked writer; the request must still
	// succeed because the session is already verified and the touch is
	// best-effort.
	hash := tokenHash(admin.Value)
	if _, e := s.Store.DB.Exec("UPDATE sessions SET last_seen_at=? WHERE token_hash=?", time.Now().Add(-120*time.Second).Unix(), hash); e != nil {
		t.Fatal(e)
	}
	release := holdControlWriteLock(t, s)
	defer release()
	endpoints := []string{
		"/status",
		"/audit",
		"/configuration/draft",
		"/configuration/active",
		"/revisions",
		"/users",
		"/proxy-accounts",
		"/events",
		"/activity",
		"/statistics",
	}
	t0 := time.Now()
	for _, p := range endpoints {
		start := time.Now()
		w := request(h, "GET", p, nil, admin, "", "")
		elapsed := time.Since(start)
		if w.Code != 200 {
			t.Errorf("GET %s under write lock: expected 200, got %d %s", p, w.Code, w.Body.String())
		}
		if elapsed > 500*time.Millisecond {
			t.Errorf("GET %s under write lock took %v; reader pool not isolated", p, elapsed)
		}
	}
	// Manager GET path (scope load) also stays fast.
	if managerCookie != nil {
		start := time.Now()
		w := request(h, "GET", "/configuration/draft", nil, managerCookie, "", "")
		elapsed := time.Since(start)
		if w.Code != 200 {
			t.Errorf("manager GET /configuration/draft under write lock: expected 200, got %d", w.Code)
		}
		if elapsed > 500*time.Millisecond {
			t.Errorf("manager GET under write lock took %v", elapsed)
		}
	}
	if total := time.Since(t0); total > 5*time.Second {
		t.Errorf("all GETs under write lock took %v total", total)
	}
}

// TestWritesUnderWriteLockReturnStorageBusy verifies mutating endpoints (PUT
// draft, login) return 503 storage_busy with Retry-After: 2 quickly while a
// write lock is held, and succeed after the lock is released.
func TestWritesUnderWriteLockReturnStorageBusy(t *testing.T) {
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	// Fetch the draft so PUT has a body.
	w := request(h, "GET", "/configuration/draft", nil, admin, "", "")
	var d Draft
	if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil {
		t.Fatal(e)
	}
	release := holdControlWriteLock(t, s)
	// PUT /configuration/draft — writer blocked → 503 storage_busy.
	start := time.Now()
	input := map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}
	w = request(h, "PUT", "/configuration/draft", input, admin, csrf, "https://panel.test")
	elapsed := time.Since(start)
	if w.Code != 503 {
		t.Fatalf("PUT draft under write lock: expected 503, got %d %s", w.Code, w.Body.String())
	}
	var v map[string]any
	json.Unmarshal(w.Body.Bytes(), &v)
	if v["code"] != "storage_busy" {
		t.Fatalf("PUT draft: expected storage_busy, got %v", v["code"])
	}
	if w.Result().Header.Get("Retry-After") != "2" {
		t.Fatal("missing Retry-After: 2 header")
	}
	if elapsed > 4*time.Second {
		t.Fatalf("PUT draft under write lock waited %v; OpTimeout ceiling not enforced", elapsed)
	}
	// POST /session (login) — newSession's BeginTx is blocked → 503 storage_busy.
	start = time.Now()
	lw := request(h, "POST", "/session", map[string]string{"username": "admin", "password": "test-password-only-123"}, admin, csrf, "https://panel.test")
	elapsed = time.Since(start)
	if lw.Code != 503 {
		t.Fatalf("login under write lock: expected 503, got %d %s", lw.Code, lw.Body.String())
	}
	json.Unmarshal(lw.Body.Bytes(), &v)
	if v["code"] != "storage_busy" {
		t.Fatalf("login: expected storage_busy, got %v", v["code"])
	}
	if lw.Result().Header.Get("Retry-After") != "2" {
		t.Fatal("missing Retry-After: 2 header on login")
	}
	if elapsed > 4*time.Second {
		t.Fatalf("login under write lock waited %v", elapsed)
	}
	// Release the lock and retry the PUT; it must now succeed.
	release()
	if w := request(h, "PUT", "/configuration/draft", input, admin, csrf, "https://panel.test"); w.Code != 200 && w.Code != 409 {
		t.Fatalf("PUT draft after lock release: expected 200 or 409, got %d %s", w.Code, w.Body.String())
	}
}

// TestLoginUnderWriteLockUsesStorageBusyEnvelope is a focused variant that
// builds the login request manually to control headers precisely.
func TestLoginUnderWriteLockUsesStorageBusyEnvelope(t *testing.T) {
	s, h := fixtureServer(t)
	// Get an anonymous session for CSRF.
	aw := request(h, "GET", "/session", nil, nil, "", "")
	var v map[string]any
	json.Unmarshal(aw.Body.Bytes(), &v)
	anon := aw.Result().Cookies()[0]
	release := holdControlWriteLock(t, s)
	defer release()
	body := []byte(`{"username":"admin","password":"test-password-only-123"}`)
	r := httptest.NewRequest("POST", "https://panel.test/api/v1/session", bytes.NewReader(body))
	r.AddCookie(anon)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://panel.test")
	r.Header.Set("X-CSRF-Token", v["csrf_token"].(string))
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, r)
	elapsed := time.Since(start)
	if rec.Code != 503 {
		t.Fatalf("expected 503, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code string `json:"code"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Code != "storage_busy" {
		t.Fatalf("expected storage_busy, got %s", resp.Code)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("login under write lock waited %v", elapsed)
	}
}
