package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	def := Defaults()
	if cfg != def {
		t.Fatalf("empty path should return defaults\n got: %+v\nwant: %+v", cfg, def)
	}
}

func TestLoadYAMLOverridesAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// Only override two fields; the rest must fall back to defaults.
	content := "listen: \":8080\"\nmax_parallel_adb: 16\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("listen = %q, want :8080", cfg.Listen)
	}
	if cfg.MaxParallelADB != 16 {
		t.Errorf("max_parallel_adb = %d, want 16", cfg.MaxParallelADB)
	}
	if cfg.PollInterval != 5*time.Second {
		t.Errorf("poll_interval = %s, want default 5s", cfg.PollInterval)
	}
	if cfg.ADBPath != "adb" {
		t.Errorf("adb_path = %q, want default adb", cfg.ADBPath)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"defaults ok", func(*Config) {}, false},
		{"empty listen", func(c *Config) { c.Listen = "" }, true},
		{"zero poll", func(c *Config) { c.PollInterval = 0 }, true},
		{"empty adb path", func(c *Config) { c.ADBPath = "" }, true},
		{"zero timeout", func(c *Config) { c.ADBTimeout = 0 }, true},
		{"zero parallel", func(c *Config) { c.MaxParallelADB = 0 }, true},
		{"timeout >= interval", func(c *Config) { c.ADBTimeout = 5 * time.Second }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
