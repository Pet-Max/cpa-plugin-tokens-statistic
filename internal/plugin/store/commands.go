package store

import (
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"
	"time"
)

type recordCommand struct {
	usage Usage
	resp  chan error
}

type statsQueryMode uint8

const (
	statsQueryFull statsQueryMode = iota
	statsQueryInitial
	statsQueryTrend
	statsQueryGroups
)

type queryCommand struct {
	queryRange Range
	filter     Filter
	mode       statsQueryMode
	resp       chan queryResult
}

type queryResult struct {
	stats   StatsResponse
	initial InitialStatsResponse
	trend   StatsTrendResponse
	groups  GroupStatsPage
	err     error
}

type requestQueryCommand struct {
	queryRange Range
	offset     int
	limit      int
	model      string
	filter     Filter
	result     string
	resp       chan requestQueryResult
}

type requestQueryResult struct {
	page RequestPage
	err  error
}

type priceQueryCommand struct{ resp chan priceQueryResult }
type priceQueryResult struct {
	response ModelPricesResponse
	err      error
}
type savePricesCommand struct {
	prices   map[string]ModelPrice
	settings *PriceSyncSettings
	resp     chan priceQueryResult
}
type syncPricesCommand struct {
	prices           map[string]ModelPrice
	settings         PriceSyncSettings
	metadata         PriceSyncMetadata
	expectedRevision uint64
	resp             chan priceQueryResult
}
type observedModelsCommand struct {
	now  time.Time
	resp chan observedModelsResult
}
type observedModelsResult struct {
	models []string
	err    error
}
type costSnapshotCommand struct {
	queryRange Range
	filter     Filter
	resp       chan costSnapshotResult
}
type costSnapshotResult struct {
	snapshot costQuerySnapshot
	err      error
}
type preferencesQueryCommand struct{ resp chan preferencesResult }
type savePreferencesCommand struct {
	preferences DashboardPreferences
	resp        chan preferencesResult
}
type preferencesResult struct {
	preferences DashboardPreferences
	err         error
}

type labelQueryCommand struct{ resp chan labelQueryResult }
type labelQueryResult struct {
	labels map[string]string
	err    error
}
type labelSetCommand struct {
	hash  string
	label string
	resp  chan error
}
type apiKeyResolveCommand struct {
	hash string
	resp chan apiKeyResolveResult
}
type apiKeyResolveResult struct {
	ref string
	err error
}

type resetCommand struct{ resp chan resetResult }
type resetResult struct {
	generation  uint64
	generations map[uint64]apikey.APIKeyCryptoGeneration
	err         error
}
type configCommand struct {
	config config.Config
	crypto apikey.CryptoContext
	resp   chan configResult
}
type configResult struct {
	generation  uint64
	generations map[uint64]apikey.APIKeyCryptoGeneration
	err         error
}
type closeCommand struct{ resp chan error }
