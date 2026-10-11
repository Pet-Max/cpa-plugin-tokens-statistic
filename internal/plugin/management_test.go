package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/store"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestPluginIDFromResourceBase(t *testing.T) {
	got, err := pluginIDFromResourceBase("/v0/resource/plugins/tokens-statistic")
	if err != nil || got != "tokens-statistic" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, invalid := range []string{"/wrong/id", "/v0/resource/plugins/a/b", "/v0/resource/plugins/bad id"} {
		if _, err := pluginIDFromResourceBase(invalid); err == nil {
			t.Fatalf("accepted invalid base %q", invalid)
		}
	}
}

func TestManagementRegistrationUsesDynamicPluginID(t *testing.T) {
	runtime := &pluginRuntime{}
	raw, _ := json.Marshal(pluginapi.ManagementRegistrationRequest{ResourceBasePath: "/v0/resource/plugins/custom-id"})
	registration, err := runtime.registerManagement(raw)
	if err != nil {
		t.Fatal(err)
	}
	type routeKey struct{ method, path string }
	want := []routeKey{
		{http.MethodGet, "/plugins/custom-id/stats"},
		{http.MethodGet, "/plugins/custom-id/stats/initial"},
		{http.MethodGet, "/plugins/custom-id/stats/trends"},
		{http.MethodGet, "/plugins/custom-id/stats/groups"},
		{http.MethodGet, "/plugins/custom-id/requests"},
		{http.MethodGet, "/plugins/custom-id/costs"},
		{http.MethodGet, "/plugins/custom-id/exchange-rate"},
		{http.MethodGet, "/plugins/custom-id/prices"},
		{http.MethodPut, "/plugins/custom-id/prices"},
		{http.MethodPost, "/plugins/custom-id/prices/sync"},
		{http.MethodGet, "/plugins/custom-id/preferences"},
		{http.MethodPost, "/plugins/custom-id/preferences"},
		{http.MethodGet, "/plugins/custom-id/api-key-info"},
		{http.MethodPut, "/plugins/custom-id/api-key-labels"},
		{http.MethodPost, "/plugins/custom-id/reset"},
		{http.MethodGet, "/plugins/custom-id/backup"},
		{http.MethodPost, "/plugins/custom-id/restore"},
	}
	if len(registration.Routes) != len(want) {
		t.Fatalf("route count = %d, want %d: %+v", len(registration.Routes), len(want), registration.Routes)
	}
	for index, route := range registration.Routes {
		if route.Method != want[index].method || route.Path != want[index].path {
			t.Fatalf("route %d = %s %s, want %s %s", index, route.Method, route.Path, want[index].method, want[index].path)
		}
		if route.Menu != "" {
			t.Fatalf("management route %s %s must not declare a legacy menu", route.Method, route.Path)
		}
	}
	if len(registration.Resources) != 1 {
		t.Fatalf("resources must only contain the static dashboard page: %+v", registration.Resources)
	}
	dashboard := registration.Resources[0]
	if dashboard.Path != "/dashboard" || dashboard.Menu != "Tokens Statistic" {
		t.Fatalf("unexpected dashboard resource: %+v", dashboard)
	}
}

