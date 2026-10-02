package handler_test

// Fork (Agenda Maestros 4x4): the WhatsApp notices to the HOST, end to end - who gets
// booking.host_created (fork_host_notices.go: creation and "Pasar a otra persona") and the
// host_5m job (webhook_reminders.go). The member number API, the guard and the texts API are
// in fork_member_whatsapp_test.go; /h/{code} in fork_host_link_test.go.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/webhook"
)

// putWhatsApp sets userID's number (as key) through the API: "" = /me.
func putWhatsApp(t *testing.T, h *handler.Handler, key, userID, body string) *httptest.ResponseRecorder {
	t.Helper()
	path, fn := "/v1/users/me/whatsapp", h.PutMyWhatsApp
	if userID != "" {
		path, fn = "/v1/users/"+userID+"/whatsapp", h.PutUserWhatsApp
	}
	req := authReq(http.MethodPut, path, body, key)
	if userID != "" {
		req.SetPathValue("id", userID)
	}
	rec := httptest.NewRecorder()
	h.RequireAuth(fn)(rec, req)
	return rec
}

// scalarOf is one value of a query, as text.
func scalarOf(t *testing.T, database *sql.DB, q string, args ...any) string {
	t.Helper()
	var v sql.NullString
	if err := database.QueryRow(q, args...).Scan(&v); err != nil && err != sql.ErrNoRows {
		t.Fatalf("scalar %q: %v", q, err)
	}
	return v.String
}

// Every creation path goes through dispatchBookingConfirmation: the host gets
// booking.host_created on their own number, in their own zone, with the client's country.
func TestHostNotice_createdOnBookingGoesToTheHost(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	h.SetPublicBaseURL("https://citas.example.com")
	mustExec(t, database, `UPDATE users SET iana_timezone = 'America/Lima' WHERE id = ?`, userID)
	slug, _ := seedEventTypeHTTP(t, h, key)
	mustStatus(t, putWhatsApp(t, h, key, "", `{"phone":"+51 987 111 222"}`), http.StatusOK, "owner's number")
	hostHook := seedReminderWebhook(t, database, userID, webhook.EventHostCreated,
		[]string{"id", "host_id", "host_whatsapp", "host_whatsapp_message", "manage_url", "attendee_whatsapp"})

	start := futureAt(10, 15, 0) // 10:00 in Lima
	id := bookInZone(t, h, slug, start, "America/Mexico_City", `,"language":"es"`)
	got := waitDeliveries(t, database, hostHook, 1)
	if got[0]["event"] != webhook.EventHostCreated {
		t.Fatalf("event = %v", got[0]["event"])
	}
	data, _ := got[0]["data"].(map[string]any)
	if data["id"] != id || data["host_id"] != userID || data["host_whatsapp"] != "51987111222" {
		t.Errorf("data = %v", data)
	}
	if _, ok := data["manage_url"]; ok {
		t.Errorf("a host notice carried manage_url: %v", data)
	}
	msg, _ := data["host_whatsapp_message"].(string)
	for _, want := range []string{"*Nombre:* Ana (México 🇲🇽)", "*Correo electrónico:* ana@example.com", ", 10:00 a. m.\nHora de Perú 🇵🇪"} {
		if !strings.Contains(msg, want) {
			t.Errorf("host_whatsapp_message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "*Número:*") {
		t.Errorf("no client number known, yet its line stayed: %s", msg)
	}
}

// A host with no number gets nothing queued (the client's confirmation is unaffected).
func TestHostNotice_noNumberNoDelivery(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	hostHook := seedReminderWebhook(t, database, userID, webhook.EventHostCreated, []string{"id", "host_whatsapp"})
	clientHook := seedReminderWebhook(t, database, userID, "booking.created", []string{"id"})
	id := bookInZone(t, h, slug, futureAt(10, 15, 0), "America/Lima", "")
	waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 4 }) // after both enqueues
	waitDeliveries(t, database, clientHook, 1)
	if n := len(deliveryPayloads(t, database, hostHook)); n != 0 {
		t.Errorf("host notices = %d; want 0 (no number)", n)
	}
}

