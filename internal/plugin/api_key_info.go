package plugin

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/store"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// apiKeyInfoResponse serves the dashboard's API-key filter state. It is a
// management route, so the caller has already been authenticated by the host's
// management-key middleware.
func (r *pluginRuntime) apiKeyInfoResponse() (pluginapi.ManagementResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "storage is not initialized"}), nil
	}
	labels, err := r.store.APIKeyLabels()
	if err != nil {
		return jsonResponse(errs.HTTPStatus(err), map[string]string{"error": err.Error()}), nil
	}
	crypto := r.crypto
	return jsonResponse(http.StatusOK, map[string]any{
		"api_key_tracking_enabled":    crypto.Enabled,
		"api_key_uses_default_secret": crypto.Enabled && crypto.UsesDefaultSecret,
		"api_key_labels":              labels,
	}), nil
}

func (r *pluginRuntime) setAPIKeyLabelResponse(request pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	var input struct {
		Ref   string `json:"ref"`
		Label string `json:"label"`
	}
	if strings.EqualFold(request.Method, http.MethodGet) {
		refs, labels := request.Query["ref"], request.Query["label"]
		if len(refs) == 1 && len(labels) == 1 {
			input.Ref, input.Label = refs[0], labels[0]
		} else {
			body := []byte(request.Headers.Get("X-API-Key-Label"))
			if len(body) == 0 {
				return jsonResponse(http.StatusBadRequest, map[string]string{"error": "API key label query requires exactly one ref and label"}), nil
			}
			if len(body) > 16<<10 || !utf8.Valid(body) || decodeStrictJSON(body, &input) != nil {
				return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid API key label JSON"}), nil
			}
		}
	} else {
		if len(request.Body) > 16<<10 {
			return jsonResponse(http.StatusRequestEntityTooLarge, map[string]string{"error": "API key label JSON is too large"}), nil
		}
		if !utf8.Valid(request.Body) {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "API key label JSON must be valid UTF-8"}), nil
		}
		if err := decodeStrictJSON(request.Body, &input); err != nil {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid API key label JSON"}), nil
		}
	}
	if err := store.ValidateAPIKeyLabel(input.Ref, input.Label); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()}), nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.store == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "storage is not initialized"}), nil
	}
	if err := r.store.SetAPIKeyLabel(input.Ref, input.Label); err != nil {
		return jsonResponse(errs.HTTPStatus(err), map[string]string{"error": err.Error()}), nil
	}
	return jsonResponse(http.StatusOK, map[string]any{"saved": true, "ref": input.Ref, "label": input.Label}), nil
}
