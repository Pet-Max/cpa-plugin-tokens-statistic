package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"time"

	bolt "go.etcd.io/bbolt"
)

func migrateAPIKeyCryptoSchema(meta, hours, requests *bolt.Bucket, version uint64, now time.Time) error {
	if version >= 8 {
		generations, err := loadAPIKeyGenerations(meta)
		if err != nil {
			return err
		}
		return validateAPIKeyGenerationReferences(hours, requests, generations)
	}
	hasData, err := databaseHasAPIKeyData(hours, requests)
	if err != nil {
		return err
	}
	keyID := string(meta.Get(cryptoKeyIDKey))
	hashVersion := string(meta.Get(apiKeyHashVersionKey))
	hasIdentity := validAPIKeyKeyID(keyID) && hashVersion != ""
	generations := make(map[uint64]apikey.APIKeyCryptoGeneration)
	legacyGeneration := uint64(0)
	if hasData || keyID != "" || hashVersion != "" {
		legacyGeneration = 1
		generation := apikey.APIKeyCryptoGeneration{ID: legacyGeneration, CreatedAt: now.UTC(), IdentityMissing: !hasIdentity}
		if hasIdentity {
			generation.KeyID = keyID
			generation.HashVersion = hashVersion
		}
		generations[legacyGeneration] = generation
	}
	if legacyGeneration != 0 {
		if err := migrateLegacyAPIKeyRecords(hours, requests, legacyGeneration); err != nil {
			return err
		}
		if raw := meta.Get(apiKeyLabelsKey); len(raw) > 0 {
			var labels map[string]string
			if err := json.Unmarshal(raw, &labels); err != nil {
				return fmt.Errorf("decode legacy API key labels: %w", err)
			}
			migrated := make(map[string]string, len(labels))
			for hash, label := range labels {
				ref := apikey.Ref(legacyGeneration, hash)
				if ref == "" {
					return fmt.Errorf("invalid legacy API key label reference %q", hash)
				}
				migrated[ref] = label
			}
			encoded, err := json.Marshal(migrated)
			if err != nil {
				return err
			}
			if err := meta.Put(apiKeyLabelsKey, encoded); err != nil {
				return err
			}
		}
	}
	if err := saveAPIKeyGenerations(meta, generations); err != nil {
		return err
	}
	next := legacyGeneration + 1
	if next == 1 {
		next = 1
	}
	if err := meta.Put(apiKeyNextGenerationKey, encodeUint64(next)); err != nil {
		return err
	}
	if err := meta.Delete(cryptoKeyIDKey); err != nil {
		return err
	}
	if err := meta.Delete(apiKeyHashVersionKey); err != nil {
		return err
	}
	return validateAPIKeyGenerationReferences(hours, requests, generations)
}

