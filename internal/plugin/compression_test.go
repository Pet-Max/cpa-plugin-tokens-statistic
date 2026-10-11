package plugin

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/store"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestAcceptsGzip(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   bool
	}{
		{name: "missing"},
		{name: "gzip", header: http.Header{"Accept-Encoding": []string{"gzip"}}, want: true},
		{name: "mixed codings", header: http.Header{"Accept-Encoding": []string{"br, gzip"}}, want: true},
		{name: "quality", header: http.Header{"Accept-Encoding": []string{"gzip;q=0.8"}}, want: true},
		{name: "disabled quality", header: http.Header{"Accept-Encoding": []string{"gzip;q=0"}}},
		{name: "other only", header: http.Header{"Accept-Encoding": []string{"br"}}},
		{name: "case and whitespace", header: http.Header{"accept-encoding": []string{" GZip ; Q=0.5 "}}, want: true},
		{name: "wildcard", header: http.Header{"Accept-Encoding": []string{"br, *;q=0.2"}}, want: true},
		{name: "explicit exclusion beats wildcard", header: http.Header{"Accept-Encoding": []string{"gzip;q=0, *;q=1"}}},
		{name: "invalid quality", header: http.Header{"Accept-Encoding": []string{"gzip;q=2"}}},
		{name: "multiple header values", header: http.Header{"Accept-Encoding": []string{"br", "gzip;q=1"}}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := acceptsGzip(test.header); got != test.want {
				t.Fatalf("acceptsGzip() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestMaybeCompressResponse(t *testing.T) {
	body := []byte(strings.Repeat(`{"model":"gpt"}`, 200))
	baseRequest := pluginapi.ManagementRequest{
		Method:  http.MethodGet,
		Path:    "/v0/resource/plugins/test/stats",
		Headers: http.Header{"Accept-Encoding": []string{"gzip"}},
	}
	baseResponse := pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers: http.Header{
			"Content-Type":   []string{"application/json; charset=utf-8"},
			"Content-Length": []string{"3200"},
			"Vary":           []string{"Origin"},
		},
		Body: body,
	}
	cfg := config.Config{CompressionEnabled: true, CompressionMinBytes: 1}

	compressed := maybeCompressResponse(baseRequest, baseResponse, cfg)
	if compressed.Headers.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", compressed.Headers.Get("Content-Encoding"))
	}
	if compressed.Headers.Get("Content-Length") != "" {
		t.Fatalf("Content-Length was retained: %q", compressed.Headers.Get("Content-Length"))
	}
	if vary := strings.Join(compressed.Headers.Values("Vary"), ","); !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Accept-Encoding") {
		t.Fatalf("Vary = %q", vary)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed.Body))
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(roundTrip, body) {
		t.Fatal("gzip round trip changed response body")
	}
	if baseResponse.Headers.Get("Content-Encoding") != "" || baseResponse.Headers.Get("Content-Length") == "" {
		t.Fatal("compression mutated the original response headers")
	}

	compressedManagement := maybeCompressResponse(pluginapi.ManagementRequest{Path: "/v0/management/plugins/test/stats", Headers: baseRequest.Headers}, baseResponse, cfg)
	if compressedManagement.Headers.Get("Content-Encoding") != "gzip" {
		t.Fatalf("management JSON route was not compressed: %+v", compressedManagement.Headers)
	}

	tests := []struct {
		name     string
		request  pluginapi.ManagementRequest
		response pluginapi.ManagementResponse
		config   config.Config
	}{
		{name: "disabled", request: baseRequest, response: baseResponse, config: config.Config{CompressionMinBytes: 1}},
		{name: "client without gzip", request: pluginapi.ManagementRequest{Path: baseRequest.Path}, response: baseResponse, config: cfg},
		{name: "below threshold", request: baseRequest, response: baseResponse, config: config.Config{CompressionEnabled: true, CompressionMinBytes: len(body) + 1}},
		{name: "already encoded", request: baseRequest, response: responseWithHeader(baseResponse, "Content-Encoding", "br"), config: cfg},
		{name: "partial content", request: baseRequest, response: responseWithHeader(baseResponse, "Content-Range", "bytes 0-99/3200"), config: cfg},
		{name: "no transform", request: baseRequest, response: responseWithHeader(baseResponse, "Cache-Control", "private, no-transform"), config: cfg},
		{name: "binary", request: baseRequest, response: responseWithHeader(baseResponse, "Content-Type", "application/octet-stream"), config: cfg},
		{name: "no content", request: baseRequest, response: responseWithStatus(baseResponse, http.StatusNoContent), config: cfg},
		{name: "not modified", request: baseRequest, response: responseWithStatus(baseResponse, http.StatusNotModified), config: cfg},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := maybeCompressResponse(test.request, test.response, test.config)
			if got.Headers.Get("Content-Encoding") == "gzip" {
				t.Fatal("response was compressed")
			}
			if !bytes.Equal(got.Body, test.response.Body) {
				t.Fatal("response body changed")
			}
		})
	}
}

