package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSettingsBareAddressesHistoryAndDiff(t *testing.T) {
	_, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	w := request(h, "GET", "/configuration/draft", nil, cookie, "", "")
	var d Draft
	json.Unmarshal(w.Body.Bytes(), &d)
	d.Snapshot.Settings.ConnectTimeout = 45
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, cookie, csrf, "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &d)
	w = request(h, "GET", "/status", nil, cookie, "", "")
	var status map[string]any
	json.Unmarshal(w.Body.Bytes(), &status)
	if status["has_unapplied_changes"] != true {
		t.Fatal("unapplied changes not shown")
	}
	w = request(h, "POST", "/revisions", map[string]any{"expected_draft_version": d.Version}, cookie, csrf, "https://panel.test")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var rev Revision
	json.Unmarshal(w.Body.Bytes(), &rev)
	if rev.Number != 1 {
		t.Fatalf("first revision number: %d", rev.Number)
	}
	var stored Revision
	if getJSON(t, h, cookie, "/revisions/"+rev.ID, &stored); stored.Number != 1 {
		t.Fatalf("stored revision number: %d", stored.Number)
	}
	w = request(h, "GET", "/revisions/"+rev.ID+"/preview", nil, cookie, "", "")
	var preview struct {
		Summary []string   `json:"summary"`
		Diff    []DiffLine `json:"config_diff"`
	}
	json.Unmarshal(w.Body.Bytes(), &preview)
	if len(preview.Diff) == 0 || len(preview.Summary) == 0 {
		t.Fatal("missing reviewable diff")
	}
	w = request(h, "GET", "/revisions", nil, cookie, "", "")
	var list struct {
		Items []RevisionSummary `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Number != 1 || list.Items[0].CanRollback {
		t.Fatal("unapplied revision offered for rollback")
	}
	if w = request(h, "GET", "/revisions", nil, nil, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatal("history exposed without auth")
	}
}
func TestOutcomePersistenceFailureBlocksFurtherApply(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	w := request(h, "POST", "/revisions", map[string]int{"expected_draft_version": 1}, cookie, csrf, "https://panel.test")
	var rev Revision
	json.Unmarshal(w.Body.Bytes(), &rev)
	_, e := s.Store.DB.Exec(`CREATE TRIGGER reject_terminal BEFORE UPDATE ON operations WHEN json_extract(NEW.body,'$.status')='validated' BEGIN SELECT RAISE(FAIL,'injected storage failure'); END`)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "https://panel.test/api/v1/operations", strings.NewReader(`{"action":"validate","revision_id":"`+rev.ID+`","expected_applied_revision":null}`))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://panel.test")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Idempotency-Key", "test-outcome-failure-001")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, r)
	if response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	s.Wait()
	if !s.persistenceFailed.Load() {
		t.Fatal("failed outcome persistence ignored")
	}
	r.Header.Set("Idempotency-Key", "test-outcome-failure-002")
	r.Body = io.NopCloser(strings.NewReader(`{"action":"validate","revision_id":"` + rev.ID + `","expected_applied_revision":null}`))
	response = httptest.NewRecorder()
	h.ServeHTTP(response, r)
	if response.Code != 503 {
		t.Fatal("new operation accepted after losing previous outcome")
	}
}

func TestComparisonIgnoresOmittedDefaultsAndSetOrdering(t *testing.T) {
	old := policy.Empty()
	old.Settings = nil
	old.Mode = ""
	old.Clients = nil
	old.Policies = nil
	current := policy.Empty()
	if got := semanticDiff(old, current); len(got) != 0 {
		t.Fatalf("defaults became changes: %v", got)
	}
	old.Clients = []policy.Client{{ID: "host", Name: "build", Kind: "", Networks: []string{"192.0.2.4"}, PolicyID: "allow"}}
	current.Clients = []policy.Client{{ID: "host", Name: "build", Kind: "host", Networks: []string{"192.0.2.4/32"}, PolicyID: "allow"}}
	current.Settings.SafePorts = []int{443, 80}
	if got := semanticDiff(old, current); len(got) != 0 {
		t.Fatalf("equivalent values became changes: %v", got)
	}
	current.Settings.ConnectTimeout = 45
	if got := semanticDiff(old, current); len(got) != 1 || got[0] != "Изменены настройки Squid" {
		t.Fatalf("actual change lost: %v", got)
	}
	if got := configDiff("same\n", "same\n"); len(got) != 0 {
		t.Fatal("identical config has a diff")
	}
}
func TestDraftPreviewDoesNotCreateRevisionAndSeparatesRuntimeUpdate(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	w := request(h, "GET", "/configuration/preview", nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct {
		User    bool       `json:"user_changes"`
		Runtime bool       `json:"runtime_changes"`
		Summary []string   `json:"summary"`
		Config  string     `json:"generated_config"`
		Diff    []DiffLine `json:"config_diff"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.User || result.Runtime || len(result.Summary) != 0 || len(result.Diff) != 0 || !strings.Contains(result.Config, "http_access deny all") {
		t.Fatalf("unexpected empty preview: %+v", result)
	}
	rows, e := s.Store.ListRevisions(context.Background())
	if e != nil || len(rows) != 0 {
		t.Fatal("preview created a durable revision")
	}
	manifest, e := s.Runtime.ReadManifest()
	if e != nil {
		t.Fatal(e)
	}
	manifest.Compiler = "legacy"
	manifest.Config = strings.Replace(manifest.Config, "# kraken compiler="+policy.CompilerVersion, "# kraken compiler=legacy", 1)
	if e = s.Runtime.State.SaveState(context.Background(), "runtime.last_good", manifest); e != nil {
		t.Fatal(e)
	}
	w = request(h, "GET", "/configuration/preview", nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.User || !result.Runtime || len(result.Summary) != 0 || len(result.Diff) == 0 {
		t.Fatalf("runtime update presented as user settings: %+v", result)
	}
	w = request(h, "GET", "/status", nil, cookie, "", "")
	var status map[string]any
	json.Unmarshal(w.Body.Bytes(), &status)
	if status["has_unapplied_changes"] != false || status["runtime_update_available"] != true {
		t.Fatal(w.Body.String())
	}
}
func TestDraftRejectsInstallationPortChange(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	var original Draft
	json.Unmarshal(request(h, "GET", "/configuration/draft", nil, cookie, "", "").Body.Bytes(), &original)
	for _, tls := range []bool{false, true} {
		d := original
		settings := *original.Snapshot.Settings
		d.Snapshot.Settings = &settings
		if tls {
			settings.TLSPort = 4443
		} else {
			settings.HTTPPort = 8080
		}
		w := request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, cookie, csrf, "https://panel.test")
		if w.Code != 422 || !strings.Contains(w.Body.String(), "installation_setting") {
			t.Fatal(w.Body.String())
		}
	}
	var saved Draft
	if e := s.Store.Get(context.Background(), "draft", &saved); e != nil {
		t.Fatal(e)
	}
	if saved.Version != original.Version || saved.Snapshot.Settings.HTTPPort != original.Snapshot.Settings.HTTPPort || saved.Snapshot.Settings.TLSPort != original.Snapshot.Settings.TLSPort {
		t.Fatal("rejected request changed draft")
	}
	// An existing installation with a non-default listener keeps that listener.
	manifest, e := s.Runtime.ReadManifest()
	if e != nil {
		t.Fatal(e)
	}
	manifest.Snapshot.Settings.HTTPPort = 8080
	if e = s.Runtime.State.SaveState(context.Background(), "runtime.last_good", manifest); e != nil {
		t.Fatal(e)
	}
	original.Snapshot.Settings.HTTPPort = 8080
	original.Snapshot.Settings.ConnectTimeout = 45
	w := request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": original.Version, "snapshot": original.Snapshot}, cookie, csrf, "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
func TestEmptyExpertTemplateIsFailClosedAndPreviewIsAdminOnly(t *testing.T) {
	_, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	w := request(h, "GET", "/configuration/expert-template", nil, cookie, "", "")
	var template map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &template) != nil || template["expert_config"] != "http_access deny all\n" {
		t.Fatal(w.Body.String())
	}
	w = request(h, "GET", "/configuration/preview", nil, nil, "", "")
	if w.Code != 401 {
		t.Fatal("anonymous configuration preview exposed")
	}
}

func TestRevisionOperationsCannotChangeInstalledListeners(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	snapshot := policy.Empty()
	snapshot.Settings.HTTPPort = 8080
	revision := Revision{ID: store.ID(), DraftVersion: 1, Hash: policy.Hash(snapshot), Compiler: policy.CompilerVersion, Image: s.Image, Created: stamp(), Snapshot: snapshot}
	encoded, _ := json.Marshal(revision)
	if _, e := s.Store.InsertRevision(context.Background(), revision.ID, string(encoded), "admin", "create_revision", ""); e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"validate", "apply", "rollback"} {
		body, _ := json.Marshal(map[string]any{"action": action, "revision_id": revision.ID, "expected_applied_revision": nil})
		r := httptest.NewRequest("POST", "https://panel.test/api/v1/operations", strings.NewReader(string(body)))
		r.AddCookie(cookie)
		r.Header.Set("Origin", "https://panel.test")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "installation-port-"+action)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 422 || !strings.Contains(w.Body.String(), "installation_setting") {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
	var n int
	if e := s.Store.DB.QueryRow("SELECT count(*) FROM operations").Scan(&n); e != nil || n != 0 {
		t.Fatal("port-changing operations were accepted")
	}
}
func TestDraftPreviewUnavailableRuntimeIsSafe503(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	if _, e := s.Store.DB.Exec("DELETE FROM kv WHERE key='runtime.last_good'"); e != nil {
		t.Fatal(e)
	}
	w := request(h, "GET", "/configuration/preview", nil, cookie, "", "")
	if w.Code != 503 || strings.Contains(w.Body.String(), s.Runtime.Dir) {
		t.Fatal(w.Body.String())
	}
}

func TestRollbackCompatibilityMatchesCompilerAndImage(t *testing.T) {
	s, _ := fixtureServer(t)
	manifest, err := s.Runtime.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, compiler, image string
		allowed             bool
	}{{"current-build", policy.CompilerVersion, s.Image, true}, {"legacy-build", "5", s.Image, false}, {"other-image", policy.CompilerVersion, "other-image", false}} {
		revision := Revision{ID: tc.id, Snapshot: manifest.Snapshot, Compiler: tc.compiler, Image: tc.image}
		body, _ := json.Marshal(revision)
		if _, err = s.Store.InsertRevision(context.Background(), tc.id, string(body), "fixture", "create_revision", ""); err != nil {
			t.Fatal(err)
		}
		op := Operation{ID: "applied-" + tc.id, Action: "apply", Revision: tc.id, Status: "succeeded"}
		body, _ = json.Marshal(op)
		if err = s.Store.InsertOperation(context.Background(), op.ID, string(body), "compatibility-"+tc.id, "fixture", "fixture", "apply"); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	s.listRevisions(w, httptest.NewRequest("GET", "/api/v1/revisions", nil), session{})
	var result struct {
		Items []RevisionSummary `json:"items"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 {
		t.Fatal(w.Body.String())
	}
	for _, item := range result.Items {
		if item.CanRollback != (item.ID == "current-build") {
			t.Fatal("incompatible rollback offered", item)
		}
	}
}

func TestConfigDiffAlignsUnchangedLinesAndHidesFingerprint(t *testing.T) {
	before := "# kraken compiler=6 revision=old\n" + strings.Repeat("unchanged\n", 40) + "reply_header_add X-Kraken-Revision old all\n# BEGIN user policy\n# END user policy\n"
	after := strings.ReplaceAll(before, "old", "new")
	after = strings.Replace(after, "# END user policy", "acl client_0 src 192.168.2.20/32\nacl destinations_0 dstdomain -n debian.org\nhttp_access allow client_0 destinations_0\n# END user policy", 1)
	added, removed := 0, 0
	for _, line := range configDiff(before, after) {
		if line.Kind == "added" {
			added++
		}
		if line.Kind == "removed" {
			removed++
		}
		if strings.Contains(line.Text, "revision=") {
			t.Fatal("generated fingerprint leaked into diff")
		}
	}
	if added != 3 || removed != 0 {
		t.Fatalf("expected three additions, got +%d -%d", added, removed)
	}
	if got := configDiff(before, strings.ReplaceAll(before, "old", "new")); len(got) != 0 {
		t.Fatalf("fingerprint-only changes: %v", got)
	}
}
func TestConfigDiffSeparateHunksAndRepeatedLines(t *testing.T) {
	before := "first\nkeep\nrepeat\nrepeat\n" + strings.Repeat("middle\n", 12) + "last\n"
	after := strings.Replace(before, "first", "changed", 1)
	after = strings.TrimSuffix(after, "last\n") + "newlast\n"
	hunks, added, removed := 0, 0, 0
	for _, line := range configDiff(before, after) {
		if strings.HasPrefix(line.Text, "@@") {
			hunks++
		}
		if line.Kind == "added" {
			added++
		}
		if line.Kind == "removed" {
			removed++
		}
	}
	if hunks != 2 || added != 2 || removed != 2 {
		t.Fatalf("hunks=%d +%d -%d", hunks, added, removed)
	}
}
func TestConfigDiffBoundsLargeDissimilarInput(t *testing.T) {
	got := configDiff(strings.Repeat("old\n", 3000), strings.Repeat("new\n", 3000))
	if len(got) != 1 || got[0].Kind != "context" || !strings.Contains(got[0].Text, "слишком большое") {
		t.Fatal(got)
	}
}

func TestConfigDiffEmptyInputHunkCoordinates(t *testing.T) {
	for _, c := range []struct{ before, after, header string }{
		{"", "new\n", "@@ -0,0 +1,1 @@"}, {"old\n", "", "@@ -1,1 +0,0 @@"},
	} {
		got := configDiff(c.before, c.after)
		if len(got) != 2 || got[0].Text != c.header {
			t.Fatal(got)
		}
	}
}

// TestUpstreamProxyComparisonNilEqualsAbsent verifies that an explicit
// "upstream_proxy": null in JSON and an omitted field are the same setting:
// the omitempty tag drops nil on marshal, so comparisonSnapshot normalizes
// both to a nil pointer and no false "Изменены настройки Squid" is reported.
func TestUpstreamProxyComparisonNilEqualsAbsent(t *testing.T) {
	absent := policy.Empty()
	// Rebuild an identical snapshot through JSON with an explicit null key.
	raw, _ := json.Marshal(absent)
	var generic map[string]any
	if e := json.Unmarshal(raw, &generic); e != nil {
		t.Fatal(e)
	}
	if settings, ok := generic["settings"].(map[string]any); ok {
		settings["upstream_proxy"] = nil
	}
	rawNull, _ := json.Marshal(generic)
	var withNull policy.Snapshot
	if e := json.Unmarshal(rawNull, &withNull); e != nil {
		t.Fatal(e)
	}
	if got := semanticDiff(absent, withNull); len(got) != 0 {
		t.Fatalf("nil vs absent produced false change: %v", got)
	}
	// A real upstream value is detected as a settings change.
	withNull.Settings.Upstream = &policy.UpstreamProxy{Address: "1.1.1.1", Port: 3128}
	if got := semanticDiff(absent, withNull); len(got) != 1 || got[0] != "Изменены настройки Squid" {
		t.Fatalf("upstream change not detected: %v", got)
	}
}

// TestActiveConfigurationForAdminManagerAnonymous covers GET
// /configuration/active: admin sees the applied snapshot, manager sees only
// his scope with expert_config hidden, anonymous gets 401.
func TestActiveConfigurationForAdminManagerAnonymous(t *testing.T) {
	h, admin, _, mgr, _ := scopeFixture(t)
	// Anonymous: 401.
	if w := request(h, "GET", "/configuration/active", nil, nil, "", ""); w.Code != 401 {
		t.Fatalf("anonymous active: %d", w.Code)
	}
	// Admin: the applied snapshot (manifest) with the seeded clients/policies.
	w := request(h, "GET", "/configuration/active", nil, admin, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var adminOut struct {
		Snapshot policy.Snapshot `json:"snapshot"`
	}
	json.Unmarshal(w.Body.Bytes(), &adminOut)
	if !hasClient(adminOut.Snapshot, "c-a") || !hasClient(adminOut.Snapshot, "c-b") {
		t.Fatalf("admin active missing clients: %+v", adminOut.Snapshot.Clients)
	}
	// Manager: only his clients, expert_config empty, foreign clients absent.
	w = request(h, "GET", "/configuration/active", nil, mgr, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var mgrOut struct {
		Snapshot policy.Snapshot `json:"snapshot"`
	}
	json.Unmarshal(w.Body.Bytes(), &mgrOut)
	if !hasClient(mgrOut.Snapshot, "c-a") || hasClient(mgrOut.Snapshot, "c-b") || hasClient(mgrOut.Snapshot, "c-d") {
		t.Fatalf("manager active scope wrong: %+v", mgrOut.Snapshot.Clients)
	}
	if mgrOut.Snapshot.Expert != "" {
		t.Fatal("manager active leaked expert_config")
	}
}

// discardDraftRows counts audit_events for a given action/entity directly so a
// discard test can assert per-entity rows without going through the API.
func discardDraftRows(t *testing.T, db *sql.DB, action, entity string) int {
	t.Helper()
	var n int
	if e := db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action=? AND entity_id=?", action, entity).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// TestDiscardDraftSuccessConflictAndManagerScope covers DELETE
// /configuration/draft: admin success bumps version and writes per-entity
// discard_draft audit rows; stale version yields 409; a manager with foreign
// draft changes gets 403 without touching the draft; a manager with only own
// changes can discard.
func TestDiscardDraftSuccessConflictAndManagerScope(t *testing.T) {
	s, h := fixtureServer(t)
	admin, acsrf := loginTest(t, h)
	// Seed an applied config with two clients via the scope fixture helpers
	// built on this server: admin applies, then we exercise discard.
	myCID, foreignCID, _ := seedDraftWithTwoClients(t, h, admin, acsrf)
	if acceptApply(t, h, admin, acsrf, admin, acsrf, "discard-admin-seed") != 201 {
		t.Fatal("admin apply failed")
	}
	createManagerUser(t, h, admin, acsrf, "mgr", []string{"10.1.0.0/16"}, []string{myCID})
	mgr, mcsrf := loginAs(t, h, "mgr", "manager-password-123")
	// Admin edits a client in the draft (differs from active), then discards.
	d := acceptDraft(t, h, admin)
	for i := range d.Snapshot.Clients {
		if d.Snapshot.Clients[i].ID == myCID {
			d.Snapshot.Clients[i].Description = "pending change"
		}
	}
	acceptCode(t, acceptPut(h, admin, acsrf, d), 200, "")
	edited := acceptDraft(t, h, admin)
	w := request(h, "DELETE", "/configuration/draft", map[string]any{"expected_version": edited.Version}, admin, acsrf, origin)
	acceptCode(t, w, 200, "")
	var after Draft
	json.Unmarshal(w.Body.Bytes(), &after)
	if after.Version != edited.Version+1 {
		t.Fatalf("version = %d, want %d", after.Version, edited.Version+1)
	}
	// Discarded draft equals the active snapshot; myCID description reverted.
	var c policy.Client
	for _, x := range after.Snapshot.Clients {
		if x.ID == myCID {
			c = x
		}
	}
	if c.Description != "" {
		t.Fatalf("discard did not reset %s: %+v", myCID, c)
	}
	// Per-entity discard_draft audit row for the changed client.
	if n := discardDraftRows(t, s.Store.DB, "discard_draft", myCID); n == 0 {
		t.Fatalf("no discard_draft audit row for %s", myCID)
	}
	// 409 on a stale expected_version.
	stale := after.Version - 1
	w = request(h, "DELETE", "/configuration/draft", map[string]any{"expected_version": stale}, admin, acsrf, origin)
	acceptCode(t, w, 409, "revision_conflict")

	// Manager path: admin stages a foreign change; manager discard → 403 and draft unchanged.
	ad := acceptDraft(t, h, admin)
	for i := range ad.Snapshot.Clients {
		if ad.Snapshot.Clients[i].ID == foreignCID {
			ad.Snapshot.Clients[i].Description = "foreign pending"
		}
	}
	acceptCode(t, acceptPut(h, admin, acsrf, ad), 200, "")
	beforeMgr := acceptDraft(t, h, mgr)
	w = request(h, "DELETE", "/configuration/draft", map[string]any{"expected_version": beforeMgr.Version}, mgr, mcsrf, origin)
	acceptCode(t, w, 403, "draft_out_of_scope")
	if got := acceptDraft(t, h, mgr); got.Version != beforeMgr.Version {
		t.Fatalf("403 discard changed draft version: %d -> %d", beforeMgr.Version, got.Version)
	}
	// Admin clears the foreign pending change so the manager's own change is
	// the only diff against active.
	ad = acceptDraft(t, h, admin)
	acceptCode(t, request(h, "DELETE", "/configuration/draft", map[string]any{"expected_version": ad.Version}, admin, acsrf, origin), 200, "")
	// Manager with only his own change can discard.
	md := acceptDraft(t, h, mgr)
	for i := range md.Snapshot.Clients {
		if md.Snapshot.Clients[i].ID == myCID {
			md.Snapshot.Clients[i].Description = "own pending"
		}
	}
	acceptCode(t, acceptPut(h, mgr, mcsrf, md), 200, "")
	md = acceptDraft(t, h, mgr)
	w = request(h, "DELETE", "/configuration/draft", map[string]any{"expected_version": md.Version}, mgr, mcsrf, origin)
	acceptCode(t, w, 200, "")
}

// The status bar shows the applied revision by number, not by UUID.
func TestStatusAppliedRevisionNumber(t *testing.T) {
	_, h, cookie, _ := inspectorFixture(t)
	var status map[string]any
	getJSON(t, h, cookie, "/status", &status)
	if status["applied_revision_number"] != float64(1) {
		t.Fatalf("applied_revision_number: %v", status["applied_revision_number"])
	}
}
