package store

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	bolt "go.etcd.io/bbolt"
)

func encryptedUsageForTest(t *testing.T, ctx apikey.CryptoContext, key, model string, tokens uint64) Usage {
	return encryptedUsageForGeneration(t, ctx, 1, key, model, tokens)
}

func encryptedUsageForGeneration(t *testing.T, ctx apikey.CryptoContext, generation uint64, key, model string, tokens uint64) Usage {
	t.Helper()
	hash := apikey.Fingerprint(key, ctx.IndexKey)
	ciphertext, err := apikey.EncryptForGeneration(ctx, key, hash, generation)
	if err != nil {
		t.Fatal(err)
	}
	return Usage{
		RequestedAt: time.Now().UTC().Add(-time.Second),
		Dimensions:  Dimensions{Model: model, Source: "test", APIKey: ciphertext, APIKeyHash: hash, APIKeyGeneration: generation},
		Counters:    Counters{Requests: 1, TotalTokens: tokens},
	}
}

func revealStatsForCrypto(stats *StatsResponse, ctx apikey.CryptoContext, generations map[uint64]apikey.APIKeyCryptoGeneration) {
	stats.Reveal(func(ciphertext, fingerprint string, generation uint64) (string, string) {
		metadata, ok := generations[generation]
		if !ok || metadata.IdentityMissing {
			return "", apikey.StatusIdentityMissing
		}
		if !ctx.Enabled || metadata.KeyID != ctx.KeyID {
			return "", apikey.StatusGenerationUnavailable
		}
		plaintext, err := apikey.DecryptForGeneration(ctx, ciphertext, fingerprint, generation)
		if err != nil {
			return "", apikey.StatusCiphertextInvalid
		}
		return plaintext, apikey.StatusAvailable
	})
}

func TestAPIKeyCryptoGenerationsRotateWithoutDataLoss(t *testing.T) {
	config := testConfig(t)
	config.APIKeySecret = strings.Repeat("a", 32)
	config.SyncOnRecord = true
	ctxA, _ := apikey.DeriveCryptoContext(config.APIKeySecret)
	store, err := OpenWithCrypto(config, ctxA)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(encryptedUsageForGeneration(t, ctxA, 1, "identity-test-key", "original", 7)); err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	configB := config
	configB.APIKeySecret = strings.Repeat("b", 32)
	ctxB, _ := apikey.DeriveCryptoContext(configB.APIKeySecret)
	if err := store.ReconfigureWithCrypto(configB, ctxB); err != nil {
		t.Fatal(err)
	}
	generationB, generations := store.APIKeyCryptoState()
	if generationB != 2 || len(generations) != 2 {
		t.Fatalf("generation state = %d, %+v", generationB, generations)
	}
	if err := store.Record(encryptedUsageForGeneration(t, ctxB, generationB, "replacement-key", "replacement", 5)); err != nil {
		t.Fatal(err)
	}
	stats, err := store.Query("24h")
	if err != nil || stats.Summary.TotalTokens != 12 || stats.Summary.Requests != 2 {
		t.Fatalf("rotated stats = %+v, %v", stats, err)
	}
	revealStatsForCrypto(&stats, ctxB, generations)
	statuses := map[uint64]string{}
	for _, option := range stats.APIKeys {
		statuses[option.Generation] = option.Status
	}
	if statuses[1] != apikey.StatusGenerationUnavailable || statuses[2] != apikey.StatusAvailable {
		t.Fatalf("B reveal statuses = %+v", statuses)
	}

	if err := store.ReconfigureWithCrypto(config, ctxA); err != nil {
		t.Fatal(err)
	}
	generationA, generations := store.APIKeyCryptoState()
	if generationA != 1 || len(generations) != 2 {
		t.Fatalf("reactivated generation = %d, %+v", generationA, generations)
	}
	stats, err = store.Query("24h")
	if err != nil || stats.Summary.TotalTokens != 12 {
		t.Fatalf("reactivated stats = %+v, %v", stats, err)
	}
	revealStatsForCrypto(&stats, ctxA, generations)
	statuses = map[uint64]string{}
	for _, option := range stats.APIKeys {
		statuses[option.Generation] = option.Status
	}
	if statuses[1] != apikey.StatusAvailable || statuses[2] != apikey.StatusGenerationUnavailable {
		t.Fatalf("A reveal statuses = %+v", statuses)
	}

	disabled := config
	disabled.APIKeySecret = ""
	if err := store.Reconfigure(disabled); err != nil {
		t.Fatal(err)
	}
	stats, err = store.Query("24h")
	if err != nil || stats.Summary.TotalTokens != 12 {
		t.Fatalf("disabled tracking lost data: %+v, %v", stats, err)
	}
}