func TestCompactStatsResourcesShapePagingAndMethods(t *testing.T) {
	config := testConfig(t)
	config.SyncOnRecord = true
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config}
	defer runtime.shutdown()
	registration, err := json.Marshal(pluginapi.ManagementRegistrationRequest{ResourceBasePath: "/v0/resource/plugins/test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.registerManagement(registration); err != nil {
		t.Fatal(err)
	}
	now := nowUTC().Truncate(5 * time.Minute)
	for _, usage := range []store.Usage{
		{Dimensions: store.Dimensions{Provider: "openai", Model: "alpha", Source: "cli"}, RequestedAt: now.Add(-4 * time.Minute), Counters: store.Counters{Requests: 2, TotalTokens: 20}},
		{Dimensions: store.Dimensions{Provider: "anthropic", Model: "beta", Source: "cli"}, RequestedAt: now.Add(-3 * time.Minute), Counters: store.Counters{Requests: 1, TotalTokens: 10}},
		{Dimensions: store.Dimensions{Provider: "openai", Model: "alpha", Source: "web"}, RequestedAt: now.Add(-2 * time.Minute), Counters: store.Counters{Requests: 3, TotalTokens: 30}},
	} {
		if err := st.Record(usage); err != nil {
			t.Fatal(err)
		}
	}
	call := func(method, path string, query url.Values) pluginapi.ManagementResponse {
		t.Helper()
		raw, err := json.Marshal(pluginapi.ManagementRequest{Method: method, Path: path, Query: query})
		if err != nil {
			t.Fatal(err)
		}
		response, err := runtime.handleManagement(raw)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	query := url.Values{"range": {"24h"}}
	initialResponse := call(http.MethodGet, runtime.routes.statsInitialPath, query)
	if initialResponse.StatusCode != http.StatusOK || strings.Contains(string(initialResponse.Body), `"groups"`) || strings.Contains(string(initialResponse.Body), `"model_series"`) {
		t.Fatalf("initial response = %+v", initialResponse)
	}
	var initial store.InitialStatsResponse
	if err := json.Unmarshal(initialResponse.Body, &initial); err != nil || initial.SchemaVersion != 2 || initial.BucketSeconds != 300 || initial.Summary.TotalTokens != 60 || len(initial.Models) != 2 || len(initial.Series) != 1 {
		t.Fatalf("initial payload = %+v, %v", initial, err)
	}
	trendResponse := call(http.MethodGet, runtime.routes.statsTrendPath, query)
	var trend store.StatsTrendResponse
	if trendResponse.StatusCode != http.StatusOK || json.Unmarshal(trendResponse.Body, &trend) != nil || trend.BucketSeconds != 300 || len(trend.ModelSeries) != 2 {
		t.Fatalf("trend response = %+v, payload=%+v", trendResponse, trend)
	}
	groupQuery := url.Values{"range": {"24h"}, "offset": {"0"}, "limit": {"1"}, "sort": {"model"}, "direction": {"asc"}}
	groupsResponse := call(http.MethodGet, runtime.routes.statsGroupsPath, groupQuery)
	var groups store.GroupStatsPage
	if groupsResponse.StatusCode != http.StatusOK || json.Unmarshal(groupsResponse.Body, &groups) != nil || groups.Total != 3 || len(groups.Items) != 1 || groups.Items[0].Model != "alpha" {
		t.Fatalf("groups response = %+v, payload=%+v", groupsResponse, groups)
	}
	groupQuery.Set("model", "alpha")
	groupQuery.Set("exclude_model", "alpha")
	groupsResponse = call(http.MethodGet, runtime.routes.statsGroupsPath, groupQuery)
	if groupsResponse.StatusCode != http.StatusOK || json.Unmarshal(groupsResponse.Body, &groups) != nil || groups.Total != 0 || len(groups.Items) != 0 {
		t.Fatalf("filtered groups response = %+v, payload=%+v", groupsResponse, groups)
	}
	for _, path := range []string{runtime.routes.statsInitialPath, runtime.routes.statsTrendPath, runtime.routes.statsGroupsPath} {
		response := call(http.MethodPost, path, nil)
		if response.StatusCode != http.StatusMethodNotAllowed || response.Headers.Get("Allow") != http.MethodGet {
			t.Fatalf("method restriction for %s = %+v", path, response)
		}
	}
}

func TestManagementStatsAndReset(t *testing.T) {
	config := testConfig(t)
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config, routes: registeredRoutes{
		pluginID:         "test",
		statsPath:        "/v0/management/plugins/test/stats",
		resetPath:        "/v0/management/plugins/test/reset",
		dashboardPath:    "/v0/resource/plugins/test/dashboard",
		requestsPath:     "/v0/management/plugins/test/requests",
		costsPath:        "/v0/management/plugins/test/costs",
		exchangeRatePath: "/v0/management/plugins/test/exchange-rate",
		pricesPath:       "/v0/management/plugins/test/prices",
		priceSyncPath:    "/v0/management/plugins/test/prices/sync",
		preferencesPath:  "/v0/management/plugins/test/preferences",
	}}
	defer runtime.shutdown()
	if err := st.Record(store.Usage{Dimensions: store.Dimensions{Model: "m"}, RequestedAt: nowUTC(), Counters: store.Counters{Requests: 1, TotalTokens: 3}}); err != nil {
		t.Fatal(err)
	}

	statsRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.statsPath, Query: url.Values{"range": []string{"24h"}}})
	response, err := runtime.handleManagement(statsRequest)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("stats response: %+v, %v", response, err)
	}
	if strings.Contains(string(response.Body), `"ok"`) {
		t.Fatal("HTTP response body was incorrectly wrapped in RPC envelope")
	}
	if response.Headers.Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store header")
	}

	resourceStatsRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.statsPath, Query: url.Values{"range": []string{"24h"}}})
	response, err = runtime.handleManagement(resourceStatsRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"total_tokens":3`) {
		t.Fatalf("resource stats response: %+v, %v", response, err)
	}
	customQuery := url.Values{
		"range": []string{"custom"},
		"start": []string{time.Now().Add(-time.Hour).Format(time.RFC3339)},
		"end":   []string{time.Now().Add(time.Hour).Format(time.RFC3339)},
	}
	customStatsRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.statsPath, Query: customQuery})
	response, err = runtime.handleManagement(customStatsRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"range":"custom"`) || !strings.Contains(string(response.Body), `"total_tokens":3`) {
		t.Fatalf("custom stats response: %+v, %v", response, err)
	}
	invalidRangeRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.statsPath, Query: url.Values{"range": []string{"custom"}, "start": []string{time.Now().Format(time.RFC3339)}}})
	response, err = runtime.handleManagement(invalidRangeRequest)
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid custom range response: %+v, %v", response, err)
	}

	requestsRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.requestsPath, Query: url.Values{"range": []string{"24h"}, "offset": []string{"0"}, "limit": []string{"20"}, "model": []string{"m"}}})
	response, err = runtime.handleManagement(requestsRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"total":1`) || !strings.Contains(string(response.Body), `"model":"m"`) {
		t.Fatalf("resource requests response: %+v, %v", response, err)
	}
	requestsResultQuery := url.Values{"range": []string{"24h"}, "result": []string{"failed"}}
	requestsResultRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.requestsPath, Query: requestsResultQuery})
	response, err = runtime.handleManagement(requestsResultRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"total":0`) {
		t.Fatalf("filtered resource requests response: %+v, %v", response, err)
	}
	requestsResultQuery.Set("result", "unknown")
	requestsResultRequest, _ = json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.requestsPath, Query: requestsResultQuery})
	response, err = runtime.handleManagement(requestsResultRequest)
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid request result response: %+v, %v", response, err)
	}
	customRequestsQuery := url.Values{}
	for key, values := range customQuery {
		customRequestsQuery[key] = append([]string(nil), values...)
	}
	customRequestsQuery.Set("offset", "0")
	customRequestsQuery.Set("limit", "20")
	customRequestsQuery.Set("model", "m")
	customRequestsRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.requestsPath, Query: customRequestsQuery})
	response, err = runtime.handleManagement(customRequestsRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"range":"custom"`) || !strings.Contains(string(response.Body), `"total":1`) {
		t.Fatalf("custom requests response: %+v, %v", response, err)
	}

	pricesRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.pricesPath})
	response, err = runtime.handleManagement(pricesRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"prices":{}`) {
		t.Fatalf("empty prices response: %+v, %v", response, err)
	}
	preferencesRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.preferencesPath})
	response, err = runtime.handleManagement(preferencesRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"request_page_size":100`) || !strings.Contains(string(response.Body), `"hidden_request_columns":[]`) || !strings.Contains(string(response.Body), `"time_range_mode":"custom"`) {
		t.Fatalf("default preferences response: %+v, %v", response, err)
	}
	savePreferencesRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: runtime.routes.preferencesPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"request_page_size":50,"dimension_page_size":200,"hidden_request_columns":["source"],"hidden_dimension_columns":["provider"],"time_range_mode":"last_7_days"}`)})
	response, err = runtime.handleManagement(savePreferencesRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"request_page_size":50`) || !strings.Contains(string(response.Body), `"hidden_dimension_columns":["provider"]`) {
		t.Fatalf("save preferences response: %+v, %v", response, err)
	}
	response, err = runtime.handleManagement(preferencesRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"dimension_page_size":200`) || !strings.Contains(string(response.Body), `"hidden_request_columns":["source"]`) || !strings.Contains(string(response.Body), `"time_range_mode":"last_7_days"`) {
		t.Fatalf("persisted preferences response: %+v, %v", response, err)
	}

	savePricesRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPut, Path: runtime.routes.pricesPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"prices":{"m":{"input":2.5,"output":10}}}`)})
	response, err = runtime.handleManagement(savePricesRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"input":2.5`) || !strings.Contains(string(response.Body), `"saved_at":"`) {
		t.Fatalf("save prices response: %+v, %v", response, err)
	}
	response, err = runtime.handleManagement(pricesRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"output":10`) || !strings.Contains(string(response.Body), `"saved_at":"`) {
		t.Fatalf("persisted prices response: %+v, %v", response, err)
	}
	costsRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.costsPath, Query: url.Values{"range": []string{"24h"}}})
	response, err = runtime.handleManagement(costsRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"priced_requests":1`) || !strings.Contains(string(response.Body), `"estimate_basis":"current_price_book"`) {
		t.Fatalf("resource costs response: %+v, %v", response, err)
	}
	customCostsRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.costsPath, Query: customQuery})
	response, err = runtime.handleManagement(customCostsRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"range":"custom"`) || !strings.Contains(string(response.Body), `"priced_requests":1`) {
		t.Fatalf("custom costs response: %+v, %v", response, err)
	}
	catalogServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"openai":{"models":{"m":{"cost":{"input":1,"output":2,"cache_read":0.1,"cache_write":1}}}}}`))
	}))
	defer catalogServer.Close()
	runtime.modelsDevFetcher = &modelsDevFetcher{client: catalogServer.Client(), url: catalogServer.URL}
	syncRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: runtime.routes.priceSyncPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"source":"models.dev","models":["m"]}`)})
	response, err = runtime.handleManagement(syncRequest)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"skipped_manual":1`) || !strings.Contains(string(response.Body), `"saved_at":"`) {
		t.Fatalf("price sync response: %+v, %v", response, err)
	}
	invalidPricesRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPut, Path: runtime.routes.pricesPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"prices":{"m":{"input":-1,"output":10}}}`)})
	response, _ = runtime.handleManagement(invalidPricesRequest)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid prices status = %d body=%s", response.StatusCode, response.Body)
	}
	for _, body := range []string{
		`{"prices":{},"unknown":true}`,
		`{"prices":{"m":{"input":1,"unknown":true}}}`,
		`{"prices":{}} {"prices":{}}`,
		``,
	} {
		requestRaw, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPut, Path: runtime.routes.pricesPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(body)})
		response, _ = runtime.handleManagement(requestRaw)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("strict prices body %q status = %d body=%s", body, response.StatusCode, response.Body)
		}
	}
	unknownSyncRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: runtime.routes.priceSyncPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"source":"models.dev","url":"https://example.com"}`)})
	response, _ = runtime.handleManagement(unknownSyncRequest)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown sync field status = %d body=%s", response.StatusCode, response.Body)
	}

	emptyModelsSyncRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: runtime.routes.priceSyncPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"source":"models.dev","models":[]}`)})
	response, _ = runtime.handleManagement(emptyModelsSyncRequest)
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(response.Body), "at least one CLIProxyAPI model") {
		t.Fatalf("empty sync models status = %d body=%s", response.StatusCode, response.Body)
	}

	badRequestsRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.requestsPath, Query: url.Values{"offset": []string{"bad"}}})
	response, _ = runtime.handleManagement(badRequestsRequest)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad requests query status = %d", response.StatusCode)
	}

	looseContentType, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: runtime.routes.resetPath, Headers: http.Header{"Content-Type": []string{"application/jsonp"}}, Body: []byte(`{"confirm":"reset"}`)})
	response, _ = runtime.handleManagement(looseContentType)
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("loose content type status = %d", response.StatusCode)
	}

	badReset, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: runtime.routes.resetPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"confirm":"no"}`)})
	response, _ = runtime.handleManagement(badReset)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad reset status = %d", response.StatusCode)
	}
	goodReset, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: runtime.routes.resetPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"confirm":"reset"}`)})
	response, _ = runtime.handleManagement(goodReset)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("good reset status = %d body=%s", response.StatusCode, response.Body)
	}
}

func TestSyncModelsDevUsesProvidedCLIModels(t *testing.T) {
	config := testConfig(t)
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config}
	defer runtime.shutdown()
	if err := st.Record(store.Usage{Dimensions: store.Dimensions{Model: "usage-only"}, RequestedAt: time.Now().UTC(), Counters: store.Counters{Requests: 1}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"openai":{"models":{"cli-only":{"cost":{"input":1,"output":2}},"usage-only":{"cost":{"input":9,"output":9}}}}}`))
	}))
	defer server.Close()
	runtime.modelsDevFetcher = &modelsDevFetcher{client: server.Client(), url: server.URL}
	response, err := runtime.syncModelsDev(nil, []string{"cli-only"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := response.Prices["cli-only"]; !ok {
		t.Fatalf("CLIProxyAPI model was not synchronized: %+v", response.Prices)
	}
	if _, ok := response.Prices["usage-only"]; ok {
		t.Fatalf("usage-only model was synchronized: %+v", response.Prices)
	}
}

func TestDashboardPreferencesManagementValidation(t *testing.T) {
	config := testConfig(t)
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config, routes: registeredRoutes{
		pluginID:        "test",
		preferencesPath: "/v0/management/plugins/test/preferences",
	}}
	defer runtime.shutdown()

	request := func(method string, headers http.Header, body []byte) pluginapi.ManagementResponse {
		raw, _ := json.Marshal(pluginapi.ManagementRequest{Method: method, Path: runtime.routes.preferencesPath, Headers: headers, Body: body})
		response, handleErr := runtime.handleManagement(raw)
		if handleErr != nil {
			t.Fatal(handleErr)
		}
		return response
	}
	jsonHeaders := http.Header{"Content-Type": []string{"application/json"}}
	if response := request(http.MethodPost, http.Header{"Content-Type": []string{"text/plain"}}, []byte(`{}`)); response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type status = %d body=%s", response.StatusCode, response.Body)
	}
	invalidBodies := map[string]string{
		"malformed json":     `{`,
		"unknown field":      `{"request_page_size":100,"dimension_page_size":100,"unknown":"value"}`,
		"invalid page size":  `{"request_page_size":0,"dimension_page_size":100}`,
		"invalid column":     `{"request_page_size":100,"dimension_page_size":100,"hidden_request_columns":["api_key"]}`,
		"invalid range mode": `{"request_page_size":100,"dimension_page_size":100,"time_range_mode":"yesterday"}`,
		"invalid display":    `{"request_page_size":100,"dimension_page_size":100,"token_display_mode":"billion"}`,
		"incomplete range":   `{"request_page_size":100,"dimension_page_size":100,"time_range_mode":"custom","time_range_start":"2026-08-05"}`,
	}
	for name, body := range invalidBodies {
		if response := request(http.MethodPost, jsonHeaders, []byte(body)); response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s", name, response.StatusCode, response.Body)
		}
	}
	valid := []byte(`{"request_page_size":50,"dimension_page_size":200,"hidden_request_columns":["source"],"hidden_dimension_columns":["provider"],"time_range_mode":"last_7_days"}`)
	if response := request(http.MethodPost, jsonHeaders, valid); response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"request_page_size":50`) {
		t.Fatalf("valid save status = %d body=%s", response.StatusCode, response.Body)
	}
	if response := request(http.MethodGet, nil, nil); response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"hidden_request_columns":["source"]`) {
		t.Fatalf("read back status = %d body=%s", response.StatusCode, response.Body)
	}
	if response := request(http.MethodPut, jsonHeaders, valid); response.StatusCode != http.StatusMethodNotAllowed || response.Headers.Get("Allow") != http.MethodGet+", "+http.MethodPost {
		t.Fatalf("wrong method response = %+v", response)
	}
}

