package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
)

// TestMain sets proxyGroupID to the test runner's GID so ChownProxyGroup and
// PrepareRunDir succeed on development hosts where group 13 (proxy) does not
// exist. In the container the image creates the proxy group with GID 13.
func TestMain(m *testing.M) {
	proxyGroupID = os.Getgid()
	os.Exit(m.Run())
}

// memState is an in-memory StateStore for tests that do not exercise the
// control database. It is safe for concurrent use.
type memState struct {
	mu sync.Mutex
	m  map[string]json.RawMessage
}

func newMemState() *memState { return &memState{m: map[string]json.RawMessage{}} }

func (s *memState) LoadState(_ context.Context, key string, v any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.m[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, v)
}

func (s *memState) SaveState(_ context.Context, key string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	s.m[key] = b
	return nil
}

// failCounter tracks every durable write (Persist and SaveState) in order so a
// test can inject a failure at the Nth boundary and then retry from the same
// partial state, mirroring a crash at each journal stage.
type failCounter struct {
	mu     sync.Mutex
	n      int
	failAt int
}

func (c *failCounter) hit() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	if c.failAt > 0 && c.n == c.failAt {
		return errors.New("injected storage failure")
	}
	return nil
}

// failState wraps memState, failing the SaveState call matching failAt.
type failState struct {
	inner *memState
	cf    *failCounter
}

func (s *failState) LoadState(ctx context.Context, key string, v any) (bool, error) {
	return s.inner.LoadState(ctx, key, v)
}

func (s *failState) SaveState(ctx context.Context, key string, v any) error {
	if e := s.cf.hit(); e != nil {
		return e
	}
	return s.inner.SaveState(ctx, key, v)
}

// persistFail wraps AtomicMode, failing the Nth write tracked by cf.
func persistFail(cf *failCounter) func(string, []byte) error {
	return func(path string, b []byte) error {
		if e := cf.hit(); e != nil {
			return e
		}
		return AtomicMode(path, b, 0640)
	}
}

// errLoadState is a StateStore whose LoadState always fails. SaveState is
// tracked so a test can assert the runtime never durable-wrote after a failed
// read: the old bug returned os.ErrNotExist when LoadState errored, which sent
// Recover into the first-run branch and overwrote the last-good manifest.
type errLoadState struct {
	loadErr error
	saved   bool
}

func (s *errLoadState) LoadState(_ context.Context, _ string, _ any) (bool, error) {
	return false, s.loadErr
}

func (s *errLoadState) SaveState(_ context.Context, _ string, _ any) error {
	s.saved = true
	return nil
}
