package plugin

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/config"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		DataPath:       filepath.Join(t.TempDir(), "usage.db"),
		RetentionDays:  30,
		FlushInterval:  time.Hour,
		FlushBatchSize: 100,
	}
}

func TestRuntimeSerializesConcurrentReconfigure(t *testing.T) {
	base := testConfig(t)
	runtime := &pluginRuntime{}
	request := func(retention int) []byte {
		config := []byte("db: " + filepath.ToSlash(base.DataPath) + "\nretention: " + fmt.Sprint(retention) + "\n")
		raw, _ := json.Marshal(lifecycleRequest{ConfigYAML: config, SchemaVersion: 1})
		return raw
	}
	if _, err := runtime.register(request(30)); err != nil {
		t.Fatal(err)
	}
	defer runtime.shutdown()

	var wg sync.WaitGroup
	for _, retention := range []int{7, 14, 21, 30} {
		wg.Add(1)
		go func(value int) {
			defer wg.Done()
			if _, err := runtime.reconfigure(request(value)); err != nil {
				t.Errorf("reconfigure %d: %v", value, err)
			}
		}(retention)
	}
	wg.Wait()
	runtime.mu.RLock()
	active := runtime.config.RetentionDays
	runtime.mu.RUnlock()
	if active != 7 && active != 14 && active != 21 && active != 30 {
		t.Fatalf("unexpected active retention: %d", active)
	}
}
