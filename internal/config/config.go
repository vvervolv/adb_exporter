// Package config loads and validates the exporter configuration.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime settings for the exporter.
//
// See SPEC.md §Config. Zero values are filled from Defaults() before validation.
type Config struct {
	// Listen is the HTTP listen address, e.g. ":9105".
	Listen string `yaml:"listen"`
	// PollInterval is the minimum spacing between poll cycles (SPEC: 5s).
	PollInterval time.Duration `yaml:"poll_interval"`
	// ADBPath is the adb executable path or name resolved via PATH.
	ADBPath string `yaml:"adb_path"`
	// ADBTimeout bounds every single adb invocation (SPEC: 3s).
	ADBTimeout time.Duration `yaml:"adb_timeout"`
	// MaxParallelADB caps the number of concurrent adb processes.
	MaxParallelADB int `yaml:"max_parallel_adb"`
}

// Defaults returns the baseline configuration from SPEC.md §Config.
func Defaults() Config {
	return Config{
		Listen:         ":9105",
		PollInterval:   5 * time.Second,
		ADBPath:        "adb",
		ADBTimeout:     3 * time.Second,
		MaxParallelADB: 8,
	}
}

// Load reads a YAML config file, applying defaults for any unset field.
// An empty path returns Defaults() without touching the filesystem.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		return cfg, cfg.Validate()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	// Decode onto the defaults so omitted keys keep their default value.
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks that the configuration is internally consistent.
func (c Config) Validate() error {
	if c.Listen == "" {
		return fmt.Errorf("listen must not be empty")
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("poll_interval must be positive, got %s", c.PollInterval)
	}
	if c.ADBPath == "" {
		return fmt.Errorf("adb_path must not be empty")
	}
	if c.ADBTimeout <= 0 {
		return fmt.Errorf("adb_timeout must be positive, got %s", c.ADBTimeout)
	}
	if c.MaxParallelADB < 1 {
		return fmt.Errorf("max_parallel_adb must be >= 1, got %d", c.MaxParallelADB)
	}
	if c.ADBTimeout >= c.PollInterval {
		// Not fatal, but a strong smell: a single slow device could eat the
		// whole cycle. Warn loudly by refusing so the operator picks sane values.
		return fmt.Errorf("adb_timeout (%s) must be less than poll_interval (%s)", c.ADBTimeout, c.PollInterval)
	}
	return nil
}
