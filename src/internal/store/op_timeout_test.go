package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// holdWriteLock opens a second *sql.DB on the same file and holds a
// BEGIN IMMEDIATE transaction until the returned release func is called. The
// control store's writer (DB pool, MaxOpenConns=1) cannot acquire the write
// lock while this is held, so a method bound by OpTimeout must surface a
// deadline error instead of waiting out busy_timeout (5 s).
func holdWriteLock(t *testing.T, path string) func() {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_txlock=immediate", path)
	db, e := sql.Open("sqlite", dsn)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		db.Close()
		t.Fatal(e)
	}
	if _, e := tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES('lock','hold','',0)"); e != nil {
		tx.Rollback()
		db.Close()
		t.Fatal(e)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			tx.Rollback()
			db.Close()
		})
	}
}

// TestWriteUnderHeldLockRespectsOpTimeout verifies a store write method called
// with context.Background() surfaces a deadline error within ~OpTimeout, not
// the SQLite busy_timeout. The ceiling is enforced by opCtx inside every store
// method.
func TestWriteUnderHeldLockRespectsOpTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	st, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	release := holdWriteLock(t, path)
	defer release()
	start := time.Now()
	e = st.Put(context.Background(), "draft", map[string]any{"v": 1})
	elapsed := time.Since(start)
	if e == nil {
		t.Fatal("Put succeeded under a held write lock")
	}
	if elapsed > 3500*time.Millisecond {
		t.Fatalf("Put waited %v under held lock; OpTimeout ceiling not enforced", elapsed)
	}
}

// TestReadUnderHeldLockIsFast verifies reads through the reader pool stay
// quick while a write lock is held elsewhere: WAL readers never block on the
// writer.
func TestReadUnderHeldLockIsFast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	st, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	if e := st.Put(context.Background(), "draft", map[string]any{"v": 1}); e != nil {
		t.Fatal(e)
	}
	release := holdWriteLock(t, path)
	defer release()
	start := time.Now()
	var v map[string]any
	if e := st.Get(context.Background(), "draft", &v); e != nil {
		t.Fatalf("reader blocked under write lock: %v", e)
	}
	// Blocked, the read would wait for OpTimeout (3 s); 1 s leaves room for a
	// loaded CI runner under -race.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("read took %v under held write lock; reader pool not isolated", elapsed)
	}
}