func migrateLegacyAPIKeyRecords(hours, requests *bolt.Bucket, generation uint64) error {
	if err := hours.ForEach(func(hourKey, value []byte) error {
		if value != nil {
			return nil
		}
		hour := hours.Bucket(hourKey)
		if hour == nil {
			return nil
		}
		type entry struct {
			dimensions Dimensions
			counters   Counters
		}
		entries := make([]entry, 0)
		if err := hour.ForEach(func(key, value []byte) error {
			if value == nil {
				return errors.New("hour bucket contains nested bucket")
			}
			var dimensions Dimensions
			var counters Counters
			if err := json.Unmarshal(key, &dimensions); err != nil {
				return fmt.Errorf("decode legacy dimensions: %w", err)
			}
			if err := json.Unmarshal(value, &counters); err != nil {
				return fmt.Errorf("decode legacy counters: %w", err)
			}
			if dimensions.APIKeyHash != "" && dimensions.APIKeyGeneration == 0 {
				dimensions.APIKeyGeneration = generation
			}
			entries = append(entries, entry{dimensions: dimensions, counters: counters})
			return nil
		}); err != nil {
			return err
		}
		cursor := hour.Cursor()
		for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
			if err := cursor.Delete(); err != nil {
				return err
			}
		}
		merged := make(map[Dimensions]Counters)
		for _, item := range entries {
			combined := merged[item.dimensions]
			combined.add(item.counters)
			merged[item.dimensions] = combined
		}
		for dimensions, counters := range merged {
			key, err := json.Marshal(dimensions)
			if err != nil {
				return err
			}
			value, err := json.Marshal(counters)
			if err != nil {
				return err
			}
			if err := hour.Put(key, value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return requests.ForEach(func(key, value []byte) error {
		if value == nil {
			return errors.New("request bucket contains nested bucket")
		}
		var request RequestDetail
		if err := json.Unmarshal(value, &request); err != nil {
			return fmt.Errorf("decode legacy request: %w", err)
		}
		if request.APIKeyHash == "" || request.APIKeyGeneration != 0 {
			return nil
		}
		request.APIKeyGeneration = generation
		encoded, err := json.Marshal(request)
		if err != nil {
			return err
		}
		return requests.Put(key, encoded)
	})
}

func validateAPIKeyGenerationReferences(hours, requests *bolt.Bucket, generations map[uint64]apikey.APIKeyCryptoGeneration) error {
	validate := func(dimensions Dimensions) error {
		if dimensions.APIKeyHash == "" {
			if dimensions.APIKeyGeneration != 0 || dimensions.APIKey != "" {
				return errors.New("API key generation or ciphertext exists without fingerprint")
			}
			return nil
		}
		if !apikey.ValidHash(dimensions.APIKeyHash) || dimensions.APIKeyGeneration == 0 {
			return errors.New("API key fingerprint has no valid crypto generation")
		}
		if _, ok := generations[dimensions.APIKeyGeneration]; !ok {
			return fmt.Errorf("API key references unknown crypto generation %d", dimensions.APIKeyGeneration)
		}
		return nil
	}
	if err := hours.ForEach(func(hourKey, value []byte) error {
		if value != nil {
			return nil
		}
		hour := hours.Bucket(hourKey)
		return hour.ForEach(func(key, value []byte) error {
			var dimensions Dimensions
			if err := json.Unmarshal(key, &dimensions); err != nil {
				return fmt.Errorf("decode dimensions while validating API key generation: %w", err)
			}
			return validate(dimensions)
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
			return fmt.Errorf("decode request while validating API key generation: %w", err)
		}
		return validate(request.Dimensions)
	})
}

func migrateUsageSources(hours, requests *bolt.Bucket, version uint64) error {
	type legacyDimensions struct {
		Dimensions
		AuthProvider string `json:"auth_provider"`
		AuthAccount  string `json:"auth_account"`
	}

	type legacyRequestDetail struct {
		Sequence uint64    `json:"sequence"`
		Time     time.Time `json:"time"`
		legacyDimensions
		Counters
		Result        string         `json:"result"`
		LatencyNS     uint64         `json:"latency_ns"`
		TTFTNS        uint64         `json:"ttft_ns"`
		GenerationNS  uint64         `json:"generation_ns"`
		TPS           float64        `json:"tps"`
		CacheHit      bool           `json:"cache_hit"`
		EstimatedCost *EstimatedCost `json:"estimated_cost,omitempty"`
	}

	toCurrentRequest := func(request legacyRequestDetail) RequestDetail {
		return RequestDetail{Sequence: request.Sequence, Time: request.Time, Dimensions: request.Dimensions, Counters: request.Counters, Result: request.Result, LatencyNS: request.LatencyNS, TTFTNS: request.TTFTNS, GenerationNS: request.GenerationNS, TPS: request.TPS, CacheHit: request.CacheHit, EstimatedCost: request.EstimatedCost}
	}

	if version >= persistenceSchemaVersion {
		return nil
	}
	if hours != nil {
		var hourKeys [][]byte
		if err := hours.ForEach(func(key, value []byte) error {
			if value == nil {
				hourKeys = append(hourKeys, append([]byte(nil), key...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, hourKey := range hourKeys {
			hour := hours.Bucket(hourKey)
			if hour == nil {
				continue
			}
			merged := make(map[Dimensions]Counters)
			var oldKeys [][]byte
			changed := false
			if err := hour.ForEach(func(key, value []byte) error {
				if value == nil {
					return nil
				}
				var dimensions legacyDimensions
				if err := json.Unmarshal(key, &dimensions); err != nil {
					return fmt.Errorf("decode dimensions for source migration: %w", err)
				}
				var counters Counters
				if err := json.Unmarshal(value, &counters); err != nil {
					return fmt.Errorf("decode counters for source migration: %w", err)
				}
				sanitized := dimensions.Dimensions
				sanitized.Source = CanonicalUsageSourceWithIdentity(sanitized, dimensions.AuthProvider, dimensions.AuthAccount)
				changed = true
				combined := merged[sanitized]
				combined.add(counters)
				merged[sanitized] = combined
				oldKeys = append(oldKeys, append([]byte(nil), key...))
				return nil
			}); err != nil {
				return err
			}
			if !changed {
				continue
			}
			for _, key := range oldKeys {
				if err := hour.Delete(key); err != nil {
					return err
				}
			}
			for dimensions, counters := range merged {
				key, err := json.Marshal(dimensions)
				if err != nil {
					return err
				}
				value, err := json.Marshal(counters)
				if err != nil {
					return err
				}
				if err := hour.Put(key, value); err != nil {
					return err
				}
			}
		}
	}
	if requests != nil {
		type requestUpdate struct {
			key   []byte
			value []byte
		}
		var updates []requestUpdate
		if err := requests.ForEach(func(key, value []byte) error {
			if value == nil {
				return nil
			}
			var request legacyRequestDetail
			if err := json.Unmarshal(value, &request); err != nil {
				return fmt.Errorf("decode request for source migration: %w", err)
			}
			current := toCurrentRequest(request)
			current.Source = CanonicalUsageSourceWithIdentity(current.Dimensions, request.AuthProvider, request.AuthAccount)
			encoded, err := json.Marshal(current)
			if err != nil {
				return err
			}
			updates = append(updates, requestUpdate{key: append([]byte(nil), key...), value: encoded})
			return nil
		}); err != nil {
			return err
		}
		for _, update := range updates {
			if err := requests.Put(update.key, update.value); err != nil {
				return err
			}
		}
	}
	return nil
}

func migratePriceMetadata(meta *bolt.Bucket, version uint64) error {
	prices := make(map[string]ModelPrice)
	if raw := meta.Get(modelPricesKey); len(raw) > 0 {
		var stored map[string]ModelPrice
		if err := json.Unmarshal(raw, &stored); err != nil {
			return fmt.Errorf("decode model prices: %w", err)
		}
		normalized, err := NormalizeModelPrices(stored)
		if err != nil {
			return fmt.Errorf("validate model prices: %w", err)
		}
		prices = normalized
	}
	settings := DefaultPriceSyncSettings()
	if raw := meta.Get(modelPriceSettingsKey); len(raw) > 0 {
		var stored PriceSyncSettings
		if err := json.Unmarshal(raw, &stored); err != nil {
			return fmt.Errorf("decode model price sync settings: %w", err)
		}
		normalized, err := NormalizePriceSyncSettings(stored)
		if err != nil {
			return fmt.Errorf("validate model price sync settings: %w", err)
		}
		settings = normalized
	}
	if raw := meta.Get(modelPriceLastSyncKey); len(raw) > 0 {
		var stored PriceSyncMetadata
		if err := json.Unmarshal(raw, &stored); err != nil {
			return fmt.Errorf("decode model price last sync: %w", err)
		}
	}
	if version >= persistenceSchemaVersion {
		return nil
	}
	encodedPrices, err := json.Marshal(prices)
	if err != nil {
		return fmt.Errorf("encode migrated model prices: %w", err)
	}
	encodedSettings, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode migrated model price sync settings: %w", err)
	}
	if len(prices) == 0 {
		if err := meta.Delete(modelPricesKey); err != nil {
			return err
		}
	} else if err := meta.Put(modelPricesKey, encodedPrices); err != nil {
		return err
	}
	if err := meta.Put(modelPriceSettingsKey, encodedSettings); err != nil {
		return err
	}
	revision := decodeUint64(meta.Get(modelPriceRevisionKey))
	if revision == 0 && len(prices) > 0 {
		revision = 1
	}
	if err := meta.Put(modelPriceRevisionKey, encodeUint64(revision)); err != nil {
		return err
	}
	return nil
}
