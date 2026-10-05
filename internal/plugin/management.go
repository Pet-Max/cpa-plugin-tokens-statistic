package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var pluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type managementRegistrationResponse struct {
	Routes    []pluginapi.ManagementRoute `json:"routes,omitempty"`
	Resources []pluginapi.ResourceRoute   `json:"resources,omitempty"`
}

type registeredRoutes struct {
	pluginID         string
	statsPath        string
	statsInitialPath string
	statsTrendPath   string
	statsGroupsPath  string
	requestsPath     string
	costsPath        string
	exchangeRatePath string
	pricesPath       string
	priceSyncPath    string
	preferencesPath  string
	apiKeyInfoPath   string
	apiKeyLabelsPath string
	dashboardPath    string
	resetPath        string
	backupPath       string
	restorePath      string
}

func (r *pluginRuntime) registerManagement(raw []byte) (managementRegistrationResponse, error) {
	var request pluginapi.ManagementRegistrationRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return managementRegistrationResponse{}, withStatus(400, "decode management registration: %v", err)
	}
	pluginID, err := pluginIDFromResourceBase(request.ResourceBasePath)
	if err != nil {
		return managementRegistrationResponse{}, err
	}

	// All dynamic endpoints live under the management route family where the
	// host enforces the management key. Resource routes serve static UI only.
	managementBase := "/v0/management/plugins/" + pluginID
	routes := registeredRoutes{
		pluginID:         pluginID,
		statsPath:        managementBase + "/stats",
		statsInitialPath: managementBase + "/stats/initial",
		statsTrendPath:   managementBase + "/stats/trends",
		statsGroupsPath:  managementBase + "/stats/groups",
		requestsPath:     managementBase + "/requests",
		costsPath:        managementBase + "/costs",
		exchangeRatePath: managementBase + "/exchange-rate",
		pricesPath:       managementBase + "/prices",
		priceSyncPath:    managementBase + "/prices/sync",
		preferencesPath:  managementBase + "/preferences",
		apiKeyInfoPath:   managementBase + "/api-key-info",
		apiKeyLabelsPath: managementBase + "/api-key-labels",
		dashboardPath:    "/v0/resource/plugins/" + pluginID + "/dashboard",
		resetPath:        managementBase + "/reset",
		backupPath:       managementBase + "/backup",
		restorePath:      managementBase + "/restore",
	}
	r.mu.Lock()
	r.routes = routes
	r.mu.Unlock()

	return managementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/stats",
				Description: "Read aggregated token usage statistics.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/stats/initial",
				Description: "Read compact first-screen token usage statistics.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/stats/trends",
				Description: "Read downsampled per-model token trends.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/stats/groups",
				Description: "Read detailed dimension statistics.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/requests",
				Description: "Read paginated per-request token usage details.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/costs",
				Description: "Read exact per-request-derived estimated cost statistics.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/exchange-rate",
				Description: "Read the cached latest USD to CNY exchange rate for dashboard display.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/prices",
				Description: "Read persisted model token prices for the plugin dashboard.",
			},
			{
				Method:      http.MethodPut,
				Path:        "/plugins/" + pluginID + "/prices",
				Description: "Persist per-model input, output, cache, and context-tier token prices.",
			},
			{
				Method:      http.MethodPost,
				Path:        "/plugins/" + pluginID + "/prices/sync",
				Description: "Synchronize CLIProxyAPI model prices from models.dev.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/preferences",
				Description: "Read dashboard table preferences.",
			},
			{
				Method:      http.MethodPost,
				Path:        "/plugins/" + pluginID + "/preferences",
				Description: "Persist dashboard table and time-range preferences.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/api-key-info",
				Description: "Read API key tracking state and labels for the dashboard filter.",
			},
			{
				Method:      http.MethodPut,
				Path:        "/plugins/" + pluginID + "/api-key-labels",
				Description: "Persist an API key label. Send JSON {ref, label} in the body.",
			},
			{
				Method:      http.MethodPost,
				Path:        "/plugins/" + pluginID + "/reset",
				Description: "Reset all persisted token usage statistics.",
			},
			{
				Method:      http.MethodGet,
				Path:        "/plugins/" + pluginID + "/backup",
				Description: "Download a full bbolt database backup of persisted token usage data.",
			},
			{
				Method:      http.MethodPost,
				Path:        "/plugins/" + pluginID + "/restore",
				Description: "Restore the persisted token usage database from a previous backup.",
			},
		},
		Resources: []pluginapi.ResourceRoute{
			{
				Path:        "/dashboard",
				Menu:        "Tokens Statistic",
				Description: "记录并分析各模型的 Token 消耗、费用与响应延迟。",
			},
		},
	}, nil
}

