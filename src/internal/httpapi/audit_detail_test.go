package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"kraken.local/kraken/internal/store"
)

func auditRows(t *testing.T, h handler, cookie *http.Cookie, query string) []store.AuditEvent {
	t.Helper()
	var page store.Page[store.AuditEvent]
	w := request(h, "GET", "/audit?limit=200"+query, nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatalf("audit%s: %d %s", query, w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	return page.Items
}

// Draft saves, revisions and discards carry a short summary; per-entity rows
// name only their own object.
func TestAuditDetail(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	myCID, foreignCID, policyID := seedDraftWithTwoClients(t, h, admin, csrf)
	byEntity := func(action, entity string) string {
		for _, ev := range auditRows(t, h, admin, "&action="+action+"&entity_id="+entity) {
			return ev.Detail
		}
		t.Fatalf("no %s row for %s", action, entity)
		return ""
	}
	if got := byEntity("update_draft", "draft"); !strings.Contains(got, "Добавлен клиент: mine") || !strings.Contains(got, "Добавлен клиент: foreign") || !strings.Contains(got, "Изменены политики доступа") {
		t.Fatalf("draft summary: %q", got)
	}
	if got := byEntity("update_draft", myCID); got != "Клиент «mine» добавлен" {
		t.Fatalf("client row: %q", got)
	}
	if got := byEntity("update_draft", policyID); got != "Политика «scoped» добавлена" {
		t.Fatalf("policy row: %q", got)
	}
	// A second save that changes one client: the draft row reports only it.
	d := acceptDraft(t, h, admin)
	for i := range d.Snapshot.Clients {
		if d.Snapshot.Clients[i].ID == myCID {
			d.Snapshot.Clients[i].Description = "changed"
		}
	}
	acceptCode(t, acceptPut(h, admin, csrf, d), 200, "")
	if got := byEntity("update_draft", "draft"); got != "Изменён клиент: mine" {
		t.Fatalf("second save summary: %q", got)
	}
	// A manager sees the row of his client, which names only that client.
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{myCID})
	mgr, _ := loginAs(t, h, "mgr", "manager-password-123")
	for _, ev := range auditRows(t, h, mgr, "") {
		if ev.Entity == "draft" || ev.Entity == foreignCID || strings.Contains(ev.Detail, "foreign") {
			t.Fatalf("manager sees foreign detail: %+v", ev)
		}
	}
	d = acceptDraft(t, h, admin)
	if w := request(h, "POST", "/revisions", map[string]any{"expected_draft_version": d.Version}, admin, csrf, "https://panel.test"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if got := auditRows(t, h, admin, "&action=create_revision"); len(got) != 1 || !strings.Contains(got[0].Detail, "Добавлен клиент: mine") {
		t.Fatalf("revision summary: %+v", got)
	}
	if w := request(h, "DELETE", "/configuration/draft", map[string]any{"expected_version": d.Version}, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if got := byEntity("discard_draft", "draft"); !strings.HasPrefix(got, "Сброшено: Добавлен клиент") {
		t.Fatalf("discard summary: %q", got)
	}
	if got := byEntity("discard_draft", foreignCID); got != "Клиент «foreign» добавлен" {
		t.Fatalf("discard client row: %q", got)
	}
}

func TestAuditSummaryBounded(t *testing.T) {
	if auditSummary("x", nil) != "" {
		t.Fatal("empty diff must give empty detail")
	}
	long := auditSummary("", []string{strings.Repeat("я", 400)})
	if utf8.RuneCountInString(long) != 300 || !strings.HasSuffix(long, "…") || !utf8.ValidString(long) {
		t.Fatalf("bounded: %d", utf8.RuneCountInString(long))
	}
	if got := auditSummary("Сброшено: ", []string{"a", "b"}); got != "Сброшено: a; b" {
		t.Fatalf("join: %q", got)
	}
}