func TestHandleManagementCompressionScope(t *testing.T) {
	runtime := &pluginRuntime{
		config: config.Config{CompressionEnabled: true, CompressionMinBytes: 1},
		routes: registeredRoutes{
			pluginID:      "test",
			dashboardPath: "/v0/resource/plugins/test/dashboard",
		},
	}

	dashboardRequest, err := json.Marshal(pluginapi.ManagementRequest{
		Method:  http.MethodGet,
		Path:    runtime.routes.dashboardPath,
		Headers: http.Header{"Accept-Encoding": []string{"gzip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dashboard, err := runtime.handleManagement(dashboardRequest)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Headers.Get("Content-Encoding") != "gzip" || dashboard.Headers.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("dashboard headers = %+v", dashboard.Headers)
	}

	managementRequest, err := json.Marshal(pluginapi.ManagementRequest{
		Method:  http.MethodGet,
		Path:    "/v0/management/plugins/test/unknown",
		Headers: http.Header{"Accept-Encoding": []string{"gzip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	management, err := runtime.handleManagement(managementRequest)
	if err != nil {
		t.Fatal(err)
	}
	if management.Headers.Get("Content-Encoding") != "gzip" || management.StatusCode != http.StatusNotFound {
		t.Fatalf("management response = %+v", management)
	}
	if body := gunzipResponseBody(t, management.Body); !json.Valid(body) {
		t.Fatalf("decompressed 404 body is not JSON: %q", body)
	}
}

func TestCompressedManagementResponseSurvivesRPCEnvelope(t *testing.T) {
	body := []byte(strings.Repeat(`{"requests":1}`, 200))
	response := maybeCompressResponse(
		pluginapi.ManagementRequest{
			Path:    "/v0/resource/plugins/test/stats",
			Headers: http.Header{"Accept-Encoding": []string{"gzip"}},
		},
		pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       body,
		},
		config.Config{CompressionEnabled: true, CompressionMinBytes: 1},
	)
	if response.Headers.Get("Content-Encoding") != "gzip" {
		t.Fatal("test response was not compressed")
	}

	var envelope rpcEnvelope
	if err := json.Unmarshal(marshalOK(response), &envelope); err != nil {
		t.Fatal(err)
	}
	var roundTrip pluginapi.ManagementResponse
	if err := json.Unmarshal(envelope.Result, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.StatusCode != response.StatusCode || roundTrip.Headers.Get("Content-Encoding") != "gzip" || !bytes.Equal(roundTrip.Body, response.Body) {
		t.Fatal("RPC envelope changed the compressed management response")
	}
}

func TestPublicJSONRoutesCompress(t *testing.T) {
	config := testConfig(t)
	config.CompressionEnabled = true
	config.CompressionMinBytes = 1
	st, err := store.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{
		store:  st,
		config: config,
		routes: registeredRoutes{
			pluginID:  "test",
			statsPath: "/v0/management/plugins/test/stats",
			costsPath: "/v0/management/plugins/test/costs",
		},
	}
	defer runtime.shutdown()
	if err := st.Record(store.Usage{
		Dimensions:  store.Dimensions{Model: "gpt-test"},
		RequestedAt: time.Now().UTC(),
		Counters:    store.Counters{Requests: 1, InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
	}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{runtime.routes.statsPath, runtime.routes.costsPath} {
		raw, err := json.Marshal(pluginapi.ManagementRequest{
			Method:  http.MethodGet,
			Path:    path,
			Headers: http.Header{"Accept-Encoding": []string{"gzip"}},
			Query:   url.Values{"range": []string{"24h"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		response, err := runtime.handleManagement(raw)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || response.Headers.Get("Content-Encoding") != "gzip" {
			t.Fatalf("response for %s = %+v", path, response)
		}
		decoded := gunzipResponseBody(t, response.Body)
		if !json.Valid(decoded) {
			t.Fatalf("decompressed response for %s is not JSON", path)
		}
	}
}

func gunzipResponseBody(t *testing.T, body []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func responseWithHeader(response pluginapi.ManagementResponse, name, value string) pluginapi.ManagementResponse {
	response.Headers = response.Headers.Clone()
	response.Headers.Set(name, value)
	return response
}

func responseWithStatus(response pluginapi.ManagementResponse, status int) pluginapi.ManagementResponse {
	response.StatusCode = status
	return response
}
