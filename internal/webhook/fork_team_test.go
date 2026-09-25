package webhook_test

import (
	"context"
	"testing"

	"github.com/calnode/calnode/internal/webhook"
)

// Fork (Agenda Maestros 4x4): the team template's copies (fork_team.go). A webhook limited
// to a template also receives the bookings of every mentor's copy of it, and a copy sends
// its template's WhatsApp texts; nothing changes for any other booking.

// seedTeamTypes: the owner's template et-t ("Mentoría privada"), the mentor testUserID's
// copy et-c of it (owned by the owner, linked), a holder et-h (kind 'holder'), and an
// ordinary type et-o. One confirmed booking of each: bk-t (host owner), bk-c, bk-h, bk-o
// (host the mentor).
func seedTeamTypes(t *testing.T, e *env) {
	t.Helper()
	withOwner(t, e)
	for _, q := range []string{
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-t', '` + ownerUser + `', 'mentoria-privada', 'Mentoría privada', 60)`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-c', '` + ownerUser + `', 'mentoria-privada-test', 'Mentoría privada', 60)`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes, is_active) VALUES ('et-h', '` + ownerUser + `', 'mentoria-privada-preguntas-retiradas', 'Preguntas retiradas', 60, 0)`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-o', '` + ownerUser + `', 'mentoria-personal', 'Mentoría personal', 60)`,
		`INSERT INTO fork_event_type_links (copy_id, template_id, user_id, kind, created_at) VALUES ('et-c', 'et-t', '` + testUserID + `', 'copy', 'x')`,
		`INSERT INTO fork_event_type_links (copy_id, template_id, user_id, kind, created_at) VALUES ('et-h', 'et-t', '` + ownerUser + `', 'holder', 'x')`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk-t', 'et-t', '` + ownerUser + `', '2026-10-01T14:00:00Z', '2026-10-01T15:00:00Z', 'confirmed')`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk-c', 'et-c', '` + testUserID + `', '2026-10-02T14:00:00Z', '2026-10-02T15:00:00Z', 'confirmed')`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk-h', 'et-h', '` + testUserID + `', '2026-10-03T14:00:00Z', '2026-10-03T15:00:00Z', 'confirmed')`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk-o', 'et-o', '` + testUserID + `', '2026-10-04T14:00:00Z', '2026-10-04T15:00:00Z', 'confirmed')`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
}

