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
