package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

type Store struct {
	db               *bolt.DB
	lease            *storeLease
	commands         chan any
	done             chan struct{}
	closeOnce        sync.Once
	stateMu          sync.RWMutex
	costMu           sync.Mutex
	costCache        map[costCacheKey]CostResponse
	costOrder        []costCacheKey
	costFlights      map[costCacheKey]*costFlight
	costScanHook     func()
	closed           bool
	closeErr         error
	cryptoMu         sync.RWMutex
	activeGeneration uint64
	generations      map[uint64]apikey.APIKeyCryptoGeneration
}

type storeActor struct {
	db                   *bolt.DB
	config               config.Config
	crypto               apikey.CryptoContext
	data                 map[aggregateKey]Counters
	dirty                map[aggregateKey]struct{}
	since                time.Time
	lastUsed             time.Time
	pending              int
	lastPruneAt          time.Time
	lastFlushErr         error
	pendingRequests      []RequestDetail
	nextRequestSeq       uint64
	modelPrices          map[string]ModelPrice
	priceRevision        uint64
	priceSyncSettings    PriceSyncSettings
	lastPriceSync        *PriceSyncMetadata
	priceSavedAt         *time.Time
	costGeneration       uint64
	dashboardPreferences DashboardPreferences
	apiKeyCiphertexts    map[string]string
	apiKeyLabels         map[string]string
	activeGeneration     uint64
	generations          map[uint64]apikey.APIKeyCryptoGeneration
}

func Open(config config.Config) (*Store, error) {
	crypto, err := apikey.DeriveCryptoContext(config.APIKeySecret)
	if err != nil {
		return nil, err
	}
	return OpenWithCrypto(config, crypto)
}

func OpenWithCrypto(config config.Config, crypto apikey.CryptoContext) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(config.DataPath), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := recoverInterruptedRestore(config.DataPath); err != nil {
		return nil, err
	}
	db, lease, err := openStoreDatabase(config.DataPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	actor := &storeActor{
		db:                   db,
		config:               config,
		crypto:               crypto,
		data:                 make(map[aggregateKey]Counters),
		dirty:                make(map[aggregateKey]struct{}),
		dashboardPreferences: defaultDashboardPreferences(),
		apiKeyCiphertexts:    make(map[string]string),
		apiKeyLabels:         make(map[string]string),
	}
	if err := actor.initialize(); err != nil {
		_ = db.Close()
		lease.release()
		return nil, err
	}

	store := &Store{
		db:               db,
		lease:            lease,
		commands:         make(chan any, 256),
		done:             make(chan struct{}),
		costCache:        make(map[costCacheKey]CostResponse),
		costFlights:      make(map[costCacheKey]*costFlight),
		activeGeneration: actor.activeGeneration,
		generations:      cloneAPIKeyGenerations(actor.generations),
	}
	go store.run(actor)
	go lease.monitor(store)
	return store, nil
}

func (s *Store) Reset() error {
	resp := make(chan resetResult, 1)
	if err := s.send(resetCommand{resp: resp}); err != nil {
		return err
	}
	result := <-resp
	if result.err != nil {
		return result.err
	}
	s.cryptoMu.Lock()
	s.activeGeneration = result.generation
	s.generations = cloneAPIKeyGenerations(result.generations)
	s.cryptoMu.Unlock()
	return nil
}

func (s *Store) Reconfigure(config config.Config) error {
	crypto, err := apikey.DeriveCryptoContext(config.APIKeySecret)
	if err != nil {
		return err
	}
	return s.ReconfigureWithCrypto(config, crypto)
}

func (s *Store) ReconfigureWithCrypto(config config.Config, crypto apikey.CryptoContext) error {
	resp := make(chan configResult, 1)
	if err := s.send(configCommand{config: config, crypto: crypto, resp: resp}); err != nil {
		return err
	}
	result := <-resp
	if result.err != nil {
		return result.err
	}
	s.cryptoMu.Lock()
	s.activeGeneration = result.generation
	s.generations = cloneAPIKeyGenerations(result.generations)
	s.cryptoMu.Unlock()
	return nil
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		resp := make(chan error, 1)
		s.stateMu.Lock()
		if s.closed {
			s.stateMu.Unlock()
			return
		}
		s.closed = true
		s.commands <- closeCommand{resp: resp}
		s.stateMu.Unlock()
		s.closeErr = <-resp
		<-s.done
		if s.lease != nil {
			s.lease.release()
		}
	})
	return s.closeErr
}