func TestConcurrentPriceSyncReturnsConflict(t *testing.T) {
	config := testConfig(t)
	config.SyncOnRecord = true
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config, routes: registeredRoutes{
		pluginID:      "test",
		priceSyncPath: "/v0/management/plugins/test/prices/sync",
	}}
	defer runtime.shutdown()
	if err := st.Record(store.Usage{Dimensions: store.Dimensions{Model: "m"}, RequestedAt: time.Now().UTC(), Counters: store.Counters{Requests: 1}}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		once.Do(func() { close(started) })
		<-release
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"openai":{"models":{"m":{"cost":{"input":1,"output":2}}}}}`))
	}))
	defer server.Close()
	runtime.modelsDevFetcher = &modelsDevFetcher{client: server.Client(), url: server.URL}
	raw, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: runtime.routes.priceSyncPath, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"source":"models.dev","models":["m"]}`)})
	firstDone := make(chan pluginapi.ManagementResponse, 1)
	go func() {
		response, _ := runtime.handleManagement(raw)
		firstDone <- response
	}()
	<-started
	response, _ := runtime.handleManagement(raw)
	if response.StatusCode != http.StatusConflict {
		close(release)
		t.Fatalf("concurrent sync status = %d body=%s", response.StatusCode, response.Body)
	}
	close(release)
	if response = <-firstDone; response.StatusCode != http.StatusOK {
		t.Fatalf("first sync status = %d body=%s", response.StatusCode, response.Body)
	}
	response, _ = runtime.handleManagement(raw)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("later sync status = %d body=%s", response.StatusCode, response.Body)
	}
}

