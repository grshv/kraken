package httpapi

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
	ctx := context.Background()
	var draft Draft
	if err := s.Store.Get(ctx, "draft", &draft); err != nil {
		t.Fatal(err)
	}
	draft.Snapshot.TLSProfiles[0].Name = "Unapplied user draft"
	draft.Version++
	if err := s.Store.Put(ctx, "draft", draft); err != nil {
		t.Fatal(err)
	}
	op, err := ApplyServiceUpdate(ctx, s.Store, s.Runtime, s.Image)
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
	if err = s.Store.Get(ctx, "draft", &after); err != nil || !reflect.DeepEqual(draft, after) {
		t.Fatal("draft changed", err)
	}
	var revision Revision
	if err = s.Store.Get(ctx, "revision:"+op.Revision, &revision); err != nil {
		t.Fatal(err)
	}
	if revision.Hash != policy.Hash(old.Snapshot) || !reflect.DeepEqual(revision.Snapshot, old.Snapshot) {
		t.Fatal(revision)
	}
	saved, err := s.loadOperation(ctx, op.ID)
	if err != nil || saved.Status != "succeeded" {
		t.Fatal(saved, err)
	}
	journal, err := s.Runtime.Journal()
	if err != nil || journal.Operation != op.ID || journal.Candidate.Hash != current.Hash || journal.Outcome != "succeeded" {
		t.Fatal(journal, err)
	}
	var count int
	if err = s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE actor='local-cli' AND action='service_update' AND entity_id=?", op.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err = ApplyServiceUpdate(ctx, s.Store, s.Runtime, s.Image); err == nil {
		t.Fatal("reapplied current service config")
	}
}
func TestServiceUpdateParseFailurePreservesAppliedAndDraft(t *testing.T) {
	s, old := oldServiceFixture(t)
	s.Runtime.Command = func(context.Context, ...string) error { return errors.New("parse fault") }
	ctx := context.Background()
	op, err := ApplyServiceUpdate(ctx, s.Store, s.Runtime, s.Image)
	if err == nil || op.Status != "rejected" {
		t.Fatal(op, err)
	}
	current, err := s.Runtime.ReadManifest()
	if err != nil || !reflect.DeepEqual(current, old) {
		t.Fatal("changed rejected config", current, err)
	}
	saved, err := s.loadOperation(ctx, op.ID)
	if err != nil || saved.Status != "rejected" {
		t.Fatal(saved, err)
	}
}

// Changing installation options (an existing install gets the issued proxy
// certificate) must be detected and applied; an unchanged one needs nothing.
func TestServiceUpdateNeededFollowsInstallationOptions(t *testing.T) {
	s, _ := fixtureServer(t)
	ctx := context.Background()
	needed, err := ServiceUpdateNeeded(s.Runtime)
	if err != nil || needed {
		t.Fatalf("fresh install: needed=%v err=%v", needed, err)
	}
	s.Runtime.Options.ProxyCert, s.Runtime.Options.ProxyKey = "/c/proxy.crt", "/c/proxy.key"
	needed, err = ServiceUpdateNeeded(s.Runtime)
	if err != nil || !needed {
		t.Fatalf("changed listen: needed=%v err=%v", needed, err)
	}
	op, err := ApplyServiceUpdate(ctx, s.Store, s.Runtime, s.Image)
	if err != nil || op.Status != "succeeded" {
		t.Fatal(op, err)
	}
	current, err := s.Runtime.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current.Config, "\nhttps_port 3129 tls-cert=/c/proxy.crt tls-key=/c/proxy.key\n") {
		t.Fatal("proxy TLS listener not applied")
	}
	if needed, err = ServiceUpdateNeeded(s.Runtime); err != nil || needed {
		t.Fatalf("after update: needed=%v err=%v", needed, err)
	}
	s.Runtime.Options.ProxyKey = ""
	if _, err = ServiceUpdateNeeded(s.Runtime); err == nil {
		t.Fatal("certificate without key accepted")
	}
}

// An image upgrade (other compiler) stays a manual, visible service update.
func TestServiceUpdateNeededLeavesCompilerUpgradeToOperator(t *testing.T) {
	s, _ := oldServiceFixture(t)
	s.Runtime.Options.ProxyCert, s.Runtime.Options.ProxyKey = "/c/proxy.crt", "/c/proxy.key"
	if needed, err := ServiceUpdateNeeded(s.Runtime); err != nil || needed {
		t.Fatalf("compiler upgrade auto-applied: needed=%v err=%v", needed, err)
	}
}
