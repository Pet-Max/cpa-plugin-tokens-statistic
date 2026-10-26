package store

import (
	"strings"
	"testing"
	"time"
)

func TestBuildHealthWindowsBucketsAnchorsAndMerging(t *testing.T) {
	now := time.Date(2026, 10, 24, 9, 7, 30, 0, time.UTC)
	mk := func(month, day, hour, minute int) int64 {
		return time.Date(2026, time.Month(month), day, hour, minute, 0, 0, time.UTC).Unix()
	}
	data := map[aggregateKey]Counters{}
	// Inside the current hour: visible in both windows.
	data[aggregateKey{Hour: mk(10, 24, 9, 5), Dimensions: Dimensions{Model: "m", Source: "s1"}}] = Counters{Requests: 2, FailedRequests: 1, TotalTokens: 100, TotalLatencyNS: 3000, LatencySamples: 2, TotalTTFTNS: 800, TTFTSamples: 2}
	// 2h+ ago: inside the 24h and 24d windows.
	data[aggregateKey{Hour: mk(10, 24, 7, 0), Dimensions: Dimensions{Model: "m", Source: "s1"}}] = Counters{Requests: 1, TotalTokens: 10}
	// Yesterday: only inside the 24d window, all calls failed.
	data[aggregateKey{Hour: mk(10, 23, 12, 0), Dimensions: Dimensions{Model: "m", Source: "s1"}}] = Counters{Requests: 4, FailedRequests: 4, TotalTokens: 40}
	// 26 days ago: outside every window.
	data[aggregateKey{Hour: mk(9, 28, 12, 0), Dimensions: Dimensions{Model: "m", Source: "s1"}}] = Counters{Requests: 9}
	// Different source: must be dropped by the object filter.
	data[aggregateKey{Hour: mk(10, 24, 9, 6), Dimensions: Dimensions{Model: "m", Source: "other"}}] = Counters{Requests: 7}

	resp := buildHealthWindows(data, Filter{Model: "m", Source: "s1"}, now, 0)

	if len(resp.Day) != 24 || len(resp.Days) != 24 {
		t.Fatalf("window lengths: day=%d days=%d", len(resp.Day), len(resp.Days))
	}
	if resp.SchemaVersion != 1 || resp.GeneratedAt == "" {
		t.Fatalf("missing schema/generated_at: %+v", resp)
	}
	// Day window anchored at the current hour, days window at UTC midnight.
	if got := resp.Day[23].BucketStart; got != "2026-10-24T09:00:00Z" {
		t.Fatalf("day newest bucket: %s", got)
	}
	if got := resp.Days[23].BucketStart; got != "2026-10-24T00:00:00Z" {
		t.Fatalf("days newest bucket: %s", got)
	}
	// Newest bucket of every window contains now and is flagged partial.
	for _, w := range []struct {
		name   string
		points []HealthPoint
	}{
		{"day", resp.Day}, {"days", resp.Days},
	} {
		if !w.points[23].Partial {
			t.Fatalf("%s newest bucket not flagged partial", w.name)
		}
		if w.points[0].Partial {
			t.Fatalf("%s oldest bucket unexpectedly partial", w.name)
		}
	}
	// Merging: the current-bucket record landed in both windows' newest bucket;
	// days[23] additionally merges today's earlier 07:00 record (3 total).
	if resp.Day[23].Requests != 2 {
		t.Fatalf("day newest counters: %+v", resp.Day[23].Counters)
	}
	if resp.Days[23].Requests != 3 || resp.Days[23].FailedRequests != 1 {
		t.Fatalf("days newest counters: %+v", resp.Days[23].Counters)
	}
	// Averages derive from totals/samples inside the merged bucket.
	if got := resp.Day[23].averageLatencyNS(); got != 1500 {
		t.Fatalf("average latency: %d", got)
	}
	if got := resp.Day[23].averageTTFTNS(); got != 400 {
		t.Fatalf("average ttft: %d", got)
	}
	// The 07:00 record is in day[21] and merges into days[23].
	if resp.Day[21].Requests != 1 {
		t.Fatalf("07:00 record window placement: day=%+v", resp.Day[21].Counters)
	}
	// Yesterday's all-failed record lands in days[22] only; the 07:00 record
	// from today merges into days[23] together with the 09:05 record.
	if resp.Days[22].Requests != 4 || resp.Days[22].FailedRequests != 4 {
		t.Fatalf("yesterday bucket: %+v", resp.Days[22].Counters)
	}
	if resp.Days[23].Requests != 3 || resp.Days[23].FailedRequests != 1 || resp.Days[23].TotalTokens != 110 {
		t.Fatalf("today bucket: %+v", resp.Days[23].Counters)
	}
	// The other-source record (7 calls) and the 26-day-old record (9 calls)
	// vanished everywhere; yesterday's 4-call record legitimately sits inside
	// the 24h window, so no bucket may reach those two magnitudes.
	for _, w := range []struct {
		name   string
		points []HealthPoint
	}{
		{"day", resp.Day}, {"days", resp.Days},
	} {
		for _, point := range w.points {
			if point.Requests == 7 || point.Requests == 9 || point.Requests > 9 {
				t.Fatalf("%s contains filtered-out usage: %+v", w.name, point.Counters)
			}
		}
	}
}