func (r *pluginRuntime) handleManagement(raw []byte) (pluginapi.ManagementResponse, error) {
	var request pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return pluginapi.ManagementResponse{}, withStatus(400, "decode management request: %v", err)
	}

	r.mu.RLock()
	routes := r.routes
	config := r.config
	r.mu.RUnlock()
	response, err := r.dispatchManagement(request, routes)
	if err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	return maybeCompressResponse(request, response, config), nil
}

func (r *pluginRuntime) dispatchManagement(request pluginapi.ManagementRequest, routes registeredRoutes) (pluginapi.ManagementResponse, error) {
	if routes.pluginID == "" {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "management routes are not registered"}), nil
	}

	switch request.Path {
	case routes.dashboardPath:
		if request.Method != "" && !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return dashboardResponse(), nil
	case routes.statsPath:
		if !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return r.statsResponse(request)
	case routes.statsInitialPath:
		if !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return r.initialStatsResponse(request)
	case routes.statsTrendPath:
		if !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return r.statsTrendResponse(request)
	case routes.statsGroupsPath:
		if !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return r.groupsStatsResponse(request)
	case routes.requestsPath:
		if !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return r.requestsResponse(request)
	case routes.costsPath:
		if !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return r.costsResponse(request)
	case routes.exchangeRatePath:
		if !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return r.exchangeRateResponse()
	case routes.pricesPath:
		if strings.EqualFold(request.Method, http.MethodGet) {
			return r.pricesResponse()
		}
		if strings.EqualFold(request.Method, http.MethodPut) {
			return r.savePricesResponse(request)
		}
		return methodNotAllowed(http.MethodGet + ", " + http.MethodPut), nil
	case routes.priceSyncPath:
		if !strings.EqualFold(request.Method, http.MethodPost) {
			return methodNotAllowed(http.MethodPost), nil
		}
		return r.syncPricesResponse(request)
	case routes.preferencesPath:
		if strings.EqualFold(request.Method, http.MethodGet) {
			return r.preferencesResponse(request)
		}
		if strings.EqualFold(request.Method, http.MethodPost) {
			return r.saveDashboardPreferencesResponse(request)
		}
		return methodNotAllowed(http.MethodGet + ", " + http.MethodPost), nil
	case routes.apiKeyInfoPath:
		if !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return r.apiKeyInfoResponse()
	case routes.apiKeyLabelsPath:
		if !strings.EqualFold(request.Method, http.MethodPut) && !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodPut), nil
		}
		return r.setAPIKeyLabelResponse(request)
	case routes.resetPath:
		if !strings.EqualFold(request.Method, http.MethodPost) {
			return methodNotAllowed(http.MethodPost), nil
		}
		return r.resetResponse(request)
	case routes.backupPath:
		if !strings.EqualFold(request.Method, http.MethodGet) {
			return methodNotAllowed(http.MethodGet), nil
		}
		return r.backupResponse()
	case routes.restorePath:
		if !strings.EqualFold(request.Method, http.MethodPost) {
			return methodNotAllowed(http.MethodPost), nil
		}
		return r.restoreResponse(request)
	default:
		return jsonResponse(http.StatusNotFound, map[string]any{"error": "route not found"}), nil
	}
}