func TestRestoreAcceptsDifferentCryptoIdentityAndPreservesHistory(t *testing.T) {
	configA := testConfig(t)
	configA.APIKeySecret = strings.Repeat("a", 32)
	configA.SyncOnRecord = true
	ctxA, _ := apikey.DeriveCryptoContext(configA.APIKeySecret)
	storeA, err := OpenWithCrypto(configA, ctxA)
	if err != nil {
		t.Fatal(err)
	}
	plainA := "restore-source-api-key"
	if err := storeA.Record(encryptedUsageForTest(t, ctxA, plainA, "source", 11)); err != nil {
		t.Fatal(err)
	}
	backup, err := storeA.Backup()
	if err != nil {
		t.Fatal(err)
	}
	if err := storeA.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(backup, []byte(plainA)) {
		t.Fatal("source backup contains plaintext API key")
	}

	configB := testConfig(t)
	configB.APIKeySecret = strings.Repeat("b", 32)
	configB.SyncOnRecord = true
	ctxB, _ := apikey.DeriveCryptoContext(configB.APIKeySecret)
	storeB, err := OpenWithCrypto(configB, ctxB)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	if err := storeB.RestoreBackup(backup); err != nil {
		t.Fatalf("restore rejected a different crypto identity: %v", err)
	}
	stats, err := storeB.Query("24h")
	if err != nil || stats.Summary.TotalTokens != 11 || len(stats.Groups) != 1 || stats.Groups[0].Model != "source" {
		t.Fatalf("restored state = %+v, %v", stats, err)
	}
	generationB, generations := storeB.APIKeyCryptoState()
	if generationB == 0 || len(generations) != 2 {
		t.Fatalf("restored generations = %d, %+v", generationB, generations)
	}
	revealStatsForCrypto(&stats, ctxB, generations)
	if len(stats.APIKeys) != 1 || stats.APIKeys[0].Status != apikey.StatusGenerationUnavailable {
		t.Fatalf("restored old key status = %+v", stats.APIKeys)
	}
}

func TestReconfigureCryptoIdentityRollsBackWithFailedCandidateFlush(t *testing.T) {
	config := testConfig(t)
	config.APIKeySecret = ""
	db, err := bolt.Open(config.DataPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	actor := &storeActor{
		db:                   db,
		config:               config,
		data:                 make(map[aggregateKey]Counters),
		dirty:                make(map[aggregateKey]struct{}),
		modelPrices:          make(map[string]ModelPrice),
		dashboardPreferences: defaultDashboardPreferences(),
		apiKeyCiphertexts:    make(map[string]string),
		apiKeyLabels:         make(map[string]string),
	}
	if err := actor.initialize(); err != nil {
		t.Fatal(err)
	}
	defer actor.db.Close()

	if err := actor.db.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket(metaBucket)
	}); err != nil {
		t.Fatal(err)
	}

	candidate := config
	candidate.APIKeySecret = strings.Repeat("c", 32)
	candidateCrypto, err := apikey.DeriveCryptoContext(candidate.APIKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := actor.reconfigure(candidate, candidateCrypto); err == nil {
		t.Fatal("reconfigure succeeded despite candidate flush failure")
	}
	if actor.config.APIKeySecret != "" || actor.crypto.Enabled {
		t.Fatalf("actor retained failed candidate config: config=%q enabled=%v", actor.config.APIKeySecret, actor.crypto.Enabled)
	}
	if actor.activeGeneration != 0 || len(actor.generations) != 0 {
		t.Fatalf("failed reconfigure changed generations: %d %+v", actor.activeGeneration, actor.generations)
	}
}

