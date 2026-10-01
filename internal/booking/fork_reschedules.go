package booking

// Fork (Agenda Maestros 4x4): the reschedule history of a booking (owner, 30 Sep 2026: the
// Pasadas tab must tag "reprogramadas"). Upstream overwrites start_at/end_at in place and
// keeps no trace of the old time (bookings.status never becomes 'rescheduled'), so every
// move is recorded in fork_booking_reschedules - a plain table created in code by
// webhook.EnsureTeamSchema, never by goose (CLAUDE.md, the fork's schema rule).
//
// ONE write point: Service.Reschedule, which every reschedule path goes through (the
// panel's PATCH /v1/bookings/{id}/reschedule, the client's /manage page, the MCP
// reschedule_booking tool). The row is written right after the commit, best effort: a
// missing table or a failed insert is logged and never fails a reschedule that already
// happened. Callers may say who moved it with WithRescheduleActor.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

type rescheduleActorKey struct{}

// WithRescheduleActor tags ctx with who is rescheduling ("panel:<user id>", "client",
// "mcp:<user id>"...), stored as fork_booking_reschedules.actor. Optional: none = NULL.
func WithRescheduleActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, rescheduleActorKey{}, actor)
}

func rescheduleActor(ctx context.Context) any {
	if a, ok := ctx.Value(rescheduleActorKey{}).(string); ok && a != "" {
		return a
	}
	return nil
}

// RescheduleActorBackfill is the actor of the rows rebuilt from webhook deliveries.
const RescheduleActorBackfill = "backfill:webhook"

// historyTime is how fork_booking_reschedules stores previous_start_at / new_start_at:
// RFC3339 in UTC to the second, the shape of the booking.rescheduled payload, so a live row
// and one rebuilt from a delivery compare equal.
func historyTime(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }

