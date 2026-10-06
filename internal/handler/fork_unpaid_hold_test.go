package handler

import (
	"context"
	"testing"
	"time"
)

// Fork (Agenda Maestros 4x4): Pasadas orders a cancelled booking by updated_at (booking/
// fork_list.go historyEnteredAt), so releasing an unpaid Stripe hold must stamp it - without
// it the hold sorted in the history by its creation time.
func TestReleaseUnpaidHold_stampsUpdatedAt(t *testing.T) {
	h, database, _ := newRefundTestSetup(t)
	const old = "2026-01-01T10:00:00Z"
	if _, err := database.Exec(`UPDATE bookings SET payment_status = 'pending', created_at = ?, updated_at = ? WHERE id = 'b-pay'`, old, old); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(-time.Second)
	h.releaseUnpaidHold(context.Background(), "b-pay")

	var status, updated string
	if err := database.QueryRow(`SELECT status, updated_at FROM bookings WHERE id = 'b-pay'`).Scan(&status, &updated); err != nil {
		t.Fatal(err)
	}
	got, err := time.Parse(time.RFC3339Nano, updated)
	if status != "cancelled" || err != nil || got.Before(before) {
		t.Errorf("after release: status %q, updated_at %q (%v); want cancelled and updated_at = now", status, updated, err)
	}
}

// Releasing an unpaid hold (Checkout expired) never tells the host: it never had a
// booking.host_created, and the release does not go through cancelSideEffects. A host with
// a number and a webhook subscribed to booking.host_cancelled still gets nothing.
func TestReleaseUnpaidHold_noHostCancelledNotice(t *testing.T) {
	h, database, ownerID := newRefundTestSetup(t)
	if _, err := database.Exec(`UPDATE bookings SET payment_status = 'pending' WHERE id = 'b-pay'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES (?, '+51987111222', 'x')`, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO webhooks (id, user_id, url, events, secret_enc, fields)
		VALUES ('wh-host-cancel', ?, 'https://hooks.example.com/f', '["booking.host_cancelled"]', 'unused', '["id","host_whatsapp","host_whatsapp_message"]')`, ownerID); err != nil {
		t.Fatal(err)
	}
	h.releaseUnpaidHold(context.Background(), "b-pay")
	var status string
	var deliveries int
	database.QueryRow(`SELECT status FROM bookings WHERE id = 'b-pay'`).Scan(&status)
	database.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&deliveries)
	if status != "cancelled" || deliveries != 0 {
		t.Errorf("after release: status %q, deliveries %d; want cancelled and none", status, deliveries)
	}
}
