package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"
)

func BenchmarkAPIKeyPersistenceFootprint(b *testing.B) {
	for _, test := range []struct {
		name   string
		secret string
		key    string
	}{
		{name: "disabled", secret: ""},
		{name: "encrypted", secret: strings.Repeat("s", 32), key: "benchmark-client-api-key"},
	} {
		b.Run(test.name, func(b *testing.B) {
			config := config.Config{
				DataPath:       filepath.Join(b.TempDir(), "usage.db"),
				RetentionDays:  30,
				FlushInterval:  time.Hour,
				FlushBatchSize: b.N + 1,
				APIKeySecret:   test.secret,
			}
			ctx, err := apikey.DeriveCryptoContext(config.APIKeySecret)
			if err != nil {
				b.Fatal(err)
			}
			store, err := OpenWithCrypto(config, ctx)
			if err != nil {
				b.Fatal(err)
			}
			generation, _ := store.APIKeyCryptoState()
			now := time.Now().UTC().Truncate(time.Minute)
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				usage := Usage{
					RequestedAt: now.Add(time.Duration(index) * time.Nanosecond),
					Dimensions:  Dimensions{Provider: "benchmark", Model: "benchmark-model", Source: "benchmark"},
					Counters:    Counters{Requests: 1, InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
				}
				if ctx.Enabled {
					hash := apikey.Fingerprint(test.key, ctx.IndexKey)
					ciphertext, err := apikey.EncryptForGeneration(ctx, test.key, hash, generation)
					if err != nil {
						b.Fatal(err)
					}
					usage.Dimensions.APIKey = ciphertext
					usage.Dimensions.APIKeyHash = hash
					usage.Dimensions.APIKeyGeneration = generation
				}
				if err := store.Record(usage); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if err := store.Close(); err != nil {
				b.Fatal(err)
			}
			info, err := os.Stat(config.DataPath)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(info.Size())/float64(b.N), "db-bytes/op")
		})
	}
}
