package webhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/webhook"
)

// Fork (Agenda Maestros 4x4): the WhatsApp text the agenda composes per event type and
// moment (fork_whatsapp.go) and sends as data.whatsapp_message.

func TestRenderWhatsApp_markersFormattingAndDroppedLines(t *testing.T) {
	tmpl := "Hola *{nombre}* 👋\r\n" +
		"Tu sesión de _{tipo}_ con {Mentor} es el {día} a las {hora} ({fecha}).\n" +
		"Desea hablar de _\"{tema}\"_\n" +
		"\n" +
		"Entra aquí: {enlace}\n" +
		"Precio: {precio}\n" + // unknown marker: kept as written
		"Motivo: {motivo}\n" + // empty here: the whole line goes
		"Cancelar: { cancelar }"
	got := webhook.RenderWhatsApp(tmpl, webhook.WhatsAppValues{
		Nombre:   "María Pérez",
		Mentor:   "Luis",
		Tipo:     "Soporte 1 a 1",
		Tema:     "",
		Fecha:    "martes 29 de septiembre de 2026, 10:00",
		Dia:      "martes 29 de septiembre",
		Hora:     "10:00",
		Enlace:   "https://meet.example.com/sala",
		Cancelar: "https://citas.example.com/manage/abc123",
	})
	want := "Hola *María Pérez* 👋\n" +
		"Tu sesión de _Soporte 1 a 1_ con Luis es el martes 29 de septiembre a las 10:00 (martes 29 de septiembre de 2026, 10:00).\n" +
		"\n" +
		"Entra aquí: https://meet.example.com/sala\n" +
		"Precio: {precio}\n" +
		"Cancelar: https://citas.example.com/manage/abc123"
	if got != want {
		t.Errorf("RenderWhatsApp =\n%s\n--- want ---\n%s", got, want)
	}
}

// A multi-line answer is squeezed onto one line (it would otherwise break the line it
// sits in, and the _cursiva_ around it); blank lines left by a dropped line collapse.
func TestRenderWhatsApp_squeezesValuesAndCollapsesBlankLines(t *testing.T) {
	got := webhook.RenderWhatsApp("Hola {nombre}\n\n{tema}\n\nTema: _{tema}_\n\n\nChao", webhook.WhatsAppValues{
		Nombre: "  Ana  ",
		Tema:   "mis finanzas\n  y mi   negocio ",
	})
	want := "Hola Ana\n\nmis finanzas y mi negocio\n\nTema: _mis finanzas y mi negocio_\n\nChao"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
	got = webhook.RenderWhatsApp("Hola {nombre}\n\n{tema}\n\nChao\n{motivo}\n", webhook.WhatsAppValues{Nombre: "Ana"})
	if want := "Hola Ana\n\nChao"; got != want {
		t.Errorf("empty markers: got %q; want %q", got, want)
	}
}

// Values are inserted once, never re-scanned: a client who types a marker as their name
// cannot pull the cancel link (or anything else) into the text.
func TestRenderWhatsApp_valuesAreNotMarkers(t *testing.T) {
	got := webhook.RenderWhatsApp("Hola {nombre}", webhook.WhatsAppValues{
		Nombre: "{cancelar}", Cancelar: "https://citas.example.com/manage/secret",
	})
	if got != "Hola {cancelar}" {
		t.Errorf("got %q; want the name verbatim", got)
	}
}

func TestUsesMarker(t *testing.T) {
	for _, tc := range []struct {
		tmpl string
		want bool
	}{
		{"Cancela aquí: {cancelar}", true},
		{"Cancela aquí: { Cancelar }", true},
		{"Cancela aquí: {cancelarlo}", false},
		{"Sin enlaces", false},
	} {
		if got := webhook.UsesMarker(tc.tmpl, "cancelar"); got != tc.want {
			t.Errorf("UsesMarker(%q) = %v; want %v", tc.tmpl, got, tc.want)
		}
	}
}

