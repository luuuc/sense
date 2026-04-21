package watch

import "testing"

func TestRunOptionsMCPDefaultFalse(t *testing.T) {
	var opts RunOptions
	if opts.MCP {
		t.Error("RunOptions.MCP zero value should be false")
	}
}
