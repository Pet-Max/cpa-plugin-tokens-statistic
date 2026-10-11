package plugin

import (
	cryptorand "crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/store"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type blockingNonceReader struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingNonceReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	for index := range p {
		p[index] = byte(index + 1)
	}
	return len(p), nil
}

func encryptedUsageForGeneration(t *testing.T, ctx apikey.CryptoContext, generation uint64, key, model string, tokens uint64) store.Usage {
	t.Helper()
	hash := apikey.Fingerprint(key, ctx.IndexKey)
	ciphertext, err := apikey.EncryptForGeneration(ctx, key, hash, generation)
	if err != nil {
		t.Fatal(err)
	}
	return store.Usage{
		RequestedAt: time.Now().UTC().Add(-time.Second),
		Dimensions:  store.Dimensions{Model: model, Source: "test", APIKey: ciphertext, APIKeyHash: hash, APIKeyGeneration: generation},
		Counters:    store.Counters{Requests: 1, TotalTokens: tokens},
	}
}

func TestRestoreResponsePublishesActiveGenerationBeforeNextUsage(t *testing.T) {
	sourceConfig := testConfig(t)
	sourceConfig.APIKeySecret = strings.Repeat("r", 32)
	sourceConfig.SyncOnRecord = true
	sourceCrypto, err := apikey.DeriveCryptoContext(sourceConfig.APIKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	sourceStore, err := store.OpenWithCrypto(sourceConfig, sourceCrypto)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Record(encryptedUsageForGeneration(t, sourceCrypto, 1, "restore-old-key", "before-restore", 1)); err != nil {
		t.Fatal(err)
	}
	backup, err := sourceStore.Backup()
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}

	targetConfig := testConfig(t)
	targetConfig.APIKeySecret = strings.Repeat("s", 32)
	targetConfig.SyncOnRecord = true
	targetCrypto, err := apikey.DeriveCryptoContext(targetConfig.APIKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	targetStore, err := store.OpenWithCrypto(targetConfig, targetCrypto)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: targetStore, config: targetConfig, crypto: targetCrypto}
	runtime.apiKeyGeneration, runtime.apiKeyGenerations = targetStore.APIKeyCryptoState()
	defer runtime.shutdown()

	response, err := runtime.restoreResponse(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Headers: http.Header{
			"Content-Type":      []string{"application/octet-stream"},
			"X-Confirm-Restore": []string{"replace"},
		},
		Body: backup,
	})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("restore response = %+v, %v", response, err)
	}
	storeGeneration, generations := targetStore.APIKeyCryptoState()
	runtime.mu.RLock()
	runtimeGeneration := runtime.apiKeyGeneration
	runtime.mu.RUnlock()
	if storeGeneration == 0 || runtimeGeneration != storeGeneration || len(generations) != 2 {
		t.Fatalf("post-restore generations = runtime %d, store %d, %+v", runtimeGeneration, storeGeneration, generations)
	}
	plainKey := "restore-new-key"
	raw, _ := json.Marshal(pluginapi.UsageRecord{
		Model:       "after-restore",
		APIKey:      plainKey,
		RequestedAt: time.Now().UTC(),
		Detail:      pluginapi.UsageDetail{TotalTokens: 2},
	})
	if _, err := runtime.handleUsage(raw); err != nil {
		t.Fatal(err)
	}
	page, err := targetStore.QueryRequests("24h", 0, 10, "after-restore")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("post-restore request page = %+v, %v", page, err)
	}
	item := page.Items[0]
	if item.APIKeyGeneration != storeGeneration {
		t.Fatalf("post-restore request generation = %d, want %d", item.APIKeyGeneration, storeGeneration)
	}
	revealed, err := apikey.DecryptForGeneration(targetCrypto, item.APIKey, item.APIKeyHash, item.APIKeyGeneration)
	if err != nil || revealed != plainKey {
		t.Fatalf("post-restore API key = %q, %v", revealed, err)
	}
}