func TestStalePriceSyncDoesNotOverwriteNewSettings(t *testing.T) {
	config := testConfig(t)
	config.SyncOnRecord = true
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config}
	defer runtime.shutdown()
	if err := st.Record(store.Usage{Dimensions: store.Dimensions{Model: "m"}, RequestedAt: time.Now().UTC(), Counters: store.Counters{Requests: 1}}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-release
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"openai":{"models":{"m":{"cost":{"input":1}}}}}`))
	}))
	defer server.Close()
	runtime.modelsDevFetcher = &modelsDevFetcher{client: server.Client(), url: server.URL}
	done := make(chan error, 1)
	go func() {
		_, err := runtime.syncModelsDev(nil, []string{"m"})
		done <- err
	}()
	<-started
	newSettings := store.PriceSyncSettings{ProviderPriority: []string{"anthropic"}, IgnoredSuffixes: []string{"-custom"}}
	if _, err := st.SavePriceBook(map[string]store.ModelPrice{}, &newSettings); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil || errs.HTTPStatus(err) != http.StatusConflict {
		t.Fatalf("stale sync error = %v", err)
	}
	book, err := st.QueryPriceBook()
	if err != nil {
		t.Fatal(err)
	}
	if len(book.SyncSettings.ProviderPriority) != 1 || book.SyncSettings.ProviderPriority[0] != "anthropic" || book.Revision != 1 {
		t.Fatalf("new settings were overwritten: %+v", book)
	}
}

func TestManagementSourceFilterAppliesToStatsRequestsAndCosts(t *testing.T) {
	config := testConfig(t)
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config, routes: registeredRoutes{
		pluginID:     "test",
		statsPath:    "/v0/management/plugins/test/stats",
		requestsPath: "/v0/management/plugins/test/requests",
		costsPath:    "/v0/management/plugins/test/costs",
	}}
	defer runtime.shutdown()
	for _, usage := range []store.Usage{
		{Dimensions: store.Dimensions{Model: "alpha", Source: "cli"}, RequestedAt: nowUTC(), Counters: store.Counters{Requests: 1, TotalTokens: 3}},
		{Dimensions: store.Dimensions{Model: "beta", Source: "web"}, RequestedAt: nowUTC(), Counters: store.Counters{Requests: 1, TotalTokens: 5}},
	} {
		if err := st.Record(usage); err != nil {
			t.Fatal(err)
		}
	}
	query := url.Values{"range": []string{"24h"}, "source": []string{"cli"}}
	for _, path := range []string{runtime.routes.statsPath, runtime.routes.requestsPath, runtime.routes.costsPath} {
		if path == runtime.routes.requestsPath {
			query.Set("offset", "0")
			query.Set("limit", "100")
		}
		request, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: path, Query: query})
		response, err := runtime.handleManagement(request)
		if err != nil || response.StatusCode != http.StatusOK || strings.Contains(string(response.Body), `"source":"web"`) {
			t.Fatalf("source-filtered %s response: %+v, %v", path, response, err)
		}
		if path == runtime.routes.statsPath && (!strings.Contains(string(response.Body), `"total_tokens":3`) || !strings.Contains(string(response.Body), `"sources":["cli","web"]`)) {
			t.Fatalf("source-filtered stats response: %+v", response)
		}
		if path == runtime.routes.requestsPath && !strings.Contains(string(response.Body), `"total":1`) {
			t.Fatalf("source-filtered request response: %+v", response)
		}
		if path == runtime.routes.costsPath && !strings.Contains(string(response.Body), `"requests":1`) {
			t.Fatalf("source-filtered cost response: %+v", response)
		}
	}

	allCosts, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.costsPath, Query: url.Values{"range": []string{"24h"}}})
	response, err := runtime.handleManagement(allCosts)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"requests":2`) {
		t.Fatalf("unfiltered costs response: %+v, %v", response, err)
	}
}

