package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

func (s *Store) QueryModelPrices() (map[string]ModelPrice, error) {
	response, err := s.QueryPriceBook()
	return response.Prices, err
}

func (s *Store) QueryPriceBook() (ModelPricesResponse, error) {
	resp := make(chan priceQueryResult, 1)
	if err := s.send(priceQueryCommand{resp: resp}); err != nil {
		return ModelPricesResponse{}, err
	}
	result := <-resp
	return result.response, result.err
}

func (s *Store) SaveModelPrices(prices map[string]ModelPrice) (map[string]ModelPrice, error) {
	response, err := s.SavePriceBook(prices, nil)
	return response.Prices, err
}

func (s *Store) SavePriceBook(prices map[string]ModelPrice, settings *PriceSyncSettings) (ModelPricesResponse, error) {
	normalized, err := NormalizeModelPrices(CloneModelPrices(prices))
	if err != nil {
		return ModelPricesResponse{}, errs.WithStatus(400, "%v", err)
	}
	if settings != nil {
		normalizedSettings, err := NormalizePriceSyncSettings(*settings)
		if err != nil {
			return ModelPricesResponse{}, errs.WithStatus(400, "%v", err)
		}
		settings = &normalizedSettings
	}
	resp := make(chan priceQueryResult, 1)
	if err := s.send(savePricesCommand{prices: normalized, settings: settings, resp: resp}); err != nil {
		return ModelPricesResponse{}, err
	}
	result := <-resp
	return result.response, result.err
}

func (s *Store) ApplyModelPriceSync(prices map[string]ModelPrice, settings PriceSyncSettings, metadata PriceSyncMetadata, expectedRevision uint64) (ModelPricesResponse, error) {
	normalized, err := NormalizeModelPrices(prices)
	if err != nil {
		return ModelPricesResponse{}, errs.WithStatus(400, "%v", err)
	}
	normalizedSettings, err := NormalizePriceSyncSettings(settings)
	if err != nil {
		return ModelPricesResponse{}, errs.WithStatus(400, "%v", err)
	}
	resp := make(chan priceQueryResult, 1)
	if err := s.send(syncPricesCommand{prices: normalized, settings: normalizedSettings, metadata: metadata, expectedRevision: expectedRevision, resp: resp}); err != nil {
		return ModelPricesResponse{}, err
	}
	result := <-resp
	return result.response, result.err
}

func (s *Store) ObservedModels() ([]string, error) {
	resp := make(chan observedModelsResult, 1)
	if err := s.send(observedModelsCommand{now: time.Now().UTC(), resp: resp}); err != nil {
		return nil, err
	}
	result := <-resp
	return result.models, result.err
}

func (a *storeActor) priceBookResponse() ModelPricesResponse {
	return ModelPricesResponse{
		SchemaVersion: 2,
		Revision:      a.priceRevision,
		Prices:        CloneModelPrices(a.modelPrices),
		SyncSettings:  clonePriceSyncSettings(a.priceSyncSettings),
		LastSync:      clonePriceSyncMetadata(a.lastPriceSync),
		SavedAt:       cloneTimePtr(a.priceSavedAt),
	}
}

func (a *storeActor) saveModelPrices(prices map[string]ModelPrice, settings *PriceSyncSettings) (ModelPricesResponse, error) {
	next := make(map[string]ModelPrice, len(prices))
	now := time.Now().UTC()
	for model, price := range prices {
		current, exists := a.modelPrices[model]
		if exists && current.Source == PriceSourceModelsDev && sameEditableModelPrice(current, price) {
			next[model] = current
			continue
		}
		price.Source = PriceSourceManual
		price.CatalogProvider = ""
		price.CatalogModel = ""
		price.UpdatedAt = now
		next[model] = price
	}
	nextSettings := a.priceSyncSettings
	if settings != nil {
		nextSettings = clonePriceSyncSettings(*settings)
	}
	nextRevision := a.priceRevision + 1
	if nextRevision == 0 {
		nextRevision = 1
	}
	if err := a.persistPriceBook(next, nextSettings, a.lastPriceSync, nextRevision, now); err != nil {
		return ModelPricesResponse{}, err
	}
	a.modelPrices = CloneModelPrices(next)
	a.priceSyncSettings = clonePriceSyncSettings(nextSettings)
	a.priceRevision = nextRevision
	a.priceSavedAt = cloneTimePtr(&now)
	return a.priceBookResponse(), nil
}