// Every moment has a built-in text, it uses only known markers, and it never claims
// "hoy" or "buenos días": with a fixed morning-reminder zone the reminder can reach a
// client in another country in the afternoon, or on the day before.
func TestDefaultWhatsAppMessages(t *testing.T) {
	all := webhook.WhatsAppValues{
		Nombre: "N", Mentor: "M", Tipo: "T", Tema: "X", Fecha: "F", Dia: "D", Hora: "H",
		Enlace: "E", Cancelar: "C", Motivo: "R",
	}
	for _, m := range webhook.WhatsAppMoments {
		def := webhook.DefaultWhatsAppMessage(m)
		if def == "" {
			t.Errorf("%s: no default text", m)
			continue
		}
		if out := webhook.RenderWhatsApp(def, all); strings.ContainsAny(out, "{}") {
			t.Errorf("%s: default uses an unknown marker: %q", m, out)
		}
		low := strings.ToLower(def)
		for _, bad := range []string{"hoy", "buenos días", "buen día", "mañana a las"} {
			if strings.Contains(low, bad) {
				t.Errorf("%s: default says %q, which a fixed-zone reminder can make false", m, bad)
			}
		}
		if !webhook.ValidWhatsAppMoment(m) {
			t.Errorf("%s: not a valid moment", m)
		}
	}
	if webhook.ValidWhatsAppMoment("reminder_10m") || webhook.DefaultWhatsAppMessage("reminder_10m") != "" {
		t.Error("an unknown moment has a default or passes validation")
	}
}

func TestWhatsAppMomentForEvent(t *testing.T) {
	for event, want := range map[string]string{
		"booking.created":             webhook.WhatsAppCreated,
		webhook.EventReminderMorning:  webhook.WhatsAppReminderMorning,
		webhook.EventReminder1h:       webhook.WhatsAppReminder1h,
		webhook.EventReminder5m:       webhook.WhatsAppReminder5m,
		"booking.cancelled":           webhook.WhatsAppCancelled,
		"booking.rescheduled":         webhook.WhatsAppRescheduled,
		"recording.completed":         "",
		"transcript.ready":            "",
		"notes.ready":                 "",
		"booking.reminder_something2": "",
	} {
		if got := webhook.WhatsAppMomentForEvent(event); got != want {
			t.Errorf("WhatsAppMomentForEvent(%q) = %q; want %q", event, got, want)
		}
	}
}

// The field is appended to AllFields and never part of the default payload.
func TestWhatsAppMessageField(t *testing.T) {
	if webhook.AllFields[len(webhook.AllFields)-1] != webhook.FieldWhatsAppMessage {
		t.Errorf("whatsapp_message is not the last of AllFields: %v", webhook.AllFields)
	}
	if got := webhook.ValidFields([]string{webhook.FieldWhatsAppMessage}); len(got) != 1 {
		t.Errorf("ValidFields dropped whatsapp_message")
	}
}

