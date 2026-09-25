package webhook_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calnode/calnode/internal/webhook"
)

// Fork (Agenda Maestros 4x4): the workspace owner's webhooks receive every team
// member's bookings (webhook.Service.matchingWebhooks); members still get only their own.

const ownerUser = "user-owner-01"

// withOwner adds a workspace owner to the env's two plain members.
func withOwner(t *testing.T, e *env) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO users (id, email, name, is_owner, is_admin) VALUES (?, 'owner@example.com', 'Owner', 1, 1)`, ownerUser); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
}

func deliveriesFor(t *testing.T, e *env, webhookID string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ?`, webhookID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEnqueue_ownerReceivesTeamBookings(t *testing.T) {
	e := newEnv(t)
	withOwner(t, e)
	ctx := context.Background()

	ownerHook, _, _ := e.svc.Create(ctx, ownerUser, "https://owner.example.com/hook", []string{webhook.EventReminder1h})
	memberHook, _, _ := e.svc.Create(ctx, testUserID, "https://member.example.com/hook", []string{webhook.EventReminder1h})
	otherHook, _, _ := e.svc.Create(ctx, otherUser, "https://other.example.com/hook", []string{webhook.EventReminder1h})

	// A member's booking: their own webhook AND the owner's fire; the other member's don't.
	if err := e.svc.Enqueue(ctx, webhook.EventReminder1h, webhook.BookingPayload{HostID: testUserID, Status: "confirmed"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := deliveriesFor(t, e, ownerHook.ID); got != 1 {
		t.Errorf("owner webhook deliveries = %d; want 1 (owner sees the whole team)", got)
	}
	if got := deliveriesFor(t, e, memberHook.ID); got != 1 {
		t.Errorf("host webhook deliveries = %d; want 1", got)
	}
	if got := deliveriesFor(t, e, otherHook.ID); got != 0 {
		t.Errorf("other member's webhook deliveries = %d; want 0 (members see only their own)", got)
	}

	// The owner's own booking reaches the owner's webhook ONCE (one query with OR, not
	// host-scope + owner-scope added together) and no member's.
	if err := e.svc.Enqueue(ctx, webhook.EventReminder1h, webhook.BookingPayload{HostID: ownerUser, Status: "confirmed"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := deliveriesFor(t, e, ownerHook.ID); got != 2 {
		t.Errorf("owner webhook deliveries = %d; want 2 (no duplicate for their own booking)", got)
	}
	if got := deliveriesFor(t, e, memberHook.ID); got != 1 {
		t.Errorf("member webhook got the owner's booking: %d deliveries; want 1", got)
	}
}

func TestEnqueue_archivedOwnerOutOfScope(t *testing.T) {
	e := newEnv(t)
	withOwner(t, e)
	ctx := context.Background()
	ownerHook, _, _ := e.svc.Create(ctx, ownerUser, "https://owner.example.com/hook", []string{"booking.created"})
	if _, err := e.db.Exec(`UPDATE users SET archived_at = '2026-01-01T00:00:00Z' WHERE id = ?`, ownerUser); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{HostID: testUserID, Status: "confirmed"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := deliveriesFor(t, e, ownerHook.ID); got != 0 {
		t.Errorf("archived owner's webhook deliveries = %d; want 0", got)
	}
}

func TestWantsField_scopeAndSelection(t *testing.T) {
	e := newEnv(t)
	withOwner(t, e)
	ctx := context.Background()

	// Unconfigured webhook = default set, which does not include manage_url.
	e.svc.Create(ctx, testUserID, "https://member.example.com/hook", []string{webhook.EventReminder5m})
	if ok, err := e.svc.WantsField(ctx, webhook.EventReminder5m, testUserID, webhook.FieldManageURL); err != nil || ok {
		t.Fatalf("WantsField = %v, %v; want false (default set has no manage_url)", ok, err)
	}
	// The owner selects it: now wanted for the member's booking too.
	wh, _, _ := e.svc.Create(ctx, ownerUser, "https://owner.example.com/hook", []string{webhook.EventReminder5m})
	fields := []string{webhook.FieldAttendeePhone, webhook.FieldManageURL}
	if err := e.svc.Update(ctx, ownerUser, wh.ID, nil, &fields); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.svc.WantsField(ctx, webhook.EventReminder5m, testUserID, webhook.FieldManageURL); !ok {
		t.Error("WantsField = false; want true via the owner's webhook")
	}
	// ...but not for an event nobody subscribes to.
	if ok, _ := e.svc.WantsField(ctx, webhook.EventReminderMorning, testUserID, webhook.FieldManageURL); ok {
		t.Error("WantsField = true for an unsubscribed event")
	}
}

// TestEnqueue_forkFieldsFromDatabase runs enrich end to end: phone from a 'phone'
// question, start_local in the ATTENDEE's zone (not the host's), manage_url as given.
func TestEnqueue_forkFieldsFromDatabase(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, q := range []string{
		`UPDATE users SET iana_timezone = 'Europe/Madrid' WHERE id = '` + testUserID + `'`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-1', '` + testUserID + `', 'mentoria', 'Mentoría', 30)`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		 VALUES ('bk-1', 'et-1', '` + testUserID + `', '2026-09-25T14:00:00.000000000Z', '2026-09-25T14:30:00.000000000Z', 'confirmed')`,
		`INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer, locale)
		 VALUES ('att-1', 'bk-1', 'Ana', 'ana@example.com', 'America/Lima', 1, 'es')`,
		`INSERT INTO event_type_questions (id, event_type_id, label, type, required, position) VALUES ('q-text', 'et-1', 'Tema', 'text', 0, 0)`,
		`INSERT INTO event_type_questions (id, event_type_id, label, type, required, position) VALUES ('q-phone', 'et-1', 'WhatsApp', 'phone', 1, 1)`,
		`INSERT INTO booking_answers (id, booking_id, question_id, value) VALUES ('a-1', 'bk-1', 'q-text', 'Ventas')`,
		`INSERT INTO booking_answers (id, booking_id, question_id, value) VALUES ('a-2', 'bk-1', 'q-phone', '+51 987 654 321')`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	wh, _, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook", []string{webhook.EventReminderMorning})
	fields := []string{webhook.FieldID, webhook.FieldAttendeePhone, webhook.FieldAttendeeWhatsApp,
		webhook.FieldStartLocal, webhook.FieldStartLocalDate, webhook.FieldStartLocalTime, webhook.FieldManageURL, webhook.FieldAnswers}
	if err := e.svc.Update(ctx, testUserID, wh.ID, nil, &fields); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.Enqueue(ctx, webhook.EventReminderMorning, webhook.BookingPayload{
		ID: "bk-1", HostID: testUserID, StartAt: "2026-09-25T14:00:00Z", Status: "confirmed",
		ManageURL: "https://citas.example.com/manage/abc",
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	var raw string
	if err := e.db.QueryRow(`SELECT payload FROM webhook_deliveries WHERE webhook_id = ?`, wh.ID).Scan(&raw); err != nil {
		t.Fatalf("load delivery: %v", err)
	}
	var env struct {
		Event string         `json:"event"`
		Data  map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatal(err)
	}
	if env.Event != webhook.EventReminderMorning {
		t.Errorf("event = %q", env.Event)
	}
	for k, want := range map[string]string{
		"attendee_phone":    "+51987654321",
		"attendee_whatsapp": "51987654321",
		"start_local":       "vie 25 sept 2026, 09:00", // Lima (UTC-5), not Madrid (16:00)
		"start_local_date":  "vie 25 sept 2026",
		"start_local_time":  "09:00",
		"manage_url":        "https://citas.example.com/manage/abc",
	} {
		if env.Data[k] != want {
			t.Errorf("data.%s = %v; want %q", k, env.Data[k], want)
		}
	}
	// The phone question still appears in answers (payload shape unchanged).
	if ans, _ := env.Data["answers"].([]any); len(ans) != 2 {
		t.Errorf("answers = %v; want both answers", env.Data["answers"])
	}
}
