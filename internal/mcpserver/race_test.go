package mcpserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/search"
)

// TestConcurrentEmbedSafety verifies that SetVectors (called by background
// embed goroutine) does not race with Search (called by request handlers).
// Run with `go test -race` to catch data races.
func TestConcurrentEmbedSafety(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	var wg sync.WaitGroup

	// Goroutine: simulate background embed periodically swapping vectors
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			// Toggle between nil and a mock index
			if i%2 == 0 {
				ts.handlers.search.SetVectors(nil)
			} else {
				ts.handlers.search.SetVectors(&mockVectorIndex{len: 10})
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Multiple goroutines: simulate concurrent requests
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{
					"query": "auth",
					"limit": 5,
				}))
				if err != nil {
					t.Logf("goroutine %d search error: %v", id, err)
				}
				time.Sleep(time.Millisecond)
			}
		}(g)
	}

	// Also exercise graph and blast handlers concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			_, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
				"symbol":    "auth.Verify",
				"direction": "callers",
			}))
			if err != nil {
				t.Logf("graph error: %v", err)
			}
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
}

type mockVectorIndex struct {
	len int
}

func (m *mockVectorIndex) Search(_ []float32, _ int) []search.VectorResult {
	return nil
}

func (m *mockVectorIndex) Len() int {
	return m.len
}
