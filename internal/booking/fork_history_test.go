package booking_test

// Fork (Agenda Maestros 4x4): the Pasadas tab is a history (ended + cancelled at any date,
// most recent first by when each entered it), Próximas keeps the live confirmed sessions,
// and every reschedule leaves a row in fork_booking_reschedules.

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/webhook"
)

// newForkDB is newTestDB plus the fork's tables made in code (fork_booking_reschedules).
func newForkDB(t *testing.T) *sql.DB {
	t.Helper()
	database := newTestDB(t)
	if err := webhook.EnsureForkSchema(database); err != nil {
		t.Fatalf("fork schema: %v", err)
	}
	if err := webhook.EnsureTeamSchema(database); err != nil {
		t.Fatalf("team schema: %v", err)
	}
	return database
}

func insertBooking(t *testing.T, db *sql.DB, id, etID, hostID, start, end, status, updatedAt string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, etID, hostID, start, end, status, updatedAt); err != nil {
		t.Fatalf("insert booking %s: %v", id, err)
	}
}

func insertMove(t *testing.T, db *sql.DB, id, bookingID, prev, next, at string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO fork_booking_reschedules (id, booking_id, previous_start_at, new_start_at, rescheduled_at)
		VALUES (?, ?, ?, ?, ?)`, id, bookingID, prev, next, at); err != nil {
		t.Fatalf("insert move %s: %v", id, err)
	}
}

func ids(bs []booking.Booking) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

// historyFixture: "now" is 2026-06-15 12:00 UTC.
//   - ended1 (ended 10:30 today), ended2 (ended 08:30 today): concluded;
//   - cancelledFuture: a session on the 20th, cancelled today at 11:00;
//   - cancelledOld: a session on the 10th, cancelled on the 14th;
//   - live (11:30-12:30, in progress) and future (the 16th): Próximas.
func historyFixture(t *testing.T) (*booking.Service, *sql.DB, time.Time) {
	t.Helper()
	database := newForkDB(t)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)
	insertBooking(t, database, "ended1", etID, hostID, "2026-06-15T10:00:00Z", "2026-06-15T10:30:00Z", "confirmed", "2026-06-01T00:00:00Z")
	insertBooking(t, database, "ended2", etID, hostID, "2026-06-15T08:00:00Z", "2026-06-15T08:30:00Z", "confirmed", "2026-06-01T00:00:00Z")
	insertBooking(t, database, "cancelledFuture", etID, hostID, "2026-06-20T09:00:00Z", "2026-06-20T09:30:00Z", "cancelled", "2026-06-15T11:00:00.5Z")
	insertBooking(t, database, "cancelledOld", etID, hostID, "2026-06-10T09:00:00Z", "2026-06-10T09:30:00Z", "cancelled", "2026-06-14T09:00:00Z")
	insertBooking(t, database, "live", etID, hostID, "2026-06-15T11:30:00Z", "2026-06-15T12:30:00Z", "confirmed", "2026-06-01T00:00:00Z")
	insertBooking(t, database, "future", etID, hostID, "2026-06-16T09:00:00Z", "2026-06-16T09:30:00Z", "confirmed", "2026-06-01T00:00:00Z")
	return booking.New(database), database, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
}

func TestHistory_membershipAndOrder(t *testing.T) {
	svc, _, now := historyFixture(t)
	ctx := context.Background()

	got, err := svc.List(ctx, booking.ListFilter{When: booking.WhenHistory, Now: now, Order: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	// Cancelled ones by their cancellation (updated_at), concluded ones by their end.
	want := []string{"cancelledFuture", "ended1", "ended2", "cancelledOld"}
	if !slices.Equal(ids(got), want) {
		t.Errorf("history = %v; want %v (a future cancelled session is history; most recent first)", ids(got), want)
	}

	// No order given still reads most recent first; asc reverses it.
	got, _ = svc.List(ctx, booking.ListFilter{When: booking.WhenHistory, Now: now})
	if !slices.Equal(ids(got), want) {
		t.Errorf("history without order = %v; want %v", ids(got), want)
	}
	got, _ = svc.List(ctx, booking.ListFilter{When: booking.WhenHistory, Now: now, Order: "asc"})
	rev := slices.Clone(want)
	slices.Reverse(rev)
	if !slices.Equal(ids(got), rev) {
		t.Errorf("history asc = %v; want %v", ids(got), rev)
	}

	up, err := svc.List(ctx, booking.ListFilter{When: "upcoming", Now: now, Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(up), []string{"live", "future"}) {
		t.Errorf("upcoming = %v; want [live future] (in progress stays, soonest first, no cancelled)", ids(up))
	}

	// Paging follows the same order.
	page, _ := svc.List(ctx, booking.ListFilter{When: booking.WhenHistory, Now: now, Limit: 2, Offset: 2})
	if !slices.Equal(ids(page), []string{"ended2", "cancelledOld"}) {
		t.Errorf("history page 2 = %v; want [ended2 cancelledOld]", ids(page))
	}
}

func TestHistory_statusAndRescheduledFilters(t *testing.T) {
	svc, database, now := historyFixture(t)
	ctx := context.Background()
	insertMove(t, database, "m1", "ended1", "2026-06-14T10:00:00Z", "2026-06-15T10:00:00Z", "2026-06-13T00:00:00Z")
	insertMove(t, database, "m2", "cancelledFuture", "2026-06-19T09:00:00Z", "2026-06-20T09:00:00Z", "2026-06-13T00:00:00Z")
	insertMove(t, database, "m3", "future", "2026-06-15T15:00:00Z", "2026-06-16T09:00:00Z", "2026-06-13T00:00:00Z")

	for _, tc := range []struct {
		name string
		f    booking.ListFilter
		want []string
	}{
		{"Canceladas: any date", booking.ListFilter{Status: "cancelled"}, []string{"cancelledFuture", "cancelledOld"}},
		{"Concluidas: confirmed and ended", booking.ListFilter{Status: "confirmed"}, []string{"ended1", "ended2"}},
		{"Reprogramadas: moved at least once", booking.ListFilter{Rescheduled: true}, []string{"cancelledFuture", "ended1"}},
	} {
		f := tc.f
		f.When, f.Now = booking.WhenHistory, now
		got, err := svc.List(ctx, f)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(ids(got), tc.want) {
			t.Errorf("%s = %v; want %v", tc.name, ids(got), tc.want)
		}
		c, err := svc.TabCounts(ctx, f)
		if err != nil {
			t.Fatalf("%s counts: %v", tc.name, err)
		}
		// The tab in view pages exactly what it lists; Próximas keeps its own label.
		if c.Past != len(tc.want) || c.Upcoming != 2 {
			t.Errorf("%s counts = %+v; want past %d, upcoming 2", tc.name, c, len(tc.want))
		}
	}
}

func TestHistory_tabCounts(t *testing.T) {
	svc, _, now := historyFixture(t)
	ctx := context.Background()
	for _, when := range []string{"upcoming", booking.WhenHistory} {
		c, err := svc.TabCounts(ctx, booking.ListFilter{When: when, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if c.Upcoming != 2 || c.Past != 4 {
			t.Errorf("when=%s: counts = %+v; want upcoming 2 (live, future), past 4 (two ended, two cancelled)", when, c)
		}
	}
}

// Upstream's "past" and Counts keep their contract (the MCP tool and API callers use them).
func TestHistory_upstreamPastUnchanged(t *testing.T) {
	svc, _, now := historyFixture(t)
	ctx := context.Background()
	got, err := svc.List(ctx, booking.ListFilter{When: "past", Now: now, Order: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"ended1", "ended2"}) {
		t.Errorf("past = %v; want [ended1 ended2] (no cancelled, by start)", ids(got))
	}
	c, err := svc.Counts(ctx, booking.ListFilter{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if c.Upcoming != 2 || c.Past != 2 {
		t.Errorf("Counts = %+v; want upstream's 2/2 (cancelled excluded)", c)
	}
}

func TestReschedule_recordsHistoryRow(t *testing.T) {
	database := newForkDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)
	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID, HostIDs: []string{hostID},
		StartAt: slot(9, 0), EndAt: slot(9, 30),
		Organizer: booking.Attendee{Name: "Ana", Email: "ana@example.com", IANATimezone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := booking.WithRescheduleActor(context.Background(), "panel:"+hostID)
	if _, err := svc.Reschedule(ctx, b.ID, slot(11, 0), slot(11, 30)); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	if _, err := svc.Reschedule(context.Background(), b.ID, slot(13, 0), slot(13, 30)); err != nil {
		t.Fatalf("Reschedule 2: %v", err)
	}
	rows, err := database.Query(`SELECT previous_start_at, new_start_at, COALESCE(actor, '') FROM fork_booking_reschedules
		WHERE booking_id = ? ORDER BY julianday(rescheduled_at), rowid`, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][3]string
	for rows.Next() {
		var r [3]string
		if err := rows.Scan(&r[0], &r[1], &r[2]); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	want := [][3]string{
		{"2026-06-15T09:00:00Z", "2026-06-15T11:00:00Z", "panel:" + hostID},
		{"2026-06-15T11:00:00Z", "2026-06-15T13:00:00Z", ""},
	}
	if !slices.Equal(got, want) {
		t.Errorf("history rows = %v; want %v", got, want)
	}

	sums, err := booking.RescheduleSummaries(context.Background(), database, []string{b.ID, "nobody"})
	if err != nil {
		t.Fatal(err)
	}
	if s := sums[b.ID]; s.Count != 2 || !s.LastPreviousStart.Equal(slot(11, 0)) {
		t.Errorf("summary = %+v; want 2 moves, last left 11:00", s)
	}
	if _, ok := sums["nobody"]; ok {
		t.Error("a booking never moved must have no summary")
	}
}

// Best effort: without the fork table the reschedule still succeeds.
func TestReschedule_historyFailureNeverFailsTheReschedule(t *testing.T) {
	database := newTestDB(t) // goose only: no fork_booking_reschedules
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)
	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID, HostIDs: []string{hostID},
		StartAt: slot(9, 0), EndAt: slot(9, 30),
		Organizer: booking.Attendee{Name: "Ana", Email: "ana@example.com", IANATimezone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := svc.Reschedule(context.Background(), b.ID, slot(10, 0), slot(10, 30))
	if err != nil || !moved.StartAt.Equal(slot(10, 0)) {
		t.Fatalf("Reschedule = %+v, %v; want the move to stand", moved, err)
	}
}

func TestBackfillRescheduleHistory_idempotent(t *testing.T) {
	database := newForkDB(t)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)
	insertBooking(t, database, "bk", etID, hostID, "2026-06-18T09:00:00Z", "2026-06-18T09:30:00Z", "confirmed", "2026-06-01T00:00:00Z")
	insertBooking(t, database, "live", etID, hostID, "2026-06-19T09:00:00Z", "2026-06-19T09:30:00Z", "confirmed", "2026-06-01T00:00:00Z")
	for _, wh := range []string{"wh1", "wh2"} {
		if _, err := database.Exec(`INSERT INTO webhooks (id, user_id, url, events, secret_enc) VALUES (?, ?, 'https://example.com', '[]', 'x')`, wh, hostID); err != nil {
			t.Fatal(err)
		}
	}
	deliver := func(id, wh, bookingID, payload string) {
		t.Helper()
		if _, err := database.Exec(`INSERT INTO webhook_deliveries (id, webhook_id, booking_id, event, payload) VALUES (?, ?, ?, 'booking.rescheduled', ?)`,
			id, wh, bookingID, payload); err != nil {
			t.Fatal(err)
		}
	}
	// bk moved 16th -> 17th (both webhooks got it), then 17th -> 18th through a webhook
	// that did not select start_at (the new start is the booking's current one).
	deliver("d1", "wh1", "bk", `{"event":"booking.rescheduled","created_at":"2026-06-10T10:00:00Z","data":{"id":"bk","start_at":"2026-06-17T09:00:00Z","previous_start_at":"2026-06-16T09:00:00Z"}}`)
	deliver("d2", "wh2", "bk", `{"event":"booking.rescheduled","created_at":"2026-06-10T10:00:00Z","data":{"id":"bk","start_at":"2026-06-17T09:00:00Z","previous_start_at":"2026-06-16T09:00:00Z"}}`)
	deliver("d3", "wh1", "bk", `{"event":"booking.rescheduled","created_at":"2026-06-11T10:00:00Z","data":{"id":"bk","previous_start_at":"2026-06-17T09:00:00Z"}}`)
	// A move already recorded live (written a few seconds before its delivery).
	deliver("d4", "wh1", "live", `{"event":"booking.rescheduled","created_at":"2026-06-12T10:00:05Z","data":{"id":"live","start_at":"2026-06-19T09:00:00Z","previous_start_at":"2026-06-18T15:00:00Z"}}`)
	insertMove(t, database, "live-row", "live", "2026-06-18T15:00:00Z", "2026-06-19T09:00:00Z", "2026-06-12T10:00:00.123Z")
	// Not rebuildable: no previous_start_at, and a payload that is not JSON.
	deliver("d5", "wh1", "bk", `{"event":"booking.rescheduled","created_at":"2026-06-12T10:00:00Z","data":{"id":"bk"}}`)
	deliver("d6", "wh1", "bk", `not json`)

	ctx := context.Background()
	n, err := booking.BackfillRescheduleHistory(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("first backfill inserted %d rows; want 2", n)
	}
	n, err = booking.BackfillRescheduleHistory(ctx, database)
	if err != nil || n != 0 {
		t.Errorf("second backfill = %d, %v; want 0 (idempotent)", n, err)
	}

	sums, err := booking.RescheduleSummaries(ctx, database, []string{"bk", "live"})
	if err != nil {
		t.Fatal(err)
	}
	if s := sums["bk"]; s.Count != 2 || !s.LastPreviousStart.Equal(time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("bk summary = %+v; want 2 moves, last left the 17th", s)
	}
	if s := sums["live"]; s.Count != 1 {
		t.Errorf("live summary = %+v; want the live row only", s)
	}
	var next, actor string
	if err := database.QueryRow(`SELECT new_start_at, actor FROM fork_booking_reschedules WHERE booking_id = 'bk' AND previous_start_at = '2026-06-17T09:00:00Z'`).Scan(&next, &actor); err != nil {
		t.Fatal(err)
	}
	if next != "2026-06-18T09:00:00Z" || actor != booking.RescheduleActorBackfill {
		t.Errorf("inferred row = %s by %s; want the current start 2026-06-18T09:00:00Z by %s", next, actor, booking.RescheduleActorBackfill)
	}
}