// recordReschedule is Service.Reschedule's hook, after the commit. Best effort.
func (s *Service) recordReschedule(ctx context.Context, bookingID string, previousStart, newStart time.Time) {
	if previousStart.Equal(newStart) {
		return // not a move
	}
	// The reschedule is committed: a client that hung up now must not lose its record.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO fork_booking_reschedules (id, booking_id, previous_start_at, new_start_at, rescheduled_at, actor)
		VALUES (?, ?, ?, ?, ?, ?)`,
		uid.New(), bookingID, historyTime(previousStart), historyTime(newStart),
		time.Now().UTC().Format(time.RFC3339Nano), rescheduleActor(ctx)); err != nil {
		slog.Default().WarnContext(ctx, "booking: record reschedule history", "error", err, "booking_id", bookingID)
	}
}

// RescheduleSummary is what the bookings list shows of a booking's history.
type RescheduleSummary struct {
	Count int
	// LastPreviousStart is the start the most recent reschedule moved the booking away from.
	LastPreviousStart time.Time
}

// RescheduleSummaries returns the history of the given bookings (absent = never moved).
// One query, bounded by the page.
func RescheduleSummaries(ctx context.Context, db *sql.DB, bookingIDs []string) (map[string]RescheduleSummary, error) {
	out := make(map[string]RescheduleSummary)
	if len(bookingIDs) == 0 {
		return out, nil
	}
	idsJSON, _ := json.Marshal(bookingIDs)
	rows, err := db.QueryContext(ctx, `
		SELECT r.booking_id, COUNT(*),
		       (SELECT r2.previous_start_at FROM fork_booking_reschedules r2
		        WHERE r2.booking_id = r.booking_id
		        ORDER BY r2.rescheduled_at DESC, r2.id DESC LIMIT 1)
		FROM fork_booking_reschedules r
		WHERE r.booking_id IN (SELECT value FROM json_each(?))
		GROUP BY r.booking_id`, string(idsJSON))
	if err != nil {
		return nil, fmt.Errorf("booking: reschedule summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, prev string
		var n int
		if err := rows.Scan(&id, &n, &prev); err != nil {
			return nil, err
		}
		sum := RescheduleSummary{Count: n}
		if t, err := time.Parse(time.RFC3339, prev); err == nil {
			sum.LastPreviousStart = t.UTC()
		}
		out[id] = sum
	}
	return out, rows.Err()
}

// backfillWindow: a delivery and a live row of the same move are written seconds apart
// (the row at the commit, the delivery by rescheduleSideEffects after the calendar and the
// e-mails). Within this window the same booking, previous and new start = the same move.
const backfillWindow = time.Hour

// BackfillRescheduleHistory rebuilds the history of moves made before the table existed
// from the booking.rescheduled webhook deliveries still kept (the worker purges them after
// ~30 days), for the bookings that still exist. Idempotent: every rebuilt row has a
// deterministic id (INSERT OR IGNORE), and a move already recorded live is skipped.
//
// A delivery carries previous_start_at / start_at only when its webhook selected them (both
// are in the default field set). Without previous_start_at nothing can be rebuilt; without
// start_at the new start is the next move's previous start or, for the last move, the
// booking's current start. Every webhook gets its own delivery of one move, with the same
// envelope created_at: they collapse into one row.
//
// Reads everything first, then writes: the pool is a single connection.
func BackfillRescheduleHistory(ctx context.Context, db *sql.DB) (int, error) {
	type move struct {
		bookingID, prev, next, at string
		current                   string // the booking's start_at now
	}
	rows, err := db.QueryContext(ctx, `
		SELECT d.booking_id,
		       json_extract(d.payload, '$.data.previous_start_at'),
		       COALESCE(json_extract(d.payload, '$.data.start_at'), ''),
		       COALESCE(json_extract(d.payload, '$.created_at'), d.last_attempted_at, ''),
		       b.start_at
		FROM webhook_deliveries d
		JOIN bookings b ON b.id = d.booking_id
		WHERE d.event = 'booking.rescheduled'
		  AND json_valid(d.payload)
		  AND json_type(d.payload, '$.data.previous_start_at') = 'text'`)
	if err != nil {
		return 0, fmt.Errorf("booking: backfill reschedules: read deliveries: %w", err)
	}
	seen := make(map[string]bool)
	byBooking := make(map[string][]move)
	for rows.Next() {
		var m move
		if err := rows.Scan(&m.bookingID, &m.prev, &m.next, &m.at, &m.current); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error
			return 0, err
		}
		prev, err1 := parseHistoryTime(m.prev)
		at, err2 := parseHistoryTime(m.at)
		if err1 != nil || err2 != nil {
			continue
		}
		m.prev, m.at = historyTime(prev), at.UTC().Format(time.RFC3339Nano)
		if t, err := parseHistoryTime(m.next); err == nil {
			m.next = historyTime(t)
		} else {
			m.next = ""
		}
		key := m.bookingID + "|" + m.prev + "|" + m.next + "|" + m.at
		if seen[key] {
			continue // the same move, delivered to another webhook
		}
		seen[key] = true
		byBooking[m.bookingID] = append(byBooking[m.bookingID], m)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(byBooking) == 0 {
		return 0, nil
	}

	have := make(map[string][]historyRow)
	ids := make([]string, 0, len(byBooking))
	for id := range byBooking {
		ids = append(ids, id)
	}
	idsJSON, _ := json.Marshal(ids)
	erows, err := db.QueryContext(ctx, `
		SELECT booking_id, previous_start_at, new_start_at, rescheduled_at
		FROM fork_booking_reschedules
		WHERE booking_id IN (SELECT value FROM json_each(?))`, string(idsJSON))
	if err != nil {
		return 0, fmt.Errorf("booking: backfill reschedules: read history: %w", err)
	}
	for erows.Next() {
		var id string
		var e historyRow
		if err := erows.Scan(&id, &e.prev, &e.next, &e.at); err != nil {
			erows.Close() // #nosec G104 -- already returning the scan error
			return 0, err
		}
		have[id] = append(have[id], e)
	}
	if err := erows.Close(); err != nil {
		return 0, err
	}

	inserted := 0
	sort.Strings(ids)
	for _, id := range ids {
		moves := byBooking[id]
		sort.SliceStable(moves, func(i, j int) bool { return moves[i].at < moves[j].at })
		for i, m := range moves {
			inferred := m.next == ""
			if inferred { // start_at not selected by that webhook: infer it
				if i+1 < len(moves) {
					m.next = moves[i+1].prev
				} else if p := firstPrevAfter(have[id], m.at); p != "" {
					m.next = p // a move recorded live came next
				} else if t, err := parseHistoryTime(m.current); err == nil {
					m.next = historyTime(t)
				}
			}
			if m.next == "" || m.next == m.prev {
				continue
			}
			// An inferred new start may be wrong; the previous one is enough to recognise
			// a move recorded live.
			match := m.next
			if inferred {
				match = ""
			}
			if alreadyRecorded(have[id], m.prev, match, m.at) {
				continue
			}
			sum := sha256.Sum256([]byte(id + "|" + m.prev + "|" + m.next + "|" + m.at))
			res, err := db.ExecContext(ctx, `
				INSERT OR IGNORE INTO fork_booking_reschedules
					(id, booking_id, previous_start_at, new_start_at, rescheduled_at, actor)
				VALUES (?, ?, ?, ?, ?, ?)`,
				"wh-"+hex.EncodeToString(sum[:16]), id, m.prev, m.next, m.at, RescheduleActorBackfill)
			if err != nil {
				return inserted, fmt.Errorf("booking: backfill reschedules: insert: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				inserted++
			}
		}
	}
	return inserted, nil
}

// historyRow is one fork_booking_reschedules row, as the backfill compares it.
type historyRow struct{ prev, next, at string }

// firstPrevAfter is the previous start of the earliest recorded move after at ("" = none).
func firstPrevAfter(rows []historyRow, at string) string {
	when, err := parseHistoryTime(at)
	if err != nil {
		return ""
	}
	best, out := time.Time{}, ""
	for _, r := range rows {
		t, err := parseHistoryTime(r.at)
		if err != nil || !t.After(when.Add(backfillWindow)) {
			continue
		}
		if out == "" || t.Before(best) {
			best, out = t, r.prev
		}
	}
	return out
}

// alreadyRecorded: a row of the same move (same previous and new start; next "" = any)
// written within backfillWindow of the delivery.
func alreadyRecorded(rows []historyRow, prev, next, at string) bool {
	when, err := parseHistoryTime(at)
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.prev != prev || (next != "" && r.next != next) {
			continue
		}
		t, err := parseHistoryTime(r.at)
		if err != nil {
			continue
		}
		if d := t.Sub(when); d < backfillWindow && d > -backfillWindow {
			return true
		}
	}
	return false
}

func parseHistoryTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}
