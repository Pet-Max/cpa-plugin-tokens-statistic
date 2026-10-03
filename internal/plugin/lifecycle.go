package plugin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// version is set at build time with:
// -X github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin.version=<version>
var version = "0.1.0"

// maxSupportedRPCSchema is intentionally independent from the SDK's latest
// schema. Future hosts may negotiate down to this verified contract without
// the plugin claiming support for semantics it has not implemented.
const maxSupportedRPCSchema uint32 = 3

type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	UsagePlugin   bool `json:"usage_plugin"`
	ManagementAPI bool `json:"management_api"`
}

type pluginRuntime struct {
	lifecycleMu       sync.Mutex
	priceSyncMu       sync.Mutex
	mu                sync.RWMutex
	store             *Store
	config            Config
	crypto            cryptoContext
	apiKeyGeneration  uint64
	apiKeyGenerations map[uint64]APIKeyCryptoGeneration
	routes            registeredRoutes
	modelsDevFetcher  *modelsDevFetcher
	exchangeRates     *exchangeRateService
	authResolver      *authIdentityResolver
	priceSyncing      bool
	fullModeMu        sync.Mutex
	fullModeSessions  map[[32]byte]fullModeSession
	fullModeUploads   map[string]fullModeUpload
}

var runtimeState = &pluginRuntime{}

func (r *pluginRuntime) register(raw []byte) (registration, error) {
	request, config, err := decodeLifecycle(raw)
	if err != nil {
		return registration{}, err
	}

	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if err := r.applyConfig(config); err != nil {
		return registration{}, err
	}
	return pluginRegistration(negotiateRPCSchema(request.SchemaVersion)), nil
}

func (r *pluginRuntime) reconfigure(raw []byte) (registration, error) {
	request, config, err := decodeLifecycle(raw)
	if err != nil {
		return registration{}, err
	}

	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if err := r.applyConfig(config); err != nil {
		return registration{}, err
	}
	return pluginRegistration(negotiateRPCSchema(request.SchemaVersion)), nil
}

func negotiateRPCSchema(hostSchema uint32) uint32 {
	if hostSchema == 0 {
		return 1
	}
	if hostSchema < maxSupportedRPCSchema {
		return hostSchema
	}
	return maxSupportedRPCSchema
}

func (r *pluginRuntime) applyConfig(config Config) error {
	crypto, err := deriveCryptoContext(config.APIKeySecret)
	if err != nil {
		return err
	}
	r.mu.RLock()
	current := r.store
	currentConfig := r.config
	r.mu.RUnlock()

	if current != nil && currentConfig.DataPath == config.DataPath {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.store != current || r.config.DataPath != config.DataPath {
			return errors.New("plugin storage changed during reconfiguration")
		}
		if err := current.ReconfigureWithCrypto(config, crypto); err != nil {
			return err
		}
		generation, generations := current.APIKeyCryptoState()
		r.config = config
		r.crypto = crypto
		r.apiKeyGeneration = generation
		r.apiKeyGenerations = generations
		return nil
	}

	next, err := openStoreWithCrypto(config, crypto)
	if err != nil {
		return err
	}
	r.mu.Lock()
	old := r.store
	r.store = next
	r.config = config
	r.crypto = crypto
	r.apiKeyGeneration, r.apiKeyGenerations = next.APIKeyCryptoState()
	r.mu.Unlock()
	r.fullModeMu.Lock()
	r.fullModeSessions = nil
	r.fullModeUploads = nil
	r.fullModeMu.Unlock()
	if old != nil {
		if err := old.Close(); err != nil {
			return fmt.Errorf("close previous store: %w", err)
		}
	}
	return nil
}