func (r *pluginRuntime) statsResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	apiKeyIdentities, err := apiKeyIdentitiesFromRequest(request, r.store)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	queryRange, err := usageRangeFromQuery(request.Query.Get("range"), request.Query.Get("start"), request.Query.Get("end"), time.Now().UTC())
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	stats, err := r.store.queryStatsByFilter(queryRange, newUsageFilterFromIdentities(request.Query.Get("source"), apiKeyIdentities))
	if err != nil {
		status := errorHTTPStatus(err)
		return jsonResponse(status, map[string]any{"error": err.Error()}), nil
	}
	_, generations := r.store.APIKeyCryptoState()
	return r.sensitiveJSONResponse(http.StatusOK, &stats, r.crypto, generations), nil
}

func (r *pluginRuntime) statsFilter(request pluginapi.ManagementRequest) (usageRange, usageFilter, error) {
	queryRange, err := usageRangeFromQuery(request.Query.Get("range"), request.Query.Get("start"), request.Query.Get("end"), time.Now().UTC())
	if err != nil {
		return usageRange{}, usageFilter{}, err
	}
	if r.store == nil {
		return usageRange{}, usageFilter{}, withStatus(http.StatusServiceUnavailable, "storage is not initialized")
	}
	apiKeyIdentities, err := apiKeyIdentitiesFromRequest(request, r.store)
	if err != nil {
		return usageRange{}, usageFilter{}, err
	}
	return queryRange, newUsageFilterFromIdentities(request.Query.Get("source"), apiKeyIdentities), nil
}

func (r *pluginRuntime) initialStatsResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	queryRange, filter, err := r.statsFilter(request)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	stats, err := r.store.queryInitialStatsByFilter(queryRange, filter)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	_, generations := r.store.APIKeyCryptoState()
	return r.sensitiveJSONResponse(http.StatusOK, &stats, r.crypto, generations), nil
}

func (r *pluginRuntime) statsTrendResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	queryRange, filter, err := r.statsFilter(request)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	stats, err := r.store.queryStatsTrendByFilter(queryRange, filter)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	return jsonResponse(http.StatusOK, stats), nil
}

func (r *pluginRuntime) groupsStatsResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	queryRange, filter, err := r.statsFilter(request)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	filter.Model = normalizeDimension(request.Query.Get("model"))
	excludedModels := make(map[string]struct{}, len(request.Query["exclude_model"]))
	for _, model := range request.Query["exclude_model"] {
		if model = normalizeDimension(model); model != "" {
			excludedModels[model] = struct{}{}
		}
	}
	stats, err := r.store.queryGroupsByFilter(queryRange, filter)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	if len(excludedModels) > 0 {
		items := stats.Items[:0]
		for _, item := range stats.Items {
			excluded := false
			for model := range excludedModels {
				if modelFilterMatches(model, item.Model) {
					excluded = true
					break
				}
			}
			if !excluded {
				items = append(items, item)
			}
		}
		stats.Items = items
		stats.Total = len(items)
	}
	if err := sortGroupStats(stats.Items, request.Query.Get("sort"), request.Query.Get("direction"), r.groupSortValues(queryRange, filter, request.Query.Get("sort"))); err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	offset, err := parseNonNegativeQueryInt(request.Query.Get("offset"), 0, "offset")
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	limit, err := parseNonNegativeQueryInt(request.Query.Get("limit"), defaultRequestPageSize, "limit")
	if err != nil || limit < 1 || limit > maxDashboardPageSize {
		return jsonResponse(http.StatusBadRequest, map[string]any{"error": "limit must be an integer between 1 and 500"}), nil
	}
	if offset > stats.Total {
		offset = stats.Total
	}
	end := offset + limit
	if end > stats.Total {
		end = stats.Total
	}
	stats.Items = stats.Items[offset:end]
	_, generations := r.store.APIKeyCryptoState()
	return r.sensitiveJSONResponse(http.StatusOK, &stats, r.crypto, generations), nil
}

