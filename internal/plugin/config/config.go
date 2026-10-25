package config

import (
	"fmt"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultRetentionDays       = 365
	defaultFlushInterval       = 5 * time.Second
	defaultFlushBatchSize      = 100
	defaultCompressionEnabled  = true
	defaultCompressionMinBytes = 1024
	maxCompressionMinBytes     = 16 << 20
	defaultHealthWarn          = 95
	defaultHealthCrit          = 80
)

type Config struct {
	DataPath            string
	RetentionDays       int
	FlushInterval       time.Duration
	FlushBatchSize      int
	SyncOnRecord        bool
	APIKeySecret        string
	CompressionEnabled  bool
	CompressionMinBytes int
	HealthWarn          int
	HealthCrit          int
}

// configYAML is the small user-facing surface: db, retention, flush and secret.
// Compression knobs and the batch size stay internal.
type configYAML struct {
	DataPath     string      `yaml:"db"`
	Retention    *int        `yaml:"retention"`
	Flush        string      `yaml:"flush"`
	APIKeySecret *string     `yaml:"secret"`
	Health       *healthYAML `yaml:"health"`
}

// healthYAML holds the success-rate thresholds of the dashboard health panel.
type healthYAML struct {
	Warn *int `yaml:"warn"`
	Crit *int `yaml:"crit"`
}

func defaultConfig() Config {
	return Config{
		DataPath:            resolvedDefaultDataPath(),
		RetentionDays:       defaultRetentionDays,
		FlushInterval:       defaultFlushInterval,
		FlushBatchSize:      defaultFlushBatchSize,
		SyncOnRecord:        true,
		APIKeySecret:        apikey.DefaultSecret,
		CompressionEnabled:  defaultCompressionEnabled,
		CompressionMinBytes: defaultCompressionMinBytes,
		HealthWarn:          defaultHealthWarn,
		HealthCrit:          defaultHealthCrit,
	}
}

func Parse(raw []byte) (Config, error) {
	cfg := defaultConfig()
	if len(raw) == 0 {
		return normalizeConfig(cfg)
	}

	var input configYAML
	if err := yaml.Unmarshal(raw, &input); err != nil {
		return Config{}, fmt.Errorf("parse config YAML: %w", err)
	}
	if strings.TrimSpace(input.DataPath) != "" {
		cfg.DataPath = strings.TrimSpace(input.DataPath)
	}
	if input.Retention != nil {
		cfg.RetentionDays = *input.Retention
	}
	if strings.TrimSpace(input.Flush) != "" {
		interval, err := time.ParseDuration(strings.TrimSpace(input.Flush))
		if err != nil {
			return Config{}, fmt.Errorf("parse flush: %w", err)
		}
		cfg.FlushInterval = interval
		cfg.SyncOnRecord = false
	}
	if input.APIKeySecret != nil {
		cfg.APIKeySecret = *input.APIKeySecret
	}
	if input.Health != nil {
		if input.Health.Warn != nil {
			cfg.HealthWarn = *input.Health.Warn
		}
		if input.Health.Crit != nil {
			cfg.HealthCrit = *input.Health.Crit
		}
	}
	return normalizeConfig(cfg)
}

func normalizeConfig(cfg Config) (Config, error) {
	if strings.TrimSpace(cfg.DataPath) == "" {
		return Config{}, fmt.Errorf("db must not be empty")
	}
	if cfg.RetentionDays < 1 || cfg.RetentionDays > 3650 {
		return Config{}, fmt.Errorf("retention must be between 1 and 3650")
	}
	if cfg.FlushInterval < time.Second || cfg.FlushInterval > time.Hour {
		return Config{}, fmt.Errorf("flush must be between 1s and 1h")
	}
	if cfg.FlushBatchSize < 1 || cfg.FlushBatchSize > 1_000_000 {
		return Config{}, fmt.Errorf("flush batch size must be between 1 and 1000000")
	}
	if cfg.APIKeySecret != "" && cfg.APIKeySecret != apikey.DefaultSecret && len([]byte(cfg.APIKeySecret)) < 32 {
		return Config{}, fmt.Errorf("secret must be empty, 123456, or at least 32 bytes")
	}
	if cfg.CompressionMinBytes < 0 || cfg.CompressionMinBytes > maxCompressionMinBytes {
		return Config{}, fmt.Errorf("compression threshold must be between 0 and %d", maxCompressionMinBytes)
	}
	if cfg.HealthWarn < 0 || cfg.HealthWarn > 100 || cfg.HealthCrit < 0 || cfg.HealthCrit > 100 {
		return Config{}, fmt.Errorf("health thresholds must be between 0 and 100")
	}
	if cfg.HealthWarn <= cfg.HealthCrit {
		return Config{}, fmt.Errorf("health warn must be greater than health crit")
	}
	absolute, err := filepath.Abs(filepath.Clean(cfg.DataPath))
	if err != nil {
		return Config{}, fmt.Errorf("resolve db path: %w", err)
	}
	cfg.DataPath = absolute
	return cfg, nil
}
