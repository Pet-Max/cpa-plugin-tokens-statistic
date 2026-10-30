package config

import "testing"

func TestHealthThresholdDefaults(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HealthWarn != defaultHealthWarn || cfg.HealthCrit != defaultHealthCrit {
		t.Fatalf("health defaults: warn=%d crit=%d", cfg.HealthWarn, cfg.HealthCrit)
	}
}

func TestHealthThresholdOverridesAndValidation(t *testing.T) {
	cfg, err := Parse([]byte("health:\n  warn: 90\n  crit: 80\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HealthWarn != 90 || cfg.HealthCrit != 80 {
		t.Fatalf("health override: warn=%d crit=%d", cfg.HealthWarn, cfg.HealthCrit)
	}
	// Partial override keeps the other default.
	cfg, err = Parse([]byte("health:\n  crit: 70\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HealthWarn != defaultHealthWarn || cfg.HealthCrit != 70 {
		t.Fatalf("health partial override: warn=%d crit=%d", cfg.HealthWarn, cfg.HealthCrit)
	}
	// warn must be greater than crit.
	if _, err := Parse([]byte("health:\n  warn: 80\n  crit: 90\n")); err == nil {
		t.Fatal("expected error when warn <= crit")
	}
	// Values outside 0-100 are rejected.
	if _, err := Parse([]byte("health:\n  warn: 101\n")); err == nil {
		t.Fatal("expected error when warn > 100")
	}
	if _, err := Parse([]byte("health:\n  crit: -1\n")); err == nil {
		t.Fatal("expected error when crit < 0")
	}
}

func TestHealthThresholdFlatKeys(t *testing.T) {
	// The management center writes flat top-level keys.
	cfg, err := Parse([]byte("health_warn: 90\nhealth_crit: 80\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HealthWarn != 90 || cfg.HealthCrit != 80 {
		t.Fatalf("flat override: warn=%d crit=%d", cfg.HealthWarn, cfg.HealthCrit)
	}
	// Partial flat override keeps the other default.
	cfg, err = Parse([]byte("health_warn: 85\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HealthWarn != 85 || cfg.HealthCrit != defaultHealthCrit {
		t.Fatalf("flat partial override: warn=%d crit=%d", cfg.HealthWarn, cfg.HealthCrit)
	}
	// Explicit flat keys win over a legacy nested block.
	cfg, err = Parse([]byte("health:\n  warn: 70\n  crit: 60\nhealth_warn: 90\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HealthWarn != 90 || cfg.HealthCrit != 60 {
		t.Fatalf("flat over nested precedence: warn=%d crit=%d", cfg.HealthWarn, cfg.HealthCrit)
	}
	// Flat keys hit the same validation as the nested block.
	if _, err := Parse([]byte("health_warn: 80\nhealth_crit: 90\n")); err == nil {
		t.Fatal("expected error when flat warn <= crit")
	}
	if _, err := Parse([]byte("health_crit: 101\n")); err == nil {
		t.Fatal("expected error when flat crit > 100")
	}
}