func sortGroupStats(items []GroupStats, sortKey, direction string, aux map[string]groupSortAux) error {
	if sortKey == "" {
		sortKey = "total_tokens"
	}
	if direction == "" {
		direction = "desc"
	}
	if direction != "asc" && direction != "desc" {
		return withStatus(http.StatusBadRequest, "direction must be asc or desc")
	}
	numeric := map[string]bool{"requests": true, "failed_requests": true, "input_tokens": true, "output_tokens": true, "reasoning_tokens": true, "cache_read_tokens": true, "cache_creation_tokens": true, "total_tokens": true, "average_latency_ns": true, "average_ttft_ns": true, "estimated_cost": true}
	text := map[string]bool{"model": true, "provider": true, "api_key": true, "alias": true, "source": true, "executor_type": true, "auth_type": true, "service_tier": true, "reasoning_effort": true, "price_source": true}
	if !numeric[sortKey] && !text[sortKey] {
		return withStatus(http.StatusBadRequest, "unsupported group sort %q", sortKey)
	}
	value := func(item GroupStats) string {
		switch sortKey {
		case "model":
			return compactModelName(item.Model)
		case "provider":
			return item.Provider
		case "api_key":
			return item.APIKeyHash
		case "alias":
			return item.Alias
		case "source":
			return item.Source
		case "executor_type":
			return item.ExecutorType
		case "auth_type":
			return item.AuthType
		case "service_tier":
			return item.ServiceTier
		case "reasoning_effort":
			return item.ReasoningEffort
		case "price_source":
			return aux[item.Model].priceSource
		default:
			return ""
		}
	}
	number := func(item GroupStats) uint64 {
		switch sortKey {
		case "requests":
			return item.Requests
		case "failed_requests":
			return item.FailedRequests
		case "input_tokens":
			return item.InputTokens
		case "output_tokens":
			return item.OutputTokens
		case "reasoning_tokens":
			return item.ReasoningTokens
		case "cache_read_tokens":
			return item.CacheReadTokens
		case "cache_creation_tokens":
			return item.CacheCreationTokens
		case "total_tokens":
			return item.TotalTokens
		case "average_latency_ns":
			return item.AverageLatencyNS
		case "average_ttft_ns":
			return item.AverageTTFTNS
		default:
			return 0
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		var less bool
		if sortKey == "estimated_cost" {
			left, right := aux[items[i].Model], aux[items[j].Model]
			if left.hasCost != right.hasCost {
				return left.hasCost
			}
			if !left.hasCost {
				return compareDimensions(items[i].Dimensions, items[j].Dimensions) < 0
			}
			less = left.totalUSD < right.totalUSD
			if left.totalUSD == right.totalUSD {
				return compareDimensions(items[i].Dimensions, items[j].Dimensions) < 0
			}
		} else {
			if numeric[sortKey] {
				less = number(items[i]) < number(items[j])
			} else {
				less = value(items[i]) < value(items[j])
			}
			if numeric[sortKey] && number(items[i]) == number(items[j]) || !numeric[sortKey] && value(items[i]) == value(items[j]) {
				return compareDimensions(items[i].Dimensions, items[j].Dimensions) < 0
			}
		}
		if direction == "desc" {
			return !less
		}
		return less
	})
	return nil
}

// groupSortAux carries the price-book-derived sort values that do not live in
// GroupStats: the aggregated cost for the requested range and the current
// price-book source per model.
type groupSortAux struct {
	hasCost     bool
	totalUSD    float64
	priceSource string
}

// groupSortValues builds the auxiliary sort values for the requested sort key.
// Failures degrade to an empty map so sorting falls back to the
// compareDimensions tiebreak instead of failing the whole table.
func (r *pluginRuntime) groupSortValues(queryRange usageRange, filter usageFilter, sortKey string) map[string]groupSortAux {
	aux := make(map[string]groupSortAux)
	if r.store == nil {
		return aux
	}
	if sortKey == "estimated_cost" {
		costs, err := r.store.queryCostsByFilter(queryRange, filter)
		if err != nil {
			return aux
		}
		for _, item := range costs.Models {
			aux[item.Model] = groupSortAux{hasCost: item.PricedRequests > 0, totalUSD: item.TotalUSD}
		}
		return aux
	}
	if sortKey == "price_source" {
		book, err := r.store.QueryPriceBook()
		if err != nil {
			return aux
		}
		for model, entry := range book.Prices {
			aux[model] = groupSortAux{priceSource: entry.Source}
		}
	}
	return aux
}

