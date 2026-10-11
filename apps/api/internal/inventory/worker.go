package inventory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Sham607/network-orchestrator/apps/api/internal/audit"
	"github.com/Sham607/network-orchestrator/apps/api/internal/unifi"
)

const (
	// breakerThreshold consecutive failed cycles open the breaker (ADR-003 §3).
	breakerThreshold = 5
	// breakerCooldown is how long the breaker stays open before a single probe
	// cycle is allowed through.
	breakerCooldown = 5 * time.Minute
	// finalWriteTimeout bounds the sync_runs/audit write of a finished cycle so
	// the record survives the shutdown that cancelled the cycle's context.
	finalWriteTimeout = 5 * time.Second
	// errKindInternal labels a failure that did not come from the adapter.
	errKindInternal = "internal_error"
)

// WorkerConfig configures a Worker. Client and Repo are required.
type WorkerConfig struct {
	Client unifi.Client
	Repo   Repository
	Audit  audit.Recorder
	Logger *slog.Logger

	// Surface is the UniFi surface Client speaks: cloud or console.
	Surface string
	// Interval is the polling period (ADR-002 §4: 60s).
	Interval time.Duration
	// StaleAfter is the age at which the cache stops satisfying /ready.
	StaleAfter time.Duration

	// SiteAllowlist optionally restricts sync to only sites whose ID or Name
	// matches an entry. When empty, all sites are synced (ZENI-19).
	SiteAllowlist []string
}

// Worker polls UniFi into the local cache and publishes its freshness.
//
// The loop is synchronous: a cycle never overlaps the next tick, which is the
// strongest form of the single-flight rule in ADR-003 §3. A cycle that the open
// circuit breaker suppresses is recorded with status 'skipped'.
type Worker struct {
	cfg WorkerConfig
	now func() time.Time

	mu        sync.RWMutex
	running   bool
	lastOK    *time.Time
	failures  int
	breaker   BreakerState
	openedAt  *time.Time
	reachable bool
	lastKind  string
	lastRun   *SyncRun
}

// NewWorker returns a Worker ready to Run.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = 180 * time.Second
	}
	return &Worker{cfg: cfg, now: time.Now, breaker: BreakerClosed}
}

// Run polls until ctx is cancelled, then returns nil. It seeds its freshness
// anchor from sync_runs so a restart does not report a healthy cache as stale.
func (w *Worker) Run(ctx context.Context) error {
	w.setRunning(true)
	defer w.setRunning(false)

	if ts, ok, err := w.cfg.Repo.LastSuccessfulSyncAt(ctx); err != nil {
		w.cfg.Logger.Warn("sync: cannot read freshness anchor", "error", err)
	} else if ok {
		w.setLastSuccess(ts)
	}

	w.runCycle(ctx, TriggerStartup)

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.runCycle(ctx, TriggerSchedule)
		}
	}
}

// Status reports the current freshness snapshot. It is safe to call from an
// HTTP handler while Run is working.
func (w *Worker) Status() Status {
	w.mu.RLock()
	defer w.mu.RUnlock()

	now := w.now().UTC()
	st := Status{
		WorkerRunning:       w.running,
		ConsecutiveFailures: w.failures,
		StaleAfter:          w.cfg.StaleAfter,
		Breaker:             w.breaker,
		Surface:             w.cfg.Surface,
		Reachable:           w.reachable,
		LastErrorKind:       w.lastKind,
	}
	if w.lastOK != nil {
		ts := *w.lastOK
		st.LastSuccessfulSyncAt = &ts
	}
	st.Stale = st.LastSuccessfulSyncAt == nil || now.Sub(*st.LastSuccessfulSyncAt) > w.cfg.StaleAfter
	if w.openedAt != nil {
		ts := *w.openedAt
		st.BreakerOpenedAt = &ts
	}
	if w.lastRun != nil {
		run := *w.lastRun
		st.LastRun = &run
	}
	return st
}

