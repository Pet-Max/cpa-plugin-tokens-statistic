package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"

	bolt "go.etcd.io/bbolt"
)

// An actor panic must not turn into a Close() deadlock: the loop exits after
// the panic, so the buffered close command is never answered and Close has to
// stop waiting once done shows the loop is gone. The panic path must also
// release the bbolt file lock so a same-process reopen succeeds.
func TestActorPanicExitsAndCloseReleases(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panic.db")
	db, err := bolt.Open(dbPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	actor := &storeActor{
		db: db,
		config: config.Config{
			RetentionDays:  30,
			FlushInterval:  time.Hour,
			FlushBatchSize: 100,
			SyncOnRecord:   true,
		},
		data:  make(map[aggregateKey]Counters),
		dirty: make(map[aggregateKey]struct{}),
		since: now,
	}
	actor.panicHook = func() { panic("boom") }
	store := &Store{commands: make(chan any, 8), done: make(chan struct{})}
	go store.run(actor)

	recordErr := make(chan error, 1)
	go func() {
		recordErr <- store.Record(Usage{
			Dimensions:  Dimensions{Provider: "test", Model: "panic"},
			RequestedAt: now,
			Counters:    Counters{Requests: 1, TotalTokens: 3},
		})
	}()
	select {
	case err := <-recordErr:
		if err == nil || !strings.Contains(err.Error(), "store actor panic") {
			t.Fatalf("record must fail with the panic answer, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked forever after the actor panic")
	}

	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close deadlocked after the actor panic exit")
	}

	// The panic path released the bbolt lock: the same file must reopen.
	reopen, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("database lock was not released after the panic exit: %v", err)
	}
	_ = reopen.Close()
}

// A store whose loop already exited (done closed, close command unanswered)
// must return from Close immediately instead of waiting for an answer.
func TestCloseReleasesWhenActorAlreadyExited(t *testing.T) {
	store := &Store{commands: make(chan any, 16), done: make(chan struct{})}
	close(store.done)

	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("post-exit close must succeed, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close deadlocked while the actor loop was already gone")
	}
}
