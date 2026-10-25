package store

import (
	"testing"
	"time"
)

// The exact-stats path (custom ranges with sub-minute boundaries, e.g. the
// dashboard's last-5-hours preset) must keep provider/executor_type populated
// in the initial payload instead of blanking them through groupIdentity.
func TestExactPathInitialKeepsProviderAndExecutor(t *testing.T) {
	store, err := Open(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// A minute boundary minus 90s pins the second at :30 so the custom range
	// deterministically takes the exact-stats path.
	requestedAt := time.Now().UTC().Truncate(time.Minute).Add(-90 * time.Second)
	if err := store.Record(Usage{
		Dimensions:  Dimensions{Provider: "openai", ExecutorType: "chat", Model: "gpt-test"},
		RequestedAt: requestedAt,
		LatencyNS:   uint64(time.Second),
		Counters:    Counters{Requests: 1, InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}); err != nil {
		t.Fatal(err)
	}

	initial, err := store.QueryInitialStatsByFilter(Range{
		Name:  "custom",
		Start: requestedAt.Add(-time.Minute),
		End:   requestedAt.Add(time.Minute),
	}, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Models) != 1 {
		t.Fatalf("expected one model row, got %d", len(initial.Models))
	}
	if row := initial.Models[0]; row.Provider != "openai" {
		t.Fatalf("provider: %q", row.Provider)
	} else if row.ExecutorType != "chat" {
		t.Fatalf("executor_type: %q", row.ExecutorType)
	}
}
