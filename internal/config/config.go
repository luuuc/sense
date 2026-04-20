// Package config loads Sense's project-level configuration from
// .sense/config.yml. Missing file is fine — defaults apply.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const DefaultMaxFileSizeKB = 512

type Config struct {
	Ignore []string `yaml:"ignore"`
	Scan   ScanConfig `yaml:"scan"`
}

type ScanConfig struct {
	MaxFileSizeKB int `yaml:"max_file_size_kb"`
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
