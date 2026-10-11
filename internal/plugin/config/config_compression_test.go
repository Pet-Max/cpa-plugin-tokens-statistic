package config

import "testing"

func TestDefaultRetentionDays(t *testing.T) {
	config, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.RetentionDays != 365 {
		t.Fatalf("default retention days = %d, want 365", config.RetentionDays)
	}
}

func TestCompressionEnabledDefaults(t *testing.T) {
	config, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !config.CompressionEnabled || config.CompressionMinBytes != defaultCompressionMinBytes {
		t.Fatalf("compression defaults = enabled %t, threshold %d", config.CompressionEnabled, config.CompressionMinBytes)
	}
}
