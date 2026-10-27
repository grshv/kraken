package httpapi

import (
	"encoding/json"
	"io"
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
