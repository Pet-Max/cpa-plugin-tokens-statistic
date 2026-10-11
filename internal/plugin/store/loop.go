package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"
	"time"

	bolt "go.etcd.io/bbolt"
)

func (s *Store) Record(usage Usage) error {
	resp := make(chan error, 1)
	if err := s.send(recordCommand{usage: usage, resp: resp}); err != nil {
		return err
	}
	return <-resp
}

func (s *Store) send(command any) error {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.closed {
		return errors.New("store is closed")
	}
	s.commands <- command
	return nil
}

func (s *Store) run(actor *storeActor) {
	ticker := time.NewTicker(actor.config.FlushInterval)
	defer ticker.Stop()
	defer close(s.done)

	for {
		select {
		case command := <-s.commands:
			switch item := command.(type) {
			case recordCommand:
				// Always accept the new usage into the dirty in-memory aggregate. A
				// previous transient flush failure must not make subsequent usage vanish.
				item.resp <- actor.record(item.usage)
			case queryCommand:
				now := time.Now().UTC()
				if requiresExactStats(item.queryRange) {
					if err := actor.flush(now, true); err != nil {
						actor.lastFlushErr = err
						item.resp <- queryResult{err: err}
						continue
					}
					stats, err := actor.queryExactStats(item.queryRange, item.filter, now)
					if err != nil {
						item.resp <- queryResult{err: err}
						continue
					}
					switch item.mode {
					case statsQueryInitial:
						item.resp <- queryResult{initial: initialStatsFromFull(stats, item.queryRange)}
					case statsQueryTrend:
						item.resp <- queryResult{trend: trendStatsFromFull(stats, item.queryRange)}
					case statsQueryGroups:
						item.resp <- queryResult{groups: GroupStatsPage{SchemaVersion: 2, GeneratedAt: stats.GeneratedAt, Range: stats.Range, Items: stats.Groups, Total: len(stats.Groups)}}
					default:
						item.resp <- queryResult{stats: stats}
					}
					continue
				}
				if err := actor.retryFailedFlush(now); err != nil {
					item.resp <- queryResult{err: err}
					continue
				}
				if err := item.queryRange.validate(); err != nil {
					item.resp <- queryResult{err: errs.WithStatus(400, "%v", err)}
					continue
				}
				switch item.mode {
				case statsQueryInitial:
					item.resp <- queryResult{initial: buildInitialStatsForRange(actor.data, actor.since, actor.lastUsed, item.queryRange, item.filter, now, actor.apiKeyCiphertexts)}
				case statsQueryTrend:
					item.resp <- queryResult{trend: buildStatsTrendForRange(actor.data, actor.since, item.queryRange, item.filter, now)}
				case statsQueryGroups:
					item.resp <- queryResult{groups: buildGroupsForRange(actor.data, item.queryRange, item.filter, now, actor.apiKeyCiphertexts)}
				default:
					stats := buildStatsForRangeWithFilter(actor.data, actor.since, actor.lastUsed, item.queryRange, item.filter, now, actor.apiKeyCiphertexts)
					item.resp <- queryResult{stats: stats}
				}
			case requestQueryCommand:
				now := time.Now().UTC()
				if err := actor.flush(now, true); err != nil {
					actor.lastFlushErr = err
					item.resp <- requestQueryResult{err: err}
					continue
				}
				page, err := actor.queryRequests(item.queryRange, item.offset, item.limit, item.model, item.filter, item.result, now)
				item.resp <- requestQueryResult{page: page, err: err}
			case preferencesQueryCommand:
				item.resp <- preferencesResult{preferences: cloneDashboardPreferences(actor.dashboardPreferences)}
			case savePreferencesCommand:
				preferences, err := actor.saveDashboardPreferences(item.preferences)
				item.resp <- preferencesResult{preferences: preferences, err: err}
			case labelQueryCommand:
				item.resp <- labelQueryResult{labels: cloneStringMap(actor.apiKeyLabels)}
			case labelSetCommand:
				item.resp <- actor.setAPIKeyLabel(item.hash, item.label)
			case apiKeyResolveCommand:
				ref, err := actor.resolveAPIKeyHash(item.hash)
				item.resp <- apiKeyResolveResult{ref: ref, err: err}
			case priceQueryCommand:
				item.resp <- priceQueryResult{response: actor.priceBookResponse()}
			case savePricesCommand:
				response, err := actor.saveModelPrices(item.prices, item.settings)
				item.resp <- priceQueryResult{response: response, err: err}
			case syncPricesCommand:
				response, err := actor.applyModelPriceSync(item.prices, item.settings, item.metadata, item.expectedRevision)
				item.resp <- priceQueryResult{response: response, err: err}
			case observedModelsCommand:
				if err := actor.flush(item.now, true); err != nil {
					actor.lastFlushErr = err
					item.resp <- observedModelsResult{err: err}
					continue
				}
				item.resp <- observedModelsResult{models: actor.observedModels(item.now)}
			case costSnapshotCommand:
				now := time.Now().UTC()
				if err := actor.flush(now, true); err != nil {
					actor.lastFlushErr = err
					item.resp <- costSnapshotResult{err: err}
					continue
				}
				err := item.queryRange.validate()
				item.resp <- costSnapshotResult{snapshot: costQuerySnapshot{
					Range:         item.queryRange.Name,
					Start:         item.queryRange.Start,
					End:           item.queryRange.End,
					GeneratedAt:   now,
					Prices:        CloneModelPrices(actor.modelPrices),
					PriceSettings: clonePriceSyncSettings(actor.priceSyncSettings),
					PriceRevision: actor.priceRevision,
					HighWater:     actor.nextRequestSeq,
					Generation:    actor.costGeneration,
					Filter:        item.filter,
				}, err: err}
			case resetCommand:
				if err := actor.retryFailedFlush(time.Now().UTC()); err != nil {
					item.resp <- resetResult{err: err}
					continue
				}
				err := actor.reset()
				item.resp <- resetResult{generation: actor.activeGeneration, generations: cloneAPIKeyGenerations(actor.generations), err: err}
			case configCommand:
				if err := actor.retryFailedFlush(time.Now().UTC()); err != nil {
					item.resp <- configResult{err: err}
					continue
				}
				err := actor.reconfigure(item.config, item.crypto)
				if err == nil {
					ticker.Reset(item.config.FlushInterval)
				}
				item.resp <- configResult{generation: actor.activeGeneration, generations: cloneAPIKeyGenerations(actor.generations), err: err}
			case backupCommand:
				now := time.Now().UTC()
				if err := actor.flush(now, true); err != nil {
					actor.lastFlushErr = err
					item.resp <- backupResult{err: err}
					continue
				}
				buffer := &limitedBuffer{max: MaxDatabaseBackupBytes}
				err := actor.db.View(func(tx *bolt.Tx) error {
					_, writeErr := tx.WriteTo(buffer)
					return writeErr
				})
				if err != nil {
					item.resp <- backupResult{err: fmt.Errorf("backup database: %w", err)}
					continue
				}
				item.resp <- backupResult{data: buffer.buf}
			case restoreCommand:
				db, err := actor.restoreBackup(item.backup)
				item.resp <- restoreResult{db: db, generation: actor.activeGeneration, generations: cloneAPIKeyGenerations(actor.generations), err: err}
			case closeCommand:
				flushErr := actor.flush(time.Now().UTC(), true)
				closeErr := actor.db.Close()
				item.resp <- errors.Join(flushErr, closeErr)
				return
			}
		case now := <-ticker.C:
			actor.lastFlushErr = actor.flush(now.UTC(), false)
		}
	}
}