func (r *pluginRuntime) requestsResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	queryRange, err := usageRangeFromQuery(request.Query.Get("range"), request.Query.Get("start"), request.Query.Get("end"), time.Now().UTC())
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	offset, err := parseNonNegativeQueryInt(request.Query.Get("offset"), 0, "offset")
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	limit, err := parseNonNegativeQueryInt(request.Query.Get("limit"), defaultRequestPageSize, "limit")
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	apiKeyIdentities, err := apiKeyIdentitiesFromRequest(request, r.store)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	page, err := r.store.queryRequestPageByFilter(queryRange, offset, limit, request.Query.Get("model"), newUsageFilterFromIdentities(request.Query.Get("source"), apiKeyIdentities), request.Query.Get("result"))
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	_, generations := r.store.APIKeyCryptoState()
	return r.sensitiveJSONResponse(http.StatusOK, &page, r.crypto, generations), nil
}

func (r *pluginRuntime) costsResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	queryRange, err := usageRangeFromQuery(request.Query.Get("range"), request.Query.Get("start"), request.Query.Get("end"), time.Now().UTC())
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	apiKeyIdentities, err := apiKeyIdentitiesFromRequest(request, r.store)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	costs, err := r.store.queryCostsByFilter(queryRange, newUsageFilterFromIdentities(request.Query.Get("source"), apiKeyIdentities))
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	_, generations := r.store.APIKeyCryptoState()
	return r.sensitiveJSONResponse(http.StatusOK, &costs, r.crypto, generations), nil
}

func apiKeyIdentitiesFromRequest(request pluginapi.ManagementRequest, store *Store) ([]string, error) {
	refs := nonEmptyQueryValues(request.Query["api_key_ref"])
	hashes := nonEmptyQueryValues(request.Query["api_key_hash"])
	if len(refs) == 0 && len(hashes) == 0 {
		return nil, nil
	}
	if len(refs) > 0 && len(hashes) > 0 {
		return nil, withStatus(http.StatusBadRequest, "api_key_ref and api_key_hash cannot be used together")
	}
	if len(refs) > 0 {
		seen := make(map[string]struct{}, len(refs))
		identities := make([]string, 0, len(refs))
		for _, ref := range refs {
			if _, _, ok := parseAPIKeyRef(ref); !ok {
				return nil, withStatus(http.StatusBadRequest, "api_key_ref is invalid")
			}
			if _, exists := seen[ref]; exists {
				continue
			}
			seen[ref] = struct{}{}
			identities = append(identities, ref)
		}
		return identities, nil
	}
	if len(hashes) > 1 {
		return nil, withStatus(http.StatusBadRequest, "api_key_hash cannot be repeated; use api_key_ref")
	}
	hash := hashes[0]
	if !validAPIKeyHash(hash) {
		return nil, withStatus(http.StatusBadRequest, "api_key_hash must be 32 lowercase hexadecimal characters")
	}
	if store == nil {
		return nil, withStatus(http.StatusServiceUnavailable, "storage is not initialized")
	}
	resolved, err := store.ResolveAPIKeyHash(hash)
	if err != nil {
		return nil, err
	}
	return []string{resolved}, nil
}

func nonEmptyQueryValues(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func (r *pluginRuntime) exchangeRateResponse() (pluginapi.ManagementResponse, error) {
	r.mu.Lock()
	service := r.exchangeRates
	if service == nil {
		service = newExchangeRateService()
		r.exchangeRates = service
	}
	r.mu.Unlock()
	rate, err := service.latest()
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	return jsonResponse(http.StatusOK, rate), nil
}

func (r *pluginRuntime) pricesResponse() (pluginapi.ManagementResponse, error) {
	r.mu.RLock()
	store := r.store
	r.mu.RUnlock()
	if store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	priceBook, err := store.QueryPriceBook()
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	return jsonResponse(http.StatusOK, priceBook), nil
}

func (r *pluginRuntime) preferencesResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	r.mu.RLock()
	store := r.store
	r.mu.RUnlock()
	if store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	preferences, err := store.QueryDashboardPreferences()
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	return jsonResponse(http.StatusOK, preferences), nil
}

