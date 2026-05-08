package watch

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockEmbedAdapter implements the minimal interface embedController needs.
type mockEmbedAdapter struct {
	debt int
	err  error
}

func (m *mockEmbedAdapter) EmbeddingDebtCount(_ context.Context) (int, error) {
	return m.debt, m.err
}

func TestEmbedLifecycleStartComplete(t *testing.T) {
	var logMsgs []string
	embedDone := make(chan struct{})

	ctl := &embedController{
		enabled:      true,
		ctx:          context.Background(),
		writeAdapter: &mockEmbedAdapter{debt: 5},
		log: func(format string, _ ...any) {
			logMsgs = append(logMsgs, format)
		},
		embedPending: func(_ context.Context, _ debtChecker, _, _ string) (int, error) {
			defer close(embedDone)
			return 5, nil
		},
	}

	ctl.Start()

	select {
	case <-embedDone:
		// embed completed
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for embed to complete")
	}

	found := false
	for _, msg := range logMsgs {
		if msg == "sense: background embed complete (%d symbols)" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected log message for embed completion")
	}
}

func TestEmbedLifecycleCancelBeforeComplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	embedStarted := make(chan struct{})
	embedBlock := make(chan struct{})

	ctl := &embedController{
		enabled:      true,
		ctx:          ctx,
		writeAdapter: &mockEmbedAdapter{debt: 5},
		log:          func(_ string, _ ...any) {},
		embedPending: func(ctx context.Context, _ debtChecker, _, _ string) (int, error) {
			close(embedStarted)
			<-ctx.Done()
			<-embedBlock
			return 0, ctx.Err()
		},
	}

	ctl.Start()
	<-embedStarted

	// Cancel should wait for the goroutine to exit.
	cancel()
	time.Sleep(50 * time.Millisecond)
	close(embedBlock)

	done := make(chan struct{})
	go func() {
		ctl.Cancel()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Cancel")
	}
}

func TestEmbedLifecycleDebtZero(t *testing.T) {
	var embedCalled bool
	ctl := &embedController{
		enabled:      true,
		ctx:          context.Background(),
		writeAdapter: &mockEmbedAdapter{debt: 0},
		log:          func(_ string, _ ...any) {},
		embedPending: func(_ context.Context, _ debtChecker, _, _ string) (int, error) {
			embedCalled = true
			return 0, nil
		},
	}

	ctl.Start()
	if embedCalled {
		t.Error("embedPending should not be called when debt is zero")
	}
}

func TestEmbedLifecycleDisabled(t *testing.T) {
	var embedCalled bool
	ctl := &embedController{
		enabled:      false,
		ctx:          context.Background(),
		writeAdapter: &mockEmbedAdapter{debt: 5},
		log:          func(_ string, _ ...any) {},
		embedPending: func(_ context.Context, _ debtChecker, _, _ string) (int, error) {
			embedCalled = true
			return 0, nil
		},
	}

	ctl.Start()
	if embedCalled {
		t.Error("embedPending should not be called when embeddings are disabled")
	}
}

func TestEmbedLifecycleDoubleCancel(t *testing.T) {
	t.Helper()
	ctl := &embedController{
		enabled:      true,
		ctx:          context.Background(),
		writeAdapter: &mockEmbedAdapter{debt: 5},
		log:          func(_ string, _ ...any) {},
		embedPending: func(ctx context.Context, _ debtChecker, _, _ string) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	ctl.Start()
	time.Sleep(50 * time.Millisecond)

	// Double cancel should not panic.
	ctl.Cancel()
	ctl.Cancel()
}

func TestEmbedLifecycleDebtError(t *testing.T) {
	var logMsgs []string
	ctl := &embedController{
		enabled:      true,
		ctx:          context.Background(),
		writeAdapter: &mockEmbedAdapter{err: errors.New("db error")},
		log: func(format string, _ ...any) {
			logMsgs = append(logMsgs, format)
		},
		embedPending: func(_ context.Context, _ debtChecker, _, _ string) (int, error) {
			return 0, nil
		},
	}

	ctl.Start()

	found := false
	for _, msg := range logMsgs {
		if msg == "sense: check embedding debt: %v" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected log message for debt check error")
	}
}
