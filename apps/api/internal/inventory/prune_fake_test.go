package inventory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakePrunerStore records the cutoffs it is asked to delete before.
type fakePrunerStore struct {
	mu      sync.Mutex
	cutoffs []time.Time
	deleted int64
	err     error
	calls   chan struct{}
}

func (f *fakePrunerStore) DeleteClientEventsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	f.cutoffs = append(f.cutoffs, cutoff)
	f.mu.Unlock()
	if f.calls != nil {
		select {
		case f.calls <- struct{}{}:
		default:
		}
	}
	return f.deleted, f.err
}

func (f *fakePrunerStore) lastCutoff() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cutoffs[len(f.cutoffs)-1]
}

func (f *fakePrunerStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cutoffs)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A zero-value config must mean the 30-day default, never "delete everything":
// a cutoff of now() would remove the whole table.
func TestPrunerZeroValueConfigFallsBackToTheLockedRetention(t *testing.T) {
	store := &fakePrunerStore{}
	p := NewPruner(PrunerConfig{Repo: store, Logger: quietLogger()})

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }

	result := p.pruneOnce(context.Background())

	want := now.Add(-30 * 24 * time.Hour)
	if !result.Threshold.Equal(want) {
		t.Fatalf("threshold = %v, want %v (30 days before now)", result.Threshold, want)
	}
	if got := store.lastCutoff(); !got.Equal(want) {
		t.Fatalf("store received cutoff %v, want %v", got, want)
	}
	if p.cfg.Interval != time.Hour {
		t.Fatalf("interval = %s, want 1h default", p.cfg.Interval)
	}
}

// The configured window is what reaches the store, and it is computed from the
// clock at the start of the run.
func TestPrunerUsesTheConfiguredRetention(t *testing.T) {
	store := &fakePrunerStore{deleted: 7}
	p := NewPruner(PrunerConfig{
		Repo:      store,
		Logger:    quietLogger(),
		Retention: 45 * time.Minute,
		Interval:  time.Minute,
	})

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }

	result := p.pruneOnce(context.Background())

	if want := now.Add(-45 * time.Minute); !result.Threshold.Equal(want) {
		t.Fatalf("threshold = %v, want %v", result.Threshold, want)
	}
	if result.Deleted != 7 {
		t.Fatalf("deleted = %d, want 7 (the count the store reported)", result.Deleted)
	}
	if store.count() != 1 {
		t.Fatalf("store called %d times, want 1", store.count())
	}
}

// A failing store is reported, not swallowed: the run must still return the
// rows that were deleted before the failure.
func TestPrunerReportsFailuresWithoutHidingProgress(t *testing.T) {
	store := &fakePrunerStore{deleted: 3, err: errors.New("database is on fire")}
	p := NewPruner(PrunerConfig{Repo: store, Logger: quietLogger()})

	result := p.pruneOnce(context.Background())

	if result.Deleted != 3 {
		t.Fatalf("deleted = %d, want 3", result.Deleted)
	}
}

// Run prunes at startup and then on the interval, and returns when the context
// is cancelled — the shape the API relies on for a scheduled job.
func TestPrunerRunPrunesOnStartupAndOnSchedule(t *testing.T) {
	store := &fakePrunerStore{calls: make(chan struct{}, 8)}
	p := NewPruner(PrunerConfig{
		Repo:      store,
		Logger:    quietLogger(),
		Retention: time.Hour,
		Interval:  10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	// Startup run, then at least one scheduled run.
	for i := 0; i < 2; i++ {
		select {
		case <-store.calls:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatalf("pruner ran %d times, want at least %d", store.count(), i+1)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil after cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