func TestBuildHealthWindowsAPIKeyRefFilter(t *testing.T) {
	now := time.Date(2026, 10, 24, 9, 7, 30, 0, time.UTC)
	hash := strings.Repeat("ab", 16)
	ref := "g1:" + hash
	data := map[aggregateKey]Counters{
		{Hour: now.Truncate(time.Minute).Unix(), Dimensions: Dimensions{APIKeyGeneration: 1, APIKeyHash: hash}}: {Requests: 3},
		{Hour: now.Truncate(time.Minute).Unix(), Dimensions: Dimensions{Model: "m"}}:                            {Requests: 5},
	}
	resp := buildHealthWindows(data, NewFilterFromIdentities("", []string{ref}), now, 0)
	if resp.Day[23].Requests != 3 {
		t.Fatalf("api key ref filter did not match: %+v", resp.Day[23].Counters)
	}
}

func TestQueryHealthByFilterRoundTrip(t *testing.T) {
	config := testConfig(t)
	st, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Record(Usage{Dimensions: Dimensions{Model: "m", Failed: true, Provider: "openai-compatibility", ExecutorType: "openaicompatexecutor"}, RequestedAt: time.Now().UTC(), Counters: Counters{Requests: 1, FailedRequests: 1, TotalTokens: 9, InputTokens: 6, CacheReadTokens: 3}}); err != nil {
		t.Fatal(err)
	}
	// A two-hour-old call must fall outside the live tail's one-hour window.
	if err := st.Record(Usage{Dimensions: Dimensions{Model: "stale"}, RequestedAt: time.Now().UTC().Add(-2 * time.Hour), Counters: Counters{Requests: 1, TotalTokens: 5}}); err != nil {
		t.Fatal(err)
	}
	resp, err := st.QueryHealthByFilter(Filter{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Day[23].Requests != 1 || resp.Day[23].FailedRequests != 1 {
		t.Fatalf("health round trip: %+v", resp.Day[23])
	}
	if len(resp.Calls) != 1 {
		t.Fatalf("health calls: %+v", resp.Calls)
	}
	call := resp.Calls[0]
	if call.Failed != true || call.TotalTokens != 9 || call.InputTokens != 6 || call.CacheReadTokens != 3 || call.Model != "m" {
		t.Fatalf("health call fields: %+v", call)
	}
	if call.Provider != "openai-compatibility" || call.ExecutorType != "openaicompatexecutor" {
		t.Fatalf("health call provider fields: %+v", call)
	}
}

func TestBuildHealthWindowsCacheRateClassification(t *testing.T) {
	now := time.Date(2026, 10, 24, 9, 7, 30, 0, time.UTC)
	minute := now.Truncate(time.Minute).Unix()
	data := map[aggregateKey]Counters{
		// OpenAI-style: cached is part of input; CLIProxyAPI dual-fills read.
		{Hour: minute, Dimensions: Dimensions{Model: "m", Provider: "openai-compatibility", ExecutorType: "openaicompatexecutor"}}: {Requests: 1, InputTokens: 100000, CachedTokens: 60000, CacheReadTokens: 60000},
		// Anthropic-style: cache buckets independent of input, dual-filled read.
		{Hour: minute, Dimensions: Dimensions{Model: "m", Provider: "claude"}}: {Requests: 1, InputTokens: 40000, CachedTokens: 60000, CacheReadTokens: 60000, CacheCreationTokens: 20000},
		// Gemini via executor-string marker: cache lives inside input totals.
		{Hour: minute, Dimensions: Dimensions{Model: "m", ExecutorType: "GeminiCLIExecutor"}}: {Requests: 1, InputTokens: 50000, CachedTokens: 10000, CacheReadTokens: 10000},
		// Unknown provider: must contribute nothing to the accounting-aware sums.
		{Hour: minute, Dimensions: Dimensions{Model: "m"}}: {Requests: 1, InputTokens: 999999, CachedTokens: 999999, CacheReadTokens: 999999},
	}
	resp := buildHealthWindows(data, Filter{Model: "m"}, now, 0)
	point := resp.Day[23]
	// Numerator counts cache tokens once per row: 60000 + 60000 + 10000; the
	// unknown row is skipped entirely, so the denominator only sums the three
	// classified rows: 100000 + 120000 + 50000.
	if point.CacheRateNumerator != 130000 || point.CacheRateDenominator != 270000 {
		t.Fatalf("cache rate sums: numerator=%d denominator=%d", point.CacheRateNumerator, point.CacheRateDenominator)
	}
	if resp.Days[22].CacheRateDenominator != 0 || resp.Days[22].CacheRateNumerator != 0 {
		t.Fatalf("empty bucket must keep zero cache-rate sums: %+v", resp.Days[22])
	}
}

func TestBuildHealthWindowsLocalTimezoneAlignment(t *testing.T) {
	now := time.Date(2026, 10, 24, 9, 7, 30, 0, time.UTC)
	mk := func(month, day, hour, minute int) int64 {
		return time.Date(2026, time.Month(month), day, hour, minute, 0, 0, time.UTC).Unix()
	}

	// UTC+8 (tz=28800s): the current local date is 10/24 (17:07 local), so the
	// newest day bucket must start at that local midnight = 10/23T16:00Z.
	resp := buildHealthWindows(map[aggregateKey]Counters{}, Filter{}, now, 28800)
	if got := resp.Days[23].BucketStart; got != "2026-10-23T16:00:00Z" {
		t.Fatalf("utc+8 days newest bucket: %s", got)
	}
	// Whole-hour offsets keep hour buckets on the hour: 17:00 local = 09:00Z.
	if got := resp.Day[23].BucketStart; got != "2026-10-24T09:00:00Z" {
		t.Fatalf("utc+8 day newest bucket: %s", got)
	}
	// An early-morning call of the current local day (10/24 02:00 local =
	// 10/23T18:00Z) belongs to the local day 10/24 bucket [10/23T16:00Z,
	// 10/24T16:00Z) = index 23 — even though UTC alignment would file it under
	// the previous UTC day.
	data := map[aggregateKey]Counters{
		{Hour: mk(10, 23, 18, 0), Dimensions: Dimensions{Model: "m"}}: {Requests: 1},
	}
	resp = buildHealthWindows(data, Filter{}, now, 28800)
	if resp.Days[23].Requests != 1 {
		t.Fatalf("utc+8 early-morning call attributed outside local day bucket: %+v", resp.Days[23].Counters)
	}
	// Under UTC alignment the same call lands in the previous day bucket.
	resp = buildHealthWindows(data, Filter{}, now, 0)
	if resp.Days[22].Requests != 1 || resp.Days[23].Requests != 0 {
		t.Fatalf("utc alignment disagrees: prev=%+v current=%+v", resp.Days[22].Counters, resp.Days[23].Counters)
	}

	// UTC+5:30 (tz=19800s): local now is 14:37, so the hour bucket starts at
	// 14:00 local = 08:30Z — half-hour offsets shift hour boundaries too.
	resp = buildHealthWindows(map[aggregateKey]Counters{}, Filter{}, now, 19800)
	if got := resp.Day[23].BucketStart; got != "2026-10-24T08:30:00Z" {
		t.Fatalf("utc+5:30 day newest bucket: %s", got)
	}

	// Negative offset (UTC-5, tz=-18000s): local now is 10/24 04:07, current
	// local date 10/24, local midnight = 10/24T05:00Z.
	resp = buildHealthWindows(map[aggregateKey]Counters{}, Filter{}, now, -18000)
	if got := resp.Days[23].BucketStart; got != "2026-10-24T05:00:00Z" {
		t.Fatalf("utc-5 days newest bucket: %s", got)
	}
}