func (a *storeActor) initialize() error {
	now := time.Now().UTC()
	var generations map[uint64]apikey.APIKeyCryptoGeneration
	var activeGeneration uint64
	if err := a.db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists(metaBucket)
		if err != nil {
			return err
		}
		hours, err := tx.CreateBucketIfNotExists(hoursBucket)
		if err != nil {
			return err
		}
		requests, err := tx.CreateBucketIfNotExists(requestsBucket)
		if err != nil {
			return err
		}
		version := decodeUint64(meta.Get(schemaKey))
		if version > persistenceSchemaVersion {
			return fmt.Errorf("unsupported database schema version %d", version)
		}
		if err := migratePriceMetadata(meta, version); err != nil {
			return err
		}
		var since time.Time
		if raw := meta.Get(sinceKey); len(raw) == 8 {
			since = time.Unix(0, decodeInt64(raw)).UTC()
		} else {
			since = now
		}
		cutoff := retentionCutoff(a.config, now)
		cutoffTime := time.Unix(cutoff, 0).UTC()
		if cutoffTime.After(since) {
			since = cutoffTime
		}
		if err := meta.Put(sinceKey, encodeInt64(since.UnixNano())); err != nil {
			return err
		}
		if err := pruneHoursBucket(hours, cutoff); err != nil {
			return err
		}
		if err := pruneRequestsBucket(requests, time.Unix(cutoff, 0).UTC().UnixNano()); err != nil {
			return err
		}
		if err := migrateUsageSources(hours, requests, version); err != nil {
			return err
		}
		if err := migrateAPIKeyCryptoSchema(meta, hours, requests, version, now); err != nil {
			return err
		}
		loadedGenerations, loadErr := loadAPIKeyGenerations(meta)
		if loadErr != nil {
			return loadErr
		}
		generations = loadedGenerations
		activatedGeneration, activatedGenerations, activateErr := activateAPIKeyGeneration(meta, generations, a.crypto, now)
		if activateErr != nil {
			return activateErr
		}
		activeGeneration = activatedGeneration
		generations = activatedGenerations
		return meta.Put(schemaKey, encodeUint64(persistenceSchemaVersion))
	}); err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	a.generations = generations
	a.activeGeneration = activeGeneration

	return a.reload()
}

