package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type warningKind int

const (
	warnParseFailed warningKind = iota
	warnFileTooLarge
	warnWriteFailed
	warnMetaError
)

func (k warningKind) label() string {
	switch k {
	case warnParseFailed:
		return "parse failed"
	case warnFileTooLarge:
		return "file too large"
	case warnWriteFailed:
		return "write failed"
	case warnMetaError:
		return "meta error"
	default:
		return "unknown"
	}
}

// warningCollector accumulates per-file warnings during a scan,
// grouped by category. Thread-safe for use from parallel parse workers.
type warningCollector struct {
	mu      sync.Mutex
	entries map[warningKind][]string
}

func newWarningCollector() *warningCollector {
	return &warningCollector{entries: make(map[warningKind][]string)}
}

func (wc *warningCollector) add(kind warningKind, detail string) {
	wc.mu.Lock()
	wc.entries[kind] = append(wc.entries[kind], detail)
	wc.mu.Unlock()
}

func (wc *warningCollector) count() int {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	n := 0
	for _, v := range wc.entries {
		n += len(v)
	}
	return n
}

// format returns the grouped warning output for the log file.
func (wc *warningCollector) format() string {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	total := 0
	for _, v := range wc.entries {
		total += len(v)
	}
	if total == 0 {
		return ""
	}

	// Stable ordering by kind value.
	kinds := make([]warningKind, 0, len(wc.entries))
	for k := range wc.entries {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })

	var b strings.Builder
	fmt.Fprintf(&b, "Warnings (%d):\n", total)
	for _, k := range kinds {
		details := wc.entries[k]
		fmt.Fprintf(&b, "  %dx %s\n", len(details), k.label())
		for _, d := range details {
			fmt.Fprintf(&b, "     %s\n", d)
		}
	}
	return b.String()
}

// writeLog writes the grouped warning output to .sense/warnings.log.
// Overwrites the file each scan. Returns nil if there are no warnings
// (and removes a stale log file if one exists).
func (wc *warningCollector) writeLog(senseDir string) error {
	path := filepath.Join(senseDir, "warnings.log")
	content := wc.format()
	if content == "" {
		_ = os.Remove(path)
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