// seedWhatsAppBooking: the host (testUserID, "Test User") is in Madrid, the client María
// in Lima. Tuesday 29 Sept 2026 15:00 UTC = 10:00 in Lima, 17:00 in Madrid. The event type
// asks a text question first (q-tema), a phone one, and a second text question.
func seedWhatsAppBooking(t *testing.T, e *env, temaAnswer string) {
	t.Helper()
	stmts := []string{
		`UPDATE users SET iana_timezone = 'Europe/Madrid' WHERE id = '` + testUserID + `'`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-wa', '` + testUserID + `', 'soporte-1-a-1', 'Soporte 1 a 1', 40)`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, location_value)
		 VALUES ('bk-wa', 'et-wa', '` + testUserID + `', '2026-09-29T15:00:00.000000000Z', '2026-09-29T15:40:00.000000000Z', 'confirmed', 'https://meet.example.com/sala-cliente')`,
		`INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer, locale)
		 VALUES ('att-wa', 'bk-wa', 'María Pérez', 'maria@example.com', 'America/Lima', 1, 'es')`,
		`INSERT INTO event_type_questions (id, event_type_id, label, type, required, position) VALUES ('q-tema', 'et-wa', '¿Qué te gustaría hablar en esta sesión?', 'text', 0, 0)`,
		`INSERT INTO event_type_questions (id, event_type_id, label, type, required, position) VALUES ('q-phone', 'et-wa', 'WhatsApp', 'phone', 1, 1)`,
		`INSERT INTO event_type_questions (id, event_type_id, label, type, required, position) VALUES ('q-otra', 'et-wa', '¿Algo más?', 'text', 0, 2)`,
		`INSERT INTO booking_answers (id, booking_id, question_id, value) VALUES ('a-phone', 'bk-wa', 'q-phone', '+51 987 654 321')`,
		`INSERT INTO booking_answers (id, booking_id, question_id, value) VALUES ('a-otra', 'bk-wa', 'q-otra', 'otra respuesta')`,
	}
	if temaAnswer != "" {
		stmts = append(stmts, `INSERT INTO booking_answers (id, booking_id, question_id, value) VALUES ('a-tema', 'bk-wa', 'q-tema', '`+temaAnswer+`')`)
	}
	for _, q := range stmts {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
}

// waHook creates a webhook of testUserID for events with the given fields.
func waHook(t *testing.T, e *env, events, fields []string) string {
	t.Helper()
	ctx := context.Background()
	wh, _, err := e.svc.Create(ctx, testUserID, "https://funnelchat.example.com/hook", events)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Update(ctx, testUserID, wh.ID, nil, &fields); err != nil {
		t.Fatal(err)
	}
	return wh.ID
}

// lastData returns data of the webhook's latest delivery.
func lastData(t *testing.T, e *env, webhookID string) map[string]any {
	t.Helper()
	var raw string
	if err := e.db.QueryRow(`SELECT payload FROM webhook_deliveries WHERE webhook_id = ? ORDER BY rowid DESC LIMIT 1`, webhookID).Scan(&raw); err != nil {
		t.Fatalf("load delivery: %v", err)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data
}

func TestEnqueue_whatsAppMessage_defaultTextInTheClientsZone(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Mis finanzas\npersonales")
	ctx := context.Background()
	waID := waHook(t, e, []string{"booking.created"}, []string{webhook.FieldWhatsAppMessage, webhook.FieldAttendeePhone})
	plainID := waHook(t, e, []string{"booking.created"}, []string{webhook.FieldID})

	if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed",
		LocationValue: "https://meet.example.com/sala-cliente",
		ManageURL:     "https://citas.example.com/manage/0a1b2c",
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	data := lastData(t, e, waID)
	want := "Hola María Pérez 👋\n" +
		"Tu sesión de *Soporte 1 a 1* con Test User quedó agendada.\n" +
		"📅 martes 29 de septiembre, a las 10:00\n" + // Lima, not Madrid's 17:00
		"Tema: _Mis finanzas personales_\n" +
		"Para entrar a la sesión: https://meet.example.com/sala-cliente\n" +
		"Si necesitas cancelar o cambiar la fecha: https://citas.example.com/manage/0a1b2c"
	if got := data["whatsapp_message"]; got != want {
		t.Errorf("whatsapp_message =\n%v\n--- want ---\n%s", got, want)
	}
	// Only the link inside the text: this webhook did not select manage_url.
	if _, ok := data["manage_url"]; ok {
		t.Errorf("manage_url present although not selected: %v", data)
	}
	if got := lastData(t, e, plainID); got["whatsapp_message"] != nil {
		t.Errorf("a webhook without the field got whatsapp_message: %v", got)
	}
}

// The saved text wins over the default; {tema} is the FIRST text question only - with it
// unanswered its line goes, even though a later text question was answered.
func TestEnqueue_whatsAppMessage_savedTextAndFirstTextQuestion(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "") // q-tema left blank; q-otra answered
	ctx := context.Background()
	if err := e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{
		webhook.WhatsAppReminder1h: "⏰ *{nombre}*, en 1 hora ({hora}) empieza tu {tipo}.\nDesea hablar de _\"{tema}\"_\n{enlace}",
	}); err != nil {
		t.Fatalf("SetWhatsAppMessages: %v", err)
	}
	id := waHook(t, e, []string{webhook.EventReminder1h}, []string{webhook.FieldWhatsAppMessage})
	if err := e.svc.Enqueue(ctx, webhook.EventReminder1h, webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed",
		LocationValue: "https://meet.example.com/sala-cliente",
	}); err != nil {
		t.Fatal(err)
	}
	want := "⏰ *María Pérez*, en 1 hora (10:00) empieza tu Soporte 1 a 1.\nhttps://meet.example.com/sala-cliente"
	if got := lastData(t, e, id)["whatsapp_message"]; got != want {
		t.Errorf("whatsapp_message = %q; want %q", got, want)
	}
}

// booking.cancelled carries the cancelled text with {motivo}; other moments drop a
// {motivo} line (no reason there).
func TestEnqueue_whatsAppMessage_cancelledHasTheReason(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Ventas")
	ctx := context.Background()
	id := waHook(t, e, []string{"booking.cancelled", "booking.rescheduled"}, []string{webhook.FieldWhatsAppMessage})
	if err := e.svc.Enqueue(ctx, "booking.cancelled", webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "cancelled",
		CancellationReason: "Me surgió un viaje",
	}); err != nil {
		t.Fatal(err)
	}
	msg, _ := lastData(t, e, id)["whatsapp_message"].(string)
	if !strings.Contains(msg, "Motivo: _Me surgió un viaje_") || !strings.Contains(msg, "fue cancelada") {
		t.Errorf("cancelled whatsapp_message = %q", msg)
	}
	if strings.Contains(msg, "manage/") {
		t.Errorf("the default cancelled text carries a manage link: %q", msg)
	}

	if err := e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{
		webhook.WhatsAppRescheduled: "Nueva fecha: {fecha}\nMotivo: {motivo}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enqueue(ctx, "booking.rescheduled", webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed",
		CancellationReason: "stale",
	}); err != nil {
		t.Fatal(err)
	}
	if got := lastData(t, e, id)["whatsapp_message"]; got != "Nueva fecha: martes 29 de septiembre de 2026, 10:00" {
		t.Errorf("rescheduled whatsapp_message = %q", got)
	}
}

// Events with no moment never carry a text, even for a webhook that selected it.
func TestEnqueue_whatsAppMessage_notForOtherEvents(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Ventas")
	id := waHook(t, e, []string{"notes.ready"}, []string{webhook.FieldID, webhook.FieldWhatsAppMessage})
	if err := e.svc.Enqueue(context.Background(), "notes.ready", webhook.BookingPayload{ID: "bk-wa", HostID: testUserID, Status: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	if got := lastData(t, e, id); got["whatsapp_message"] != nil {
		t.Errorf("notes.ready carries whatsapp_message: %v", got)
	}
}

func TestWhatsAppNeedsManageURL(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Ventas")
	ctx := context.Background()
	need := func(event string) bool {
		t.Helper()
		ok, err := e.svc.WhatsAppNeedsManageURL(ctx, event, testUserID, "bk-wa")
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if need("booking.created") {
		t.Error("true with no webhook at all")
	}
	waHook(t, e, []string{"booking.created", webhook.EventReminder5m}, []string{webhook.FieldWhatsAppMessage})
	if !need("booking.created") {
		t.Error("the default confirmation uses {cancelar}: want true")
	}
	if need(webhook.EventReminder5m) {
		t.Error("the default 5-minute text has no {cancelar}: want false")
	}
	if err := e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{webhook.WhatsAppReminder5m: "¿No puedes? {cancelar}"}); err != nil {
		t.Fatal(err)
	}
	if !need(webhook.EventReminder5m) {
		t.Error("a saved 5-minute text with {cancelar}: want true")
	}
	if need(webhook.EventReminder1h) {
		t.Error("no webhook for the 1 h reminder: want false")
	}
}

func TestSetWhatsAppMessages_validationClearAndCascade(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "")
	ctx := context.Background()

	err := e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{"reminder_10m": "x"})
	if !errors.Is(err, webhook.ErrInvalidWhatsAppMessage) {
		t.Errorf("unknown moment: err = %v; want ErrInvalidWhatsAppMessage", err)
	}
	err = e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{webhook.WhatsAppCreated: strings.Repeat("á", webhook.MaxWhatsAppMessageLen+1)})
	if !errors.Is(err, webhook.ErrInvalidWhatsAppMessage) {
		t.Errorf("too long: err = %v; want ErrInvalidWhatsAppMessage", err)
	}

	if err := e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{
		webhook.WhatsAppCreated:   "  Hola {nombre}\r\n✅  ",
		webhook.WhatsAppCancelled: "Chao {nombre}",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.WhatsAppMessages(ctx, "et-wa")
	if err != nil {
		t.Fatal(err)
	}
	if got[webhook.WhatsAppCreated] != "Hola {nombre}\n✅" || got[webhook.WhatsAppCancelled] != "Chao {nombre}" || got[webhook.WhatsAppReminder1h] != "" {
		t.Errorf("saved = %q", got)
	}
	if len(got) != len(webhook.WhatsAppMoments) {
		t.Errorf("WhatsAppMessages returned %d moments; want all %d", len(got), len(webhook.WhatsAppMoments))
	}

	// "" = back to the default; a moment not sent is left alone.
	if err := e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{webhook.WhatsAppCreated: "   "}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.WhatsAppMessages(ctx, "et-wa")
	if got[webhook.WhatsAppCreated] != "" || got[webhook.WhatsAppCancelled] != "Chao {nombre}" {
		t.Errorf("after clearing created: %q", got)
	}

	// Deleting the event type takes its texts with it (CASCADE).
	if _, err := e.db.Exec(`DELETE FROM booking_answers WHERE booking_id = 'bk-wa'`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM bookings WHERE id = 'bk-wa'`, `DELETE FROM event_types WHERE id = 'et-wa'`} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM event_type_whatsapp_messages WHERE event_type_id = 'et-wa'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("texts left after deleting the event type: %d (err %v)", n, err)
	}
}