// reload loads actor state from the current database handle without creating
// buckets, migrating schema, or mutating on-disk data.
func (a *storeActor) reload() error {
	a.data = make(map[aggregateKey]Counters)
	a.dirty = make(map[aggregateKey]struct{})
	a.pending = 0
	a.pendingRequests = nil
	a.lastFlushErr = nil
	a.lastPruneAt = time.Time{}
	a.since = time.Time{}
	a.lastUsed = time.Time{}
	a.nextRequestSeq = 0
	a.modelPrices = make(map[string]ModelPrice)
	a.priceRevision = 0
	a.priceSyncSettings = DefaultPriceSyncSettings()
	a.lastPriceSync = nil
	a.dashboardPreferences = defaultDashboardPreferences()
	a.apiKeyCiphertexts = make(map[string]string)
	a.apiKeyLabels = make(map[string]string)
	a.generations = make(map[uint64]apikey.APIKeyCryptoGeneration)
	a.activeGeneration = 0

	retainedHashes := make(map[string]struct{})
	err := a.db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		hours := tx.Bucket(hoursBucket)
		requests := tx.Bucket(requestsBucket)
		if meta == nil || hours == nil || requests == nil {
			return errors.New("database buckets are missing")
		}
		if decodeUint64(meta.Get(schemaKey)) != persistenceSchemaVersion {
			return errors.New("database schema is not initialized")
		}
		generations, err := loadAPIKeyGenerations(meta)
		if err != nil {
			return err
		}
		if err := validateAPIKeyGenerationReferences(hours, requests, generations); err != nil {
			return err
		}
		a.generations = generations
		a.activeGeneration = findAPIKeyGeneration(generations, a.crypto)
		a.since = time.Unix(0, decodeInt64(meta.Get(sinceKey))).UTC()
		a.nextRequestSeq = decodeUint64(meta.Get(requestSequenceKey))
		if raw := meta.Get(modelPricesKey); len(raw) > 0 {
			var stored map[string]ModelPrice
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode model prices: %w", err)
			}
			normalized, err := NormalizeModelPrices(stored)
			if err != nil {
				return fmt.Errorf("validate model prices: %w", err)
			}
			a.modelPrices = normalized
		}
		if raw := meta.Get(dashboardPreferencesKey); len(raw) > 0 {
			var stored DashboardPreferences
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode dashboard preferences: %w", err)
			}
			normalized, err := NormalizeDashboardPreferences(stored)
			if err != nil {
				return fmt.Errorf("validate dashboard preferences: %w", err)
			}
			a.dashboardPreferences = normalized
		}
		if raw := meta.Get(apiKeyLabelsKey); len(raw) > 0 {
			var labels map[string]string
			if err := json.Unmarshal(raw, &labels); err != nil {
				return fmt.Errorf("decode API key labels: %w", err)
			}
			if err := validateAPIKeyLabels(labels); err != nil {
				return fmt.Errorf("validate API key labels: %w", err)
			}
			a.apiKeyLabels = cloneStringMap(labels)
		}
		a.priceRevision = decodeUint64(meta.Get(modelPriceRevisionKey))
		if a.priceRevision == 0 && len(a.modelPrices) > 0 {
			a.priceRevision = 1
		}
		if raw := meta.Get(modelPriceSettingsKey); len(raw) > 0 {
			var stored PriceSyncSettings
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode model price sync settings: %w", err)
			}
			normalized, err := NormalizePriceSyncSettings(stored)
			if err != nil {
				return fmt.Errorf("validate model price sync settings: %w", err)
			}
			a.priceSyncSettings = normalized
		}
		if raw := meta.Get(modelPriceLastSyncKey); len(raw) > 0 {
			var stored PriceSyncMetadata
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode model price last sync: %w", err)
			}
			a.lastPriceSync = &stored
		}
		if raw := meta.Get(modelPriceSavedAtKey); len(raw) > 0 {
			var stored time.Time
			if err := json.Unmarshal(raw, &stored); err != nil {
				return fmt.Errorf("decode model price saved at: %w", err)
			}
			a.priceSavedAt = &stored
		}
		if raw := meta.Get(lastUsedKey); len(raw) > 0 {
			a.lastUsed = time.Unix(0, decodeInt64(raw)).UTC()
		}
		if err := hours.ForEach(func(hourKey, value []byte) error {
			if value != nil {
				return nil
			}
			hourBucket := hours.Bucket(hourKey)
			if hourBucket == nil {
				return nil
			}
			hour := decodeInt64(hourKey)
			return hourBucket.ForEach(func(dimensionKey, counterValue []byte) error {
				var dimensions Dimensions
				if err := json.Unmarshal(dimensionKey, &dimensions); err != nil {
					return fmt.Errorf("decode dimensions: %w", err)
				}
				var counters Counters
				if err := json.Unmarshal(counterValue, &counters); err != nil {
					return fmt.Errorf("decode counters: %w", err)
				}
				a.data[aggregateKey{Hour: hour, Dimensions: dimensions}] = counters
				if ref := apikey.Ref(dimensions.APIKeyGeneration, dimensions.APIKeyHash); ref != "" {
					retainedHashes[ref] = struct{}{}
				}
				return nil
			})
		}); err != nil {
			return err
		}
		return requests.ForEach(func(_, value []byte) error {
			if value == nil {
				return errors.New("request bucket contains nested bucket")
			}
			var request RequestDetail
			if err := json.Unmarshal(value, &request); err != nil {
				return fmt.Errorf("decode request detail: %w", err)
			}
			ref := apikey.Ref(request.APIKeyGeneration, request.APIKeyHash)
			if ref != "" && request.APIKey != "" {
				a.apiKeyCiphertexts[ref] = request.APIKey
			}
			if ref != "" {
				retainedHashes[ref] = struct{}{}
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	for hash := range a.apiKeyLabels {
		if _, retained := retainedHashes[hash]; !retained {
			return fmt.Errorf("API key label references data outside retention: %s", hash)
		}
	}
	return nil
}

func (a *storeActor) reconfigure(config config.Config, crypto apikey.CryptoContext) error {
	if config.DataPath != a.config.DataPath {
		return errors.New("data_path changes require opening a new store")
	}
	if err := a.flush(time.Now().UTC(), true); err != nil {
		a.lastFlushErr = err
		return err
	}
	previous := a.config
	previousCrypto := a.crypto
	previousPrune := a.lastPruneAt
	a.config = config
	a.crypto = crypto
	a.lastPruneAt = time.Time{}
	var generations map[uint64]apikey.APIKeyCryptoGeneration
	var generation uint64
	if err := a.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		if meta == nil {
			return errors.New("metadata bucket is missing")
		}
		var err error
		generation, generations, err = activateAPIKeyGeneration(meta, a.generations, crypto, time.Now().UTC())
		return err
	}); err != nil {
		a.config = previous
		a.crypto = previousCrypto
		a.lastPruneAt = previousPrune
		a.lastFlushErr = err
		return err
	}
	a.generations = generations
	a.activeGeneration = generation
	a.lastFlushErr = nil
	a.costGeneration++
	return nil
}

