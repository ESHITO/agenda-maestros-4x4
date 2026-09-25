package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/handler"
)

// Fork (Agenda Maestros 4x4): the WhatsApp texts API (whatsapp_messages.go), the morning
// reminder settings (fork_settings.go), the manage-page cancellation and the bookings
// list's notice status (booking_whatsapp_status.go).

// addMember inserts a plain member with an API key and returns that key.
func addMember(t *testing.T, database *sql.DB, id, tz string) string {
	t.Helper()
	key := "member-key-" + id
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES (?,?,?,?,0)`,
		id, id+"@example.com", "Mentor "+id, tz)
	mustExec(t, database, `INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES (?,?,'t',?,'2024-01-01')`,
		"k-"+id, id, sha256HexForTest(key))
	return key
}

// slugCall runs fn behind RequireAuth with {slug} set.
func slugCall(h *handler.Handler, fn http.HandlerFunc, method, path, slug, body, apiKey string) *httptest.ResponseRecorder {
	req := authReq(method, path, body, apiKey)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(fn)(rec, req)
	return rec
}

func TestWhatsAppMessagesAPI_saveClearAndValidate(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	path := "/v1/event-types/" + slug + "/whatsapp-messages"

	got := mustJSON(t, slugCall(h, h.GetWhatsAppMessages, http.MethodGet, path, slug, "", key), http.StatusOK, "get")
	for _, m := range []string{"created", "reminder_morning", "reminder_1h", "reminder_5m", "cancelled", "rescheduled"} {
		if got[m] != "" {
			t.Errorf("fresh event type: %s = %v; want \"\" (the default applies)", m, got[m])
		}
	}
	defaults, _ := got["defaults"].(map[string]any)
	if d, _ := defaults["created"].(string); !strings.Contains(d, "{nombre}") || !strings.Contains(d, "{cancelar}") {
		t.Errorf("defaults.created = %q", d)
	}

	body := `{"created":"Hola *{nombre}* ✅\nTema: _{tema}_","reminder_5m":"   ","cancelled":"Chao {nombre}\nMotivo: {motivo}"}`
	got = mustJSON(t, slugCall(h, h.PutWhatsAppMessages, http.MethodPut, path, slug, body, key), http.StatusOK, "put")
	if got["created"] != "Hola *{nombre}* ✅\nTema: _{tema}_" || got["reminder_5m"] != "" || got["cancelled"] != "Chao {nombre}\nMotivo: {motivo}" {
		t.Errorf("after PUT = %v", got)
	}
	var n int
	database.QueryRow(`SELECT COUNT(*) FROM event_type_whatsapp_messages WHERE event_type_id = ?`, etID).Scan(&n)
	if n != 2 {
		t.Errorf("stored rows = %d; want 2 (a blank text stores nothing)", n)
	}

	// An omitted moment is left alone; "" puts it back on the default.
	got = mustJSON(t, slugCall(h, h.PutWhatsAppMessages, http.MethodPut, path, slug, `{"created":""}`, key), http.StatusOK, "clear")
	if got["created"] != "" || got["cancelled"] != "Chao {nombre}\nMotivo: {motivo}" {
		t.Errorf("after clearing created = %v", got)
	}

	tooLong := fmt.Sprintf(`{"reminder_1h":%q}`, strings.Repeat("a", 3001))
	rec := slugCall(h, h.PutWhatsAppMessages, http.MethodPut, path, slug, tooLong, key)
	mustStatus(t, rec, http.StatusBadRequest, "too long")
	if !strings.Contains(rec.Body.String(), "1 hora antes") {
		t.Errorf("error does not name the moment: %s", rec.Body.String())
	}
	mustStatus(t, slugCall(h, h.PutWhatsAppMessages, http.MethodPut, path, slug, `{not json`, key), http.StatusBadRequest, "bad json")
}

// Same rule as editing the event type: someone who does not own it gets 404 on all three.
func TestWhatsAppMessagesAPI_onlyTheEventTypesOwner(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	member := addMember(t, database, "u-wa", "America/Lima")
	path := "/v1/event-types/" + slug + "/whatsapp-messages"
	mustStatus(t, slugCall(h, h.GetWhatsAppMessages, http.MethodGet, path, slug, "", member), http.StatusNotFound, "member get")
	mustStatus(t, slugCall(h, h.PutWhatsAppMessages, http.MethodPut, path, slug, `{"created":"x"}`, member), http.StatusNotFound, "member put")
	mustStatus(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path+"/preview", slug, `{}`, member), http.StatusNotFound, "member preview")
	mustStatus(t, slugCall(h, h.GetWhatsAppMessages, http.MethodGet, path, slug, "", ""), http.StatusUnauthorized, "anonymous")
}

// The preview is rendered by the server with sample data: the editor's unsaved texts, an
// empty box = the default, and {tema} only when the type asks a text question.
func TestWhatsAppMessagesAPI_preview(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	h.SetPublicBaseURL("https://citas.example.com")
	slug, etID := seedEventTypeHTTP(t, h, key)
	path := "/v1/event-types/" + slug + "/whatsapp-messages/preview"
	body := `{"created":"Hola {nombre}, tu {tipo} con {mentor} es el {dia} a las {hora}.\nTema: _{tema}_\n{cancelar}","cancelled":""}`

	p := mustJSON(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path, slug, body, key), http.StatusOK, "preview")
	created, _ := p["created"].(string)
	if !strings.HasPrefix(created, "Hola María Pérez, tu Test Meeting con Test Host es el ") || !strings.Contains(created, " a las 10:00.") {
		t.Errorf("preview created = %q", created)
	}
	if strings.Contains(created, "Tema:") {
		t.Errorf("no text question, yet the {tema} line was kept: %q", created)
	}
	if !strings.HasSuffix(created, "\nhttps://citas.example.com/manage/ejemplo") {
		t.Errorf("preview {cancelar} = %q", created)
	}
	if c, _ := p["cancelled"].(string); !strings.Contains(c, "Motivo: _Me surgió un imprevisto en el trabajo_") {
		t.Errorf("empty box must preview the default cancelled text with a sample reason: %q", c)
	}
	if r, _ := p["reminder_5m"].(string); !strings.Contains(r, "empieza en 5 minutos") {
		t.Errorf("an omitted moment previews the default: %q", r)
	}
	if p["timezone"] != "America/Lima" || p["has_text_question"] != false {
		t.Errorf("timezone/has_text_question = %v / %v (the setup user is on UTC: sample falls back to Lima)", p["timezone"], p["has_text_question"])
	}

	mustExec(t, database, `INSERT INTO event_type_questions (id, event_type_id, label, type, required, position)
		VALUES ('q-tema', ?, '¿Qué te gustaría hablar en esta sesión?', 'text', 0, 0)`, etID)
	p = mustJSON(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path, slug, body, key), http.StatusOK, "preview with question")
	if c, _ := p["created"].(string); !strings.Contains(c, "\nTema: _Quiero ordenar mis finanzas y armar un presupuesto_\n") {
		t.Errorf("with a text question the sample {tema} shows: %q", c)
	}
}

// putSettings calls PUT /v1/webhooks/settings.
func putSettings(t *testing.T, h *handler.Handler, apiKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PutWebhookSettings)(rec, authReq(http.MethodPut, "/v1/webhooks/settings", body, apiKey))
	return rec
}

// limaMorning is 07:00 in Lima on the Lima calendar day of start, in UTC RFC3339.
func limaMorning(t *testing.T, start time.Time) string {
	t.Helper()
	lima, err := time.LoadLocation("America/Lima")
	if err != nil {
		t.Fatal(err)
	}
	l := start.In(lima)
	return time.Date(l.Year(), l.Month(), l.Day(), 7, 0, 0, 0, lima).UTC().Format(time.RFC3339)
}

func TestPutWebhookSettings_validationAndOwnerOnly(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	member := addMember(t, database, "u-set", "UTC")
	for _, tc := range []struct{ body, what string }{
		{`{"reminder_morning_hour":"7:00"}`, "hour without leading zero"},
		{`{"reminder_morning_hour":"24:00"}`, "hour out of range"},
		{`{"reminder_morning_timezone":"Mars/Olympus"}`, "unknown zone"},
		{`{"reminder_morning_timezone":"Local"}`, "the server's own clock"},
		{`{}`, "nothing to save"},
		{`nope`, "bad json"},
	} {
		mustStatus(t, putSettings(t, h, key, tc.body), http.StatusBadRequest, tc.what)
	}
	mustStatus(t, putSettings(t, h, member, `{"reminder_morning_hour":"07:00"}`), http.StatusForbidden, "member")

	// GET for the member: read-only, can_edit false.
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetWebhookSettings)(rec, authReq(http.MethodGet, "/v1/webhooks/settings", "", member))
	got := mustJSON(t, rec, http.StatusOK, "member get")
	if got["can_edit"] != false || got["reminder_morning_timezone"] != "" || got["reminder_morning_hour"] != "08:00" {
		t.Errorf("member settings = %v", got)
	}
}

// Saving 07:00 America/Lima re-plans the morning jobs already on the calendar right away
// (moved, or dropped when the new moment is no longer before the 1 h reminder), new
// bookings use it too, and "" goes back to each client's own zone.
func TestPutWebhookSettings_fixedZoneResyncsMorningReminders(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	madrid, _ := time.LoadLocation("Europe/Madrid")
	mexico, _ := time.LoadLocation("America/Mexico_City")

	// Madrid client, 18:00 their time: 07:00 Lima comes hours before - moved there.
	late := futureAt(10, 16, 0)
	late = time.Date(late.In(madrid).Year(), late.In(madrid).Month(), late.In(madrid).Day(), 18, 0, 0, 0, madrid).UTC()
	lateID := bookInZone(t, h, slug, late, "Europe/Madrid", "")
	// Madrid client, 10:00 their time: 07:00 Lima is 14:00 (or 13:00) in Madrid - after the
	// meeting, so under the ordering rule there is no morning reminder any more.
	early := futureAt(11, 8, 0)
	early = time.Date(early.In(madrid).Year(), early.In(madrid).Month(), early.In(madrid).Day(), 10, 0, 0, 0, madrid).UTC()
	earlyID := bookInZone(t, h, slug, early, "Europe/Madrid", "")
	for _, id := range []string{lateID, earlyID} {
		waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })
	}

	got := mustJSON(t, putSettings(t, h, key, `{"reminder_morning_hour":"07:00","reminder_morning_timezone":"America/Lima"}`), http.StatusOK, "save")
	if got["reminder_morning_hour"] != "07:00" || got["reminder_morning_timezone"] != "America/Lima" || got["resync_ok"] != true {
		t.Errorf("PUT answer = %v", got)
	}
	if n, _ := got["resynced_bookings"].(float64); n < 2 {
		t.Errorf("resynced_bookings = %v; want the 2 upcoming bookings", got["resynced_bookings"])
	}
	if j := reminderJobs(t, database, lateID); len(j) != 3 || j["morning"].RunAt != limaMorning(t, late) {
		t.Errorf("Madrid 18:00: jobs %v; want the morning job at %s (07:00 Lima)", j, limaMorning(t, late))
	}
	if j := reminderJobs(t, database, earlyID); len(j) != 2 || j["morning"] != (reminderJob{}) {
		t.Errorf("Madrid 10:00: jobs %v; want the morning job dropped (07:00 Lima is after the meeting)", j)
	}

	// A booking made after saving: Mexico City client at 10:00 their time. 07:00 Lima is
	// 06:00 there, before the 1 h reminder: planned at 07:00 Lima.
	cdmx := futureAt(12, 16, 0)
	cdmx = time.Date(cdmx.In(mexico).Year(), cdmx.In(mexico).Month(), cdmx.In(mexico).Day(), 10, 0, 0, 0, mexico).UTC()
	cdmxID := bookInZone(t, h, slug, cdmx, "America/Mexico_City", "")
	waitReminderJobs(t, database, cdmxID, "create after save", func(j map[string]reminderJob) bool {
		return len(j) == 3 && j["morning"].RunAt == limaMorning(t, cdmx)
	})

	// GET reflects it; back to each client's zone restores the early Madrid reminder at
	// 07:00 MADRID (the hour stays 07:00).
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetWebhookSettings)(rec, authReq(http.MethodGet, "/v1/webhooks/settings", "", key))
	if g := mustJSON(t, rec, http.StatusOK, "get"); g["reminder_morning_timezone"] != "America/Lima" || g["can_edit"] != true {
		t.Errorf("GET = %v", g)
	}
	mustStatus(t, putSettings(t, h, key, `{"reminder_morning_timezone":""}`), http.StatusOK, "back to client zones")
	e := early.In(madrid)
	want := time.Date(e.Year(), e.Month(), e.Day(), 7, 0, 0, 0, madrid).UTC().Format(time.RFC3339)
	if j := reminderJobs(t, database, earlyID); len(j) != 3 || j["morning"].RunAt != want {
		t.Errorf("after going back to client zones: %v; want morning at %s (07:00 Madrid)", j, want)
	}
}

// waitDeliveries polls until the webhook has n deliveries (side effects run in a goroutine).
func waitDeliveries(t *testing.T, database *sql.DB, webhookID string, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := deliveryPayloads(t, database, webhookID)
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("webhook %s: %d deliveries; want %d", webhookID, len(got), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Cancelling from the {cancelar} link with a reason: pending reminders are dropped and
// booking.cancelled carries the cancelled WhatsApp text with {motivo}.
func TestCancelByToken_reasonReachesTheCancelledWhatsApp(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := bookInZone(t, h, slug, futureAt(10, 15, 0), "America/Lima", `,"language":"es"`)
	waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })
	whID := seedReminderWebhook(t, database, userID, "booking.cancelled", []string{"id", "whatsapp_message"})

	tok := issueTestToken(t, database, id)
	req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/cancel", strings.NewReader(`{"reason":"Me surgió un viaje"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.CancelByToken(rec, req)
	mustStatus(t, rec, http.StatusOK, "cancel by token")

	waitReminderJobs(t, database, id, "cancel", func(j map[string]reminderJob) bool { return len(j) == 0 })
	data, _ := waitDeliveries(t, database, whID, 1)[0]["data"].(map[string]any)
	msg, _ := data["whatsapp_message"].(string)
	if !strings.HasPrefix(msg, "Hola Ana, tu sesión de *Test Meeting* con Test Host del ") ||
		!strings.Contains(msg, "fue cancelada.\nMotivo: _Me surgió un viaje_\n") {
		t.Errorf("cancelled whatsapp_message = %q", msg)
	}
	var reason string
	database.QueryRow(`SELECT cancellation_reason FROM bookings WHERE id = ?`, id).Scan(&reason)
	if reason != "Me surgió un viaje" {
		t.Errorf("cancellation_reason = %q", reason)
	}
}

// A webhook that selected ONLY whatsapp_message still gets a working {cancelar}: the
// handler mints an additive manage token for the text, and manage_url itself stays out of
// the payload (that webhook did not ask for it).
func TestJobWebhookReminder_whatsAppOnlyWebhookGetsAWorkingCancelLink(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	h.SetPublicBaseURL("https://citas.example.com")
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := bookInZone(t, h, slug, futureAt(10, 15, 0), "America/Lima", `,"language":"es"`)
	jobs := waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })
	whID := seedReminderWebhook(t, database, userID, "booking.reminder_morning", []string{"whatsapp_message"})

	if err := h.JobWebhookReminder(context.Background(), jobs["morning"].Payload); err != nil {
		t.Fatalf("JobWebhookReminder: %v", err)
	}
	got := deliveryPayloads(t, database, whID)
	if len(got) != 1 {
		t.Fatalf("deliveries = %d; want 1", len(got))
	}
	data, _ := got[0]["data"].(map[string]any)
	if _, ok := data["manage_url"]; ok {
		t.Errorf("manage_url sent to a webhook that did not select it: %v", data)
	}
	msg, _ := data["whatsapp_message"].(string)
	const prefix = "https://citas.example.com/manage/"
	i := strings.Index(msg, prefix)
	if i < 0 || !strings.HasPrefix(msg, "Hola Ana ☀️\nTe recordamos tu sesión de *Test Meeting* con Test Host: ") {
		t.Fatalf("whatsapp_message = %q", msg)
	}
	tok := strings.TrimSpace(msg[i+len(prefix):])
	b, err := booking.New(database).ValidateManageToken(context.Background(), tok)
	if err != nil || b.ID != id {
		t.Fatalf("the {cancelar} link does not open this booking: %v", err)
	}

	// Without {cancelar} in the text, no token is minted at all.
	mustExec(t, database, `DELETE FROM webhook_deliveries`)
	var before, after int
	database.QueryRow(`SELECT COUNT(*) FROM booking_manage_tokens WHERE booking_id = ?`, id).Scan(&before)
	whID5 := seedReminderWebhook(t, database, userID, "booking.reminder_5m", []string{"whatsapp_message"})
	if err := h.JobWebhookReminder(context.Background(), jobs["5m"].Payload); err != nil {
		t.Fatal(err)
	}
	database.QueryRow(`SELECT COUNT(*) FROM booking_manage_tokens WHERE booking_id = ?`, id).Scan(&after)
	if after != before {
		t.Errorf("manage tokens %d → %d for a text with no {cancelar}", before, after)
	}
	// The job runs early here (a test calls it directly): it still fires, with a text that
	// has no link.
	got5 := deliveryPayloads(t, database, whID5)
	if len(got5) != 1 {
		t.Fatalf("5 min deliveries = %d; want 1", len(got5))
	}
	d5, _ := got5[0]["data"].(map[string]any)
	if m, _ := d5["whatsapp_message"].(string); !strings.Contains(m, "empieza en 5 minutos") || strings.Contains(m, "/manage/") {
		t.Errorf("5 min whatsapp_message = %q", m)
	}
}

