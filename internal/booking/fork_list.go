package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// forkConds adds the fork's list predicates (Agenda Maestros 4x4) to where()'s: the
// event-type set of ListFilter.EventTypeIDs, which the handler fills from ?area= (the
// team's Mentoría or Soporte families) and from ?event_type=<template slug> (the template
// and every copy of it), and ListFilter.Rescheduled (bookings moved at least once, read
// from fork_booking_reschedules). The ids travel as one JSON array (json_each), so the SQL
// text never depends on the input.
func (f ListFilter) forkConds(conds []string, args []any) ([]string, []any) {
	if f.Rescheduled {
		conds = append(conds, rescheduledBooking)
	}
	if f.EventTypeIDs == nil {
		return conds, args
	}
	idsJSON, _ := json.Marshal(f.EventTypeIDs)
	conds = append(conds, "bookings.event_type_id IN (SELECT value FROM json_each(?))")
	return conds, append(args, string(idsJSON))
}

// rescheduledBooking matches bookings with at least one recorded reschedule.
const rescheduledBooking = `EXISTS (SELECT 1 FROM fork_booking_reschedules r WHERE r.booking_id = bookings.id)`

// WhenHistory is the fork's "Pasadas" tab (owner, 30 Sep 2026: "en pasadas solo las que ya
// ocurrieron, fueron canceladas y acabaron"): the history of the agenda, i.e. confirmed
// bookings that already ended PLUS cancelled bookings whatever their date (a cancelled
// session next week is history, not an upcoming meeting). Upstream's "past" (ended,
// cancelled excluded unless asked) keeps its meaning for every other caller.
//
// Inside the history, Status narrows it: "cancelled" = the cancelled ones (any date), any
// other status = that status AND ended ("confirmed" = the concluded sessions).
const WhenHistory = "history"

// history reports whether where() is building the history predicate.
func (f ListFilter) history(includeWhen bool) bool {
	return includeWhen && f.When == WhenHistory && !f.Now.IsZero()
}

// historyConds is where()'s history branch. Called only when f.history(true); the default
// "status != 'cancelled'" was already skipped for it.
func (f ListFilter) historyConds(conds []string, args []any) ([]string, []any) {
	switch f.Status {
	case "cancelled":
		return conds, args // where() already added status = 'cancelled'
	case "":
		conds = append(conds, "(bookings.status = 'cancelled' OR bookings.end_at < ?)")
	default:
		conds = append(conds, "bookings.end_at < ?")
	}
	return conds, append(args, sqlTime(f.Now))
}

// historyEnteredAt is when a booking entered the history: a cancelled one when it was
// cancelled (bookings has no cancelled_at; every cancel path writes updated_at - the
// service's Cancel paths, and the Stripe unpaid-hold releases in handler/stripe_booking.go
// and worker.Poll since 30 Sep 2026 - and nothing in the panel edits a cancelled booking
// afterwards), a concluded one when it ended.
// Both are RFC3339 strings in UTC, so they sort as text (a fraction on updated_at shifts a
// tie by under a second, the same tolerance as sqlTime).
const historyEnteredAt = `(CASE WHEN bookings.status = 'cancelled' THEN bookings.updated_at ELSE bookings.end_at END)`

// forkOrderBy is List's ORDER BY for the history: most recent first by historyEnteredAt
// (order=asc reverses it), id as the stable tie-break. "" = upstream's start_at order.
func (f ListFilter) forkOrderBy() string {
	if f.When != WhenHistory {
		return ""
	}
	if f.Order == "asc" {
		return historyEnteredAt + " ASC, bookings.id ASC"
	}
	return historyEnteredAt + " DESC, bookings.id DESC"
}

// TabCounts labels the panel's two tabs (Próximas, Pasadas) by the fork's definitions, for
// a list asked with When "upcoming" or WhenHistory:
//   - Upcoming = not cancelled and ending now or later (a session in progress included);
//   - Past = the history (WhenHistory): ended, or cancelled at any date.
//
// The tab being listed is counted with the list's own filter, so the number pages exactly
// what is shown (Status and Rescheduled included). The other tab drops those two: they are
// sub-filters of one tab ("Concluidas", "Canceladas", "Reprogramadas" exist only on
// Pasadas), and its label must not change when the operator narrows the tab in view.
// Every other filter (visibility, host, team, type, área, dates) applies to both.
//
// One round trip, two scalar subqueries: the pool is a single connection.
func (s *Service) TabCounts(ctx context.Context, f ListFilter) (Counts, error) {
	if f.Now.IsZero() {
		f.Now = time.Now().UTC()
	}
	up, past := f, f
	up.When, past.When = "upcoming", WhenHistory
	if f.When == WhenHistory {
		up.Status, up.Rescheduled = "", false
	} else {
		past.Status, past.Rescheduled = "", false
	}
	upSQL, upArgs := up.where(true)
	pastSQL, pastArgs := past.where(true)
	q := `SELECT (SELECT COUNT(*) FROM bookings ` + upSQL + `),
		       (SELECT COUNT(*) FROM bookings ` + pastSQL + `)` //#nosec G202 -- both WHERE clauses are assembled from literal fragments only; every value is bound via args

	var c Counts
	if err := s.db.QueryRowContext(ctx, q, append(upArgs, pastArgs...)...).Scan(&c.Upcoming, &c.Past); err != nil {
		return Counts{}, fmt.Errorf("booking: tab counts: %w", err)
	}
	return c, nil
}
