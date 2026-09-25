package webhook

// Fork (Agenda Maestros 4x4): limit a webhook to some event types ("tipos de atención").
// The owner sends WhatsApp through FunnelChat only for some appointments ("Soporte 1 a 1",
// "Mentoría privada") and not others ("Mentoría personal"); FunnelChat cannot branch on
// the payload, so the choice has to be made here, before a delivery exists.
//
// Semantics: a webhook with NO rows in webhook_event_type_filters receives every event
// type (the upstream behaviour, untouched). With rows, it receives only bookings whose
// bookings.event_type_id is one of them - for EVERY event (created, cancelled,
// rescheduled, reminder_*, recording/transcript/notes), since they all go through
// matchingWebhooks. A delivery whose booking cannot be resolved (no booking id) never
// reaches a filtered webhook: the filter is an allow-list and "unknown" is not on it.

import (
	"context"
	"database/sql"
	"fmt"
)

// forkSchema is created at boot by EnsureForkSchema, NOT by a goose migration: this fork's
// migration numbers 00066/00067 already collide with upstream's own 00066/00067, and every
// extra numbered file makes the upstream merge worse. Every statement is idempotent
// (IF NOT EXISTS), so running it on each boot - and from every New - is safe.
//
//   - The table: both foreign keys CASCADE, so deleting a webhook or an event type removes
//     its rows. db.Open sets PRAGMA foreign_keys=ON on the pool's single connection (and
//     pins it: max 1 conn, never recycled), which is what makes the CASCADE work. If it
//     were ever off, a deleted event type would leave its row behind and the webhook would
//     stay limited to a type that no longer exists - it would send less, never more.
//   - The index: SQLite looks child rows up by event_type_id on every event_types delete.
//   - The trigger keeps a filter from silently WIDENING. Without it, deleting the only
//     event type a webhook is limited to would cascade its last row away, and "no rows"
//     means "every type": the WhatsApp flow meant for one type would start messaging the
//     clients of all of them. So the webhook is switched off (is_active = 0) instead, in
//     the same statement. It runs BEFORE the delete so the rows are still there to look
//     at; a delete refused by a foreign key (an event type with bookings is RESTRICT)
//     backs the trigger's UPDATE out with it. A DROP TABLE of event_types (an upstream
//     table rebuild) drops the trigger and never fires it; the next boot re-creates it.
//     A rebuild of `webhooks` is the dangerous one: the trigger lives on event_types but
//     names webhooks, and since SQLite 3.26 ALTER TABLE ... RENAME re-checks every trigger
//     in the schema, so the standard CREATE webhooks_new / DROP webhooks / RENAME fails
//     with "error in trigger ...: no such table: main.webhooks" and the server would never
//     boot. That is why the boot path brackets db.Migrate: DropForkTrigger before it,
//     EnsureForkSchema right after (migrateWithForkSchema, cmd/calnode/fork_schema.go).
var forkSchema = []string{
	`CREATE TABLE IF NOT EXISTS webhook_event_type_filters (
		webhook_id    TEXT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
		event_type_id TEXT NOT NULL REFERENCES event_types(id) ON DELETE CASCADE,
		PRIMARY KEY (webhook_id, event_type_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_webhook_event_type_filters_event_type
		ON webhook_event_type_filters (event_type_id)`,
	`CREATE TRIGGER IF NOT EXISTS ` + forkTrigger + `
	BEFORE DELETE ON event_types
	BEGIN
		UPDATE webhooks SET is_active = 0
		WHERE id IN (SELECT webhook_id FROM webhook_event_type_filters WHERE event_type_id = OLD.id)
		  AND NOT EXISTS (SELECT 1 FROM webhook_event_type_filters o
		                  WHERE o.webhook_id = webhooks.id AND o.event_type_id <> OLD.id);
	END`,
}

// forkTrigger is the name of forkSchema's trigger (DropForkTrigger removes it).
const forkTrigger = "webhook_event_type_filters_never_widen"