func TestManagementIgnoresLegacyAuthenticationIdentityParameters(t *testing.T) {
	config := testConfig(t)
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config, routes: registeredRoutes{
		pluginID:     "test",
		statsPath:    "/v0/management/plugins/test/stats",
		requestsPath: "/v0/management/plugins/test/requests",
		costsPath:    "/v0/management/plugins/test/costs",
	}}
	defer runtime.shutdown()
	for _, usage := range []store.Usage{
		{Dimensions: store.Dimensions{Model: "alpha", Source: "Codex-user@example.com"}, RequestedAt: nowUTC(), Counters: store.Counters{Requests: 1, TotalTokens: 3}},
		{Dimensions: store.Dimensions{Model: "beta", Source: "Antigravity-user@example.com"}, RequestedAt: nowUTC(), Counters: store.Counters{Requests: 1, TotalTokens: 5}},
	} {
		if err := st.Record(usage); err != nil {
			t.Fatal(err)
		}
	}
	query := url.Values{"range": []string{"24h"}, "source": []string{"Codex-user@example.com"}, "auth_provider": []string{"ignored"}, "auth_account": []string{"ignored"}}
	for _, path := range []string{runtime.routes.statsPath, runtime.routes.requestsPath, runtime.routes.costsPath} {
		if path == runtime.routes.requestsPath {
			query.Set("offset", "0")
			query.Set("limit", "100")
		}
		request, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: path, Query: query})
		response, err := runtime.handleManagement(request)
		if err != nil || response.StatusCode != http.StatusOK || strings.Contains(string(response.Body), "\"auth_provider\"") || strings.Contains(string(response.Body), "\"auth_account\"") {
			t.Fatalf("identity-filtered %s response: %+v, %v", path, response, err)
		}
	}
}