func TestSchemaSixDatabaseMigratesToCryptoIdentity(t *testing.T) {
	config := testConfig(t)
	config.APIKeySecret = strings.Repeat("d", 32)
	ctx, err := apikey.DeriveCryptoContext(config.APIKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(config.DataPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucket(metaBucket)
		if err != nil {
			return err
		}
		if err := meta.Put(schemaKey, encodeUint64(6)); err != nil {
			return err
		}
		if err := meta.Put(sinceKey, encodeInt64(time.Now().UTC().UnixNano())); err != nil {
			return err
		}
		if _, err := tx.CreateBucket(hoursBucket); err != nil {
			return err
		}
		_, err = tx.CreateBucket(requestsBucket)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenWithCrypto(config, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = bolt.Open(config.DataPath, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		if decodeUint64(meta.Get(schemaKey)) != persistenceSchemaVersion {
			t.Fatalf("schema version = %d", decodeUint64(meta.Get(schemaKey)))
		}
		generations, err := loadAPIKeyGenerations(meta)
		if err != nil {
			return err
		}
		if len(generations) != 1 || generations[1].KeyID != ctx.KeyID || generations[1].HashVersion != apikey.HashVersion {
			t.Fatalf("schema-six generations = %+v", generations)
		}
		if len(meta.Get(cryptoKeyIDKey)) != 0 || len(meta.Get(apiKeyHashVersionKey)) != 0 {
			t.Fatal("legacy crypto identity metadata was not removed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationPreservesAPIKeyDataWithoutCryptoIdentity(t *testing.T) {
	config := testConfig(t)
	config.APIKeySecret = apikey.DefaultSecret
	db, err := bolt.Open(config.DataPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Minute)
	dimensions, err := json.Marshal(Dimensions{Model: "legacy", APIKeyHash: strings.Repeat("a", 32)})
	if err != nil {
		t.Fatal(err)
	}
	counters, err := json.Marshal(Counters{Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucket(metaBucket)
		if err != nil {
			return err
		}
		if err := meta.Put(schemaKey, encodeUint64(6)); err != nil {
			return err
		}
		if err := meta.Put(sinceKey, encodeInt64(now.UnixNano())); err != nil {
			return err
		}
		hours, err := tx.CreateBucket(hoursBucket)
		if err != nil {
			return err
		}
		hour, err := hours.CreateBucket(encodeInt64(now.Unix()))
		if err != nil {
			return err
		}
		if err := hour.Put(dimensions, counters); err != nil {
			return err
		}
		_, err = tx.CreateBucket(requestsBucket)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	active, generations := store.APIKeyCryptoState()
	if active != 2 || len(generations) != 2 || !generations[1].IdentityMissing {
		t.Fatalf("migrated generations = active %d, %+v", active, generations)
	}
	stats, err := store.Query("24h")
	if err != nil || stats.Summary.Requests != 1 || len(stats.APIKeys) != 1 || stats.APIKeys[0].Generation != 1 {
		t.Fatalf("migrated stats = %+v, %v", stats, err)
	}
	ctx, _ := apikey.DeriveCryptoContext(config.APIKeySecret)
	revealStatsForCrypto(&stats, ctx, generations)
	if stats.APIKeys[0].Status != apikey.StatusIdentityMissing {
		t.Fatalf("legacy status = %+v", stats.APIKeys[0])
	}
}

func TestSchemaSevenMigrationMergesAggregatesAndPreservesSensitiveMetadata(t *testing.T) {
	config := testConfig(t)
	config.APIKeySecret = strings.Repeat("m", 32)
	ctx, err := apikey.DeriveCryptoContext(config.APIKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	plainKey := "schema-seven-client-key"
	hash := apikey.Fingerprint(plainKey, ctx.IndexKey)
	ciphertext, err := apikey.EncryptAPIKey(ctx, plainKey, hash)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Minute)
	request := RequestDetail{
		Sequence: 1,
		Time:     now,
		Dimensions: Dimensions{
			Model:      "legacy-model",
			APIKey:     ciphertext,
			APIKeyHash: hash,
		},
		Counters: Counters{Requests: 1, TotalTokens: 9},
		Result:   "成功",
	}
	requestValue, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	labelsValue, err := json.Marshal(map[string]string{hash: "legacy label"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(config.DataPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucket(metaBucket)
		if err != nil {
			return err
		}
		for key, value := range map[string][]byte{
			string(schemaKey):            encodeUint64(7),
			string(sinceKey):             encodeInt64(now.UnixNano()),
			string(requestSequenceKey):   encodeUint64(1),
			string(cryptoKeyIDKey):       []byte(ctx.KeyID),
			string(apiKeyHashVersionKey): []byte(apikey.HashVersion),
			string(apiKeyLabelsKey):      labelsValue,
		} {
			if err := meta.Put([]byte(key), value); err != nil {
				return err
			}
		}
		hours, err := tx.CreateBucket(hoursBucket)
		if err != nil {
			return err
		}
		hour, err := hours.CreateBucket(encodeInt64(now.Unix()))
		if err != nil {
			return err
		}
		firstDimensions := []byte(`{"model":"legacy-model","api_key_hash":"` + hash + `"}`)
		secondDimensions := []byte(`{"api_key_hash":"` + hash + `","model":"legacy-model"}`)
		firstCounters, _ := json.Marshal(Counters{Requests: 2, InputTokens: 3, TotalTokens: 3})
		secondCounters, _ := json.Marshal(Counters{Requests: 3, OutputTokens: 4, TotalTokens: 4})
		if err := hour.Put(firstDimensions, firstCounters); err != nil {
			return err
		}
		if err := hour.Put(secondDimensions, secondCounters); err != nil {
			return err
		}
		requests, err := tx.CreateBucket(requestsBucket)
		if err != nil {
			return err
		}
		return requests.Put(encodeRequestKey(now.UnixNano(), 1), requestValue)
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenWithCrypto(config, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	active, generations := store.APIKeyCryptoState()
	if active != 1 || len(generations) != 1 || generations[1].KeyID != ctx.KeyID {
		t.Fatalf("migrated crypto state = %d, %+v", active, generations)
	}
	stats, err := store.Query("24h")
	if err != nil || stats.Summary.Requests != 5 || stats.Summary.InputTokens != 3 || stats.Summary.OutputTokens != 4 || stats.Summary.TotalTokens != 7 || len(stats.Groups) != 1 {
		t.Fatalf("migrated aggregate stats = %+v, %v", stats, err)
	}
	revealStatsForCrypto(&stats, ctx, generations)
	if len(stats.APIKeys) != 1 || stats.APIKeys[0].Ref != apikey.Ref(1, hash) || stats.APIKeys[0].Key != plainKey || stats.APIKeys[0].Status != apikey.StatusAvailable {
		t.Fatalf("migrated aggregate API key = %+v", stats.APIKeys)
	}
	page, err := store.QueryRequests("24h", 0, 10, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].APIKeyGeneration != 1 {
		t.Fatalf("migrated requests = %+v, %v", page, err)
	}
	page.Reveal(func(ciphertext, fingerprint string, generation uint64) (string, string) {
		plaintext, decryptErr := apikey.DecryptForGeneration(ctx, ciphertext, fingerprint, generation)
		if decryptErr != nil {
			return "", apikey.StatusCiphertextInvalid
		}
		return plaintext, apikey.StatusAvailable
	})
	if page.Items[0].APIKey != plainKey || page.Items[0].APIKeyStatus != apikey.StatusAvailable {
		t.Fatalf("migrated v1 request ciphertext = %+v", page.Items[0])
	}
	labels, err := store.APIKeyLabels()
	if err != nil || labels[apikey.Ref(1, hash)] != "legacy label" || len(labels) != 1 {
		t.Fatalf("migrated labels = %+v, %v", labels, err)
	}
}

func TestAPIKeyGenerationRegistryRejectsAmbiguousMetadata(t *testing.T) {
	config := testConfig(t)
	db, err := bolt.Open(config.DataPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, err := apikey.DeriveCryptoContext(strings.Repeat("v", 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucket(metaBucket)
		if err != nil {
			return err
		}
		duplicate, err := json.Marshal([]apikey.APIKeyCryptoGeneration{
			{ID: 1, KeyID: ctx.KeyID, HashVersion: apikey.HashVersion},
			{ID: 2, KeyID: ctx.KeyID, HashVersion: apikey.HashVersion},
		})
		if err != nil {
			return err
		}
		if err := meta.Put(apiKeyGenerationsKey, duplicate); err != nil {
			return err
		}
		if _, err := loadAPIKeyGenerations(meta); err == nil || !strings.Contains(err.Error(), "duplicate identities") {
			t.Fatalf("duplicate generation identity error = %v", err)
		}
		generations := map[uint64]apikey.APIKeyCryptoGeneration{
			2: {ID: 2, KeyID: ctx.KeyID, HashVersion: apikey.HashVersion},
		}
		if err := meta.Put(apiKeyNextGenerationKey, encodeUint64(2)); err != nil {
			return err
		}
		candidate, err := apikey.DeriveCryptoContext(strings.Repeat("w", 32))
		if err != nil {
			return err
		}
		if _, _, err := activateAPIKeyGeneration(meta, generations, candidate, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "does not follow existing generation") {
			t.Fatalf("regressed generation sequence error = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDegradesIncompleteCryptoIdentityMetadata(t *testing.T) {
	config := testConfig(t)
	config.APIKeySecret = apikey.DefaultSecret
	ctx, err := apikey.DeriveCryptoContext(config.APIKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(config.DataPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucket(metaBucket)
		if err != nil {
			return err
		}
		if err := meta.Put(schemaKey, encodeUint64(7)); err != nil {
			return err
		}
		if err := meta.Put(sinceKey, encodeInt64(time.Now().UTC().UnixNano())); err != nil {
			return err
		}
		if err := meta.Put(cryptoKeyIDKey, []byte(ctx.KeyID)); err != nil {
			return err
		}
		if _, err := tx.CreateBucket(hoursBucket); err != nil {
			return err
		}
		_, err = tx.CreateBucket(requestsBucket)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenWithCrypto(config, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	active, generations := store.APIKeyCryptoState()
	if active != 2 || len(generations) != 2 || !generations[1].IdentityMissing {
		t.Fatalf("incomplete identity generations = active %d, %+v", active, generations)
	}
}
