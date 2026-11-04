package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry is one append-only audit record. The trail is never updated or deleted
// by the application (ADR-003 §5); retention pruning is deliberately out of
// scope for now.
type Entry struct {
	// ActorType is 'system' for unattended writers, 'operator' for humans.
	ActorType string
	// ActorLabel names the writer without joining to identity (e.g. "sync-worker").
	ActorLabel string
	// Action is the stable verb, e.g. "sync.cycle".
	Action string
	// TargetType/TargetID point at the affected row, when there is one.
	TargetType string
	TargetID   string
	// Detail carries the structured payload; nil is stored as an empty object.
	Detail map[string]any
}

// Recorder writes audit entries. Callers treat a failure as a degraded trail,
// not as a failure of the operation being audited.
type Recorder interface {
	Record(ctx context.Context, e Entry) error
	RecordAction(ctx context.Context, e Entry) (int64, time.Time, error)
}

// PostgresRecorder appends to audit_log.
type PostgresRecorder struct {
	pool *pgxpool.Pool
}

// NewPostgresRecorder returns a Recorder backed by pool.
func NewPostgresRecorder(pool *pgxpool.Pool) *PostgresRecorder {
	return &PostgresRecorder{pool: pool}
}

const insertEntryWithReturn = `
INSERT INTO audit_log (actor_type, actor_label, action, target_type, target_id, detail)
VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6)
RETURNING id, created_at`

// RecordAction appends e to the trail and returns the new audit id and timestamp.
func (r *PostgresRecorder) RecordAction(ctx context.Context, e Entry) (int64, time.Time, error) {
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("audit: encode detail: %w", err)
	}
	actorType := e.ActorType
	if actorType == "" {
		actorType = "system"
	}
	var id int64
	var createdAt time.Time
	if err := r.pool.QueryRow(ctx, insertEntryWithReturn,
		actorType, e.ActorLabel, e.Action, e.TargetType, e.TargetID, payload,
	).Scan(&id, &createdAt); err != nil {
		return 0, time.Time{}, fmt.Errorf("audit: insert entry: %w", err)
	}
	return id, createdAt, nil
}

// Record appends e to the trail.
func (r *PostgresRecorder) Record(ctx context.Context, e Entry) error {
	_, _, err := r.RecordAction(ctx, e)
	return err
}
