package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRotateGateBlocksRotation verifies that Maintain skips log rotation when
// RotateGate returns false (collector behind), and rotates when it returns true.
func TestRotateGateBlocksRotation(t *testing.T) {
	m := testManager(t)
	logDir := t.TempDir()
	logPath := filepath.Join(logDir, "access.log")
	if e := os.WriteFile(logPath, make([]byte, 11*1024*1024), 0640); e != nil {
		t.Fatal(e)
	}
	m.Options.LogPath = logPath
	rotated := false
	m.Reload = func(_ context.Context, action string) error {
		if action == "rotate" {
			rotated = true
		}
		return nil
	}
	// Gate forbids rotation (collector is behind).
	m.RotateGate = func(size int64) bool { return false }
	rotated = false
	if e := m.Maintain(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if rotated {
		t.Fatal("rotation proceeded while RotateGate forbade it")
	}
	// Gate allows rotation.
	m.RotateGate = func(size int64) bool { return true }
	rotated = false
	if e := m.Maintain(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if !rotated {
		t.Fatal("rotation skipped while RotateGate allowed it")
	}
}

// TestRotateGateHardCapAndCacheLog verifies that a nil RotateGate still rotates
// (the default behavior) and that cache.log (not gated) also triggers rotation.
func TestRotateGateHardCapAndCacheLog(t *testing.T) {
	m := testManager(t)
	logDir := t.TempDir()
	logPath := filepath.Join(logDir, "access.log")
	if e := os.WriteFile(logPath, make([]byte, 11*1024*1024), 0640); e != nil {
		t.Fatal(e)
	}
	m.Options.LogPath = logPath
	m.Options.SquidDir = logDir
	cachePath := filepath.Join(m.Options.SquidDir, "cache.log")
	if e := os.WriteFile(cachePath, make([]byte, 11*1024*1024), 0640); e != nil {
		t.Fatal(e)
	}
	rotated := false
	m.Reload = func(_ context.Context, action string) error {
		if action == "rotate" {
			rotated = true
		}
		return nil
	}
	m.RotateGate = nil // no gate: rotate always
	if e := m.Maintain(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if !rotated {
		t.Fatal("nil RotateGate should allow rotation")
	}
}

// TestRotateGateCacheLogGated verifies that cache.log > 10 MiB alone does not
// bypass the gate: Squid rotates both logs on one action, so a big cache.log
// must not tear access.log away from the collector. With a forbidding gate and
// access.log under the threshold, no rotation happens.
func TestRotateGateCacheLogGated(t *testing.T) {
	m := testManager(t)
	logDir := t.TempDir()
	m.Options.LogPath = filepath.Join(logDir, "access.log")
	m.Options.SquidDir = logDir
	// access.log small, cache.log over threshold but under the hard cap.
	if e := os.WriteFile(m.Options.LogPath, []byte("small"), 0640); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(logDir, "cache.log"), make([]byte, 11*1024*1024), 0640); e != nil {
		t.Fatal(e)
	}
	rotated := false
	m.Reload = func(_ context.Context, action string) error {
		if action == "rotate" {
			rotated = true
		}
		return nil
	}
	m.RotateGate = func(size int64) bool { return false }
	if e := m.Maintain(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if rotated {
		t.Fatal("cache.log over threshold rotated despite forbidding gate")
	}
}

// TestRotateHardCapCacheLogForcesRotation verifies that a log reaching
// RotateHardCap forces rotation regardless of the gate. Uses a sparse file
// (Truncate) so the test does not allocate 256 MiB.
func TestRotateHardCapCacheLogForcesRotation(t *testing.T) {
	m := testManager(t)
	logDir := t.TempDir()
	m.Options.LogPath = filepath.Join(logDir, "access.log")
	m.Options.SquidDir = logDir
	// Sparse cache.log: Truncate reports the size without allocating blocks.
	cachePath := filepath.Join(logDir, "cache.log")
	f, e := os.Create(cachePath)
	if e != nil {
		t.Fatal(e)
	}
	if e := f.Truncate(RotateHardCap); e != nil {
		f.Close()
		t.Fatal(e)
	}
	f.Close()
	rotated := false
	m.Reload = func(_ context.Context, action string) error {
		if action == "rotate" {
			rotated = true
		}
		return nil
	}
	m.RotateGate = func(size int64) bool { return false }
	if e := m.Maintain(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if !rotated {
		t.Fatal("hard-cap cache.log did not force rotation")
	}
}
