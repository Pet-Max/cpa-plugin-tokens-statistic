package plugin

import "testing"

func TestDefaultRetentionDays(t *testing.T) {
	config, err := parseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.RetentionDays != 365 {
		t.Fatalf("default retention days = %d, want 365", config.RetentionDays)
	}
	if config.FullModeSessionTTLMinutes != defaultFullModeSessionTTLMinutes {
		t.Fatalf("default full-mode session TTL = %d minutes, want %d", config.FullModeSessionTTLMinutes, defaultFullModeSessionTTLMinutes)
	}
}

func TestFullModeSessionTTLConfig(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    int
		wantErr bool
	}{
		{name: "default", want: defaultFullModeSessionTTLMinutes},
		{name: "custom", yaml: "session_ttl: 60\n", want: 60},
		{name: "minimum", yaml: "session_ttl: 1\n", want: 1},
		{name: "maximum", yaml: "session_ttl: 1440\n", want: 1440},
		{name: "zero", yaml: "session_ttl: 0\n", wantErr: true},
		{name: "negative", yaml: "session_ttl: -1\n", wantErr: true},
		{name: "too large", yaml: "session_ttl: 1441\n", wantErr: true},
		{name: "not an integer", yaml: "session_ttl: 15m\n", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := parseConfig([]byte(test.yaml))
			if test.wantErr {
				if err == nil {
					t.Fatalf("accepted invalid full-mode session TTL: %d", config.FullModeSessionTTLMinutes)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.FullModeSessionTTLMinutes != test.want {
				t.Fatalf("full-mode session TTL = %d, want %d", config.FullModeSessionTTLMinutes, test.want)
			}
		})
	}
}

func TestCompressionEnabledDefaults(t *testing.T) {
	config, err := parseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !config.CompressionEnabled || config.CompressionMinBytes != defaultCompressionMinBytes {
		t.Fatalf("compression defaults = enabled %t, threshold %d", config.CompressionEnabled, config.CompressionMinBytes)
	}
}