func (r *pluginRuntime) handleUsage(raw []byte) (map[string]any, error) {
	usage, err := decodeUsage(raw, nowUTC())
	if err != nil {
		return nil, withStatus(400, "%v", err)
	}
	r.resolveUsageIdentity(&usage)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.store == nil {
		return nil, withStatus(503, "plugin storage is not initialized")
	}
	crypto := r.crypto
	generation := r.apiKeyGeneration
	if generation == 0 && crypto.enabled {
		generation, _ = r.store.APIKeyCryptoState()
	}
	plainKey := usage.Dimensions.APIKey
	if plainKey != "" && crypto.enabled {
		if generation == 0 {
			return nil, errors.New("API key tracking has no active crypto generation")
		}
		fingerprint := apiKeyFingerprint(plainKey, crypto.indexKey)
		ciphertext, err := encryptAPIKeyForGeneration(crypto, plainKey, fingerprint, generation)
		if err != nil {
			return nil, fmt.Errorf("encrypt api key: %w", err)
		}
		usage.Dimensions.APIKeyHash = fingerprint
		usage.Dimensions.APIKeyGeneration = generation
		usage.Dimensions.APIKey = ciphertext
		usage.Dimensions.APIKeyStatus = ""
	} else {
		usage.Dimensions.APIKey = ""
		usage.Dimensions.APIKeyHash = ""
		usage.Dimensions.APIKeyGeneration = 0
		usage.Dimensions.APIKeyStatus = ""
		if crypto.enabled {
			usage.Dimensions.APIKeyStatus = apiKeyStatusSourceMissing
		}
	}
	if err := r.store.Record(usage); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func (r *pluginRuntime) shutdown() error {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	r.mu.Lock()
	store := r.store
	r.store = nil
	r.config = Config{}
	r.crypto = cryptoContext{}
	r.apiKeyGeneration = 0
	r.apiKeyGenerations = nil
	r.routes = registeredRoutes{}
	r.exchangeRates = nil
	r.authResolver = nil
	r.mu.Unlock()
	r.fullModeMu.Lock()
	r.fullModeSessions = nil
	r.fullModeUploads = nil
	r.fullModeMu.Unlock()
	if store == nil {
		return nil
	}
	return store.Close()
}

func decodeLifecycle(raw []byte) (lifecycleRequest, Config, error) {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	var envelope struct {
		ConfigYAML    json.RawMessage `json:"config_yaml"`
		SchemaVersion uint32          `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return lifecycleRequest{}, Config{}, fmt.Errorf("decode lifecycle request: %w", err)
	}
	configYAML, err := decodeLifecycleConfigYAML(envelope.ConfigYAML)
	if err != nil {
		return lifecycleRequest{}, Config{}, err
	}
	request := lifecycleRequest{ConfigYAML: configYAML, SchemaVersion: envelope.SchemaVersion}
	config, err := parseConfig(request.ConfigYAML)
	if err != nil {
		return lifecycleRequest{}, Config{}, err
	}
	return request, config, nil
}

// decodeLifecycleConfigYAML accepts the host's standard base64 encoding of a
// []byte field, while remaining compatible with hosts/tools that send a plain
// YAML string or an explicit JSON byte array.
func decodeLifecycleConfigYAML(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if decoded, decodeErr := base64.StdEncoding.DecodeString(text); decodeErr == nil && strings.Contains(string(decoded), ":") {
			return decoded, nil
		}
		return []byte(text), nil
	}
	var bytes []byte
	if err := json.Unmarshal(raw, &bytes); err == nil {
		return bytes, nil
	}
	return nil, fmt.Errorf("config_yaml must be a base64/plain string or byte array")
}

func pluginRegistration(schemaVersion uint32) registration {
	return registration{
		SchemaVersion: schemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "Tokens Statistic",
			Version:          version,
			Author:           "Pet-Max",
			GitHubRepository: "https://github.com/Pet-Max/cpa-plugin-tokens-statistic",
			Logo:             pluginLogo,
			ConfigFields: []pluginapi.ConfigField{
				{Name: "db", Type: pluginapi.ConfigFieldTypeString, Description: "bbolt 数据库文件的存放路径。留空时自动使用插件目录旁的数据目录（文件名 tokens-statistic.db）；若手动填写相对路径，则相对于 CLIProxyAPI 的工作目录解析。一般保持留空即可。"},
				{Name: "retention", Type: pluginapi.ConfigFieldTypeInteger, Description: "分钟级统计与请求明细在数据库中的保留天数，超过保留期的数据会被自动清理；可选 1-3650 天，默认 365。"},
				{Name: "flush", Type: pluginapi.ConfigFieldTypeString, Description: "用量数据批量写入数据库的间隔，例如 5s，允许 1s-1h；等待期间攒满 100 条记录也会立即写入一批。留空表示每条用量记录到达后立即提交。"},
				{Name: "secret", Type: pluginapi.ConfigFieldTypeString, Description: "用于加密 API Key 并计算其指纹的密钥。默认值 123456 仅建议在本地测试时使用；公开部署请设置至少 32 字节的自定义随机值，防止数据库文件泄露后 API Key 被还原。留空将完全禁用 API Key 追踪。修改此值会开启新的加密代数，历史记录仍会保留。"},
				{Name: "session_ttl", Type: pluginapi.ConfigFieldTypeInteger, Description: "解锁完整功能面板后的会话有效时长，单位为分钟，范围 1-1440，默认 15。会话令牌仅保存在页面内存中，不会持久化到磁盘。"},
			},
		},
		Capabilities: registrationCapabilities{UsagePlugin: true, ManagementAPI: true},
	}
}

var nowUTC = func() time.Time { return time.Now().UTC() }
