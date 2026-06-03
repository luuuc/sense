package freshen

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEmbedLifecycleStartComplete(t *testing.T) {
	embedDone := make(chan struct{})
	var completed bool

	ctl := &embedController{
		enabled:   true,
		ctx:       context.Background(),
		log:       func(_ string, _ ...any) {},
		checkDebt: func(_ context.Context) (int, error) { return 5, nil },
		runEmbed: func(_ context.Context) (int, error) {
			defer close(embedDone)
			return 5, nil
		},
	}

	ctl.Start()

	select {
	case <-embedDone:
		completed = true
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for embed to complete")
	}

	if !completed {
		t.Error("expected embed to complete")
	}
}

func TestEmbedLifecycleCancelBeforeComplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	embedStarted := make(chan struct{})
	embedBlock := make(chan struct{})

	ctl := &embedController{
		enabled:   true,
		ctx:       ctx,
		log:       func(_ string, _ ...any) {},
		checkDebt: func(_ context.Context) (int, error) { return 5, nil },
		runEmbed: func(ctx context.Context) (int, error) {
			close(embedStarted)
			<-ctx.Done()
			<-embedBlock
			return 0, ctx.Err()
		},
	}

	ctl.Start()
	<-embedStarted

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
		enabled:   true,
		ctx:       context.Background(),
		log:       func(_ string, _ ...any) {},
		checkDebt: func(_ context.Context) (int, error) { return 0, nil },
		runEmbed: func(_ context.Context) (int, error) {
			embedCalled = true
			return 0, nil
		},
	}

	ctl.Start()
	if embedCalled {
		t.Error("runEmbed should not be called when debt is zero")
	}
}

func TestEmbedLifecycleDisabled(t *testing.T) {
	var embedCalled bool
	ctl := &embedController{
		enabled:   false,
		ctx:       context.Background(),
		log:       func(_ string, _ ...any) {},
		checkDebt: func(_ context.Context) (int, error) { return 5, nil },
		runEmbed: func(_ context.Context) (int, error) {
			embedCalled = true
			return 0, nil
		},
	}

	ctl.Start()
	if embedCalled {
		t.Error("runEmbed should not be called when embeddings are disabled")
	}
}

func TestEmbedLifecycleDoubleCancel(t *testing.T) {
	ctl := &embedController{
		enabled:   true,
		ctx:       context.Background(),
		log:       func(_ string, _ ...any) {},
		checkDebt: func(_ context.Context) (int, error) { return 5, nil },
		runEmbed: func(ctx context.Context) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	ctl.Start()
	time.Sleep(50 * time.Millisecond)

	// Double cancel should not panic.
	ctl.Cancel()
	ctl.Cancel()
	t.Log("double cancel completed without panic")
}

func TestEmbedLifecycleDebtError(t *testing.T) {
	var logged bool
	ctl := &embedController{
		enabled:   true,
		ctx:       context.Background(),
		checkDebt: func(_ context.Context) (int, error) { return 0, errors.New("db error") },
		log: func(_ string, _ ...any) {
			logged = true
		},
		runEmbed: func(_ context.Context) (int, error) {
			return 0, nil
		},
	}

	ctl.Start()

	if !logged {
		t.Error("expected log call for debt check error")
	}
}