// EnsureForkSchema creates the fork's webhook_event_type_filters table, its index and its
// trigger if missing (see forkSchema for why this is not a migration). Idempotent. The
// boot path runs it right after db.Migrate and refuses to start without it, like a failed
// migration; New also tries it (best effort) for callers that skip that path.
func EnsureForkSchema(db *sql.DB) error {
	for _, stmt := range forkSchema {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("webhook: fork schema: %w", err)
		}
	}
	return nil
}

// DropForkTrigger removes forkSchema's trigger so that db.Migrate can run an upstream
// migration that rebuilds `webhooks` (see forkSchema: the trigger would make its RENAME
// fail). Call it right before db.Migrate and EnsureForkSchema right after, which puts the
// trigger back; nothing serves requests in between. Idempotent (IF EXISTS).
func DropForkTrigger(db *sql.DB) error {
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + forkTrigger); err != nil {
		return fmt.Errorf("webhook: drop fork trigger: %w", err)
	}
	return nil
}

// CreateWithEventTypes is Create plus the event-type filter, written in the SAME
// transaction as the webhook row: a webhook briefly visible to Enqueue without its filter
// would message the clients of every type. Empty or nil eventTypeIDs = every type. The
// caller validates that the ids exist and are the user's to pick; an unknown id fails the
// foreign key and nothing is created.
func (s *Service) CreateWithEventTypes(ctx context.Context, userID, url string, events, eventTypeIDs []string) (*Webhook, string, error) {
	return s.create(ctx, userID, url, events, eventTypeIDs)
}

// SetEventTypes replaces the event-type filter of a webhook owned by userID, atomically:
// the old rows are deleted and the new ones inserted in one transaction, so Enqueue sees
// either the old filter or the new one, never an empty one in between. Empty = every
// type. Returns ErrNotFound when the webhook is not the user's.
func (s *Service) SetEventTypes(ctx context.Context, userID, webhookID string, eventTypeIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("webhook: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM webhooks WHERE id = ? AND user_id = ?`, webhookID, userID).Scan(&one)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("webhook: load for filter: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM webhook_event_type_filters WHERE webhook_id = ?`, webhookID); err != nil {
		return fmt.Errorf("webhook: clear event type filter: %w", err)
	}
	if err := insertEventTypeFilter(ctx, tx, webhookID, eventTypeIDs); err != nil {
		return err
	}
	return tx.Commit()
}

// insertEventTypeFilter writes a webhook's filter rows inside tx. Duplicates are skipped.
func insertEventTypeFilter(ctx context.Context, tx *sql.Tx, webhookID string, eventTypeIDs []string) error {
	for _, etID := range uniqueNonEmpty(eventTypeIDs) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO webhook_event_type_filters (webhook_id, event_type_id) VALUES (?, ?)`,
			webhookID, etID); err != nil {
			return fmt.Errorf("webhook: insert event type filter: %w", err)
		}
	}
	return nil
}

// attachEventTypeFilters fills EventTypeIDs on the user's webhooks with ONE query (no
// query per webhook). A webhook with no rows gets an empty, non-nil slice: every type.
func (s *Service) attachEventTypeFilters(ctx context.Context, userID string, whs []Webhook) error {
	byWebhook := make(map[string][]string, len(whs))
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.webhook_id, f.event_type_id
		FROM webhook_event_type_filters f
		JOIN webhooks w ON w.id = f.webhook_id
		LEFT JOIN event_types et ON et.id = f.event_type_id
		WHERE w.user_id = ?
		ORDER BY COALESCE(et.name, ''), f.event_type_id`, userID)
	if err != nil {
		return fmt.Errorf("webhook: list event type filters: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var whID, etID string
		if err := rows.Scan(&whID, &etID); err != nil {
			return fmt.Errorf("webhook: scan event type filter: %w", err)
		}
		byWebhook[whID] = append(byWebhook[whID], etID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range whs {
		whs[i].EventTypeIDs = byWebhook[whs[i].ID]
		if whs[i].EventTypeIDs == nil {
			whs[i].EventTypeIDs = []string{}
		}
	}
	return nil
}

// uniqueNonEmpty drops empty strings and repeats, keeping first-seen order.
func uniqueNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
