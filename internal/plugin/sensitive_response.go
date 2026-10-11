package plugin

import (
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/store"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// sensitiveJSONResponse always reveals sensitive fields: every request that
// reaches the plugin's management handlers has already been authenticated by
// the host's management-key middleware, so the caller holds admin privileges.
func (r *pluginRuntime) sensitiveJSONResponse(status int, value store.Sensitive, crypto apikey.CryptoContext, generations map[uint64]apikey.APIKeyCryptoGeneration) pluginapi.ManagementResponse {
	value.Reveal(func(ciphertext, fingerprint string, generation uint64) (string, string) {
		metadata, ok := generations[generation]
		if !ok || metadata.IdentityMissing || metadata.KeyID == "" {
			return "", apikey.StatusIdentityMissing
		}
		if !crypto.Enabled || metadata.KeyID != crypto.KeyID || metadata.HashVersion != apikey.HashVersion {
			return "", apikey.StatusGenerationUnavailable
		}
		if ciphertext == "" {
			return "", apikey.StatusCiphertextMissing
		}
		plaintext, err := apikey.DecryptForGeneration(crypto, ciphertext, fingerprint, generation)
		if err != nil || plaintext == "" {
			return "", apikey.StatusCiphertextInvalid
		}
		return plaintext, apikey.StatusAvailable
	})
	return jsonResponse(status, value)
}