func (a *storeActor) applyModelPriceSync(prices map[string]ModelPrice, settings PriceSyncSettings, metadata PriceSyncMetadata, expectedRevision uint64) (ModelPricesResponse, error) {
	if a.priceRevision != expectedRevision {
		return ModelPricesResponse{}, errs.WithStatus(409, "model prices changed while synchronization was running")
	}
	next := CloneModelPrices(a.modelPrices)
	metadata.Created = 0
	metadata.Updated = 0
	metadata.SkippedManual = 0
	for model, price := range prices {
		current, exists := next[model]
		if exists && current.Source != PriceSourceModelsDev {
			metadata.SkippedManual++
			continue
		}
		if exists {
			metadata.Updated++
		} else {
			metadata.Created++
		}
		next[model] = price
	}
	metadata.Source = PriceSourceModelsDev
	metadata.CompletedAt = metadata.CompletedAt.UTC()
	if metadata.CompletedAt.IsZero() {
		metadata.CompletedAt = time.Now().UTC()
	}
	nextRevision := a.priceRevision + 1
	if nextRevision == 0 {
		nextRevision = 1
	}
	if err := a.persistPriceBook(next, settings, &metadata, nextRevision, metadata.CompletedAt); err != nil {
		return ModelPricesResponse{}, err
	}
	a.modelPrices = CloneModelPrices(next)
	a.priceSyncSettings = clonePriceSyncSettings(settings)
	a.lastPriceSync = clonePriceSyncMetadata(&metadata)
	a.priceRevision = nextRevision
	a.priceSavedAt = cloneTimePtr(&metadata.CompletedAt)
	return a.priceBookResponse(), nil
}

func (a *storeActor) persistPriceBook(prices map[string]ModelPrice, settings PriceSyncSettings, lastSync *PriceSyncMetadata, revision uint64, savedAt time.Time) error {
	encodedPrices, err := json.Marshal(prices)
	if err != nil {
		return fmt.Errorf("encode model prices: %w", err)
	}
	encodedSettings, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode model price sync settings: %w", err)
	}
	var encodedLastSync []byte
	if lastSync != nil {
		encodedLastSync, err = json.Marshal(lastSync)
		if err != nil {
			return fmt.Errorf("encode model price last sync: %w", err)
		}
	}
	encodedSavedAt, err := json.Marshal(savedAt.UTC())
	if err != nil {
		return fmt.Errorf("encode model price saved at: %w", err)
	}
	if err := a.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		if meta == nil {
			return errors.New("metadata bucket is missing")
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
		if lastSync == nil {
			if err := meta.Delete(modelPriceLastSyncKey); err != nil {
				return err
			}
		} else if err := meta.Put(modelPriceLastSyncKey, encodedLastSync); err != nil {
			return err
		}
		if err := meta.Put(modelPriceSavedAtKey, encodedSavedAt); err != nil {
			return err
		}
		return meta.Put(modelPriceRevisionKey, encodeUint64(revision))
	}); err != nil {
		return fmt.Errorf("save model prices: %w", err)
	}
	return nil
}

func (a *storeActor) observedModels(now time.Time) []string {
	seen := make(map[string]struct{})
	cutoff := retentionCutoff(a.config, now)
	for key := range a.data {
		if key.Hour < cutoff {
			continue
		}
		model := strings.TrimSpace(key.Dimensions.Model)
		if model != "" {
			seen[model] = struct{}{}
		}
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}
