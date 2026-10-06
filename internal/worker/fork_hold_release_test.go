package worker_test

import (
	"context"
	"testing"
	"time"
)

// Fork (Agenda Maestros 4x4): the expired-hold backstop stamps updated_at, which Pasadas
// uses as the cancellation time (booking/fork_list.go historyEnteredAt).
func TestPoll_expiredHoldReleaseStampsUpdatedAt(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	if _, err := database.ExecContext(ctx,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-hold','host-01','hold','Hold',30)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, payment_status, created_at, updated_at)
		 VALUES ('bk-hold','et-hold','host-01','2099-06-14T09:00:00Z','2099-06-14T09:30:00Z','confirmed','pending',?,?)`, old, old); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(-time.Second)
	newWorker(t, database, svc).Poll(ctx)

	var status, updated string
	if err := database.QueryRowContext(ctx, `SELECT status, updated_at FROM bookings WHERE id = 'bk-hold'`).Scan(&status, &updated); err != nil {
		t.Fatal(err)
	}
	got, err := time.Parse(time.RFC3339Nano, updated)
	if status != "cancelled" || err != nil || got.Before(before) {
		t.Errorf("after Poll: status %q, updated_at %q (%v); want cancelled and updated_at = now", status, updated, err)
	}
}

// The backstop release is a bare UPDATE: it never queues a notice, booking.host_cancelled
// included (an unpaid hold never had a booking.host_created).
func TestPoll_expiredHoldReleaseQueuesNoHostNotice(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	for _, q := range []string{
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-hold2','host-01','hold2','Hold',30)`,
		`INSERT INTO webhooks (id, user_id, url, events, secret_enc, fields)
		 VALUES ('wh-host-cancel','host-01','https://hooks.example.com/f','["booking.host_cancelled"]','unused','["id","host_whatsapp"]')`,
	} {
		if _, err := database.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, payment_status, created_at, updated_at)
		 VALUES ('bk-hold2','et-hold2','host-01','2099-06-14T09:00:00Z','2099-06-14T09:30:00Z','confirmed','pending',?,?)`, old, old); err != nil {
		t.Fatal(err)
	}
	newWorker(t, database, svc).Poll(ctx)
	var status string
	var deliveries int
	database.QueryRowContext(ctx, `SELECT status FROM bookings WHERE id = 'bk-hold2'`).Scan(&status)
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries`).Scan(&deliveries)
	if status != "cancelled" || deliveries != 0 {
		t.Errorf("after Poll: status %q, deliveries %d; want cancelled and none", status, deliveries)
	}
}
