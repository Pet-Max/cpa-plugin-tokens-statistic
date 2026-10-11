package plugin

import (
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/store"
	"strings"
	"testing"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
)

func TestSensitiveRevealDegradesOnlyCorruptItem(t *testing.T) {
	ctx, err := apikey.DeriveCryptoContext(strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	goodHash := apikey.Fingerprint("good-key", ctx.IndexKey)
	badHash := apikey.Fingerprint("bad-key", ctx.IndexKey)
	goodCiphertext, _ := apikey.EncryptAPIKey(ctx, "good-key", goodHash)
	badCiphertext, _ := apikey.EncryptAPIKey(ctx, "bad-key", badHash)
	stats := store.StatsResponse{
		Groups: []store.GroupStats{
			{Dimensions: store.Dimensions{APIKey: goodCiphertext, APIKeyHash: goodHash, APIKeyGeneration: 1}},
			{Dimensions: store.Dimensions{APIKey: badCiphertext, APIKeyHash: goodHash, APIKeyGeneration: 1}},
		},
		APIKeys: []store.APIKeyOption{
			{Ref: apikey.Ref(1, goodHash), Hash: goodHash, Generation: 1, Key: goodCiphertext},
			{Ref: apikey.Ref(1, badHash), Hash: badHash, Generation: 1, Key: badCiphertext + "corrupt"},
		},
	}
	stats.Reveal(func(ciphertext, fingerprint string, generation uint64) (string, string) {
		plaintext, err := apikey.DecryptForGeneration(ctx, ciphertext, fingerprint, generation)
		if err != nil {
			return "", apikey.StatusCiphertextInvalid
		}
		return plaintext, apikey.StatusAvailable
	})
	if stats.Groups[0].APIKey != "good-key" || stats.APIKeys[0].Key != "good-key" {
		t.Fatalf("valid ciphertext did not reveal: %+v", stats)
	}
	if stats.Groups[1].APIKey != "" || stats.APIKeys[1].Key != "" {
		t.Fatalf("corrupt ciphertext was not isolated: %+v", stats)
	}
	if stats.Groups[1].APIKeyHash != goodHash || stats.APIKeys[1].Hash != badHash {
		t.Fatal("reveal failure removed the stable fingerprint")
	}
}
