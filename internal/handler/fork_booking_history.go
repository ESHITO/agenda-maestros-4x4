package handler

// Fork (Agenda Maestros 4x4): Reservas' two tabs as the owner defined them (30 Sep 2026):
//   - Próximas = confirmed sessions that have not ended (in progress included), soonest
//     first - upstream's when=upcoming, unchanged;
//   - Pasadas = the history: sessions that ended plus cancelled ones whatever their date,
//     most recent first by when each entered it (when=history, booking/fork_list.go).
//
// GET /v1/bookings gains:
//   - when=history; with it, status=confirmed = "Concluidas", status=cancelled =
//     "Canceladas", and rescheduled=1 = "Reprogramadas" (moved at least once);
//   - counts by those definitions on when=history, and on when=upcoming only with tabs=1
//     (the panel sends it on both tabs; TabCounts); any other request (no when, when=past,
//     when=upcoming without tabs=1 - e.g. an API-key integration) keeps upstream's counts
//     and total (non-cancelled, split by end_at);
//   - items gain "rescheduled": {count, last_previous_start_at} in bookingListItem only -
//     never in bookingJSON, which the public GET /v1/bookings/{id} and MCP also serve.
//
// Upstream's when=past and the MCP list_bookings tool keep their contract.

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/calnode/calnode/internal/booking"
)

// rescheduledJSON is a list item's reschedule history.
type rescheduledJSON struct {
	Count int `json:"count"`
	// LastPreviousStartAt is the start the most recent move left (RFC3339 UTC).
	LastPreviousStartAt string `json:"last_previous_start_at,omitempty"`
}

// forkRescheduledFilter reads ?rescheduled= (1/true = only bookings moved at least once)
// and ?tabs= (1/true = the panel's tab counts, see bookingListCounts).
func forkRescheduledFilter(q url.Values, f *booking.ListFilter) error {
	switch q.Get("tabs") {
	case "", "0", "false":
	case "1", "true":
		f.Tabs = true
	default:
		return fmt.Errorf("tabs must be 1 or 0")
	}
	switch q.Get("rescheduled") {
	case "", "0", "false":
		return nil
	case "1", "true":
		f.Rescheduled = true
		return nil
	default:
		return fmt.Errorf("rescheduled must be 1 or 0")
	}
}

// bookingListCounts is ListBookings' counts: the tabs' definitions for when=history (fork
// only, no upstream contract) and for when=upcoming when the panel asks (tabs=1);
// upstream's split for everything else, so API-key callers of when=upcoming keep their
// counts.past and total.
func (h *Handler) bookingListCounts(ctx context.Context, f booking.ListFilter) (booking.Counts, error) {
	if f.When == booking.WhenHistory || (f.When == "upcoming" && f.Tabs) {
		return h.bookingSvc.TabCounts(ctx, f)
	}
	return h.bookingSvc.Counts(ctx, f)
}

// fillBookingReschedules sets each list item's reschedule history. One query for the page;
// best effort (withWhatsAppNotices logs an error and serves the list without it).
func (h *Handler) fillBookingReschedules(ctx context.Context, ids []string, idx map[string]int, out []bookingListItem) error {
	sums, err := booking.RescheduleSummaries(ctx, h.db, ids)
	if err != nil {
		return err
	}
	for id, s := range sums {
		i, ok := idx[id]
		if !ok || s.Count == 0 {
			continue
		}
		r := &rescheduledJSON{Count: s.Count}
		if !s.LastPreviousStart.IsZero() {
			r.LastPreviousStartAt = s.LastPreviousStart.UTC().Format(time.RFC3339)
		}
		out[i].Rescheduled = r
	}
	return nil
}

// backfillRescheduleHistory is the boot pass of the history (StartTeamBoot): moves made
// before the table existed, from the deliveries still kept. Idempotent, best effort.
func (h *Handler) backfillRescheduleHistory(ctx context.Context) {
	n, err := booking.BackfillRescheduleHistory(ctx, h.db)
	if err != nil {
		h.logger.ErrorContext(ctx, "reschedule history backfill failed", "error", err)
		return
	}
	if n > 0 {
		h.logger.InfoContext(ctx, "reschedule history: rebuilt from webhook deliveries", "rows", n)
	}
}

// mcpRescheduleActor names the MCP caller in the history ("mcp" for the local stdio operator).
func mcpRescheduleActor(ctx context.Context) string {
	if userID, _ := mcpCallerScope(ctx); userID != "" {
		return "mcp:" + userID
	}
	return "mcp"
}
