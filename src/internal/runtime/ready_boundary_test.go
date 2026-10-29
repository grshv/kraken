package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readyFixture(t *testing.T, lkg *Manifest, active *string, journal *Journal) *Manager {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, v any) {
		b, _ := json.Marshal(v)
		if e := os.WriteFile(filepath.Join(dir, name), b, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	if lkg != nil {
		write("lkg.json", lkg)
	}
	if active != nil {
		os.WriteFile(filepath.Join(dir, "active.conf"), []byte(*active), 0o640)
	}
	if journal != nil {
		write("journal.json", journal)
	}
	return New(dir, "/unused", filepath.Join(dir, "access.log"), "", "", "")
}

func TestSquidReadyGate(t *testing.T) {
	prev := Manifest{Hash: "prev", Config: "config-prev\n"}
	next := Manifest{Hash: "next", Config: "config-next\n"}
	prevConf, nextConf := prev.Config, next.Config
	cases := []struct {
		name    string
		lkg     *Manifest
		active  *string
		journal *Journal
		want    bool
	}{
		{"no lkg", nil, &prevConf, nil, false},
		{"no active", &prev, nil, nil, false},
		{"no journal", &prev, &prevConf, nil, true},
		{"terminal", &prev, &prevConf, &Journal{Stage: "terminal", Previous: next, Candidate: prev}, true},
		{"active differs", &prev, &nextConf, &Journal{Stage: "terminal"}, false},
		{"pending, previous is lkg", &prev, &prevConf, &Journal{Stage: "reloading", Previous: prev, Candidate: next}, true},
		{"pending, lkg promoted to candidate", &next, &nextConf, &Journal{Stage: "promoting", Previous: prev, Candidate: next}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := readyFixture(t, c.lkg, c.active, c.journal)
			got, e := m.SquidReady()
			if c.lkg == nil {
				if got || e != nil {
					t.Fatalf("no lkg -> %v %v", got, e)
				}
				return
			}
			if got != c.want {
				t.Fatalf("SquidReady -> %v (%v), want %v", got, e, c.want)
			}
		})
	}
}