func deliveredFor(t *testing.T, e *env, webhookID, bookingID string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND booking_id = ?`, webhookID, bookingID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEnqueue_templateFilterReachesCopies(t *testing.T) {
	e := newEnv(t)
	seedTeamTypes(t, e)
	ctx := context.Background()
	events := []string{"booking.created", webhook.EventReminder1h}
	onlyT, _, err := e.svc.CreateWithEventTypes(ctx, ownerUser, "https://owner.example.com/t", events, []string{"et-t"})
	if err != nil {
		t.Fatal(err)
	}
	onlyO, _, err := e.svc.CreateWithEventTypes(ctx, ownerUser, "https://owner.example.com/o", events, []string{"et-o"})
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(ev, bk, host string) {
		t.Helper()
		if err := e.svc.Enqueue(ctx, ev, webhook.BookingPayload{ID: bk, HostID: host, Status: "confirmed"}); err != nil {
			t.Fatalf("Enqueue %s %s: %v", ev, bk, err)
		}
	}
	enqueue("booking.created", "bk-t", ownerUser)
	enqueue("booking.created", "bk-c", testUserID)
	enqueue("booking.created", "bk-h", testUserID)
	enqueue("booking.created", "bk-o", testUserID)

	for _, c := range []struct {
		webhookID, booking string
		want               int
	}{
		{onlyT.ID, "bk-t", 1},
		{onlyT.ID, "bk-c", 1}, // the mentor's copy, through its template
		{onlyT.ID, "bk-h", 0}, // a holder link never widens
		{onlyT.ID, "bk-o", 0},
		{onlyO.ID, "bk-o", 1}, // a non-copy booking: exactly as before
		{onlyO.ID, "bk-c", 0},
		{onlyO.ID, "bk-t", 0},
	} {
		if got := deliveredFor(t, e, c.webhookID, c.booking); got != c.want {
			t.Errorf("deliveries of %s to the webhook limited to %v = %d; want %d",
				c.booking, map[string]string{onlyT.ID: "et-t", onlyO.ID: "et-o"}[c.webhookID], got, c.want)
		}
	}

	// A DEACTIVATED copy keeps sending for the bookings it already has: neither matching
	// nor the reminder job looks at event_types.is_active.
	if _, err := e.db.Exec(`UPDATE event_types SET is_active = 0 WHERE id = 'et-c'`); err != nil {
		t.Fatal(err)
	}
	enqueue(webhook.EventReminder1h, "bk-c", testUserID)
	if got := deliveredFor(t, e, onlyT.ID, "bk-c"); got != 2 {
		t.Errorf("deactivated copy: deliveries of bk-c = %d; want 2 (the reminder still goes out)", got)
	}
	if ok, err := e.svc.WantsField(ctx, webhook.EventReminder1h, testUserID, "bk-c", webhook.FieldEventTypeSlug); err != nil || !ok {
		t.Errorf("WantsField(bk-c) = %v, %v; want true through the template", ok, err)
	}
}

// A copy sends its TEMPLATE's saved texts; the template's own bookings too, and an
// ordinary type keeps the default.
func TestEnqueue_whatsAppMessage_copyInheritsTemplateTexts(t *testing.T) {
	e := newEnv(t)
	seedTeamTypes(t, e)
	ctx := context.Background()
	if err := e.svc.SetWhatsAppMessages(ctx, "et-t", map[string]string{
		webhook.WhatsAppCreated: "Texto de la plantilla: {tipo}",
	}); err != nil {
		t.Fatal(err)
	}
	wh, _, err := e.svc.CreateWithEventTypes(ctx, ownerUser, "https://owner.example.com/wa", []string{"booking.created"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := []string{webhook.FieldWhatsAppMessage}
	if err := e.svc.Update(ctx, ownerUser, wh.ID, nil, &fields); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ booking, host, want string }{
		{"bk-c", testUserID, "Texto de la plantilla: Mentoría privada"},
		{"bk-t", ownerUser, "Texto de la plantilla: Mentoría privada"},
	} {
		if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{ID: c.booking, HostID: c.host, Status: "confirmed"}); err != nil {
			t.Fatal(err)
		}
		if got := lastData(t, e, wh.ID)["whatsapp_message"]; got != c.want {
			t.Errorf("%s whatsapp_message = %q; want %q", c.booking, got, c.want)
		}
	}
	if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{ID: "bk-o", HostID: testUserID, Status: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := lastData(t, e, wh.ID)["whatsapp_message"].(string); got == "" || got == "Texto de la plantilla: Mentoría personal" {
		t.Errorf("ordinary type whatsapp_message = %q; want its own default text", got)
	}
}

func TestEnsureTeamSchema_idempotent(t *testing.T) {
	e := newEnv(t) // New already ran it once
	for i := 0; i < 2; i++ {
		if err := webhook.EnsureTeamSchema(e.db); err != nil {
			t.Fatalf("EnsureTeamSchema call %d: %v", i+1, err)
		}
	}
	for _, table := range []string{"fork_event_type_links", "fork_question_links", "fork_member_areas",
		"fork_invite_roles", "fork_livekit_mints", "fork_livekit_sessions", "fork_livekit_host_links"} {
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: count %d, err %v", table, n, err)
		}
	}
	var since string
	if err := e.db.QueryRow(`SELECT value FROM fork_settings WHERE key = 'attendance_since'`).Scan(&since); err != nil || since == "" {
		t.Errorf("attendance_since = %q, err %v", since, err)
	}
}