func TestDashboardSecurityContract(t *testing.T) {
	response := dashboardResponse()
	html := string(response.Body)
	for _, required := range []string{"/v0/resource/plugins/", "/v0/management/plugins/", "Authorization", "type=\"password\"", "textContent", "replaceChildren", "load().catch"} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing %q", required)
		}
	}
	// Storage policy: browser storage writes are limited to the obfuscated
	// remembered-key entry and the remember-preference flag, both namespaced,
	// always removed by name. No wholesale wipes, nothing else is ever written.
	if strings.Count(html, "localStorage.setItem") != 2 || !strings.Contains(html, "localStorage.setItem('tokens-statistic-remembered-key',encodeStorageValue(JSON.stringify({key:key})))") || !strings.Contains(html, "localStorage.setItem('tokens-statistic-remember-pref',remember?'1':'0')") {
		t.Fatal("dashboard must limit localStorage writes to the obfuscated remembered-key entry and the remember-preference flag")
	}
	if !strings.Contains(html, "localStorage.removeItem('tokens-statistic-remembered-key')") {
		t.Fatal("dashboard must remove the remembered-key localStorage entry by name")
	}
	if strings.Count(html, "sessionStorage.removeItem") != 1 || !strings.Contains(html, "sessionStorage.removeItem('tokens-statistic-key')") {
		t.Fatal("dashboard must limit sessionStorage removals to its own per-tab key entry")
	}
	for _, forbidden := range []string{"sessionStorage.clear", "connectButton", "logoutButton", "fetch('stats')", `fetch("stats")`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard contains forbidden pattern %q", forbidden)
		}
	}
	if response.Headers.Get("Content-Security-Policy") == "" {
		t.Fatal("missing content security policy")
	}
}

func TestManagementBackupAndRestore(t *testing.T) {
	config := testConfig(t)
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config, routes: registeredRoutes{
		pluginID:    "test",
		backupPath:  "/v0/management/plugins/test/backup",
		restorePath: "/v0/management/plugins/test/restore",
	}}
	defer runtime.shutdown()
	if err := st.Record(store.Usage{
		Dimensions:  store.Dimensions{Model: "backup-endpoint", Source: "cli"},
		RequestedAt: nowUTC(),
		Counters:    store.Counters{Requests: 1, InputTokens: 4, OutputTokens: 5, TotalTokens: 9},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveDashboardPreferences(store.DashboardPreferences{
		RequestPageSize:   25,
		DimensionPageSize: 50,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveModelPrices(map[string]store.ModelPrice{
		"backup-endpoint": {Input: 1.5, Output: 6},
	}); err != nil {
		t.Fatal(err)
	}

	backupRequest, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: runtime.routes.backupPath})
	response, err := runtime.handleManagement(backupRequest)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("backup response: %+v, %v", response, err)
	}
	if response.Headers.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("backup content type = %q", response.Headers.Get("Content-Type"))
	}
	if !strings.Contains(response.Headers.Get("Content-Disposition"), "attachment;") {
		t.Fatalf("backup disposition = %q", response.Headers.Get("Content-Disposition"))
	}
	if response.Headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("backup cache control = %q", response.Headers.Get("Cache-Control"))
	}
	if response.Headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("backup nosniff header = %q", response.Headers.Get("X-Content-Type-Options"))
	}
	if len(response.Body) == 0 {
		t.Fatal("expected non-empty backup body")
	}

	backupPath := filepath.Join(t.TempDir(), "endpoint-backup.db")
	if err := os.WriteFile(backupPath, response.Body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateRestoreDatabase(backupPath); err != nil {
		t.Fatalf("backup body is not a valid bolt database: %v", err)
	}
	backupBody := append([]byte(nil), response.Body...)

	emptyRestore, _ := json.Marshal(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   runtime.routes.restorePath,
		Headers: http.Header{
			"Content-Type":      []string{"application/octet-stream"},
			"X-Confirm-Restore": []string{"replace"},
		},
	})
	response, err = runtime.handleManagement(emptyRestore)
	if err != nil || response.StatusCode != http.StatusBadRequest || !strings.Contains(string(response.Body), "backup body must not be empty") {
		t.Fatalf("empty restore response: %+v, %v", response, err)
	}

	invalidTypeRestore, _ := json.Marshal(pluginapi.ManagementRequest{
		Method:  http.MethodPost,
		Path:    runtime.routes.restorePath,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte("not-a-database"),
	})
	response, err = runtime.handleManagement(invalidTypeRestore)
	if err != nil || response.StatusCode != http.StatusUnsupportedMediaType || !strings.Contains(string(response.Body), "application/octet-stream") {
		t.Fatalf("invalid content type restore response: %+v, %v", response, err)
	}

	missingConfirmRestore, _ := json.Marshal(pluginapi.ManagementRequest{
		Method:  http.MethodPost,
		Path:    runtime.routes.restorePath,
		Headers: http.Header{"Content-Type": []string{"application/octet-stream"}},
		Body:    backupBody,
	})
	response, err = runtime.handleManagement(missingConfirmRestore)
	if err != nil || response.StatusCode != http.StatusBadRequest || !strings.Contains(string(response.Body), "missing X-Confirm-Restore: replace header") {
		t.Fatalf("missing confirm restore response: %+v, %v", response, err)
	}

	invalidBodyRestore, _ := json.Marshal(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   runtime.routes.restorePath,
		Headers: http.Header{
			"Content-Type":      []string{"application/octet-stream"},
			"X-Confirm-Restore": []string{"replace"},
		},
		Body: []byte("not-a-database"),
	})
	response, err = runtime.handleManagement(invalidBodyRestore)
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid body restore response: %+v, %v", response, err)
	}

	// Oversized restore body is rejected at the management layer before touching storage.
	// Call restoreResponse directly to avoid base64-encoding a 64 MiB payload through JSON.
	oversizedBody := make([]byte, store.MaxDatabaseBackupBytes+1)
	response, err = runtime.restoreResponse(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   runtime.routes.restorePath,
		Headers: http.Header{
			"Content-Type":      []string{"application/octet-stream"},
			"X-Confirm-Restore": []string{"replace"},
		},
		Body: oversizedBody,
	})
	if err != nil || response.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(string(response.Body), "backup body is too large") {
		t.Fatalf("oversized restore response: %+v, %v", response, err)
	}

	if err := st.Reset(); err != nil {
		t.Fatal(err)
	}
	// Mutate preferences/prices after backup so restore must reintroduce the originals.
	// Reset only clears usage counters, not preferences or prices.
	if _, err := st.SaveDashboardPreferences(store.DashboardPreferences{
		RequestPageSize:   100,
		DimensionPageSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveModelPrices(map[string]store.ModelPrice{
		"mutated-model": {Input: 9, Output: 9},
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := st.Query("retention")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Summary.TotalTokens != 0 {
		t.Fatalf("expected empty stats after reset, got %+v", stats.Summary)
	}

	roundTripRestore, _ := json.Marshal(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   runtime.routes.restorePath,
		Headers: http.Header{
			"Content-Type":      []string{"application/octet-stream"},
			"X-Confirm-Restore": []string{"replace"},
		},
		Body: backupBody,
	})
	response, err = runtime.handleManagement(roundTripRestore)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), `"restored":true`) {
		t.Fatalf("round-trip restore response: %+v, %v", response, err)
	}
	stats, err = st.Query("retention")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Summary.TotalTokens != 9 {
		t.Fatalf("restored total tokens = %d, want 9", stats.Summary.TotalTokens)
	}
	preferences, err := st.QueryDashboardPreferences()
	if err != nil {
		t.Fatal(err)
	}
	if preferences.RequestPageSize != 25 || preferences.DimensionPageSize != 50 {
		t.Fatalf("restored preferences = %+v, want request=25 dimension=50", preferences)
	}
	prices, err := st.QueryModelPrices()
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 1 || prices["backup-endpoint"].Input != 1.5 || prices["backup-endpoint"].Output != 6 {
		t.Fatalf("restored model prices = %+v", prices)
	}
}