// runCycle performs exactly one sync cycle and records it.
func (w *Worker) runCycle(ctx context.Context, trigger string) SyncRun {
	started := w.now().UTC()
	run := SyncRun{Surface: w.cfg.Surface, Trigger: trigger, Status: RunRunning, StartedAt: started}

	if !w.allowProbe(started) {
		run.Status = RunSkipped
		run.ErrorKind = w.errorKind()
		run.ErrorMessage = "sync skipped: circuit breaker is open"
		w.finish(ctx, &run)
		return run
	}

	if err := w.cfg.Client.Health(ctx); err != nil {
		w.setReachable(false)
		run.Status = RunFailed
		run.ErrorKind, run.ErrorMessage = classify(err)
		w.finish(ctx, &run)
		return run
	}
	w.setReachable(true)

	sites, err := w.cfg.Client.ListSites(ctx)
	if err != nil {
		run.Status = RunFailed
		run.ErrorKind, run.ErrorMessage = classify(err)
		w.finish(ctx, &run)
		return run
	}

	uniqueSites := make([]unifi.Site, 0, len(sites))
	seenSites := make(map[string]struct{})
	for _, s := range sites {
		if _, ok := seenSites[s.ID]; ok {
			continue
		}
		seenSites[s.ID] = struct{}{}
		uniqueSites = append(uniqueSites, s)
	}

	if len(w.cfg.SiteAllowlist) > 0 {
		allowed := make(map[string]bool, len(w.cfg.SiteAllowlist))
		for _, a := range w.cfg.SiteAllowlist {
			allowed[strings.ToLower(strings.TrimSpace(a))] = true
		}
		filtered := make([]unifi.Site, 0, len(uniqueSites))
		for _, s := range uniqueSites {
			if allowed[strings.ToLower(s.ID)] || allowed[strings.ToLower(s.Name)] {
				filtered = append(filtered, s)
			}
		}
		uniqueSites = filtered
	}
	run.SitesTotal = len(uniqueSites)

	var firstErr error
	for _, site := range uniqueSites {
		snap, err := w.collect(ctx, site, started)
		if err == nil {
			err = w.cfg.Repo.ApplySite(ctx, snap)
		}
		if err != nil {
			run.SitesFailed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		run.SitesOK++
		run.Devices += len(snap.Devices)
		run.Clients += len(snap.Clients)
		run.Networks += len(snap.Networks)
	}

	switch {
	case run.SitesFailed == 0:
		run.Status = RunSuccess
	case run.SitesOK == 0:
		run.Status = RunFailed
	default:
		run.Status = RunPartial
	}
	if firstErr != nil {
		run.ErrorKind, run.ErrorMessage = classify(firstErr)
	}

	w.finish(ctx, &run)
	return run
}

// collect gathers one site's children. Devices are required; clients and
// networks are best-effort because the cloud surface does not serve them
// (ADR-001), and a partially cached site is better than none (ADR-003 §3).
func (w *Worker) collect(ctx context.Context, site unifi.Site, at time.Time) (SiteSnapshot, error) {
	snap := SiteSnapshot{
		Site:      site,
		ConsoleID: site.ConsoleID,
		Source:    w.cfg.Surface,
		SyncedAt:  at,
	}
	devices, err := w.cfg.Client.ListDevices(ctx, site.ID)
	if err != nil {
		return snap, err
	}
	snap.Devices = devices

	if clients, err := w.cfg.Client.ListClients(ctx, site.ID); err == nil {
		snap.Clients = clients
	} else if isUnsupported(err) {
		w.cfg.Logger.Debug("sync: surface has no clients", "site_id", site.ID)
	} else {
		w.cfg.Logger.Warn("sync: clients unavailable", "site_id", site.ID, "error", err)
	}

	if networks, err := w.cfg.Client.ListNetworks(ctx, site.ID); err == nil {
		snap.Networks = networks
	} else if isUnsupported(err) {
		w.cfg.Logger.Debug("sync: surface has no networks", "site_id", site.ID)
	} else {
		w.cfg.Logger.Warn("sync: networks unavailable", "site_id", site.ID, "error", err)
	}
	return snap, nil
}

// finish stamps, persists and publishes a completed cycle.
func (w *Worker) finish(ctx context.Context, run *SyncRun) {
	run.FinishedAt = w.now().UTC()
	if run.DurationMS = int(run.FinishedAt.Sub(run.StartedAt).Milliseconds()); run.DurationMS < 0 {
		run.DurationMS = 0
	}

	// The record of a finished cycle must outlive a shutdown that cancelled the
	// cycle's own context.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalWriteTimeout)
	defer cancel()

	id, err := w.cfg.Repo.RecordSyncRun(writeCtx, *run)
	if err != nil {
		w.cfg.Logger.Error("sync: cannot record run", "error", err)
	} else {
		run.ID = id
	}
	w.recordAudit(writeCtx, *run)

	w.mu.Lock()
	defer w.mu.Unlock()
	recorded := *run
	w.lastRun = &recorded
	w.lastKind = run.ErrorKind
	switch run.Status {
	case RunSuccess, RunPartial:
		w.failures = 0
		w.breaker = BreakerClosed
		w.openedAt = nil
		ts := run.FinishedAt
		w.lastOK = &ts
	case RunFailed:
		if run.ErrorKind == errKindInternal {
			// A failure on our side (the database is down, say) must not trip the
			// breaker. It exists to stop hammering an unreachable controller, and
			// counting it would keep syncing suspended for the whole cooldown
			// even after the database recovered.
			break
		}
		w.failures++
		if w.breaker == BreakerHalfOpen || w.failures >= breakerThreshold {
			w.breaker = BreakerOpen
			opened := run.FinishedAt
			w.openedAt = &opened
		}
	}
}

func (w *Worker) recordAudit(ctx context.Context, run SyncRun) {
	if w.cfg.Audit == nil {
		return
	}
	detail := map[string]any{
		"surface":           run.Surface,
		"trigger":           run.Trigger,
		"status":            run.Status,
		"sites_total":       run.SitesTotal,
		"sites_ok":          run.SitesOK,
		"sites_failed":      run.SitesFailed,
		"devices_upserted":  run.Devices,
		"clients_upserted":  run.Clients,
		"networks_upserted": run.Networks,
		"duration_ms":       run.DurationMS,
	}
	if run.ErrorKind != "" {
		detail["error_kind"] = run.ErrorKind
	}
	entry := audit.Entry{
		ActorType:  "system",
		ActorLabel: "sync-worker",
		Action:     "sync.cycle",
		TargetType: "sync_run",
		TargetID:   run.ID,
		Detail:     detail,
	}
	if err := w.cfg.Audit.Record(ctx, entry); err != nil {
		w.cfg.Logger.Warn("sync: cannot write audit entry", "error", err)
	}
}

// allowProbe reports whether a cycle may run now, moving an open breaker into
// half_open once the cooldown has elapsed.
func (w *Worker) allowProbe(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.breaker != BreakerOpen {
		return true
	}
	if w.openedAt != nil && now.Sub(*w.openedAt) >= breakerCooldown {
		w.breaker = BreakerHalfOpen
		return true
	}
	return false
}

func (w *Worker) setRunning(v bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.running = v
}

func (w *Worker) setReachable(v bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reachable = v
}

func (w *Worker) setLastSuccess(ts time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastOK = &ts
}

func (w *Worker) errorKind() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastKind
}

// classify maps an error onto the sync_runs.error_kind vocabulary. Adapter
// messages are sanitized by construction (internal/unifi); our own errors are
// never echoed because they can carry connection details.
func classify(err error) (kind, message string) {
	var uerr *unifi.Error
	if errors.As(err, &uerr) {
		return string(uerr.Kind), uerr.Message
	}
	return errKindInternal, "sync cycle failed: internal error"
}

func isUnsupported(err error) bool {
	return errors.Is(err, &unifi.Error{Kind: unifi.ErrNotFound})
}
