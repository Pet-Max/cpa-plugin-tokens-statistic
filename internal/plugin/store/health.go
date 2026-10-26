package store

import (
	"encoding/json"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// HealthPoint is one fixed-width bucket of the dashboard health panel. The
// embedded Counters carry sums (latency/TTFT totals plus sample counts) so the
// dashboard can compute per-bucket averages and success rates itself.
// CacheRateNumerator/CacheRateDenominator accumulate the accounting-aware
// cache-rate pair at query time; rows with an unknown provider contribute
// nothing, so a zero denominator means "unclassified", not "no cache".
type HealthPoint struct {
	BucketStart          string `json:"bucket_start"`
	BucketEnd            string `json:"bucket_end"`
	Partial              bool   `json:"partial"`
	CacheRateNumerator   uint64 `json:"cache_rate_numerator"`
	CacheRateDenominator uint64 `json:"cache_rate_denominator"`
	Counters
}

type cacheAccounting uint8

const (
	cacheAccountingSubset cacheAccounting = iota
	cacheAccountingIndependent
)

// cacheAccountingForDimensions mirrors the dashboard's cacheAccountingKind so
// bucket cache rates follow the same provider semantics as the request table.
func cacheAccountingForDimensions(d Dimensions) (cacheAccounting, bool) {
	provider := strings.ToLower(strings.TrimSpace(d.Provider))
	executor := strings.ToLower(strings.TrimSpace(d.ExecutorType))
	value := provider + " " + executor
	if provider == "openai-compatibility" || strings.HasPrefix(provider, "openai-compatible-") || executor == "openaicompatexecutor" {
		return cacheAccountingSubset, true
	}
	if provider == "claude" || provider == "anthropic" || strings.Contains(value, "claude") || strings.Contains(value, "anthropic") {
		return cacheAccountingIndependent, true
	}
	if provider == "openai" || provider == "codex" || provider == "gemini" || provider == "gemini-interactions" {
		return cacheAccountingSubset, true
	}
	for _, marker := range []string{"openai", "codex", "xai", "grok", "kimi", "qwen", "deepseek", "openrouter", "gemini", "aistudio", "antigravity", "vertex", "interaction"} {
		if strings.Contains(value, marker) {
			return cacheAccountingSubset, true
		}
	}
	return cacheAccountingSubset, false
}

// cacheRateParts reduces one aggregate row to the numerator/denominator pair
// of its accounting-aware cache rate. CLIProxyAPI reports CachedTokens and
// CacheReadTokens with the same value, so the numerator must count cache
// tokens once. ok is false for rows whose cache fields are inconsistent with
// the row's accounting flavour or that carry no input total, matching the
// dashboard's request-table logic of skipping what it cannot classify.
func cacheRateParts(kind cacheAccounting, c Counters) (numerator, denominator uint64, ok bool) {
	if c.CacheReadTokens > 0 {
		numerator = c.CacheReadTokens
	} else if c.CacheCreationTokens > 0 && c.CachedTokens == c.CacheCreationTokens {
		numerator = 0
	} else if kind == cacheAccountingIndependent {
		if c.CacheCreationTokens == 0 && c.CachedTokens > 0 {
			numerator = c.CachedTokens
		} else if c.CachedTokens != 0 {
			return 0, 0, false
		}
	} else if c.CachedTokens > 0 {
		numerator = c.CachedTokens
	}
	if kind == cacheAccountingIndependent {
		denominator = c.InputTokens + c.CacheCreationTokens + c.CacheReadTokens
	} else {
		denominator = c.InputTokens
	}
	if denominator == 0 {
		return 0, 0, false
	}
	return numerator, denominator, true
}

// HealthCall is one individual request shown in the live tail of the health
// panel. Only non-sensitive fields are exposed — API key identities never
// leave the store. Provider/ExecutorType carry no identity either (the
// request log already serves them) and let the dashboard classify cache
// accounting exactly like the request table does.
type HealthCall struct {
	Time                string `json:"time"`
	Failed              bool   `json:"failed"`
	LatencyNS           uint64 `json:"latency_ns"`
	TTFTNS              uint64 `json:"ttft_ns"`
	TotalTokens         uint64 `json:"total_tokens"`
	InputTokens         uint64 `json:"input_tokens"`
	CacheReadTokens     uint64 `json:"cache_read_tokens"`
	CachedTokens        uint64 `json:"cached_tokens"`
	CacheCreationTokens uint64 `json:"cache_creation_tokens"`
	Model               string `json:"model"`
	Provider            string `json:"provider"`
	ExecutorType        string `json:"executor_type"`
}

// HealthResponse carries the rolling windows of the health panel: Day
// (24h × 1h) and Days (24d × 1day), both anchored at the current bucket and
// ordered oldest → newest, plus Calls, the live tail of individual requests
// backing the 实时 row.
type HealthResponse struct {
	SchemaVersion uint32        `json:"schema_version"`
	GeneratedAt   string        `json:"generated_at"`
	WarnPercent   int           `json:"warn_percent"`
	CritPercent   int           `json:"crit_percent"`
	Day           []HealthPoint `json:"day"`
	Days          []HealthPoint `json:"days"`
	Calls         []HealthCall  `json:"calls"`
}

// healthCallWindow bounds how far back the live tail reaches: the 实时 row
// shows individual calls from the last hour only, so stale traffic never
// masquerades as current health.
const healthCallWindow = time.Hour

// queryHealth builds the fixed windows plus the live tail of individual
// calls backing the 实时 row. tzSeconds is the viewer's offset east of UTC;
// window buckets align to the viewer's local clock (local midnight for the
// day window, local hour boundaries for the hour window).
func (a *storeActor) queryHealth(filter Filter, now time.Time, tzSeconds int64) (HealthResponse, error) {
	resp := buildHealthWindows(a.data, filter, now, tzSeconds)
	resp.Calls = a.lastCalls(filter, 24, now)
	return resp, nil
}

// lastCalls returns up to limit individual requests from the last hour,
// oldest → newest, filtered by object dimensions, scanning the request log
// backwards from the newest entry.
func (a *storeActor) lastCalls(filter Filter, limit int, now time.Time) []HealthCall {
	cutoff := now.Add(-healthCallWindow)
	collected := make([]HealthCall, 0, limit)
	_ = a.db.View(func(tx *bolt.Tx) error {
		requests := tx.Bucket(requestsBucket)
		if requests == nil {
			return nil
		}
		cursor := requests.Cursor()
		for key, value := cursor.Last(); key != nil && len(collected) < limit; key, value = cursor.Prev() {
			if len(key) != 16 || value == nil {
				continue
			}
			requestedAt := time.Unix(0, decodeInt64(key[:8])).UTC()
			if requestedAt.Before(cutoff) {
				break
			}
			var item RequestDetail
			if err := json.Unmarshal(value, &item); err != nil {
				continue
			}
			dimensions := SanitizeDimensionsSource(item.Dimensions)
			if !filter.matches(dimensions) {
				continue
			}
			collected = append(collected, HealthCall{
				Time:                requestedAt.Format(time.RFC3339),
				Failed:              dimensions.Failed,
				LatencyNS:           item.LatencyNS,
				TTFTNS:              item.TTFTNS,
				TotalTokens:         item.Counters.TotalTokens,
				InputTokens:         item.Counters.InputTokens,
				CacheReadTokens:     item.Counters.CacheReadTokens,
				CachedTokens:        item.Counters.CachedTokens,
				CacheCreationTokens: item.Counters.CacheCreationTokens,
				Model:               CompactModelName(dimensions.Model),
				Provider:            dimensions.Provider,
				ExecutorType:        dimensions.ExecutorType,
			})
		}
		return nil
	})
	// The scan yields newest-first; the panel expects oldest → newest.
	for left, right := 0, len(collected)-1; left < right; left, right = left+1, right-1 {
		collected[left], collected[right] = collected[right], collected[left]
	}
	return collected
}

// buildHealthWindows reduces minute aggregates into the fixed rolling
// windows. Unlike the range-based stats builders the windows are anchored at
// the current bucket of `now` and never read time-range parameters: the health
// panel is a "state of the service right now" surface. tzSeconds shifts the
// bucket grid to the viewer's local clock (0 keeps UTC alignment) so a "day"
// bucket is a local calendar day, matching the dashboard's frontend-side
// chart bucketing.
func buildHealthWindows(data map[aggregateKey]Counters, filter Filter, now time.Time, tzSeconds int64) HealthResponse {
	nowUnix := now.Unix()
	type healthWindow struct {
		width  int64
		count  int
		start  int64
		points []HealthPoint
	}
	windows := []healthWindow{
		{width: 3600, count: 24},
		{width: 86400, count: 24},
	}
	for i := range windows {
		w := &windows[i]
		// Bucket boundaries satisfy (unix + tzSeconds) ≡ 0 (mod width), i.e.
		// local midnight / local hour for the day / hour windows.
		aligned := ((nowUnix+tzSeconds)/w.width)*w.width - tzSeconds
		w.start = aligned - int64(w.count-1)*w.width
		w.points = make([]HealthPoint, w.count)
		for bucket := 0; bucket < w.count; bucket++ {
			bucketStart := w.start + int64(bucket)*w.width
			w.points[bucket] = HealthPoint{
				BucketStart: time.Unix(bucketStart, 0).UTC().Format(time.RFC3339),
				BucketEnd:   time.Unix(bucketStart+w.width, 0).UTC().Format(time.RFC3339),
				Partial:     bucket == w.count-1,
			}
		}
	}
	for key, counters := range data {
		if !filter.matches(key.Dimensions) {
			continue
		}
		minute := key.Hour
		numerator, denominator, hasRate := uint64(0), uint64(0), false
		if kind, known := cacheAccountingForDimensions(key.Dimensions); known {
			numerator, denominator, hasRate = cacheRateParts(kind, counters)
		}
		for wi := range windows {
			w := &windows[wi]
			if minute < w.start {
				continue
			}
			index := int((minute - w.start) / w.width)
			if index >= w.count {
				continue
			}
			w.points[index].Counters.add(counters)
			if hasRate {
				w.points[index].CacheRateNumerator += numerator
				w.points[index].CacheRateDenominator += denominator
			}
		}
	}
	return HealthResponse{
		SchemaVersion: 1,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Day:           windows[0].points,
		Days:          windows[1].points,
	}
}

// QueryHealthByFilter returns the fixed rolling health windows filtered by
// object dimensions (source, model, API key identities) aligned to the
// viewer's timezone. tzMinutes is the offset east of UTC in minutes (the
// inverse of JavaScript's getTimezoneOffset). It deliberately takes no time
// range: the health panel ignores the dashboard's time-range filter.
func (s *Store) QueryHealthByFilter(filter Filter, tzMinutes int) (HealthResponse, error) {
	resp := make(chan queryResult, 1)
	if err := s.send(queryCommand{filter: filter, tzMinutes: tzMinutes, mode: statsQueryHealth, resp: resp}); err != nil {
		return HealthResponse{}, err
	}
	result := <-resp
	return result.health, result.err
}
