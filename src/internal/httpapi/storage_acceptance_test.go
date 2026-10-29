package httpapi

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAcceptanceStorageFullRejectsWritesBeforeChange(t *testing.T) {
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	d := acceptDraft(t, h, admin)
	var rev struct {
		ID string `json:"id"`
	}
	json.Unmarshal(request(h, "POST", "/revisions", map[string]any{"expected_draft_version": d.Version}, admin, csrf, origin).Body.Bytes(), &rev)
	if rev.ID == "" {
		t.Fatal("revision not created")
	}
	s.freeBytes = func() (uint64, error) { return 10 << 20, nil }
	w := acceptPut(h, admin, csrf, d)
	acceptCode(t, w, 507, "storage_full")
	if after := acceptDraft(t, h, admin); after.Version != d.Version {
		t.Fatalf("draft changed on full disk: %d -> %d", d.Version, after.Version)
	}
	if w := request(h, "GET", "/status", nil, admin, "", ""); w.Code != 200 {
		t.Fatalf("reads must keep working: %d", w.Code)
	}
	acceptCode(t, acceptOp(h, admin, csrf, "storage-full-op-0001", map[string]any{"action": "validate", "revision_id": rev.ID}), 507, "storage_full")
	s.freeBytes = func() (uint64, error) { return 1 << 30, nil }
	acceptCode(t, acceptPut(h, admin, csrf, acceptDraft(t, h, admin)), 200, "")
}

func TestAcceptanceLowDiskPausesIngestion(t *testing.T) {
	s, h := fixtureServer(t)
	admin, _ := loginTest(t, h)
	s.freeBytes = func() (uint64, error) { return 300 << 20, nil }
	if _, e := s.CollectEvents(); e != nil {
		t.Fatalf("collect must not fail on low disk: %v", e)
	}
	var st struct {
		Ingestion struct {
			State string `json:"state"`
		} `json:"ingestion"`
	}
	json.Unmarshal(request(h, "GET", "/status", nil, admin, "", "").Body.Bytes(), &st)
	if st.Ingestion.State != "storage_low" {
		t.Fatalf("status ingestion.state = %q, want storage_low", st.Ingestion.State)
	}
}

func TestAcceptanceSessionTouchThrottled(t *testing.T) {
	s, h := fixtureServer(t)
	admin, _ := loginTest(t, h)
	last := func() int64 {
		tx, _ := s.Store.BeginTx()
		defer tx.Rollback()
		_, _, _, ls, e := s.Store.LookupSession(tx, tokenHash(admin.Value))
		if e != nil {
			t.Fatal(e)
		}
		return ls
	}
	before := last()
	time.Sleep(1100 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if w := request(h, "GET", "/status", nil, admin, "", ""); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if got := last(); got != before {
		t.Fatalf("last_seen_at rewritten within 60 s: %d -> %d", before, got)
	}
}
