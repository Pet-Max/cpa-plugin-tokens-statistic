package store

import (
	"encoding/binary"
)

var (
	metaBucket              = []byte("meta")
	hoursBucket             = []byte("hours")
	requestsBucket          = []byte("requests")
	schemaKey               = []byte("schema_version")
	sinceKey                = []byte("since_unix_nano")
	lastUsedKey             = []byte("last_used_unix_nano")
	requestSequenceKey      = []byte("request_sequence")
	modelPricesKey          = []byte("model_prices")
	modelPriceRevisionKey   = []byte("model_price_revision")
	modelPriceSettingsKey   = []byte("model_price_sync_settings")
	modelPriceLastSyncKey   = []byte("model_price_last_sync")
	modelPriceSavedAtKey    = []byte("model_price_saved_at")
	dashboardPreferencesKey = []byte("dashboard_preferences")
	cryptoKeyIDKey          = []byte("crypto_key_id")
	apiKeyHashVersionKey    = []byte("api_key_hash_version")
	apiKeyGenerationsKey    = []byte("api_key_crypto_generations")
	apiKeyNextGenerationKey = []byte("api_key_next_generation")
	apiKeyLabelsKey         = []byte("api_key_labels")
)

const persistenceSchemaVersion uint64 = 9

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func encodeUint64(value uint64) []byte {
	result := make([]byte, 8)
	binary.BigEndian.PutUint64(result, value)
	return result
}

func decodeUint64(value []byte) uint64 {
	if len(value) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(value)
}

func encodeInt64(value int64) []byte {
	return encodeUint64(uint64(value) ^ (uint64(1) << 63))
}

func decodeInt64(value []byte) int64 {
	return int64(decodeUint64(value) ^ (uint64(1) << 63))
}