// ScrubManageURL also removes whatsapp_message - whole: its links are credentials (short
// codes, or the long links when a code could not be made) - once a delivery is finished; a
// delivery still in flight keeps it (its next attempt sends it).
func TestScrubManageURL_removesTheWhatsAppMessage(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Ventas")
	ctx := context.Background()
	id := waHook(t, e, []string{"booking.created"}, []string{webhook.FieldWhatsAppMessage, webhook.FieldManageURL})
	link := "https://citas.example.com/manage/0123456789abcdef0123456789abcdef"
	if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed", ManageURL: link,
	}); err != nil {
		t.Fatal(err)
	}
	var deliveryID string
	if err := e.db.QueryRow(`SELECT id FROM webhook_deliveries WHERE webhook_id = ?`, id).Scan(&deliveryID); err != nil {
		t.Fatal(err)
	}

	// Still pending: nothing is touched.
	if err := e.svc.ScrubManageURL(ctx, deliveryID); err != nil {
		t.Fatal(err)
	}
	if msg, _ := lastData(t, e, id)["whatsapp_message"].(string); !strings.Contains(msg, link) {
		t.Fatalf("pending delivery lost its link: %q", msg)
	}

	if _, err := e.db.Exec(`UPDATE webhook_deliveries SET status = 'success' WHERE id = ?`, deliveryID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ScrubManageURL(ctx, deliveryID); err != nil {
		t.Fatal(err)
	}
	data := lastData(t, e, id)
	if _, ok := data["whatsapp_message"]; ok {
		t.Errorf("finished delivery still holds whatsapp_message: %v", data["whatsapp_message"])
	}
	if _, ok := data["manage_url"]; ok {
		t.Errorf("manage_url not removed: %v", data)
	}
	var raw string
	if err := e.db.QueryRow(`SELECT payload FROM webhook_deliveries WHERE id = ?`, deliveryID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "0123456789abcdef") || !strings.Contains(raw, `"event":"booking.created"`) {
		t.Errorf("stored payload after scrubbing = %s; want the envelope without the link", raw)
	}
	// Idempotent.
	if err := e.svc.ScrubManageURL(ctx, deliveryID); err != nil {
		t.Fatal(err)
	}
}

