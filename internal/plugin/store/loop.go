package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"

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
	stop := func() (exit bool) {
		var pending any
		defer func() {
			if recovered := recover(); recovered != nil {
				fmt.Fprintf(os.Stderr, "tokens-statistic: store actor panic: %v\n%s", recovered, debug.Stack())
				if pending != nil {
					// The interrupted command's work may have partially run; answer
					// it so the caller does not block forever. Non-blocking: when the
					// command already produced its own answer, drop this one.
					actor.respondWith(pending, fmt.Errorf("store actor panic: %v", recovered))
				}
				// The loop is about to exit with no one left to run the close
				// command's flush+db.Close. Best-effort release of the bbolt file
				// lock so a same-process handover can still reopen the database.
				// Guarded and time-capped: a panic mid-transaction may have left
				// bolt's internal mutex poisoned.
				releaseDone := make(chan struct{})
				go func() {
					defer close(releaseDone)
					defer func() { _ = recover() }()
					_ = actor.db.Close()
				}()
				select {
				case <-releaseDone:
				case <-time.After(2 * time.Second):
					fmt.Fprintf(os.Stderr, "tokens-statistic: store db.Close timed out after actor panic\n")
				}
				exit = true // unknown panic site: loop state is no longer trustworthy
			}
		}()
		select {
		case command := <-s.commands:
			pending = command
			if hook := actor.panicHook; hook != nil {
				hook()
			}
			if actor.terminalErr != nil {
				if _, isClose := command.(closeCommand); isClose {
					// A dead store must still answer Close: the nil response releases
					// the waiter and the true return below fires the deferred done
					// close, so Close stops waiting.
					actor.respondTerminal(command)
					return true
				}
				actor.respondTerminal(command)
				return false
			}
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
						return false
					}
					spec := item.spec
					switch item.mode {
					case statsQueryFull, statsQueryInitial:
						// Mirror the bucket path, which builds full stats with
						// allGroupFields: provider and executor_type must stay
						// populated, and initialStatsFromFull merges the group
						// rows back per model. A zero spec would blank them.
						spec = allGroupFields()
					}
					stats, err := actor.queryExactStats(item.queryRange, item.filter, spec, now)
					if err != nil {
						item.resp <- queryResult{err: err}
						return false
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
					return false
				}
				if err := actor.retryFailedFlush(now); err != nil {
					item.resp <- queryResult{err: err}
					return false
				}
				if item.mode == statsQueryHealth {
					// The health panel needs no time range: its windows are
					// fixed rolling buckets anchored at the current minute.
					// A flush first makes the newest request details queryable.
					if err := actor.flush(now, true); err != nil {
						actor.lastFlushErr = err
						item.resp <- queryResult{err: err}
						return false
					}
					health, err := actor.queryHealth(item.filter, now, int64(item.tzMinutes)*60)
					if err != nil {
						item.resp <- queryResult{err: err}
						return false
					}
					item.resp <- queryResult{health: health}
					return false
				}
				if err := item.queryRange.validate(); err != nil {
					item.resp <- queryResult{err: errs.WithStatus(400, "%v", err)}
					return false
				}
				switch item.mode {
				case statsQueryInitial:
					item.resp <- queryResult{initial: buildInitialStatsForRange(actor.data, actor.since, actor.lastUsed, item.queryRange, item.filter, now, actor.apiKeyCiphertexts)}
				case statsQueryTrend:
					item.resp <- queryResult{trend: buildStatsTrendForRange(actor.data, actor.since, item.queryRange, item.filter, now)}
				case statsQueryGroups:
					item.resp <- queryResult{groups: buildGroupsForRange(actor.data, item.queryRange, item.filter, now, actor.apiKeyCiphertexts, item.spec)}
				default:
					stats := buildStatsForRangeWithFilter(actor.data, actor.since, actor.lastUsed, item.queryRange, item.filter, now, actor.apiKeyCiphertexts, allGroupFields())
					item.resp <- queryResult{stats: stats}
				}
			case requestQueryCommand:
				now := time.Now().UTC()
				if err := actor.flush(now, true); err != nil {
					actor.lastFlushErr = err
					item.resp <- requestQueryResult{err: err}
					return false
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
					return false
				}
				item.resp <- observedModelsResult{models: actor.observedModels(item.now)}
			case costSnapshotCommand:
				now := time.Now().UTC()
				if err := actor.flush(now, true); err != nil {
					actor.lastFlushErr = err
					item.resp <- costSnapshotResult{err: err}
					return false
				}
				if err := item.queryRange.validate(); err != nil {
					item.resp <- costSnapshotResult{err: errs.WithStatus(400, "%v", err)}
					return false
				}
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
				}}
			case resetCommand:
				if err := actor.retryFailedFlush(time.Now().UTC()); err != nil {
					item.resp <- resetResult{err: err}
					return false
				}
				err := actor.reset()
				item.resp <- resetResult{generation: actor.activeGeneration, generations: cloneAPIKeyGenerations(actor.generations), err: err}
			case configCommand:
				if err := actor.retryFailedFlush(time.Now().UTC()); err != nil {
					item.resp <- configResult{err: err}
					return false
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
					return false
				}
				buffer := &limitedBuffer{max: MaxDatabaseBackupBytes}
				err := actor.db.View(func(tx *bolt.Tx) error {
					_, writeErr := tx.WriteTo(buffer)
					return writeErr
				})
				if err != nil {
					item.resp <- backupResult{err: fmt.Errorf("backup database: %w", err)}
					return false
				}
				item.resp <- backupResult{data: buffer.buf}
			case restoreCommand:
				db, err := actor.restoreBackup(item.backup)
				item.resp <- restoreResult{db: db, generation: actor.activeGeneration, generations: cloneAPIKeyGenerations(actor.generations), err: err}
			case closeCommand:
				flushErr := actor.flush(time.Now().UTC(), true)
				closeErr := actor.db.Close()
				item.resp <- errors.Join(flushErr, closeErr)
				return true
			}
		case now := <-ticker.C:
			if actor.terminalErr != nil {
				return false
			}
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						fmt.Fprintf(os.Stderr, "tokens-statistic: store flush panic: %v\n%s", recovered, debug.Stack())
						actor.lastFlushErr = fmt.Errorf("flush panic: %v", recovered)
					}
				}()
				actor.lastFlushErr = actor.flush(now.UTC(), false)
			}()
		}
		return exit
	}
	for {
		if stop() {
			return
		}
	}
}

