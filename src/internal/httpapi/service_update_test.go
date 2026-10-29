package httpapi

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"kraken.local/kraken/internal/policy"
	local "kraken.local/kraken/internal/runtime"
)

func oldServiceFixture(t *testing.T) (*Server, local.Manifest) {
	t.Helper()
	s, _ := fixtureServer(t)
	old, err := s.Runtime.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	old.Compiler = "5"
	if err = s.Runtime.State.SaveState(context.Background(), "runtime.last_good", old); err != nil {
		t.Fatal(err)
	}
	return s, old
}
func TestServiceUpdateUsesAppliedSnapshotAndAuditedPipeline(t *testing.T) {
	s, old := oldServiceFixture(t)
	var draft Draft
	if err := s.Store.Get("draft", &draft); err != nil {
		t.Fatal(err)
	}
	draft.Snapshot.TLSProfiles[0].Name = "Unapplied user draft"
	draft.Version++
	if err := s.Store.Put("draft", draft); err != nil {
		t.Fatal(err)
	}
	op, err := ApplyServiceUpdate(context.Background(), s.Store, s.Runtime, s.Image)
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != "succeeded" || op.Action != "service_update" {
		t.Fatal(op)
	}
	current, err := s.Runtime.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if current.Compiler != policy.CompilerVersion || current.Revision != op.Revision || !reflect.DeepEqual(current.Snapshot, old.Snapshot) {
		t.Fatal("service update changed user policy", current)
	}
	var after Draft
	if err = s.Store.Get("draft", &after); err != nil || !reflect.DeepEqual(draft, after) {
		t.Fatal("draft changed", err)
	}
	var revision Revision
	if err = s.Store.Get("revision:"+op.Revision, &revision); err != nil {
		t.Fatal(err)
	}
	if revision.Hash != policy.Hash(old.Snapshot) || !reflect.DeepEqual(revision.Snapshot, old.Snapshot) {
		t.Fatal(revision)
	}
	saved, err := s.loadOperation(op.ID)
	if err != nil || saved.Status != "succeeded" {
		t.Fatal(saved, err)
	}
	journal, err := s.Runtime.Journal()
	if err != nil || journal.Operation != op.ID || journal.Candidate.Hash != current.Hash || journal.Outcome != "succeeded" {
		t.Fatal(journal, err)
	}
	var count int
	if err = s.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE actor='local-cli' AND action='service_update' AND entity_id=?", op.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err = ApplyServiceUpdate(context.Background(), s.Store, s.Runtime, s.Image); err == nil {
		t.Fatal("reapplied current service config")
	}
}
func TestServiceUpdateParseFailurePreservesAppliedAndDraft(t *testing.T) {
	s, old := oldServiceFixture(t)
	s.Runtime.Command = func(context.Context, ...string) error { return errors.New("parse fault") }
	op, err := ApplyServiceUpdate(context.Background(), s.Store, s.Runtime, s.Image)
	if err == nil || op.Status != "rejected" {
		t.Fatal(op, err)
	}
	current, err := s.Runtime.ReadManifest()
	if err != nil || !reflect.DeepEqual(current, old) {
		t.Fatal("changed rejected config", current, err)
	}
	saved, err := s.loadOperation(op.ID)
	if err != nil || saved.Status != "rejected" {
		t.Fatal(saved, err)
	}
}