func TestResetResponsePublishesRecreatedGenerationBeforeNextUsage(t *testing.T) {
	config := testConfig(t)
	config.APIKeySecret = strings.Repeat("t", 32)
	config.SyncOnRecord = true
	crypto, err := apikey.DeriveCryptoContext(config.APIKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenWithCrypto(config, crypto)
	if err != nil {
		t.Fatal(err)
	}
	rotatedConfig := config
	rotatedConfig.APIKeySecret = strings.Repeat("u", 32)
	rotatedCrypto, err := apikey.DeriveCryptoContext(rotatedConfig.APIKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReconfigureWithCrypto(rotatedConfig, rotatedCrypto); err != nil {
		t.Fatal(err)
	}
	beforeReset, _ := st.APIKeyCryptoState()
	if beforeReset != 2 {
		t.Fatalf("pre-reset generation = %d, want 2", beforeReset)
	}
	runtime := &pluginRuntime{store: st, config: rotatedConfig, crypto: rotatedCrypto}
	runtime.apiKeyGeneration, runtime.apiKeyGenerations = st.APIKeyCryptoState()
	defer runtime.shutdown()

	response, err := runtime.resetResponse(pluginapi.ManagementRequest{
		Method:  http.MethodPost,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"confirm":"reset"}`),
	})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("reset response = %+v, %v", response, err)
	}
	storeGeneration, generations := st.APIKeyCryptoState()
	runtime.mu.RLock()
	runtimeGeneration := runtime.apiKeyGeneration
	runtime.mu.RUnlock()
	if storeGeneration != 1 || runtimeGeneration != storeGeneration || len(generations) != 1 {
		t.Fatalf("post-reset generations = runtime %d, store %d, %+v", runtimeGeneration, storeGeneration, generations)
	}
	plainKey := "reset-new-key"
	raw, _ := json.Marshal(pluginapi.UsageRecord{
		Model:       "after-reset",
		APIKey:      plainKey,
		RequestedAt: time.Now().UTC(),
		Detail:      pluginapi.UsageDetail{TotalTokens: 3},
	})
	if _, err := runtime.handleUsage(raw); err != nil {
		t.Fatal(err)
	}
	page, err := st.QueryRequests("24h", 0, 10, "after-reset")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("post-reset request page = %+v, %v", page, err)
	}
	item := page.Items[0]
	if item.APIKeyGeneration != storeGeneration {
		t.Fatalf("post-reset request generation = %d, want %d", item.APIKeyGeneration, storeGeneration)
	}
	revealed, err := apikey.DecryptForGeneration(rotatedCrypto, item.APIKey, item.APIKeyHash, item.APIKeyGeneration)
	if err != nil || revealed != plainKey {
		t.Fatalf("post-reset API key = %q, %v", revealed, err)
	}
}

func TestConcurrentUsageKeepsInFlightCryptoGeneration(t *testing.T) {
	config := testConfig(t)
	config.APIKeySecret = strings.Repeat("e", 32)
	config.SyncOnRecord = true
	ctx, err := apikey.DeriveCryptoContext(config.APIKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenWithCrypto(config, ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &pluginRuntime{store: st, config: config, crypto: ctx}
	defer runtime.shutdown()
	if err := st.Reset(); err != nil {
		t.Fatal(err)
	}

	reader := &blockingNonceReader{entered: make(chan struct{}), release: make(chan struct{})}
	originalReader := cryptorand.Reader
	cryptorand.Reader = reader
	defer func() { cryptorand.Reader = originalReader }()

	plainKey := "concurrent-reset-window-key"
	raw, err := json.Marshal(pluginapi.UsageRecord{
		Model:       "concurrent-model",
		APIKey:      plainKey,
		RequestedAt: time.Now().UTC(),
		Detail:      pluginapi.UsageDetail{TotalTokens: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	usageResult := make(chan error, 1)
	go func() {
		_, err := runtime.handleUsage(raw)
		usageResult <- err
	}()
	<-reader.entered

	candidate := config
	candidate.APIKeySecret = strings.Repeat("f", 32)
	reconfigureResult := make(chan error, 1)
	go func() { reconfigureResult <- runtime.applyConfig(candidate) }()
	close(reader.release)
	if err := <-usageResult; err != nil {
		t.Fatalf("usage failed: %v", err)
	}
	if err := <-reconfigureResult; err != nil {
		t.Fatalf("concurrent reconfigure failed: %v", err)
	}

	page, err := st.QueryRequests("24h", 0, 10, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("request page = %+v, %v", page, err)
	}
	if page.Items[0].APIKeyGeneration != 1 {
		t.Fatalf("in-flight request generation = %d", page.Items[0].APIKeyGeneration)
	}
	revealed, err := apikey.DecryptForGeneration(ctx, page.Items[0].APIKey, page.Items[0].APIKeyHash, page.Items[0].APIKeyGeneration)
	if err != nil || revealed != plainKey {
		t.Fatalf("persisted request was not encrypted with the active generation: %q, %v", revealed, err)
	}
	runtime.mu.RLock()
	activeSecret := runtime.config.APIKeySecret
	activeKeyID := runtime.crypto.KeyID
	runtime.mu.RUnlock()
	if activeSecret != candidate.APIKeySecret || activeKeyID == ctx.KeyID {
		t.Fatal("successful concurrent reconfigure did not publish the replacement generation")
	}
}

func encryptedUsageForTest(t *testing.T, ctx apikey.CryptoContext, key, model string, tokens uint64) store.Usage {
	return encryptedUsageForGeneration(t, ctx, 1, key, model, tokens)
}