// respondTerminal answers every command with the terminal error after a
// failed restore left the actor without a usable database handle. It must
// handle every command type the run loop accepts — update both together.
func (a *storeActor) respondTerminal(command any) {
	a.respondWith(command, a.terminalErr)
}

// respondWith answers any command with err. Response channels are freshly
// created and buffered by the callers, so the non-blocking send never skips
// on the normal path; it only keeps a panic recovery from answering twice
// and blocking forever on a full channel. It must handle every command type
// the run loop accepts — update both together.
func (a *storeActor) respondWith(command any, err error) {
	switch item := command.(type) {
	case recordCommand:
		select {
		case item.resp <- err:
		default:
		}
	case queryCommand:
		select {
		case item.resp <- queryResult{err: err}:
		default:
		}
	case requestQueryCommand:
		select {
		case item.resp <- requestQueryResult{err: err}:
		default:
		}
	case preferencesQueryCommand:
		select {
		case item.resp <- preferencesResult{err: err}:
		default:
		}
	case savePreferencesCommand:
		select {
		case item.resp <- preferencesResult{err: err}:
		default:
		}
	case labelQueryCommand:
		select {
		case item.resp <- labelQueryResult{err: err}:
		default:
		}
	case labelSetCommand:
		select {
		case item.resp <- err:
		default:
		}
	case apiKeyResolveCommand:
		select {
		case item.resp <- apiKeyResolveResult{err: err}:
		default:
		}
	case priceQueryCommand:
		select {
		case item.resp <- priceQueryResult{err: err}:
		default:
		}
	case savePricesCommand:
		select {
		case item.resp <- priceQueryResult{err: err}:
		default:
		}
	case syncPricesCommand:
		select {
		case item.resp <- priceQueryResult{err: err}:
		default:
		}
	case observedModelsCommand:
		select {
		case item.resp <- observedModelsResult{err: err}:
		default:
		}
	case costSnapshotCommand:
		select {
		case item.resp <- costSnapshotResult{err: err}:
		default:
		}
	case resetCommand:
		select {
		case item.resp <- resetResult{err: err}:
		default:
		}
	case configCommand:
		select {
		case item.resp <- configResult{err: err}:
		default:
		}
	case backupCommand:
		select {
		case item.resp <- backupResult{err: err}:
		default:
		}
	case restoreCommand:
		select {
		case item.resp <- restoreResult{err: err}:
		default:
		}
	case closeCommand:
		// Nothing to flush and no handle to close: release the waiter.
		select {
		case item.resp <- nil:
		default:
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
