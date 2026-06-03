// Package config loads Sense's project-level configuration from
// .sense/config.yml. Missing file is fine — defaults apply.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultMaxFileSizeKB = 512

type Config struct {
	Ignore     []string         `yaml:"ignore"`
	Scan       ScanConfig       `yaml:"scan"`
	Embeddings EmbeddingsConfig `yaml:"embeddings"`
	// Watch toggles the embedded watcher the `sense mcp` server runs to
	// keep the index fresh in the background. Default true; set watch:
	// false to turn it off (queries still serve, the index just goes stale
	// until the next scan). `sense scan --watch` is unaffected — it is an
	// explicit opt-in.
	Watch *bool `yaml:"watch"`
}

type ScanConfig struct {
	MaxFileSizeKB   int `yaml:"max_file_size_kb"`
	WatchDebounceMs int `yaml:"watch_debounce_ms"`
}

type EmbeddingsConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// EmbeddingsEnabled returns whether embedding generation is active.
// Default is true; set embeddings.enabled: false in config.yml or
// SENSE_EMBEDDINGS=false to disable.
func (c *Config) EmbeddingsEnabled() bool {
	if c.Embeddings.Enabled != nil {
		return *c.Embeddings.Enabled
	}
	return true
}

// IsEmbeddingsEnabled checks the SENSE_EMBEDDINGS env var first, then
// falls back to the config file. Used by packages that can't import cli.
func IsEmbeddingsEnabled(root string) bool {
	return envOrConfigBool("SENSE_EMBEDDINGS", (*Config).EmbeddingsEnabled, root)
}

// envOrConfigBool resolves a boolean toggle from an env var, then the
// config file. A non-empty env var wins (anything but "false"/"0" is true).
// Otherwise getter reads the loaded config. A missing or unparseable config
// defaults to enabled, so a typo in config.yml never silently turns a
// feature off.
func envOrConfigBool(envVar string, getter func(*Config) bool, root string) bool {
	if env := os.Getenv(envVar); env != "" {
		return !strings.EqualFold(env, "false") && env != "0"
	}
	cfg, err := Load(root)
	if err != nil {
		return true
	}
	return getter(cfg)
}

// WatchEnabled reports whether the embedded watcher is active. Default is
// true; set watch: false in config.yml to disable it.
func (c *Config) WatchEnabled() bool {
	if c.Watch != nil {
		return *c.Watch
	}
	return true
}

// IsWatchEnabled checks the SENSE_WATCH env var first, then falls back to
// the config file. A missing or unparseable config defaults to enabled.
func IsWatchEnabled(root string) bool {
	return envOrConfigBool("SENSE_WATCH", (*Config).WatchEnabled, root)
}

// Load reads .sense/config.yml under root. A missing file returns
// defaults. An unparseable file returns an error.
func Load(root string) (*Config, error) {
	c := &Config{
		Scan: ScanConfig{MaxFileSizeKB: DefaultMaxFileSizeKB},
	}

	path := filepath.Join(root, ".sense", "config.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, err
	}

	if c.Scan.MaxFileSizeKB <= 0 {
		c.Scan.MaxFileSizeKB = DefaultMaxFileSizeKB
	}

	return c, nil
}