// A webhook that selected whatsapp_message but not manage_url: the text still goes.
func TestScrubManageURL_removesTheWhatsAppMessageWithoutManageURL(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Ventas")
	ctx := context.Background()
	id := waHook(t, e, []string{"booking.created"}, []string{webhook.FieldID, webhook.FieldWhatsAppMessage})
	if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed",
		ManageURL: "https://citas.example.com/manage/0123456789abcdef0123456789abcdef",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE webhook_deliveries SET status = 'failed' WHERE webhook_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var deliveryID string
	if err := e.db.QueryRow(`SELECT id FROM webhook_deliveries WHERE webhook_id = ?`, id).Scan(&deliveryID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ScrubManageURL(ctx, deliveryID); err != nil {
		t.Fatal(err)
	}
	data := lastData(t, e, id)
	if _, ok := data["whatsapp_message"]; ok || data["id"] != "bk-wa" {
		t.Errorf("after scrubbing a failed delivery: %v; want id kept and whatsapp_message gone", data)
	}
}

func TestEnsureForkSchema_whatsAppTables(t *testing.T) {
	e := newEnv(t)
	if err := webhook.EnsureForkSchema(e.db); err != nil {
		t.Fatal(err)
	}
	for _, obj := range []struct{ typ, name string }{
		{"table", "event_type_whatsapp_messages"},
		{"table", "fork_settings"},
		{"index", "idx_fork_webhook_deliveries_booking"},
	} {
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, obj.typ, obj.name).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s %s: count = %d, err = %v; want 1", obj.typ, obj.name, n, err)
		}
	}
	// No trigger of ours names another table (they would break upstream rebuilds).
	var triggers int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND (sql LIKE '%whatsapp%' OR sql LIKE '%fork_settings%')`).Scan(&triggers); err != nil || triggers != 0 {
		t.Errorf("fork triggers on the new tables = %d (err %v); want 0", triggers, err)
	}
}

func TestWhatsAppDateTexts(t *testing.T) {
	e := newEnv(t)
	lima, err := time.LoadLocation("America/Lima")
	if err != nil {
		t.Fatal(err)
	}
	fecha, dia, hora := e.svc.WhatsAppDateTexts(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC), lima)
	if fecha != "martes 29 de septiembre de 2026, 10:00" || dia != "martes 29 de septiembre" || hora != "10:00" {
		t.Errorf("WhatsAppDateTexts = %q, %q, %q", fecha, dia, hora)
	}
}