func (r *pluginRuntime) saveDashboardPreferencesResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	contentType, _, err := mime.ParseMediaType(request.Headers.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "application/json") {
		return jsonResponse(http.StatusUnsupportedMediaType, map[string]any{"error": "Content-Type must be application/json"}), nil
	}
	if len(request.Body) > 2<<20 {
		return jsonResponse(http.StatusRequestEntityTooLarge, map[string]any{"error": "dashboard preferences JSON is too large"}), nil
	}
	preferences, err := dashboardPreferencesFromBody(request.Body)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	return r.persistDashboardPreferences(preferences)
}

func (r *pluginRuntime) persistDashboardPreferences(preferences DashboardPreferences) (pluginapi.ManagementResponse, error) {
	r.mu.RLock()
	store := r.store
	r.mu.RUnlock()
	if store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	preferences, err := store.SaveDashboardPreferences(preferences)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	return jsonResponse(http.StatusOK, preferences), nil
}

func dashboardPreferencesFromBody(raw []byte) (DashboardPreferences, error) {
	var preferences DashboardPreferences
	if err := decodeStrictJSON(raw, &preferences); err != nil {
		return DashboardPreferences{}, withStatus(http.StatusBadRequest, "invalid dashboard preferences JSON: %v", err)
	}
	normalized, err := normalizeDashboardPreferences(preferences)
	if err != nil {
		return DashboardPreferences{}, withStatus(http.StatusBadRequest, "%v", err)
	}
	return normalized, nil
}

func (r *pluginRuntime) savePricesResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	contentType, _, err := mime.ParseMediaType(request.Headers.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "application/json") {
		return jsonResponse(http.StatusUnsupportedMediaType, map[string]any{"error": "Content-Type must be application/json"}), nil
	}
	if len(request.Body) > 2<<20 {
		return jsonResponse(http.StatusRequestEntityTooLarge, map[string]any{"error": "model prices JSON is too large"}), nil
	}
	var input struct {
		Prices       map[string]ModelPrice `json:"prices"`
		SyncSettings *PriceSyncSettings    `json:"sync_settings,omitempty"`
	}
	if err := decodeStrictJSON(request.Body, &input); err != nil || input.Prices == nil {
		return jsonResponse(http.StatusBadRequest, map[string]any{"error": "invalid model prices JSON"}), nil
	}
	r.mu.RLock()
	store := r.store
	r.mu.RUnlock()
	if store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	priceBook, err := store.SavePriceBook(input.Prices, input.SyncSettings)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	return jsonResponse(http.StatusOK, priceBook), nil
}

func (r *pluginRuntime) syncPricesResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	contentType, _, err := mime.ParseMediaType(request.Headers.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "application/json") {
		return jsonResponse(http.StatusUnsupportedMediaType, map[string]any{"error": "Content-Type must be application/json"}), nil
	}
	if len(request.Body) > 2<<20 {
		return jsonResponse(http.StatusRequestEntityTooLarge, map[string]any{"error": "model price synchronization JSON is too large"}), nil
	}
	var input struct {
		Source       string             `json:"source"`
		Models       []string           `json:"models"`
		SyncSettings *PriceSyncSettings `json:"sync_settings,omitempty"`
	}
	if err := decodeStrictJSON(request.Body, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]any{"error": "invalid model price synchronization JSON"}), nil
	}
	if input.Source != "" && input.Source != priceSourceModelsDev {
		return jsonResponse(http.StatusBadRequest, map[string]any{"error": `source must be "models.dev"`}), nil
	}
	priceBook, err := r.syncModelsDev(input.SyncSettings, input.Models)
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	return jsonResponse(http.StatusOK, priceBook), nil
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func parseNonNegativeQueryInt(raw string, fallback int, name string) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, withStatus(http.StatusBadRequest, "%s must be a non-negative integer", name)
	}
	return value, nil
}

