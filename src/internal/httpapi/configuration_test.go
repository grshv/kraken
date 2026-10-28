package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"squid-control.local/squid/internal/policy"
	"squid-control.local/squid/internal/store"
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
	if len(list.Items) != 1 || list.Items[0].CanRollback {
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
	rows, e := s.Store.ListRevisions()
	if e != nil || len(rows) != 0 {
		t.Fatal("preview created a durable revision")
	}
	manifest, e := s.Runtime.ReadManifest()
	if e != nil {
		t.Fatal(e)
	}
	manifest.Compiler = "legacy"
	manifest.Config = strings.Replace(manifest.Config, "# squid-control compiler="+policy.CompilerVersion, "# squid-control compiler=legacy", 1)
	body, _ := json.Marshal(manifest)
	if e = s.Runtime.Persist(filepath.Join(s.Runtime.Dir, "lkg.json"), body); e != nil {
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
	if e := s.Store.Get("draft", &saved); e != nil {
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
	body, _ := json.Marshal(manifest)
	if e = s.Runtime.Persist(filepath.Join(s.Runtime.Dir, "lkg.json"), body); e != nil {
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
	if e := s.Store.InsertRevision(revision.ID, string(encoded), "admin", "create_revision"); e != nil {
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
	if e := os.Remove(filepath.Join(s.Runtime.Dir, "lkg.json")); e != nil {
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
		if err = s.Store.InsertRevision(tc.id, string(body), "fixture", "create_revision"); err != nil {
			t.Fatal(err)
		}
		op := Operation{ID: "applied-" + tc.id, Action: "apply", Revision: tc.id, Status: "succeeded"}
		body, _ = json.Marshal(op)
		if err = s.Store.InsertOperation(op.ID, string(body), "compatibility-"+tc.id, "fixture", "fixture", "apply"); err != nil {
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
