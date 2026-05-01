package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarningCollector_GroupedFormat(t *testing.T) {
	wc := newWarningCollector()

	wc.add(warnParseFailed, "vendor/legacy.js (syntax error at line 42)")
	wc.add(warnParseFailed, "assets/generated.min.js (syntax error at line 1)")
	wc.add(warnParseFailed, "lib/old.js (nil parse tree)")
	wc.add(warnFileTooLarge, "assets/bundle.min.js (1024 KB > 512 KB max)")
	wc.add(warnFileTooLarge, "dist/vendor.js (2048 KB > 512 KB max)")
	wc.add(warnWriteFailed, "tmp/broken.go (disk full)")
	wc.add(warnMetaError, "config.rb (stat failed)")
	wc.add(warnMetaError, "lib/util.py (read failed)")
	wc.add(warnParseFailed, "test/bad.ts (extract failed)")
	wc.add(warnWriteFailed, "src/flaky.go (transaction rolled back)")

	if wc.count() != 10 {
		t.Fatalf("count = %d, want 10", wc.count())
	}

	out := wc.format()

	if !strings.HasPrefix(out, "Warnings (10):\n") {
		t.Errorf("missing header, got:\n%s", out)
	}
	if !strings.Contains(out, "4x parse failed") {
		t.Errorf("missing '4x parse failed' group, got:\n%s", out)
	}
	if !strings.Contains(out, "2x file too large") {
		t.Errorf("missing '2x file too large' group, got:\n%s", out)
	}
	if !strings.Contains(out, "2x write failed") {
		t.Errorf("missing '2x write failed' group, got:\n%s", out)
	}
	if !strings.Contains(out, "2x meta error") {
		t.Errorf("missing '2x meta error' group, got:\n%s", out)
	}
	if !strings.Contains(out, "     vendor/legacy.js (syntax error at line 42)") {
		t.Errorf("missing detail line, got:\n%s", out)
	}
}

func TestWarningCollector_WriteLog(t *testing.T) {
	dir := t.TempDir()
	wc := newWarningCollector()

	wc.add(warnParseFailed, "bad.go (syntax error)")
	wc.add(warnFileTooLarge, "huge.js (4096 KB > 512 KB max)")

	if err := wc.writeLog(dir); err != nil {
		t.Fatalf("writeLog: %v", err)
	}

	path := filepath.Join(dir, "warnings.log")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read warnings.log: %v", err)
	}
	if !strings.Contains(string(content), "Warnings (2):") {
		t.Errorf("log file missing header, got:\n%s", content)
	}
	if !strings.Contains(string(content), "parse failed") {
		t.Errorf("log file missing parse failed group, got:\n%s", content)
	}
	if !strings.Contains(string(content), "file too large") {
		t.Errorf("log file missing file too large group, got:\n%s", content)
	}
}

func TestWarningCollector_ZeroWarnings_NoLogFile(t *testing.T) {
	dir := t.TempDir()
	wc := newWarningCollector()

	if err := wc.writeLog(dir); err != nil {
		t.Fatalf("writeLog: %v", err)
	}

	path := filepath.Join(dir, "warnings.log")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected no warnings.log for zero warnings, but file exists")
	}
}

func TestWarningCollector_OverwritesStaleLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "warnings.log")
	if err := os.WriteFile(path, []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}

	wc := newWarningCollector()
	if err := wc.writeLog(dir); err != nil {
		t.Fatalf("writeLog: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected stale warnings.log to be removed when zero warnings")
	}
}