// "Pasar a otra persona": the NEW host gets booking.host_created on their number.
func TestHostNotice_reassignNotifiesTheNewHost(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','h2@example.com','Madrid','Europe/Madrid',0)`)
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','h3@example.com','Lima','America/Lima',0)`)
	mustExec(t, database, `INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES ('u2','+34612345678','x'), ('u3','+51987333444','x')`)
	mustExec(t, database, `INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et1','u2','et-slug','Intro',30)`)
	start := futureAt(10, 15, 0)
	mustExec(t, database, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b1','et1','u2',?,?,'confirmed')`, start.Format(time.RFC3339), start.Add(30*time.Minute).Format(time.RFC3339))
	mustExec(t, database, `INSERT INTO booking_attendees (id,booking_id,name,email,iana_timezone,is_organizer)
		VALUES ('a1','b1','Alice Smith','alice@example.com','America/Bogota',1)`)
	hostHook := seedReminderWebhook(t, database, ownerID, webhook.EventHostCreated, []string{"id", "host_id", "host_whatsapp", "host_whatsapp_message"})

	req := authReq(http.MethodPost, "/v1/bookings/b1/reassign", `{"host_id":"u3"}`, ownerKey)
	req.SetPathValue("id", "b1")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ReassignBooking)(rec, req)
	mustStatus(t, rec, http.StatusOK, "reassign")

	data, _ := waitDeliveries(t, database, hostHook, 1)[0]["data"].(map[string]any)
	if data["host_id"] != "u3" || data["host_whatsapp"] != "51987333444" {
		t.Errorf("reassign notice = %v; want the new host u3 and their number", data)
	}
	if msg, _ := data["host_whatsapp_message"].(string); !strings.Contains(msg, "Alice Smith (Colombia 🇨🇴)") || !strings.Contains(msg, "Hora de Perú 🇵🇪") {
		t.Errorf("host_whatsapp_message = %q", msg)
	}
}

