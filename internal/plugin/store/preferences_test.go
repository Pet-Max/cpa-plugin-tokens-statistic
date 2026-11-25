package store

import "testing"

func TestDashboardPreferencesAllowCacheHitRateColumn(t *testing.T) {
	preferences, err := NormalizeDashboardPreferences(DashboardPreferences{
		RequestPageSize:        10,
		DimensionPageSize:      10,
		HiddenRequestColumns:   []string{"cache_hit_rate"},
		HiddenDimensionColumns: []string{},
		TimeRangeMode:          "custom",
	})
	if err != nil {
		t.Fatalf("cache hit rate column rejected by preferences: %v", err)
	}
	if len(preferences.HiddenRequestColumns) != 1 || preferences.HiddenRequestColumns[0] != "cache_hit_rate" {
		t.Fatalf("cache hit rate column was not retained: %+v", preferences)
	}
}

func TestDashboardPreferencesNormalizeTokenDisplayMode(t *testing.T) {
	for _, mode := range []string{"full", "auto", "k", "m", "B"} {
		preferences, err := NormalizeDashboardPreferences(DashboardPreferences{
			RequestPageSize:   10,
			DimensionPageSize: 10,
			TimeRangeMode:     "custom",
			TokenDisplayMode:  mode,
		})
		if err != nil || preferences.TokenDisplayMode != mode {
			t.Fatalf("token display mode %q: preferences=%+v err=%v", mode, preferences, err)
		}
	}

	empty := defaultDashboardPreferences()
	if _, err := NormalizeDashboardPreferences(empty); err != nil || empty.TokenDisplayMode != "auto" {
		t.Fatalf("empty token display mode: preferences=%+v err=%v", empty, err)
	}

	invalid := defaultDashboardPreferences()
	invalid.TokenDisplayMode = "billion"
	if _, err := NormalizeDashboardPreferences(invalid); err == nil {
		t.Fatal("unsupported token display mode was accepted")
	}
}

func TestDashboardPreferencesPersistHealthSection(t *testing.T) {
	normalized, err := NormalizeDashboardPreferences(DashboardPreferences{
		RequestPageSize:   25,
		DimensionPageSize: 25,
		HiddenSections:    []string{"health", "unknown", "requests", "health"},
		TimeRangeMode:     "today",
	})
	if err != nil {
		t.Fatalf("normalize hidden health section failed: %v", err)
	}
	if len(normalized.HiddenSections) != 2 || normalized.HiddenSections[0] != "health" || normalized.HiddenSections[1] != "requests" {
		t.Fatalf("hidden_sections after normalize = %+v", normalized.HiddenSections)
	}

	st, err := Open(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	saved, err := st.SaveDashboardPreferences(normalized)
	if err != nil {
		t.Fatalf("save preferences failed: %v", err)
	}
	loaded, err := st.QueryDashboardPreferences()
	if err != nil {
		t.Fatalf("query preferences failed: %v", err)
	}
	if len(saved.HiddenSections) != 2 || len(loaded.HiddenSections) != 2 || loaded.HiddenSections[0] != "health" || loaded.HiddenSections[1] != "requests" {
		t.Fatalf("persisted hidden_sections = saved:%+v loaded:%+v", saved.HiddenSections, loaded.HiddenSections)
	}
}
