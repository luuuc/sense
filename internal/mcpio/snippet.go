package mcpio

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/luuuc/sense/internal/blast"
)

const (
	DefaultContextLines = 2
	SnippetCap          = 10
)

type CallSite struct {
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

type SnippetReader struct {
	root         string
	contextLines int
	count        int
}

func NewSnippetReader(root string, contextLines int) *SnippetReader {
	if contextLines < 0 {
		contextLines = 0
	}
	return &SnippetReader{root: root, contextLines: contextLines}
}

func (r *SnippetReader) Enabled() bool {
	return r != nil && r.contextLines > 0
}

func (r *SnippetReader) Exhausted() bool {
	return r == nil || r.count >= SnippetCap
}

func (r *SnippetReader) Truncated(totalEdges int) bool {
	return r != nil && r.count >= SnippetCap && totalEdges > r.count
}

func (r *SnippetReader) Read(ctx context.Context, relPath string, line int) *CallSite {
	if r == nil || r.contextLines <= 0 || r.count >= SnippetCap || line <= 0 || relPath == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return nil
	}
	abs := filepath.Join(r.root, relPath)
	f, err := os.Open(abs)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	start := line - r.contextLines
	if start < 1 {
		start = 1
	}
	end := line + r.contextLines

	var lines []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum > end {
			break
		}
		if lineNum >= start {
			lines = append(lines, scanner.Text())
		}
	}
	if len(lines) == 0 {
		return nil
	}
	r.count++
	return &CallSite{
		Line:    line,
		Snippet: strings.Join(lines, "\n"),
	}
}

// ReadBlastEdgeSite resolves an edge site from a blast result into a snippet.
func (r *SnippetReader) ReadBlastEdgeSite(ctx context.Context, callerID int64, sites map[int64]blast.EdgeSite, files FileLookup) *CallSite {
	if !r.Enabled() || r.Exhausted() {
		return nil
	}
	es, ok := sites[callerID]
	if !ok || es.Line == nil {
		return nil
	}
	if es.FileID == nil {
		return nil
	}
	edgeFile, ok := files(*es.FileID)
	if !ok {
		return nil
	}
	return r.Read(ctx, edgeFile, *es.Line)
}