// The public page asks why (required box) and, once cancelled, offers a NEW booking of the
// same type or a friendly close - while the same-booking reschedule stays in place.
func TestManagePage_cancelAsksWhyAndOffersANewBooking(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	h.SetForceLocale("es")
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	tok := issueTestToken(t, database, id)

	render := func() string {
		req := httptest.NewRequest(http.MethodGet, "/manage/"+tok, nil)
		req.SetPathValue("token", tok)
		rec := httptest.NewRecorder()
		h.ManagePage(rec, req)
		mustStatus(t, rec, http.StatusOK, "manage page")
		return rec.Body.String()
	}
	body := render()
	for _, want := range []string{
		"¿Por qué cancelas tu sesión?",
		`<textarea id="cancel-reason"`,
		"required",
		"Confirmar cancelación",
		"Tu sesión fue cancelada",
		"¿Deseas reprogramar?",
		`class="rebook-question"`,
		`id="rebook-no"`,
		`href="/book/` + slug + `"`,
		"Sí, elegir otra fecha",
		"No, cerrar",
		"¡Gracias por avisarnos!",
		`id="reschedule-btn"`, // the same-booking reschedule is untouched
		"cancel_reason_required",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("manage page lacks %q", want)
		}
	}

	// The type stopped being bookable (inactive, or "No listado": is_public = 0): /book/{slug}
	// would 404, so no "choose another date" - the friendly close comes right away.
	for _, off := range []string{`is_active = 0`, `is_public = 0`} {
		mustExec(t, database, `UPDATE event_types SET is_active = 1, is_public = 1 WHERE slug = ?`, slug)
		mustExec(t, database, `UPDATE event_types SET `+off+` WHERE slug = ?`, slug)
		body = render()
		// (the question's text is also in the page's i18n table, so look for its markup)
		if strings.Contains(body, `href="/book/`) || strings.Contains(body, `class="rebook-question"`) || strings.Contains(body, `id="rebook-no"`) {
			t.Errorf("%s: the page still offers a new booking of a type /book cannot open", off)
		}
		if !strings.Contains(body, "Tu sesión fue cancelada") || !strings.Contains(body, "Tu horario quedó libre.") {
			t.Errorf("%s: the cancelled view lacks its title or the friendly close", off)
		}
	}
	mustExec(t, database, `UPDATE event_types SET is_active = 1, is_public = 1 WHERE slug = ?`, slug)

	mustExec(t, database, `UPDATE bookings SET status = 'cancelled' WHERE id = ?`, id)
	body = render()
	if !strings.Contains(body, "Reserva ya cancelada") || !strings.Contains(body, `href="/book/`+slug+`"`) || !strings.Contains(body, "Elegir otra fecha") {
		t.Errorf("an already-cancelled booking should offer a new date")
	}
	// Its icon must not use the class "info": booking.css styles .info as the side panel.
	if strings.Contains(body, `class="state-icon info"`) {
		t.Errorf("state icon uses the .info class, which booking.css sizes as the side panel")
	}
	mustExec(t, database, `UPDATE event_types SET is_public = 0 WHERE slug = ?`, slug)
	if body = render(); strings.Contains(body, `href="/book/`) {
		t.Errorf("an already-cancelled booking of an unlisted type should not link to /book")
	}
}