func (a *storeActor) reset() error {
	now := time.Now().UTC()
	if err := a.db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(hoursBucket); err != nil && !errors.Is(err, bolt.ErrBucketNotFound) {
			return err
		}
		if _, err := tx.CreateBucket(hoursBucket); err != nil {
			return err
		}
		if err := tx.DeleteBucket(requestsBucket); err != nil && !errors.Is(err, bolt.ErrBucketNotFound) {
			return err
		}
		if _, err := tx.CreateBucket(requestsBucket); err != nil {
			return err
		}
		meta := tx.Bucket(metaBucket)
		if meta == nil {
			return errors.New("metadata bucket is missing")
		}
		if err := meta.Put(sinceKey, encodeInt64(now.UnixNano())); err != nil {
			return err
		}
		if err := meta.Put(requestSequenceKey, encodeUint64(0)); err != nil {
			return err
		}
		if err := meta.Delete(cryptoKeyIDKey); err != nil {
			return err
		}
		if err := meta.Delete(apiKeyHashVersionKey); err != nil {
			return err
		}
		if err := meta.Delete(apiKeyGenerationsKey); err != nil {
			return err
		}
		if err := meta.Delete(apiKeyNextGenerationKey); err != nil {
			return err
		}
		if err := meta.Delete(apiKeyLabelsKey); err != nil {
			return err
		}
		return meta.Delete(lastUsedKey)
	}); err != nil {
		return fmt.Errorf("reset database: %w", err)
	}
	a.data = make(map[aggregateKey]Counters)
	a.dirty = make(map[aggregateKey]struct{})
	a.pending = 0
	a.pendingRequests = nil
	a.nextRequestSeq = 0
	a.lastFlushErr = nil
	a.since = now
	a.lastUsed = time.Time{}
	a.apiKeyCiphertexts = make(map[string]string)
	a.apiKeyLabels = make(map[string]string)
	a.generations = make(map[uint64]apikey.APIKeyCryptoGeneration)
	a.activeGeneration = 0
	if a.crypto.Enabled {
		if err := a.db.Update(func(tx *bolt.Tx) error {
			var err error
			a.activeGeneration, a.generations, err = activateAPIKeyGeneration(tx.Bucket(metaBucket), a.generations, a.crypto, now)
			return err
		}); err != nil {
			return fmt.Errorf("reset API key generation: %w", err)
		}
	}
	a.costGeneration++
	return nil
}