// "Pasar a otra persona" on an unpaid Stripe hold sends the new host NOTHING: it is not an
// appointment yet (the checkout may expire and free the slot). The paid-later dispatch sends
// the single notice. booking.rescheduled, queued after the host notice in the same goroutine,
// tells the side effects ran.
func TestHostNotice_reassignOfAnUnpaidHoldSendsNothing(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','h2@example.com','Madrid','Europe/Madrid',0)`)
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','h3@example.com','Lima','America/Lima',0)`)
	mustExec(t, database, `INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES ('u2','+34612345678','x'), ('u3','+51987333444','x')`)
	mustExec(t, database, `INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et1','u2','et-slug','Intro',30)`)
	start := futureAt(10, 15, 0)
	mustExec(t, database, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status,payment_status)
		VALUES ('b1','et1','u2',?,?,'confirmed','pending')`, start.Format(time.RFC3339), start.Add(30*time.Minute).Format(time.RFC3339))
	mustExec(t, database, `INSERT INTO booking_attendees (id,booking_id,name,email,iana_timezone,is_organizer)
		VALUES ('a1','b1','Alice Smith','alice@example.com','America/Bogota',1)`)
	hostHook := seedReminderWebhook(t, database, ownerID, webhook.EventHostCreated, []string{"id", "host_id", "host_whatsapp", "host_whatsapp_message"})
	doneHook := seedReminderWebhook(t, database, ownerID, "booking.rescheduled", []string{"id"})

	req := authReq(http.MethodPost, "/v1/bookings/b1/reassign", `{"host_id":"u3"}`, ownerKey)
	req.SetPathValue("id", "b1")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ReassignBooking)(rec, req)
	mustStatus(t, rec, http.StatusOK, "reassign")

	waitDeliveries(t, database, doneHook, 1)
	if n := len(deliveryPayloads(t, database, hostHook)); n != 0 {
		t.Errorf("host notices for an unpaid hold = %d; want 0", n)
	}
}

// The host_5m job: sent to whoever hosts the booking WHEN it runs, never with a manage link
// (and never minting one); nothing once cancelled, rescheduled away or too late.
func TestJobWebhookReminder_host5m(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	h.SetPublicBaseURL("https://citas.example.com")
	slug, _ := seedEventTypeHTTP(t, h, key)
	mustStatus(t, putWhatsApp(t, h, key, "", `{"phone":"+51 987 111 222"}`), http.StatusOK, "owner's number")
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','h3@example.com','Lima','America/Lima',0)`)
	mustExec(t, database, `INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES ('u3','+51987333444','x')`)
	id := bookInZone(t, h, slug, futureAt(10, 15, 0), "America/Lima", `,"language":"es"`)
	jobs := waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 4 })
	job, ok := jobs["host_5m"]
	if !ok || job.RunAt != jobs["5m"].RunAt {
		t.Fatalf("host_5m job = %+v; want one at the client's 5 min moment", job)
	}
	hook := seedReminderWebhook(t, database, userID, webhook.EventHostReminder5m,
		[]string{"id", "host_id", "host_whatsapp", "host_whatsapp_message", "manage_url"})
	tokens := func() string {
		return scalarOf(t, database, `SELECT COUNT(*) FROM booking_manage_tokens WHERE booking_id = ?`, id)
	}
	before := tokens()
	run := func(payload string) {
		t.Helper()
		if err := h.JobWebhookReminder(context.Background(), payload); err != nil {
			t.Fatal(err)
		}
	}

	run(job.Payload)
	got := deliveryPayloads(t, database, hook)
	if len(got) != 1 || got[0]["event"] != webhook.EventHostReminder5m {
		t.Fatalf("deliveries = %v", got)
	}
	data, _ := got[0]["data"].(map[string]any)
	if data["host_whatsapp"] != "51987111222" || data["host_id"] != userID {
		t.Errorf("data = %v", data)
	}
	if _, ok := data["manage_url"]; ok || tokens() != before {
		t.Errorf("manage_url %v, tokens %s → %s; a host notice never mints a manage link", data["manage_url"], before, tokens())
	}
	if msg, _ := data["host_whatsapp_message"].(string); !strings.HasPrefix(msg, "*FALTAN 5 MINUTOS:* Ya casi inicia tu sesión de Test Meeting") {
		t.Errorf("host_whatsapp_message = %q", msg)
	}

	// Passed to another person: the job reads the host when it runs.
	mustExec(t, database, `UPDATE bookings SET host_id = 'u3' WHERE id = ?`, id)
	run(job.Payload)
	got = deliveryPayloads(t, database, hook)
	if d, _ := got[len(got)-1]["data"].(map[string]any); len(got) != 2 || d["host_whatsapp"] != "51987333444" {
		t.Errorf("after the reassign: %d deliveries, last %v; want u3's number", len(got), got[len(got)-1])
	}

	// Cancelled: nothing.
	mustExec(t, database, `UPDATE bookings SET status = 'cancelled' WHERE id = ?`, id)
	run(job.Payload)
	// Rescheduled since the job was planned: nothing (its replacement fires instead).
	mustExec(t, database, `UPDATE bookings SET status = 'confirmed' WHERE id = ?`, id)
	bookingTimes(t, database, id, futureAt(12, 15, 0))
	run(job.Payload)
	// Too late (the session already started): nothing.
	late := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	bookingTimes(t, database, id, late)
	run(fmt.Sprintf(`{"booking_id":%q,"kind":"host_5m","start_at":%q}`, id, late.Format(time.RFC3339)))
	if n := len(deliveryPayloads(t, database, hook)); n != 2 {
		t.Errorf("deliveries = %d; want still 2 (cancelled, rescheduled and late send nothing)", n)
	}
}