type listedNotice struct {
	Kind, Status, At string
}

type listedBooking struct {
	ID            string         `json:"id"`
	Status        string         `json:"status"`
	EventTypeName string         `json:"event_type_name"`
	HostName      string         `json:"host_name"`
	WhatsApp      []listedNotice `json:"whatsapp"`
}

func listBookingsWA(t *testing.T, h *handler.Handler, apiKey, query string) map[string]listedBooking {
	t.Helper()
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(rec, authReq(http.MethodGet, "/v1/bookings?"+query, "", apiKey))
	mustStatus(t, rec, http.StatusOK, "list bookings")
	var body struct {
		Items []listedBooking `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := map[string]listedBooking{}
	for _, it := range body.Items {
		out[it.ID] = it
	}
	return out
}

func noticeMap(b listedBooking) map[string]listedNotice {
	m := map[string]listedNotice{}
	for _, n := range b.WhatsApp {
		m[n.Kind] = n
	}
	return m
}

// The four notices per booking: sent / pending (with the planned moment) / failed /
// not_applicable (morning left out by the ordering rule; no webhook for 5 min), and
// cancelled once the meeting is cancelled from the panel - which stops the pending jobs.
func TestListBookings_whatsAppNoticesAndPanelCancel(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	createdHook := seedReminderWebhook(t, database, userID, "booking.created", []string{"id"})
	seedReminderWebhook(t, database, userID, "booking.reminder_morning", []string{"id"})
	seedReminderWebhook(t, database, userID, "booking.reminder_1h", []string{"id"})
	cancelHook := seedReminderWebhook(t, database, userID, "booking.cancelled", []string{"id", "whatsapp_message"})

	start := futureAt(10, 15, 0) // 10:00 Lima: morning at 08:00 Lima = 13:00Z
	id := bookInZone(t, h, slug, start, "America/Lima", "")
	early := futureAt(11, 13, 30) // 08:30 Lima: no morning reminder (it would follow the 1 h one)
	earlyID := bookInZone(t, h, slug, early, "America/Lima", "")
	waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })
	waitReminderJobs(t, database, earlyID, "create early", func(j map[string]reminderJob) bool { return len(j) == 2 })
	waitDeliveries(t, database, createdHook, 2)
	mustExec(t, database, `UPDATE webhook_deliveries SET status = 'success', last_attempted_at = '2026-01-02T03:04:05Z' WHERE booking_id = ? AND event = 'booking.created'`, id)
	mustExec(t, database, `UPDATE webhook_deliveries SET status = 'failed', attempt_count = 5, last_attempted_at = '2026-01-02T03:04:05Z' WHERE booking_id = ? AND event = 'booking.created'`, earlyID)

	list := listBookingsWA(t, h, key, "when=upcoming")
	b := list[id]
	if b.EventTypeName != "Test Meeting" || b.HostName != "Test Host" {
		t.Errorf("names = %q / %q; want the type's and the host's in the default view", b.EventTypeName, b.HostName)
	}
	if len(b.WhatsApp) != 4 || b.WhatsApp[0].Kind != "created" || b.WhatsApp[3].Kind != "5m" {
		t.Fatalf("whatsapp = %+v; want 4 notices in order", b.WhatsApp)
	}
	day := start.Format("2006-01-02")
	for kind, want := range map[string]listedNotice{
		"created": {"created", "sent", "2026-01-02T03:04:05Z"},
		"morning": {"morning", "pending", day + "T13:00:00Z"},
		"1h":      {"1h", "pending", day + "T14:00:00Z"},
		"5m":      {"5m", "not_applicable", ""}, // planned, but no webhook would receive it
	} {
		if got := noticeMap(b)[kind]; got != want {
			t.Errorf("%s = %+v; want %+v", kind, got, want)
		}
	}
	e := noticeMap(list[earlyID])
	if e["created"].Status != "failed" || e["morning"].Status != "not_applicable" || e["1h"].Status != "pending" {
		t.Errorf("early booking notices = %+v", e)
	}

	// Cancel from the panel, with a reason: jobs stop, the notices say so.
	req := authReq(http.MethodPost, "/v1/bookings/"+id+"/cancel", `{"reason":"El mentor tuvo un imprevisto"}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CancelBooking)(rec, req)
	mustStatus(t, rec, http.StatusOK, "panel cancel")
	waitReminderJobs(t, database, id, "cancel", func(j map[string]reminderJob) bool { return len(j) == 0 })
	data, _ := waitDeliveries(t, database, cancelHook, 1)[0]["data"].(map[string]any)
	if msg, _ := data["whatsapp_message"].(string); !strings.Contains(msg, "Motivo: _El mentor tuvo un imprevisto_") {
		t.Errorf("cancelled whatsapp_message = %q", msg)
	}

	c := noticeMap(listBookingsWA(t, h, key, "status=cancelled")[id])
	if c["created"].Status != "sent" || c["morning"].Status != "cancelled" || c["1h"].Status != "cancelled" || c["5m"].Status != "cancelled" {
		t.Errorf("after cancel = %+v; want the confirmation still sent and the reminders cancelled", c)
	}
}

