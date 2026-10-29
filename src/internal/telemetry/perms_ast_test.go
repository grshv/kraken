package telemetry_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/telemetry"
)

// TestTelemetryFilesAre0600 verifies that telemetry DB files and their WAL/SHM
// sidecars are created with mode 0600 and tightened on open.
func TestTelemetryFilesAre0600(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: base}
	// Pre-create rollups.db with a loose mode to verify it is tightened.
	if e := os.WriteFile(filepath.Join(dir, "rollups.db"), []byte{}, 0644); e != nil {
		t.Fatal(e)
	}
	s := open(t, dir, c, nil)
	ctx := context.Background()
	// Ingest to force a monthly file + WAL activity.
	if e := s.Ingest(ctx, []events.Event{ev(base.Add(-time.Minute), "c1", "10.1.0.5", "a.example", false, 1)}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	rollPath := filepath.Join(dir, "rollups.db")
	monthPath := filepath.Join(dir, "events-"+base.UTC().Format("2006-01")+".db")
	for _, p := range []string{rollPath, monthPath} {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			path := p + suffix
			st, e := os.Stat(path)
			if e != nil {
				// Sidecar may not exist yet; skip.
				continue
			}
			if st.Mode().Perm() != 0600 {
				t.Errorf("%s mode %o want 0600", path, st.Mode().Perm())
			}
		}
	}
}

// TestNoContextlessSQLCalls is a static check (go/ast) that no non-test .go file
// in the telemetry package calls Exec/Query/QueryRow/Begin/Prepare without the
// Context/Tx variant. Requirement 7: blocking calls are forbidden.
func TestNoContextlessSQLCalls(t *testing.T) {
	pkgDir := currentPackageDir(t)
	fset := token.NewFileSet()
	files, e := os.ReadDir(pkgDir)
	if e != nil {
		t.Fatal(e)
	}
	forbidden := map[string]bool{
		"Exec": true, "Query": true, "QueryRow": true, "Begin": true, "Prepare": true,
	}
	var offenders []string
	for _, fi := range files {
		name := fi.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		p := filepath.Join(pkgDir, name)
		node, e := parser.ParseFile(fset, p, nil, 0)
		if e != nil {
			t.Fatalf("parse %s: %v", p, e)
		}
		ast.Inspect(node, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if forbidden[sel.Sel.Name] {
				pos := fset.Position(ce.Pos())
				offenders = append(offenders, pos.String())
			}
			return true
		})
	}
	if len(offenders) != 0 {
		t.Fatalf("contextless SQL calls found in telemetry package:\n%s", strings.Join(offenders, "\n"))
	}
}

// currentPackageDir returns the directory of this test file.
func currentPackageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

// Silence unused import warnings for telemetry if all helpers move.
var _ = telemetry.Store{}
