package scan_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/scan"
)

// BenchmarkScan measures end-to-end scan throughput on a small but
// heterogeneous corpus: one Go file, one Ruby file, one Python file,
// one TypeScript file. The fixture is written to a fresh t.TempDir
// on each iteration's setup — the timed region only covers scan.Run.
//
// A regression here after a grammar upgrade or a change to the
// resolution strategy is a signal to profile before merging.
func BenchmarkScan(b *testing.B) {
	fixtures := map[string]string{
		"demo.go": `package demo

const Greeting = "hi"

type User struct {
	Name string
}

func (u User) Greet() string {
	return Greeting
}

func New() User {
	return User{}
}
`,
		"mix.rb": `class Base
  def hello
  end
end

class Child < Base
  def greet
  end
end
`,
		"util.py": `MAX = 100

class Service:
    def run(self):
        pass
`,
		"api.ts": `export class Api {
  greet(): string { return "hi"; }
}

export const VERSION = "1.0";
`,
	}

	// The benchmark is gated on `for b.Loop()` — Go 1.24+ idiom that
	// also handles the initial ResetTimer so setup done before the
	// loop doesn't count. Per-iteration setup (fresh tempdir so we
	// don't reuse the previous iteration's .sense/) and cleanup both
	// run with the timer paused so only scan.Run is measured.
	for b.Loop() {
		b.StopTimer()
		root, err := os.MkdirTemp("", "sense-bench")
		if err != nil {
			b.Fatal(err)
		}
		for name, src := range fixtures {
			if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()

		if _, err := scan.Run(context.Background(), scan.Options{
			Root:     root,
			Output:   &bytes.Buffer{},
			Warnings: io.Discard,
		}); err != nil {
			b.Fatalf("scan.Run: %v", err)
		}

		b.StopTimer()
		_ = os.RemoveAll(root)
		b.StartTimer()
	}
}
