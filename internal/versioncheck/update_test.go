package versioncheck

import (
	"path/filepath"
	"testing"
)

func TestInGopathBin(t *testing.T) {
	tests := []struct {
		name   string
		exe    string
		gopath string
		want   bool
	}{
		{"normal install path", "/usr/local/bin/sense", "/home/user/go", false},
		{"gopath bin", "/home/user/go/bin/sense", "/home/user/go", true},
		{"similar prefix", "/home/user/go-tools/bin/sense", "/home/user/go", false},
		{"home local bin", filepath.Join("/home/user/.local/bin", "sense"), "/home/user/go", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inGopathBin(tt.exe, tt.gopath); got != tt.want {
				t.Errorf("inGopathBin(%q, %q) = %v, want %v", tt.exe, tt.gopath, got, tt.want)
			}
		})
	}
}