func TestAPIKeyIdentitiesFromRequestUnionsRepeatedRefs(t *testing.T) {
	hashA := strings.Repeat("a", 32)
	hashB := strings.Repeat("b", 32)
	refA := apikey.Ref(1, hashA)
	refB := apikey.Ref(2, hashB)
	request := pluginapi.ManagementRequest{Query: url.Values{"api_key_ref": {refA, "", refB, refA}}}
	got, err := apiKeyIdentitiesFromRequest(request, nil)
	if err != nil {
		t.Fatalf("parse repeated refs: %v", err)
	}
	if len(got) != 2 || got[0] != refA || got[1] != refB {
		t.Fatalf("identities = %#v, want first-seen unique [%q %q]", got, refA, refB)
	}
}

func TestAPIKeyIdentitiesFromRequestRejectsRefAndHashTogether(t *testing.T) {
	refA := apikey.Ref(1, strings.Repeat("a", 32))
	hashB := strings.Repeat("b", 32)
	_, err := apiKeyIdentitiesFromRequest(pluginapi.ManagementRequest{Query: url.Values{"api_key_ref": {refA}, "api_key_hash": {hashB}}}, nil)
	if err == nil || errs.HTTPStatus(err) != http.StatusBadRequest || err.Error() != "api_key_ref and api_key_hash cannot be used together" {
		t.Fatalf("mixed filter error = %v", err)
	}
}

func TestAPIKeyIdentitiesFromRequestRejectsRepeatedHashes(t *testing.T) {
	hashA := strings.Repeat("a", 32)
	hashB := strings.Repeat("b", 32)
	_, err := apiKeyIdentitiesFromRequest(pluginapi.ManagementRequest{Query: url.Values{"api_key_hash": {hashA, hashB}}}, nil)
	if err == nil || errs.HTTPStatus(err) != http.StatusBadRequest || err.Error() != "api_key_hash cannot be repeated; use api_key_ref" {
		t.Fatalf("repeated hash error = %v", err)
	}
}

