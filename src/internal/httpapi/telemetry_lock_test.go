package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// Telemetry reads do not wait for a held telemetry write lock.
func TestTelemetryReadsDoNotWaitForWriter(t *testing.T) {
	s, h := fixtureServer(t)
	admin, _ := loginTest(t, h)
	timed := func(p string) (int, time.Duration) {
		st := time.Now()
		w := request(h, "GET", p, nil, admin, "", "")
		return w.Code, time.Since(st)
	}
	if code, _ := timed("/events"); code != 200 {
		t.Fatal(code)
	}
	files, _ := filepath.Glob(filepath.Join(s.Store.DataDir(), "telemetry", "*.db"))
	if len(files) == 0 {
		t.Fatal("no telemetry files")
	}
	for _, f := range files {
		db, _ := sql.Open("sqlite", "file:"+f+"?_pragma=busy_timeout(0)")
		conn, e := db.Conn(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { conn.ExecContext(context.Background(), "ROLLBACK"); conn.Close(); db.Close() })
	}
	for _, p := range []string{"/events", "/events/clients", "/statistics", "/inspector/unknown-sources"} {
		if code, d := timed(p); code != http.StatusOK || d > time.Second {
			t.Errorf("GET %s under telemetry write lock: %d after %v", p, code, d)
		}
	}
}
