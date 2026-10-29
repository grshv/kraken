package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/telemetry"
)

// TestRunCollectorDrainsWithoutPauses verifies that RunCollector reads a
// multi-MB access log across several batches without pausing (interval = 1h,
// so progress is only possible via the backlog-driven immediate continuation),
// stops promptly on ctx cancellation, and that a stream of identical denials
// is coalesced into few rows carrying count.
func TestRunCollectorDrainsWithoutPauses(t *testing.T) {
	s, _ := fixtureServer(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	// 1000 seconds × 10 identical denial lines each: ~600 KiB (>1 batch of
	// 256 KiB). The 10 lines within each second are identical, so each second
	// coalesces to one row with count 10.
	var b strings.Builder
	for sec := 0; sec < 1000; sec++ {
		stamp := strconv.FormatFloat(float64(1793059200+sec), 'f', 3, 64)
		for n := 0; n < 10; n++ {
			b.WriteString(stamp + " 5 192.0.2.10 TCP_DENIED/403 42 CONNECT example.test:443 - NONE/- -\n")
		}
	}
	if e := os.WriteFile(logPath, []byte(b.String()), 0600); e != nil {
		t.Fatal(e)
	}
	s.Runtime.Options.LogPath = logPath

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	start := time.Now()
	go func() {
		s.RunCollector(ctx, time.Hour)
		close(done)
	}()
	// Drain: poll the checkpoint until Backlog is false. With interval = 1h the
	// only way this finishes in seconds is the backlog-driven continuation.
	deadline := time.Now().Add(30 * time.Second)
	drained := false
	for time.Now().Before(deadline) {
		cp, e := s.Telemetry.Checkpoint(context.Background())
		if e == nil && !cp.Backlog && cp.ObservedAt != "" {
			drained = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !drained {
		t.Fatalf("collector did not drain within 30s (no-pause path broken)")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("drain took %s; backlog continuation not working", elapsed)
	}
	// Stop on ctx cancellation: RunCollector is now waiting on the 1h ticker.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunCollector did not return after ctx cancel")
	}
	// Coalescing: 10000 identical-per-second lines → at most 1000 rows, each
	// with count 10.
	page, e := s.Telemetry.Events(context.Background(), telemetry.EventQuery{Limit: 200})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) == 0 {
		t.Fatal("no events ingested")
	}
	var withCount int
	for _, it := range page.Items {
		if it.Count == 10 {
			withCount++
		}
	}
	if withCount == 0 {
		t.Fatalf("no coalesced rows with count=10 found; first item count=%d", page.Items[0].Count)
	}
}

// TestRunCollectorStopsOnCancelUnderBacklog verifies that RunCollector returns
// promptly after ctx cancellation even while access.log keeps growing (backlog
// never ends). Without the ctx.Err() check before each backlog-driven
// continuation the loop would keep reading new batches and stall shutdown.
func TestRunCollectorStopsOnCancelUnderBacklog(t *testing.T) {
	s, _ := fixtureServer(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	// Start with a sizeable log so the first batch returns backlog.
	var b strings.Builder
	for sec := 0; sec < 5000; sec++ {
		stamp := strconv.FormatFloat(float64(1793059200+sec), 'f', 3, 64)
		b.WriteString(stamp + " 5 192.0.2.10 TCP_DENIED/403 42 CONNECT example.test:443 - NONE/- -\n")
	}
	if e := os.WriteFile(logPath, []byte(b.String()), 0600); e != nil {
		t.Fatal(e)
	}
	s.Runtime.Options.LogPath = logPath

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunCollector(ctx, time.Hour)
		close(done)
	}()

	// Keep appending lines so backlog never clears while the test runs.
	writerStop := make(chan struct{})
	go func() {
		f, e := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return
		}
		defer f.Close()
		sec := 6000
		for {
			select {
			case <-writerStop:
				return
			default:
			}
			stamp := strconv.FormatFloat(float64(1793059200+sec), 'f', 3, 64)
			f.WriteString(stamp + " 5 192.0.2.10 TCP_DENIED/403 42 CONNECT example.test:443 - NONE/- -\n")
			sec++
		}
	}()

	// Let one batch land, then cancel while backlog persists.
	time.Sleep(300 * time.Millisecond)
	cancel()
	start := time.Now()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		close(writerStop)
		t.Fatal("RunCollector did not return within 3s of ctx cancel under continuous backlog")
	}
	close(writerStop)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("RunCollector took %s to return after cancel; want < 2s", elapsed)
	}
}