func (r *pluginRuntime) backupResponse() (pluginapi.ManagementResponse, error) {
	r.mu.RLock()
	store := r.store
	r.mu.RUnlock()
	if store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	data, err := store.Backup()
	if err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	filename := "tokens-statistic-" + nowUTC().UTC().Format("20060102-150405") + ".db"
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers: http.Header{
			"Content-Type":           []string{"application/octet-stream"},
			"Content-Disposition":    []string{`attachment; filename="` + filename + `"`},
			"Cache-Control":          []string{"no-store"},
			"X-Content-Type-Options": []string{"nosniff"},
		},
		Body: data,
	}, nil
}

func (r *pluginRuntime) restoreResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	contentType := strings.TrimSpace(request.Headers.Get("Content-Type"))
	if contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/octet-stream") {
			return jsonResponse(http.StatusUnsupportedMediaType, map[string]any{"error": "Content-Type must be application/octet-stream"}), nil
		}
	}
	if request.Headers.Get("X-Confirm-Restore") != "replace" {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "missing X-Confirm-Restore: replace header"}), nil
	}
	if len(request.Body) == 0 {
		return jsonResponse(http.StatusBadRequest, map[string]any{"error": "backup body must not be empty"}), nil
	}
	if len(request.Body) > maxDatabaseBackupBytes {
		return jsonResponse(http.StatusRequestEntityTooLarge, map[string]any{"error": "backup body is too large"}), nil
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	store := r.store
	if store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	if err := store.RestoreBackup(request.Body); err != nil {
		return jsonResponse(errorHTTPStatus(err), map[string]any{"error": err.Error()}), nil
	}
	generation, generations := store.APIKeyCryptoState()
	if r.store == store {
		r.apiKeyGeneration = generation
		r.apiKeyGenerations = generations
	}
	return jsonResponse(http.StatusOK, map[string]any{
		"restored":    true,
		"restored_at": nowUTC(),
	}), nil
}

func (r *pluginRuntime) resetResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	contentType, _, err := mime.ParseMediaType(request.Headers.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "application/json") {
		return jsonResponse(http.StatusUnsupportedMediaType, map[string]any{"error": "Content-Type must be application/json"}), nil
	}
	var confirmation struct {
		Confirm string `json:"confirm"`
	}
	if err := json.Unmarshal(request.Body, &confirmation); err != nil || confirmation.Confirm != "reset" {
		return jsonResponse(http.StatusBadRequest, map[string]any{"error": `body must be {"confirm":"reset"}`}), nil
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	store := r.store
	if store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "storage is not initialized"}), nil
	}
	if err := store.Reset(); err != nil {
		return jsonResponse(http.StatusInternalServerError, map[string]any{"error": err.Error()}), nil
	}
	generation, generations := store.APIKeyCryptoState()
	if r.store == store {
		r.apiKeyGeneration = generation
		r.apiKeyGenerations = generations
	}
	return jsonResponse(http.StatusOK, map[string]any{
		"reset":    true,
		"reset_at": nowUTC(),
	}), nil
}

func pluginIDFromResourceBase(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	const prefix = "/v0/resource/plugins/"
	if !strings.HasPrefix(base, prefix) {
		return "", withStatus(400, "invalid resource base path %q", base)
	}
	pluginID := strings.TrimPrefix(base, prefix)
	if strings.Contains(pluginID, "/") || !pluginIDPattern.MatchString(pluginID) {
		return "", withStatus(400, "invalid plugin ID in resource base path")
	}
	return pluginID, nil
}

func methodNotAllowed(allowed string) pluginapi.ManagementResponse {
	response := jsonResponse(http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	response.Headers.Set("Allow", allowed)
	return response
}

func jsonResponse(status int, value any) pluginapi.ManagementResponse {
	body, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers: http.Header{
			"Content-Type":           []string{"application/json; charset=utf-8"},
			"Cache-Control":          []string{"no-store"},
			"X-Content-Type-Options": []string{"nosniff"},
		},
		Body: body,
	}
}