func (a *storeActor) record(usage Usage) error {
	usage.Dimensions = SanitizeDimensionsSource(usage.Dimensions)
	if usage.Dimensions.APIKey != "" || usage.Dimensions.APIKeyHash != "" || usage.Dimensions.APIKeyGeneration != 0 {
		if usage.Dimensions.APIKey == "" || !apikey.ValidHash(usage.Dimensions.APIKeyHash) || usage.Dimensions.APIKeyGeneration == 0 {
			return errors.New("API key ciphertext, fingerprint, and generation must be recorded together")
		}
		if _, ok := a.generations[usage.Dimensions.APIKeyGeneration]; !ok {
			return fmt.Errorf("API key references unregistered crypto generation %d", usage.Dimensions.APIKeyGeneration)
		}
	}
	aggregateDimensions := usage.Dimensions
	ciphertext := aggregateDimensions.APIKey
	aggregateDimensions.APIKey = ""
	key := aggregateKey{
		Hour:       usage.RequestedAt.UTC().Truncate(time.Minute).Unix(),
		Dimensions: aggregateDimensions,
	}
	counters := a.data[key]
	counters.add(countersForUsage(usage))
	a.data[key] = counters
	a.dirty[key] = struct{}{}
	a.nextRequestSeq++
	if a.nextRequestSeq == 0 {
		a.nextRequestSeq = 1
	}
	a.pendingRequests = append(a.pendingRequests, requestDetailForUsage(usage, a.nextRequestSeq))
	if ref := apikey.Ref(aggregateDimensions.APIKeyGeneration, aggregateDimensions.APIKeyHash); ciphertext != "" && ref != "" {
		a.apiKeyCiphertexts[ref] = ciphertext
	}
	a.pending++
	if a.lastUsed.IsZero() || usage.RequestedAt.After(a.lastUsed) {
		a.lastUsed = usage.RequestedAt
	}

	if a.lastFlushErr != nil || a.config.SyncOnRecord || a.pending >= a.config.FlushBatchSize {
		a.lastFlushErr = a.flush(time.Now().UTC(), false)
		return a.lastFlushErr
	}
	return nil
}

