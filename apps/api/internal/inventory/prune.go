package inventory

import (
	"context"
	"io"
	"log/slog"
	"time"
)

const (
	// defaultClientEventsRetention is the phase-1 window locked by ADR-005:
	// 30 days, counted from occurred_at.
	defaultClientEventsRetention = 30 * 24 * time.Hour
	// defaultClientEventsPruneInterval keeps the overshoot of the threshold
	// small — at most one hour on top of 30 days — while each run only has to
	// delete the transitions that crossed the line since the previous run.
	defaultClientEventsPruneInterval = time.Hour
)

// ClientEventPruner is the store surface the retention job needs. It is
// deliberately smaller than Repository: the job deletes, it never writes cache
// rows.
type ClientEventPruner interface {
	// DeleteClientEventsBefore removes every client_events row whose
	// occurred_at is strictly older than cutoff and returns how many rows it
	// deleted. Rows at exactly cutoff are kept: the rule is "older than the
	// threshold", not "at or before it".
	DeleteClientEventsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// PrunerConfig configures the scheduled client_events retention job (ADR-005).
// Repo is required.
type PrunerConfig struct {
	Repo   ClientEventPruner
	Logger *slog.Logger

	// Retention is how much history client_events keeps. Values <= 0 fall back
	// to 30 days: a zero-value config must never mean "delete everything".
	Retention time.Duration
	// Interval is how often the job runs. Values <= 0 fall back to one hour.
	Interval time.Duration
}

// Pruner deletes client_events rows older than the retention threshold. It is
// the only deleter of that table, and it runs inside the API process — not in
// pg_cron or a database extension — so it stays part of the single deployable
// and is testable without a modified Postgres (ADR-005).
type Pruner struct {
	cfg PrunerConfig
	now func() time.Time
}

// pruneResult reports one run of the job.
type pruneResult struct {
	// Threshold is the cutoff the run used: every row older than this was
	// deleted, and no row newer than it was touched.
	Threshold time.Time
	Deleted   int64
	Duration  time.Duration
}

// NewPruner returns a Pruner ready to Run.
func NewPruner(cfg PrunerConfig) *Pruner {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Retention <= 0 {
		cfg.Retention = defaultClientEventsRetention
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultClientEventsPruneInterval
	}
	return &Pruner{cfg: cfg, now: time.Now}
}

// Run prunes once at startup and then every Interval until ctx is cancelled,
// returning nil. A failed run is logged and retried on the next tick: this is a
// maintenance job, and stopping the loop would turn one bad run into no
// retention at all.
func (p *Pruner) Run(ctx context.Context) error {
	p.pruneOnce(ctx)

	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.pruneOnce(ctx)
		}
	}
}

// pruneOnce deletes everything older than the threshold and records the outcome
// in one structured log line, deleted count included, as ADR-005 requires.
//
// The cutoff is computed once per run from the same clock, so two consecutive
// runs cannot disagree about the boundary; because the predicate is strict and
// the threshold moves forward, a second run deletes nothing and does not fail.
func (p *Pruner) pruneOnce(ctx context.Context) pruneResult {
	started := p.now().UTC()
	result := pruneResult{Threshold: started.Add(-p.cfg.Retention)}

	deleted, err := p.cfg.Repo.DeleteClientEventsBefore(ctx, result.Threshold)
	result.Deleted = deleted
	result.Duration = p.now().UTC().Sub(started)

	fields := []any{
		"module", "inventory",
		"rows_deleted", result.Deleted,
		"retention", p.cfg.Retention.String(),
		"threshold", result.Threshold,
		"duration_ms", result.Duration.Milliseconds(),
	}
	if err != nil {
		p.cfg.Logger.Error("client_events retention: prune failed", append(fields, "error", err)...)
		return result
	}
	p.cfg.Logger.Info("client_events retention: pruned", fields...)
	return result
}
