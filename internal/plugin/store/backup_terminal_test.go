package store

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"

	bolt "go.etcd.io/bbolt"
)

// A restore whose rename/reopen sequence fails twice must not hand the actor
// back to the run loop with a nil database handle: the next flush would
// dereference nil and crash the host process. finishRestore either salvages a
// usable handle or puts the actor into a terminal state.
func TestFinishRestoreEntersTerminalWhenNoCandidateOpens(t *testing.T) {
	dir := t.TempDir()
	actor := &storeActor{config: config.Config{DataPath: filepath.Join(dir, "live.db")}}

	db, err := actor.finishRestore(errors.New("rename failed"),
		filepath.Join(dir, "missing.db"), filepath.Join(dir, "also-missing.db"))
	if db != nil {
		t.Fatalf("expected nil db, got %v", db)
	}
	if err == nil || !strings.Contains(err.Error(), "rename failed") {
		t.Fatalf("joined error must keep the original failure: %v", err)
	}
	if actor.terminalErr == nil {
		t.Fatal("terminal state must be set when no candidate reopens")
	}
	if status := errs.HTTPStatus(actor.terminalErr); status != http.StatusServiceUnavailable {
		t.Fatalf("terminal error status: %d", status)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("missing candidates must not create database files: %v", entries)
	}
}

func TestFinishRestoreReopensValidCandidate(t *testing.T) {
	cfg := testConfig(t)
	actor := &storeActor{
		config:            cfg,
		data:              make(map[aggregateKey]Counters),
		dirty:             make(map[aggregateKey]struct{}),
		apiKeyCiphertexts: make(map[string]string),
	}
	db, lease, err := openStoreDatabase(cfg.DataPath)
	if err != nil {
		t.Fatal(err)
	}
	actor.db = db
	if err := actor.initialize(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	lease.release()

	restored, restoreErr := actor.finishRestore(errors.New("promote staged restore database: boom"), cfg.DataPath)
	if restored == nil {
		t.Fatalf("expected salvage to reopen the candidate: %v", restoreErr)
	}
	if restoreErr == nil || !strings.Contains(restoreErr.Error(), "promote staged restore database") {
		t.Fatalf("original failure must be preserved: %v", restoreErr)
	}
	if actor.terminalErr != nil {
		t.Fatalf("successful salvage must not enter terminal state: %v", actor.terminalErr)
	}
	defer func() {
		if err := actor.db.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if err := actor.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(metaBucket) == nil {
			return errors.New("meta bucket missing after reopen")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// respondTerminal must answer every command type the run loop accepts; a
// missing case would leave the sender blocked forever instead of failing fast.
func TestRespondTerminalRepliesToEveryCommand(t *testing.T) {
	actor := &storeActor{terminalErr: errors.New("terminal")}
	expectErr := func(name string, got error) {
		t.Helper()
		if got == nil || got.Error() != "terminal" {
			t.Fatalf("%s: expected terminal error, got %v", name, got)
		}
	}

	record := make(chan error, 1)
	actor.respondTerminal(recordCommand{resp: record})
	expectErr("record", <-record)

	query := make(chan queryResult, 1)
	actor.respondTerminal(queryCommand{resp: query})
	expectErr("query", (<-query).err)

	requests := make(chan requestQueryResult, 1)
	actor.respondTerminal(requestQueryCommand{resp: requests})
	expectErr("requests", (<-requests).err)

	preferences := make(chan preferencesResult, 1)
	actor.respondTerminal(preferencesQueryCommand{resp: preferences})
	expectErr("preferences query", (<-preferences).err)
	preferences = make(chan preferencesResult, 1)
	actor.respondTerminal(savePreferencesCommand{resp: preferences})
	expectErr("preferences save", (<-preferences).err)

	labels := make(chan labelQueryResult, 1)
	actor.respondTerminal(labelQueryCommand{resp: labels})
	expectErr("labels query", (<-labels).err)

	labelSet := make(chan error, 1)
	actor.respondTerminal(labelSetCommand{resp: labelSet})
	expectErr("labels set", <-labelSet)

	resolve := make(chan apiKeyResolveResult, 1)
	actor.respondTerminal(apiKeyResolveCommand{resp: resolve})
	expectErr("resolve", (<-resolve).err)

	prices := make(chan priceQueryResult, 1)
	actor.respondTerminal(priceQueryCommand{resp: prices})
	expectErr("prices query", (<-prices).err)
	prices = make(chan priceQueryResult, 1)
	actor.respondTerminal(savePricesCommand{resp: prices})
	expectErr("prices save", (<-prices).err)
	prices = make(chan priceQueryResult, 1)
	actor.respondTerminal(syncPricesCommand{resp: prices})
	expectErr("prices sync", (<-prices).err)

	observed := make(chan observedModelsResult, 1)
	actor.respondTerminal(observedModelsCommand{resp: observed})
	expectErr("observed models", (<-observed).err)

	costs := make(chan costSnapshotResult, 1)
	actor.respondTerminal(costSnapshotCommand{resp: costs})
	expectErr("costs", (<-costs).err)

	resets := make(chan resetResult, 1)
	actor.respondTerminal(resetCommand{resp: resets})
	expectErr("reset", (<-resets).err)

	configs := make(chan configResult, 1)
	actor.respondTerminal(configCommand{resp: configs})
	expectErr("config", (<-configs).err)

	backups := make(chan backupResult, 1)
	actor.respondTerminal(backupCommand{resp: backups})
	expectErr("backup", (<-backups).err)

	restores := make(chan restoreResult, 1)
	actor.respondTerminal(restoreCommand{resp: restores})
	expectErr("restore", (<-restores).err)

	closed := make(chan error, 1)
	actor.respondTerminal(closeCommand{resp: closed})
	if err := <-closed; err != nil {
		t.Fatalf("close must release the waiter without error, got %v", err)
	}
}

// A store that entered terminal state after a failed restore must still let
// Close return: the actor loop has to answer the close command and exit so
// the deferred done close fires instead of deadlocking the host shutdown.
func TestTerminalStoreCloseReleases(t *testing.T) {
	actor := &storeActor{terminalErr: errors.New("terminal"), config: testConfig(t)}
	store := &Store{commands: make(chan any, 16), done: make(chan struct{})}
	go store.run(actor)

	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("terminal close must release without error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close deadlocked while the actor was in terminal state")
	}
}

// respondWith must never block even when the response channel is already
// full: the panic recovery path answers best-effort and moves on.
func TestRespondWithNeverBlocksOnFullChannel(t *testing.T) {
	actor := &storeActor{terminalErr: errors.New("terminal")}
	record := make(chan error, 1)
	actor.respondWith(recordCommand{resp: record}, actor.terminalErr)
	done := make(chan struct{})
	go func() {
		actor.respondWith(recordCommand{resp: record}, errors.New("second"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("respondWith blocked on a full response channel")
	}
	if got := <-record; got == nil || got.Error() != "terminal" {
		t.Fatalf("first answer must be preserved, got %v", got)
	}
}