func TestDashboardPreferencesManagementSaveRoute(t *testing.T) {
	config := testConfig(t)
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{
		store:  st,
		config: config,
		routes: registeredRoutes{
			pluginID:        "test",
			preferencesPath: "/v0/management/plugins/test/preferences",
		},
	}
	defer runtime.shutdown()

	call := func(method, path string, query url.Values, headers http.Header, body []byte) pluginapi.ManagementResponse {
		if headers == nil {
			headers = http.Header{}
		}
		raw, err := json.Marshal(pluginapi.ManagementRequest{Method: method, Path: path, Query: query, Headers: headers, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		response, err := runtime.handleManagement(raw)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	wrongMethod := call(http.MethodPut, runtime.routes.preferencesPath, nil, nil, nil)
	if wrongMethod.StatusCode != http.StatusMethodNotAllowed || wrongMethod.Headers.Get("Allow") != http.MethodGet+", "+http.MethodPost {
		t.Fatalf("wrong management save method response = %+v", wrongMethod)
	}

	body, err := json.Marshal(store.DashboardPreferences{
		RequestPageSize:      25,
		DimensionPageSize:    50,
		HiddenRequestColumns: []string{"model", "source"},
		TimeRangeMode:        "last_7_days",
		TokenDisplayMode:     "B",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := call(http.MethodPost, runtime.routes.preferencesPath, nil, http.Header{"Content-Type": []string{"application/json"}}, body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("management preferences save response = %+v body=%s", response, response.Body)
	}
	var saved store.DashboardPreferences
	if err := json.Unmarshal(response.Body, &saved); err != nil || saved.RequestPageSize != 25 || saved.DimensionPageSize != 50 || len(saved.HiddenRequestColumns) != 2 || saved.TimeRangeMode != "last_7_days" || saved.TokenDisplayMode != "B" {
		t.Fatalf("saved preferences payload = %s, err = %v", response.Body, err)
	}

	invalidBody := []byte(`{"request_page_size":0,"dimension_page_size":50}`)
	response = call(http.MethodPost, runtime.routes.preferencesPath, nil, http.Header{"Content-Type": []string{"application/json"}}, invalidBody)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid management preferences save response = %+v body=%s", response, response.Body)
	}

	response = call(http.MethodGet, runtime.routes.preferencesPath, nil, nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("resource preferences read response = %+v body=%s", response, response.Body)
	}
	saved = store.DashboardPreferences{}
	if err := json.Unmarshal(response.Body, &saved); err != nil || saved.RequestPageSize != 25 || saved.DimensionPageSize != 50 || saved.TokenDisplayMode != "B" {
		t.Fatalf("stored preferences payload = %s, err = %v", response.Body, err)
	}

	legacyQuery := url.Values{"save": {"1"}, "request_page_size": {"75"}, "dimension_page_size": {"100"}, "token_display_mode": {"m"}}
	response = call(http.MethodGet, runtime.routes.preferencesPath, legacyQuery, nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("legacy query parameters must be ignored on GET = %+v body=%s", response, response.Body)
	}
	saved = store.DashboardPreferences{}
	if err := json.Unmarshal(response.Body, &saved); err != nil || saved.RequestPageSize != 25 || saved.DimensionPageSize != 50 || saved.TokenDisplayMode != "B" {
		t.Fatalf("legacy query must not change stored preferences = %s, err = %v", response.Body, err)
	}
}

func TestSortGroupStatsEstimatedCostAndPriceSource(t *testing.T) {
	models := func(items []store.GroupStats) []string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, item.Model)
		}
		return out
	}
	newItems := func() []store.GroupStats {
		return []store.GroupStats{
			{Dimensions: store.Dimensions{Model: "m-a"}},
			{Dimensions: store.Dimensions{Model: "m-b"}},
			{Dimensions: store.Dimensions{Model: "m-c"}},
			{Dimensions: store.Dimensions{Model: "m-d"}},
		}
	}
	costs := map[string]groupSortAux{
		"m-a": {hasCost: true, totalUSD: 5},
		"m-b": {hasCost: true, totalUSD: 1},
		"m-d": {hasCost: true, totalUSD: 5},
	}
	items := newItems()
	if err := sortGroupStats(items, "estimated_cost", "desc", costs); err != nil {
		t.Fatalf("estimated_cost desc: %v", err)
	}
	if got, want := models(items), []string{"m-a", "m-d", "m-b", "m-c"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("estimated_cost desc order = %v, want %v", got, want)
	}
	items = newItems()
	if err := sortGroupStats(items, "estimated_cost", "asc", costs); err != nil {
		t.Fatalf("estimated_cost asc: %v", err)
	}
	if got, want := models(items), []string{"m-b", "m-a", "m-d", "m-c"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("estimated_cost asc order = %v, want %v", got, want)
	}
	items = newItems()
	if err := sortGroupStats(items, "estimated_cost", "desc", nil); err != nil {
		t.Fatalf("estimated_cost without aux must degrade gracefully: %v", err)
	}
	sources := map[string]groupSortAux{
		"m-a": {priceSource: "models.dev"},
		"m-b": {priceSource: "manual"},
	}
	items = newItems()
	if err := sortGroupStats(items, "price_source", "asc", sources); err != nil {
		t.Fatalf("price_source asc: %v", err)
	}
	if got, want := models(items), []string{"m-c", "m-d", "m-b", "m-a"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("price_source asc order = %v, want %v", got, want)
	}
	items = newItems()
	if err := sortGroupStats(items, "price_source", "desc", sources); err != nil {
		t.Fatalf("price_source desc: %v", err)
	}
	if got, want := models(items), []string{"m-a", "m-b", "m-c", "m-d"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("price_source desc order = %v, want %v", got, want)
	}
	items = newItems()
	if err := sortGroupStats(items, "not_a_column", "desc", nil); err == nil {
		t.Fatal("unknown sort key must be rejected")
	}
}
