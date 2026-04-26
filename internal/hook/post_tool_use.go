package hook

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// 4s leaves 1s headroom for process startup within the 5s hook timeout.
const postToolUseTimeout = 4 * time.Second

type postToolUseInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

func handlePostToolUse(ctx context.Context, input json.RawMessage, adapter *sqlite.Adapter, dir string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, postToolUseTimeout)
	defer cancel()

	var req postToolUseInput
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, err
	}

	path := extractWrittenPath(req)
	if path == "" {
		return nil, nil
	}

	rel, err := filepath.Rel(dir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, nil
	}

	if strings.HasPrefix(filepath.ToSlash(rel), ".sense/") {
		return nil, nil
	}

	if extract.ForExtension(filepath.Ext(path)) == nil {
		return nil, nil
	}

	matcher := ignore.New(ignore.DefaultPatterns()...)
	if matcher.Match(filepath.ToSlash(rel), false) {
		return nil, nil
	}

	_, err = scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:              dir,
		Idx:               adapter,
		Matcher:           matcher,
		EmbeddingsEnabled: false,
		Output:            io.Discard,
		Warnings:          io.Discard,
		Changed:           []string{rel},
	})
	return nil, err
}

func extractWrittenPath(req postToolUseInput) string {
	var input struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if err := json.Unmarshal(req.ToolInput, &input); err != nil {
		return ""
	}

	switch req.ToolName {
	case "Write", "Edit":
		return input.FilePath
	case "NotebookEdit":
		return input.NotebookPath
	default:
		return ""
	}
}