// A member sees the notices of the bookings they host, and only those (the list's own rule).
func TestListBookings_whatsAppNoticesFollowTheListVisibility(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	_, etID := seedEventTypeHTTP(t, h, key)
	member := addMember(t, database, "u-vis", "UTC")
	mustExec(t, database, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b-member', ?, 'u-vis', '2027-07-01T10:00:00Z', '2027-07-01T10:30:00Z', 'confirmed')`, etID)
	mustExec(t, database, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b-owner', ?, (SELECT id FROM users WHERE email = 'host@example.com'), '2027-07-01T12:00:00Z', '2027-07-01T12:30:00Z', 'confirmed')`, etID)

	got := listBookingsWA(t, h, member, "when=upcoming&scope=all")
	if _, ok := got["b-owner"]; ok || len(got) != 1 {
		t.Fatalf("member list = %v; want only b-member", got)
	}
	if b := got["b-member"]; len(b.WhatsApp) != 4 || b.HostName != "Mentor u-vis" {
		t.Errorf("member's own booking = %+v", b)
	}
}

// meetCalendar is a Google destination calendar whose events come back with a Meet link.
type meetCalendar struct{ telephoneCalendar }

func (p meetCalendar) CreateEvent(_ context.Context, _ string, in calendar.CreateEventParams) (string, string, string, error) {
	return "event-id", "https://meet.google.com/abc-defg-hij", "primary", nil
}

// A Meet link generated while booking reaches booking.created - its location_value and the
// confirmation WhatsApp's {enlace} line - not only the e-mails and the stored booking.
func TestBookingCreated_generatedMeetLinkReachesTheWhatsAppConfirmation(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	mustExec(t, database, `UPDATE event_types SET location_type = 'google_meet' WHERE id = ?`, etID)
	mustExec(t, database, `INSERT INTO calendar_connections (id,user_id,provider,access_token_enc,calendar_id,is_destination) VALUES ('conn',?,'google','test','primary',1)`, userID)
	svc := calendar.NewService(database)
	svc.Register(meetCalendar{})
	h.SetCalendar(svc)
	whID := seedReminderWebhook(t, database, userID, "booking.created", []string{"id", "location_value", "whatsapp_message"})

	bookInZone(t, h, slug, futureAt(10, 15, 0), "America/Lima", `,"language":"es"`)
	data, _ := waitDeliveries(t, database, whID, 1)[0]["data"].(map[string]any)
	const link = "https://meet.google.com/abc-defg-hij"
	if got, _ := data["location_value"].(string); got != link {
		t.Errorf("location_value = %q; want the generated Meet link", got)
	}
	if msg, _ := data["whatsapp_message"].(string); !strings.Contains(msg, "Para entrar a la sesión: "+link) {
		t.Errorf("whatsapp_message = %q; want the {enlace} line with the Meet link", msg)
	}
}
