package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

const MaxDatabaseBackupBytes = 64 << 20

type backupCommand struct{ resp chan backupResult }
type backupResult struct {
	data []byte
	err  error
}

type restoreCommand struct {
	backup []byte
	resp   chan restoreResult
}
type restoreResult struct {
	db          *bolt.DB
	generation  uint64
	generations map[uint64]apikey.APIKeyCryptoGeneration
	err         error
}

// limitedBuffer rejects WriteTo output once it exceeds MaxDatabaseBackupBytes.
type limitedBuffer struct {
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.max > 0 && len(b.buf)+len(p) > b.max {
		return 0, fmt.Errorf("database backup exceeds %d bytes", b.max)
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (s *Store) Backup() ([]byte, error) {
	resp := make(chan backupResult, 1)
	if err := s.send(backupCommand{resp: resp}); err != nil {
		return nil, err
	}
	result := <-resp
	return result.data, result.err
}

// RestoreBackup replaces the live database with a previously exported backup while
// keeping the store actor and handover lease alive. It locks stateMu directly so
// concurrent cost scans cannot observe a half-swapped *bolt.DB handle.
func (s *Store) RestoreBackup(backup []byte) error {
	if len(backup) == 0 {
		return errs.WithStatus(400, "backup body must not be empty")
	}
	if len(backup) > MaxDatabaseBackupBytes {
		return errs.WithStatus(413, "backup body exceeds %d bytes", MaxDatabaseBackupBytes)
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.closed {
		return errors.New("store is closed")
	}

	resp := make(chan restoreResult, 1)
	s.commands <- restoreCommand{backup: append([]byte(nil), backup...), resp: resp}
	result := <-resp
	if result.db != nil {
		s.db = result.db
		s.cryptoMu.Lock()
		s.activeGeneration = result.generation
		s.generations = cloneAPIKeyGenerations(result.generations)
		s.cryptoMu.Unlock()
		s.costMu.Lock()
		s.costCache = make(map[costCacheKey]CostResponse)
		s.costOrder = nil
		s.costFlights = make(map[costCacheKey]*costFlight)
		s.costMu.Unlock()
	}
	return result.err
}

func recoverInterruptedRestore(dataPath string) error {
	rollbackPath := dataPath + ".rollback"
	rollbackInfo, err := os.Stat(rollbackPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat restore rollback database: %w", err)
	}
	if rollbackInfo.IsDir() {
		return fmt.Errorf("restore rollback path is a directory: %s", rollbackPath)
	}

	liveInfo, err := os.Stat(dataPath)
	switch {
	case errors.Is(err, os.ErrNotExist), err == nil && !liveInfo.IsDir() && liveInfo.Size() == 0:
		if err == nil {
			if removeErr := os.Remove(dataPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("remove empty live database before rollback recovery: %w", removeErr)
			}
		}
		if renameErr := os.Rename(rollbackPath, dataPath); renameErr != nil {
			return fmt.Errorf("recover restore rollback database: %w", renameErr)
		}
		fmt.Fprintln(os.Stderr, "tokens-statistic: recovered database from interrupted restore rollback")
		return nil
	case err != nil:
		return fmt.Errorf("stat live database during restore recovery: %w", err)
	default:
		// A usable live database already exists; drop the leftover rollback file.
		if removeErr := os.Remove(rollbackPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("remove stale restore rollback database: %w", removeErr)
		}
		return nil
	}
}

func ValidateRestoreDatabase(path string) error {
	return validateRestoreDatabaseWithCrypto(path, apikey.CryptoContext{})
}

func validateRestoreDatabaseWithCrypto(path string, crypto apikey.CryptoContext) error {
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: storeOpenProbeTimeout})
	if err != nil {
		return fmt.Errorf("open staged restore database: %w", err)
	}
	defer db.Close()

	return db.View(func(tx *bolt.Tx) error {
		var firstErr error
		for checkErr := range tx.Check() {
			if checkErr != nil && firstErr == nil {
				firstErr = checkErr
			}
		}
		if firstErr != nil {
			return fmt.Errorf("database integrity check failed: %w", firstErr)
		}
		meta := tx.Bucket(metaBucket)
		hours := tx.Bucket(hoursBucket)
		requests := tx.Bucket(requestsBucket)
		if meta == nil || hours == nil || requests == nil {
			return errors.New("database buckets are missing")
		}
		version := decodeUint64(meta.Get(schemaKey))
		if version != persistenceSchemaVersion {
			return fmt.Errorf("unsupported restore schema version %d", version)
		}
		generations, err := loadAPIKeyGenerations(meta)
		if err != nil {
			return err
		}
		if err := validateAPIKeyGenerationReferences(hours, requests, generations); err != nil {
			return err
		}
		if raw := meta.Get(sinceKey); len(raw) != 0 && len(raw) != 8 {
			return errors.New("invalid since metadata")
		}
		if raw := meta.Get(lastUsedKey); len(raw) != 0 && len(raw) != 8 {
			return errors.New("invalid last_used metadata")
		}
		if raw := meta.Get(requestSequenceKey); len(raw) != 0 && len(raw) != 8 {
			return errors.New("invalid request sequence metadata")
		}
		if raw := meta.Get(modelPriceRevisionKey); len(raw) != 0 && len(raw) != 8 {
			return errors.New("invalid model price revision metadata")
		}
		if raw := meta.Get(modelPricesKey); len(raw) > 0 {
			var stored map[string]ModelPrice
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode model prices: %w", err)
			}
			if _, err := NormalizeModelPrices(stored); err != nil {
				return fmt.Errorf("validate model prices: %w", err)
			}
		}
		if raw := meta.Get(modelPriceSettingsKey); len(raw) > 0 {
			var stored PriceSyncSettings
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode model price sync settings: %w", err)
			}
			if _, err := NormalizePriceSyncSettings(stored); err != nil {
				return fmt.Errorf("validate model price sync settings: %w", err)
			}
		}
		if raw := meta.Get(modelPriceLastSyncKey); len(raw) > 0 {
			var stored PriceSyncMetadata
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode model price last sync: %w", err)
			}
		}
		if raw := meta.Get(dashboardPreferencesKey); len(raw) > 0 {
			var stored DashboardPreferences
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode dashboard preferences: %w", err)
			}
			if _, err := NormalizeDashboardPreferences(stored); err != nil {
				return fmt.Errorf("validate dashboard preferences: %w", err)
			}
		}
		if raw := meta.Get(apiKeyLabelsKey); len(raw) > 0 {
			var labels map[string]string
			if err := json.Unmarshal(raw, &labels); err != nil {
				return fmt.Errorf("decode API key labels: %w", err)
			}
			if err := validateAPIKeyLabels(labels); err != nil {
				return fmt.Errorf("validate API key labels: %w", err)
			}
			retained, _, err := retainedAPIKeyState(hours, requests)
			if err != nil {
				return err
			}
			for hash := range labels {
				if _, ok := retained[hash]; !ok {
					return fmt.Errorf("API key label references data outside retention: %s", hash)
				}
			}
		}
		return requests.ForEach(func(key, value []byte) error {
			if len(key) != 16 {
				return fmt.Errorf("invalid request key length %d", len(key))
			}
			if value == nil {
				return errors.New("request bucket contains nested bucket")
			}
			var detail RequestDetail
			if err := json.Unmarshal(value, &detail); err != nil {
				return fmt.Errorf("decode request detail: %w", err)
			}
			return nil
		})
	})
}

