package worker_test

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/worker"
)

// Fork (Agenda Maestros 4x4): a reminder for a booking stored with the default "UTC"
// attendee zone (API/MCP bookings) prints the host's time, as the WhatsApp reminder
// does (webhook.AttendeeZone) - not "1:30 AM UTC" for a Lima 20:30 session.
func TestWorker_reminderFallsBackToTheHostZoneForAStoredUTC(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	pastRunAt := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	// Thu 30 Sep 2027, 20:30 Lima = Fri 1 Oct 01:30Z.
	start, end := "2027-10-01T01:30:00Z", "2027-10-01T02:10:00Z"
	for _, q := range []string{
		`UPDATE users SET iana_timezone = 'America/Lima' WHERE id = 'host-01'`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-z1','host-01','rem-zone','Mentoría',40)`,
	} {
		if _, err := database.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	database.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		 VALUES ('bk-z1','et-z1','host-01',?,?,'confirmed')`, start, end)
	database.ExecContext(ctx,
		`INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer)
		 VALUES ('att-z1','bk-z1','Ana','ana@example.com','UTC',1)`)
	database.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES ('job-z1','reminder.send','{"booking_id":"bk-z1"}',?,'pending',0,3)`, pastRunAt)

	m := &captureMailer{}
	w := worker.New(database, svc, slog.Default(), worker.WithMailer(m), worker.WithHTTPClient(&http.Client{}))
	w.Poll(ctx)

	if len(m.sent) != 1 {
		t.Fatalf("sent %d emails; want 1", len(m.sent))
	}
	if body := m.sent[0].Text; !strings.Contains(body, "Thu 30 Sep 2027, 8:30 PM") || strings.Contains(body, "1:30 AM") {
		t.Errorf("reminder is not in the host's (Lima) time:\n%s", body)
	}
}
