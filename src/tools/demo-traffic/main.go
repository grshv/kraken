// Local development fixture: temporary scoped clients, audited durable apply,
// then restoration of the original confirmed policy. Never edits the draft.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"kraken.local/kraken/internal/httpapi"
	"kraken.local/kraken/internal/policy"
	runtime "kraken.local/kraken/internal/runtime"
	"kraken.local/kraken/internal/store"
)

const backup = "/data/runtime/.demo-traffic-original.json"

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func apply(st *store.Store, m *runtime.Manager, s policy.Snapshot, action string) error {
	previous, err := m.ReadManifest()
	if err != nil {
		return err
	}
	rev := httpapi.Revision{ID: store.ID(), Hash: policy.Hash(s), Compiler: policy.CompilerVersion, Image: m.Image, Created: time.Now().UTC().Format(time.RFC3339Nano), Snapshot: s}
	raw, err := json.Marshal(rev)
	if err != nil {
		return err
	}
	if err = st.InsertRevision(rev.ID, string(raw), "demo-traffic", action+"_revision"); err != nil {
		return err
	}
	op := httpapi.Operation{ID: store.ID(), Action: action, Revision: rev.ID, Status: "reloading", Findings: []string{}, Created: time.Now().UTC().Format(time.RFC3339Nano), Updated: time.Now().UTC().Format(time.RFC3339Nano)}
	raw, _ = json.Marshal(op)
	if err = st.InsertOperation(op.ID, string(raw), op.ID, rev.Hash, "demo-traffic", action); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	op.Status, err = m.Apply(ctx, op.ID, rev.ID, previous.Revision, s)
	if err != nil {
		op.Findings = []string{"Local demo policy apply did not succeed"}
	}
	op.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	raw, _ = json.Marshal(op)
	if saveErr := st.UpdateOperation(op.ID, string(raw)); saveErr != nil {
		return saveErr
	}
	if err != nil {
		return err
	}
	if op.Status != "succeeded" {
		return errors.New("demo policy was not confirmed")
	}
	fmt.Println(action + ": succeeded")
	return nil
}
func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: demo-traffic start|restore")
	}
	dir := env("KRAKEN_RUNTIME_STATE_DIR", "/data/runtime")
	data := env("KRAKEN_DATABASE_PATH", "/data/app/control.sqlite3")
	st, err := store.Open(data)
	if err != nil {
		return err
	}
	defer st.DB.Close()
	m := runtime.New(dir, "/usr/sbin/squid", env("KRAKEN_ACCESS_LOG", "/data/logs/access.log"), os.Getenv("KRAKEN_PROXY_CERT"), os.Getenv("KRAKEN_PROXY_KEY"), "")
	m.AppDir = env("KRAKEN_APP_DIR", "/data/app")
	m.Options.SquidDir = env("KRAKEN_SQUID_DIR", "/data/squid")
	m.Options.ProxyAccounts = env("KRAKEN_PROXY_ACCOUNTS", "/data/runtime/proxy-accounts.json")
	current, err := m.ReadManifest()
	if err != nil {
		return err
	}
	m.Image = current.ImageDigest
	switch os.Args[1] {
	case "start":
		if current.Snapshot.Mode == "expert" {
			return errors.New("demo requires structured mode")
		}
		if _, e := os.Stat(backup); !errors.Is(e, os.ErrNotExist) {
			return errors.New("original demo policy backup already exists; restore it first")
		}
		snapshot := current.Snapshot
		raw, _ := json.Marshal(current)
		if err = runtime.Atomic(backup, raw); err != nil {
			return err
		}
		profileID := ""
		for _, p := range snapshot.TLSProfiles {
			if p.Transport == "http" && p.Mode == "tunnel" {
				profileID = p.ID
				break
			}
		}
		if profileID == "" {
			profileID = store.ID()
			snapshot.TLSProfiles = append(snapshot.TLSProfiles, policy.TLSProfile{ID: profileID, Name: "demo HTTP/CONNECT", Mode: "tunnel", Transport: "http"})
		}
		for i, name := range []string{"demo-build", "demo-restricted"} {
			domains := []string{"repo.demo.test"}
			if i == 0 {
				domains = []string{"repo.demo.test", "packages.demo.test", "mirror.demo.test", "downloads.demo.test", "status.demo.test", "offline.demo.test"}
			}
			destinations := []policy.Destination{}
			for _, domain := range domains {
				destinations = append(destinations, policy.Destination{Domain: domain})
			}
			pid := store.ID()
			snapshot.Policies = append(snapshot.Policies, policy.Policy{ID: pid, Name: name, Mode: "allowlist", TLSProfileID: profileID, Destinations: destinations})
			snapshot.Clients = append(snapshot.Clients, policy.Client{ID: store.ID(), Name: name, Description: "Temporary demo traffic fixture", Kind: "host", Enabled: true, Networks: []string{fmt.Sprintf("11.251.37.%d/32", 10+i)}, PolicyID: pid})
		}
		return apply(st, m, snapshot, "demo_traffic_start")
	case "restore":
		raw, e := os.ReadFile(backup)
		if e != nil {
			return e
		}
		var original runtime.Manifest
		if err = json.Unmarshal(raw, &original); err != nil {
			return err
		}
		if err = apply(st, m, original.Snapshot, "demo_traffic_restore"); err != nil {
			return err
		}
		if err = os.Remove(backup); err != nil {
			return err
		}
		d, e := os.Open(filepath.Dir(backup))
		if e != nil {
			return e
		}
		defer d.Close()
		return d.Sync()
	default:
		return errors.New("unknown demo mode")
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