func migrateRestoreDatabase(path string, config config.Config, crypto apikey.CryptoContext) error {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: storeOpenProbeTimeout})
	if err != nil {
		return fmt.Errorf("open staged restore database for migration: %w", err)
	}
	actor := &storeActor{
		db:                   db,
		config:               config,
		crypto:               crypto,
		data:                 make(map[aggregateKey]Counters),
		dirty:                make(map[aggregateKey]struct{}),
		dashboardPreferences: defaultDashboardPreferences(),
		apiKeyCiphertexts:    make(map[string]string),
	}
	initializeErr := actor.initialize()
	syncErr := db.Sync()
	closeErr := db.Close()
	return errors.Join(initializeErr, syncErr, closeErr)
}

// syncDir fsyncs a directory so directory entries (creates/renames) become durable.
func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (a *storeActor) restoreBackup(backup []byte) (*bolt.DB, error) {
	if len(backup) == 0 {
		return a.db, errs.WithStatus(400, "backup body must not be empty")
	}
	if len(backup) > MaxDatabaseBackupBytes {
		return a.db, errs.WithStatus(413, "backup body exceeds %d bytes", MaxDatabaseBackupBytes)
	}
	if err := a.flush(time.Now().UTC(), true); err != nil {
		a.lastFlushErr = err
		return a.db, err
	}

	dir := filepath.Dir(a.config.DataPath)
	staged, err := os.CreateTemp(dir, "usage-restore-*.db")
	if err != nil {
		return a.db, fmt.Errorf("create staged restore database: %w", err)
	}
	stagedPath := staged.Name()
	cleanupStaged := true
	defer func() {
		if cleanupStaged {
			_ = os.Remove(stagedPath)
		}
	}()
	if _, err := staged.Write(backup); err != nil {
		_ = staged.Close()
		return a.db, fmt.Errorf("write staged restore database: %w", err)
	}
	// Persist the staged payload before validation/rename so a crash cannot leave a
	// half-written restore candidate that later renames would promote.
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		return a.db, fmt.Errorf("sync staged restore database: %w", err)
	}
	if err := staged.Close(); err != nil {
		return a.db, fmt.Errorf("close staged restore database: %w", err)
	}
	if err := migrateRestoreDatabase(stagedPath, a.config, a.crypto); err != nil {
		return a.db, errs.WithStatus(400, "invalid backup database: %v", err)
	}
	if err := validateRestoreDatabaseWithCrypto(stagedPath, a.crypto); err != nil {
		return a.db, errs.WithStatus(400, "invalid backup database: %v", err)
	}

	livePath := a.config.DataPath
	rollbackPath := livePath + ".rollback"
	_ = os.Remove(rollbackPath)

	if err := a.db.Close(); err != nil {
		return nil, fmt.Errorf("close database for restore: %w", err)
	}
	a.db = nil

	reopenLive := func(path string) (*bolt.DB, error) {
		db, openErr := bolt.Open(path, 0o600, &bolt.Options{Timeout: storeOpenProbeTimeout})
		if openErr != nil {
			return nil, openErr
		}
		a.db = db
		if reloadErr := a.reload(); reloadErr != nil {
			_ = db.Close()
			a.db = nil
			return nil, reloadErr
		}
		return db, nil
	}

	if err := os.Rename(livePath, rollbackPath); err != nil {
		db, openErr := reopenLive(livePath)
		if openErr != nil {
			return nil, errors.Join(fmt.Errorf("rename live database aside: %w", err), openErr)
		}
		return db, fmt.Errorf("rename live database aside: %w", err)
	}

	if err := os.Rename(stagedPath, livePath); err != nil {
		cleanupStaged = false
		_ = os.Remove(stagedPath)
		if renameErr := os.Rename(rollbackPath, livePath); renameErr != nil {
			return nil, errors.Join(fmt.Errorf("promote staged restore database: %w", err), renameErr)
		}
		db, openErr := reopenLive(livePath)
		if openErr != nil {
			return nil, errors.Join(fmt.Errorf("promote staged restore database: %w", err), openErr)
		}
		return db, fmt.Errorf("promote staged restore database: %w", err)
	}
	cleanupStaged = false
	// Directory fsync makes the staged→live rename durable across power loss.
	// The rename already succeeded, so treat a directory sync failure as best-effort:
	// continue opening the restored database rather than leaving the actor without a handle.
	_ = syncDir(dir)

	db, err := bolt.Open(livePath, 0o600, &bolt.Options{Timeout: storeOpenProbeTimeout})
	if err != nil {
		_ = os.Remove(livePath)
		if renameErr := os.Rename(rollbackPath, livePath); renameErr != nil {
			return nil, errors.Join(fmt.Errorf("open restored database: %w", err), renameErr)
		}
		db, openErr := reopenLive(livePath)
		if openErr != nil {
			return nil, errors.Join(fmt.Errorf("open restored database: %w", err), openErr)
		}
		return db, fmt.Errorf("open restored database: %w", err)
	}

	a.db = db
	if err := a.reload(); err != nil {
		_ = db.Close()
		a.db = nil
		_ = os.Remove(livePath)
		if renameErr := os.Rename(rollbackPath, livePath); renameErr != nil {
			return nil, errors.Join(fmt.Errorf("reload restored database: %w", err), renameErr)
		}
		db, openErr := reopenLive(livePath)
		if openErr != nil {
			return nil, errors.Join(fmt.Errorf("reload restored database: %w", err), openErr)
		}
		return db, fmt.Errorf("reload restored database: %w", err)
	}

	a.costGeneration++
	_ = os.Remove(rollbackPath)
	return db, nil
}