func (a *storeActor) retryFailedFlush(now time.Time) error {
	if a.lastFlushErr == nil {
		return nil
	}
	a.lastFlushErr = a.flush(now, true)
	return a.lastFlushErr
}

func (a *storeActor) flush(now time.Time, force bool) error {
	shouldPrune := a.lastPruneAt.IsZero() || now.Sub(a.lastPruneAt) >= time.Hour
	if len(a.dirty) == 0 && len(a.pendingRequests) == 0 && !shouldPrune && !force {
		return nil
	}
	cutoff := retentionCutoff(a.config, now)
	nextSince := a.since
	if shouldPrune {
		cutoffTime := time.Unix(cutoff, 0).UTC()
		if cutoffTime.After(nextSince) {
			nextSince = cutoffTime
		}
	}
	var nextCiphertexts map[string]string
	var nextLabels map[string]string
	err := a.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		hours := tx.Bucket(hoursBucket)
		requests := tx.Bucket(requestsBucket)
		if meta == nil || hours == nil || requests == nil {
			return errors.New("database buckets are missing")
		}
		for key := range a.dirty {
			hourBucket, err := hours.CreateBucketIfNotExists(encodeInt64(key.Hour))
			if err != nil {
				return err
			}
			dimensions, err := json.Marshal(key.Dimensions)
			if err != nil {
				return err
			}
			counters, err := json.Marshal(a.data[key])
			if err != nil {
				return err
			}
			if err := hourBucket.Put(dimensions, counters); err != nil {
				return err
			}
		}
		for _, request := range a.pendingRequests {
			encoded, err := json.Marshal(request)
			if err != nil {
				return err
			}
			if err := requests.Put(encodeRequestKey(request.Time.UnixNano(), request.Sequence), encoded); err != nil {
				return err
			}
		}
		if err := meta.Put(sinceKey, encodeInt64(nextSince.UnixNano())); err != nil {
			return err
		}
		if !a.lastUsed.IsZero() {
			if err := meta.Put(lastUsedKey, encodeInt64(a.lastUsed.UnixNano())); err != nil {
				return err
			}
		}
		if err := meta.Put(requestSequenceKey, encodeUint64(a.nextRequestSeq)); err != nil {
			return err
		}
		if shouldPrune {
			if err := pruneHoursBucket(hours, cutoff); err != nil {
				return err
			}
			if err := pruneRequestsBucket(requests, time.Unix(cutoff, 0).UTC().UnixNano()); err != nil {
				return err
			}
			retained, ciphertexts, err := retainedAPIKeyState(hours, requests)
			if err != nil {
				return err
			}
			labels := cloneStringMap(a.apiKeyLabels)
			for hash := range labels {
				if _, ok := retained[hash]; !ok {
					delete(labels, hash)
				}
			}
			encoded, err := json.Marshal(labels)
			if err != nil {
				return err
			}
			if len(labels) == 0 {
				if err := meta.Delete(apiKeyLabelsKey); err != nil {
					return err
				}
			} else if err := meta.Put(apiKeyLabelsKey, encoded); err != nil {
				return err
			}
			nextCiphertexts = ciphertexts
			nextLabels = labels
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("flush database: %w", err)
	}

	clear(a.dirty)
	a.pendingRequests = a.pendingRequests[:0]
	a.pending = 0
	a.lastFlushErr = nil
	if shouldPrune {
		a.since = nextSince
		for key := range a.data {
			if key.Hour < cutoff {
				delete(a.data, key)
			}
		}
		a.lastPruneAt = now
		a.apiKeyCiphertexts = nextCiphertexts
		a.apiKeyLabels = nextLabels
	}
	return nil
}

func retentionCutoff(config config.Config, now time.Time) int64 {
	return now.UTC().Add(-time.Duration(config.RetentionDays) * 24 * time.Hour).Truncate(time.Minute).Unix()
}

func pruneHoursBucket(hours *bolt.Bucket, cutoff int64) error {
	var expired [][]byte
	if err := hours.ForEach(func(key, value []byte) error {
		if value == nil && decodeInt64(key) < cutoff {
			expired = append(expired, append([]byte(nil), key...))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, key := range expired {
		if err := hours.DeleteBucket(key); err != nil {
			return err
		}
	}
	return nil
}
