package webhook

// Fork (Agenda Maestros 4x4): the team's predefined event types ("tipos predefinidos").
// The owner names two templates - "Mentoría privada" T and "Soporte 1 a 1" S - and every
// person of the matching área (mentoría, soporte) gets a COPY of it: their own bookable
// link, owned by the template's owner and kept in step with it by the reconcile
// (internal/handler/fork_team.go). The rows that say "this type is a copy of that one"
// live here, in the webhook package, because this package's SQL reads them: the
// template-aware event-type filter (matchingWebhooks) and the WhatsApp texts a copy
// inherits (whatsAppTemplate). A missing table there would silently stop every delivery,
// so they are part of EnsureForkSchema and a failure stops the boot.
//
// Everything else the team feature stores (áreas, invite roles, the attendance records of
// the video room) is in EnsureTeamSchema, a sibling run on the same boot path.

import (
	"database/sql"
	"fmt"
)

// forkTeamLinkSchema is created by EnsureForkSchema with the rest of the fork's schema, in
// code, NOT by goose (see forkSchema). Plain tables and indexes, no trigger.
//
//   - fork_event_type_links: one row per copy (kind 'copy': one person's copy of a template)
//     or holder (kind 'holder': the hidden per-template type that keeps the questions a
//     template retired while clients' answers still point at them). copy_id CASCADEs with
//     its event type; template_id has NO foreign key on purpose - a link must survive the
//     template being changed or deleted, so the copy keeps its old texts and webhook
//     matching for the bookings it already has. One copy per (template, user).
//   - fork_question_links: which copy question mirrors which template question, so the
//     reconcile updates copy questions IN PLACE (their ids, and so the clients' answers,
//     survive). No foreign key on template_question_id either: the reconcile must be able
//     to see that a template question was deleted.
var forkTeamLinkSchema = []string{
	`CREATE TABLE IF NOT EXISTS fork_event_type_links (
		copy_id     TEXT PRIMARY KEY REFERENCES event_types(id) ON DELETE CASCADE,
		template_id TEXT NOT NULL,
		user_id     TEXT NOT NULL,
		kind        TEXT NOT NULL DEFAULT 'copy',
		created_at  TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_fork_event_type_links_template
		ON fork_event_type_links (template_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_fork_event_type_links_copy_user
		ON fork_event_type_links (template_id, user_id) WHERE kind = 'copy'`,
	`CREATE TABLE IF NOT EXISTS fork_question_links (
		copy_question_id     TEXT PRIMARY KEY REFERENCES event_type_questions(id) ON DELETE CASCADE,
		template_question_id TEXT NOT NULL,
		copy_id              TEXT NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_fork_question_links_copy_template
		ON fork_question_links (copy_id, template_question_id)`,
}

// forkTeamSchema is created by EnsureTeamSchema: plain fork_ tables (a generic name could
// collide with a future upstream CREATE TABLE, which does not say IF NOT EXISTS), foreign
// keys only to non-leaf upstream tables the fork already references, never to users.
//
//   - fork_member_areas: what a person attends ('mentoria' | 'soporte'); no row = nothing.
//     Independent of the permission tier (users.is_owner / is_admin).
//   - fork_template_areas: the área ('mentoria' | 'soporte') each template served while it
//     was a setting, written by the reconcile. A copy of a template that is no longer a
//     setting keeps its área through it (its bookings stay Mentoría or Soporte); a template
//     with no row (a copy made before Soporte was a template) is Mentoría.
//   - fork_invite_roles: the role an invite grants on claim ('mentoria' | 'soporte' |
//     'admin'), keyed by the lowercased email because a resend re-creates the invite row.
//   - fork_livekit_mints / fork_livekit_sessions / fork_livekit_host_links: attendance of
//     the built-in video room (who got a room token, the LiveKit webhook's sessions, and
//     the hash of the host link a booking's current host holds).
var forkTeamSchema = []string{
	`CREATE TABLE IF NOT EXISTS fork_member_areas (
		user_id    TEXT PRIMARY KEY,
		area       TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS fork_template_areas (
		template_id TEXT PRIMARY KEY,
		area        TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS fork_invite_roles (
		email      TEXT PRIMARY KEY,
		role       TEXT NOT NULL,
		created_by TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS fork_livekit_mints (
		identity   TEXT PRIMARY KEY,
		booking_id TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
		kind       TEXT NOT NULL,
		user_id    TEXT,
		verified   INTEGER NOT NULL,
		minted_at  TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_fork_livekit_mints_booking
		ON fork_livekit_mints (booking_id)`,
	`CREATE TABLE IF NOT EXISTS fork_livekit_sessions (
		participant_sid TEXT PRIMARY KEY,
		identity        TEXT NOT NULL,
		booking_id      TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
		joined_at       TEXT,
		left_at         TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_fork_livekit_sessions_booking
		ON fork_livekit_sessions (booking_id)`,
	`CREATE TABLE IF NOT EXISTS fork_livekit_host_links (
		booking_id TEXT PRIMARY KEY REFERENCES bookings(id) ON DELETE CASCADE,
		token_hash TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`,
	// attendance_since: bookings that started before the attendance records existed read
	// "not_applicable", never a false "nadie entró". Set once, on the first boot that has it.
	`INSERT OR IGNORE INTO fork_settings (key, value)
		VALUES ('attendance_since', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))`,
}

// EnsureTeamSchema creates the team feature's fork tables if missing (see forkTeamSchema)
// and records attendance_since once. Idempotent. Runs right after EnsureForkSchema, which
// creates fork_settings; the boot path refuses to start without it, New tries it best
// effort for the callers that skip that path (handler.New, tests).
func EnsureTeamSchema(db *sql.DB) error {
	for _, stmt := range forkTeamSchema {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("webhook: team schema: %w", err)
		}
	}
	return nil
}

// forkEventTypeFilterClause is matchingWebhooks' event-type filter (fork_event_types.go),
// made template-aware: a filtered webhook keeps a booking whose type it lists OR whose
// type is a copy of a template it lists, so the owner's webhook limited to "Mentoría
// privada" (or "Soporte 1 a 1") also carries every copy of it. It reads ONLY the links, never fork_settings:
// a copy of a template that is no longer the setting keeps matching for the bookings it
// already has. It cannot widen anything: the NOT EXISTS branch is unchanged, copy_id is
// the primary key (at most one template, one level), and a missing link is NULL - no
// match. For every non-copy booking the match set is identical to the plain filter's.
// Takes one argument: the booking id.
const forkEventTypeFilterClause = `(NOT EXISTS (SELECT 1 FROM webhook_event_type_filters f WHERE f.webhook_id = w.id)
		       OR EXISTS (SELECT 1 FROM webhook_event_type_filters f
		                  JOIN bookings b ON b.id = ?
		                  WHERE f.webhook_id = w.id
		                    AND (f.event_type_id = b.event_type_id
		                         OR f.event_type_id = (SELECT l.template_id FROM fork_event_type_links l
		                                               WHERE l.copy_id = b.event_type_id AND l.kind = 'copy'))))`
