package worker_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/webhook"
)

// Fork (Agenda Maestros 4x4): manage_url is a working bearer link (view, cancel,
// reschedule) and booking_manage_tokens only ever stores hashes. Once a delivery is
// finished its stored payload must not keep the link in clear; while it is still
// retrying, the next attempt needs it.

const scrubTestManageURL = "https://citas.example.com/manage/raw-token-123"

// envelopeData is the "data" object of a delivery envelope.
type envelopeData struct {
	Data map[string]any `json:"data"`
}

func TestWorker_scrubsManageURLOnceDelivered(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh, _, _ := svc.Create(ctx, "host-01", srv.URL, []string{"booking.created"})
	database.ExecContext(ctx, `UPDATE webhooks SET fields = '["status","manage_url"]' WHERE id = ?`, wh.ID)
	if err := svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		HostID: "host-01", Status: "confirmed", ManageURL: scrubTestManageURL,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	newWorker(t, database, svc).Poll(ctx)

	var sent envelopeData
	if err := json.Unmarshal(received, &sent); err != nil {
		t.Fatalf("endpoint got no JSON: %v", err)
	}
	if sent.Data["manage_url"] != scrubTestManageURL {
		t.Fatalf("sent data = %v; the endpoint must receive manage_url", sent.Data)
	}

	var status, payload string
	database.QueryRowContext(ctx, `SELECT status, payload FROM webhook_deliveries WHERE webhook_id = ?`, wh.ID).
		Scan(&status, &payload)
	var stored envelopeData
	if err := json.Unmarshal([]byte(payload), &stored); err != nil {
		t.Fatalf("stored payload is not JSON after scrubbing: %v", err)
	}
	if status != "success" {
		t.Fatalf("delivery status = %q; want success", status)
	}
	if _, ok := stored.Data["manage_url"]; ok {
		t.Errorf("stored payload still holds manage_url after success: %s", payload)
	}
	if stored.Data["status"] != "confirmed" {
		t.Errorf("scrubbing removed more than manage_url: %s", payload)
	}
}

func TestWorker_keepsManageURLWhileRetrying_scrubsWhenFailed(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	wh, _, _ := svc.Create(ctx, "host-02", srv.URL, []string{"booking.created"})
	database.ExecContext(ctx, `UPDATE webhooks SET fields = '["manage_url"]' WHERE id = ?`, wh.ID)
	if err := svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		HostID: "host-02", Status: "confirmed", ManageURL: scrubTestManageURL,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	w := newWorker(t, database, svc)
	link := func() (string, any) {
		t.Helper()
		var status, payload string
		database.QueryRowContext(ctx, `SELECT status, payload FROM webhook_deliveries WHERE webhook_id = ?`, wh.ID).
			Scan(&status, &payload)
		var stored envelopeData
		_ = json.Unmarshal([]byte(payload), &stored)
		return status, stored.Data["manage_url"]
	}
	past := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	poll := func() {
		database.ExecContext(ctx, `UPDATE jobs SET run_at = ? WHERE type = 'webhook.deliver' AND status = 'pending'`, past)
		w.Poll(ctx)
	}

	poll() // attempt 1 of 3 fails
	if status, l := link(); status != "pending" || l != scrubTestManageURL {
		t.Fatalf("after one failed attempt: status %q, manage_url %v; want pending with the link kept for the retry", status, l)
	}
	poll()
	poll() // attempt 3: no attempts left
	if status, l := link(); status != "failed" || l != nil {
		t.Errorf("after exhausting retries: status %q, manage_url %v; want failed and the link removed", status, l)
	}
}
