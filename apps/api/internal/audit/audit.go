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
	// ActorUserID is the operator's UUID when the action comes from a logged-in session.
	ActorUserID *string
	// ActorType is 'system' for unattended writers, 'user' or 'operator' for humans.
	ActorType string
	// ActorLabel names the writer without joining to identity (e.g. "sync-worker" or operator email).
	ActorLabel string
	// Action is the stable verb, e.g. "sync.cycle" or "device.restart".
	Action string
	// TargetType/TargetID point at the affected row, when there is one.
	TargetType string
	TargetID   string
	// SiteID is the optional site UUID scope.
	SiteID *string
	// RequestID correlates the audit row with HTTP/log request_id.
	RequestID string
	// IP is the optional client IP address.
	IP string
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
INSERT INTO audit_log (actor_user_id, actor_type, actor_label, action, target_type, target_id, site_id, request_id, ip, detail)
VALUES (NULLIF($1, '')::uuid, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, '')::uuid, NULLIF($8, ''), NULLIF($9, '')::inet, $10)
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
	var actorUserID, siteID string
	if e.ActorUserID != nil {
		actorUserID = *e.ActorUserID
	}
	if e.SiteID != nil {
		siteID = *e.SiteID
	}
	var id int64
	var createdAt time.Time
	if err := r.pool.QueryRow(ctx, insertEntryWithReturn,
		actorUserID, actorType, e.ActorLabel, e.Action, e.TargetType, e.TargetID,
		siteID, e.RequestID, e.IP, payload,
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
