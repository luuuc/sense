package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1K"},
		{6400, "6K"},
		{138200, "138K"},
		{999999, "1000K"},
		{1000000, "1.0M"},
		{4200000, "4.2M"},
		{10500000, "10.5M"},
	}
	for _, tt := range tests {
		got := formatTokens(tt.n)
		if got != tt.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestEmbeddingsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		cfgYAML string
		want    bool
	}{
		{"default (no env, no config)", "", "", true},
		{"env false", "false", "", false},
		{"env FALSE", "FALSE", "", false},
		{"env 0", "0", "", false},
		{"env true", "true", "", true},
		{"env 1", "1", "", true},
		{"config disabled", "", "embeddings:\n  enabled: false\n", false},
		{"config enabled", "", "embeddings:\n  enabled: true\n", true},
		{"env overrides config", "false", "embeddings:\n  enabled: true\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()

			if tt.env != "" {
				t.Setenv("SENSE_EMBEDDINGS", tt.env)
			} else {
				t.Setenv("SENSE_EMBEDDINGS", "")
			}

			if tt.cfgYAML != "" {
				senseDir := filepath.Join(root, ".sense")
				if err := os.MkdirAll(senseDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(senseDir, "config.yml"), []byte(tt.cfgYAML), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got := EmbeddingsEnabled(root)
			if got != tt.want {
				t.Errorf("EmbeddingsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
