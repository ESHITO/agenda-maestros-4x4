package webhook_test

// Fork (Agenda Maestros 4x4): the WhatsApp notices to the HOST (fork_host.go) - events,
// payload audience, skips, texts, {enlace_mentor} and the /h codes.

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/webhook"
)

func TestHostEventsAndMoments(t *testing.T) {
	for event, want := range map[string]bool{
		webhook.EventHostCreated: true, webhook.EventHostReminder5m: true,
		"booking.created": false, webhook.EventReminder5m: false, "": false,
	} {
		if got := webhook.IsHostEvent(event); got != want {
			t.Errorf("IsHostEvent(%q) = %v", event, got)
		}
	}
	if webhook.HostWhatsAppMomentForEvent(webhook.EventHostCreated) != webhook.WhatsAppHostCreated ||
		webhook.HostWhatsAppMomentForEvent(webhook.EventHostReminder5m) != webhook.WhatsAppHostReminder5m ||
		webhook.HostWhatsAppMomentForEvent("booking.created") != "" {
		t.Error("HostWhatsAppMomentForEvent maps the wrong moments")
	}
	// The client's mapping never answers for a host event (no client text, no manage link).
	if webhook.WhatsAppMomentForEvent(webhook.EventHostCreated) != "" || webhook.WhatsAppMomentForEvent(webhook.EventHostReminder5m) != "" {
		t.Error("WhatsAppMomentForEvent answered for a host event")
	}
	if len(webhook.WhatsAppMoments) != 8 || !webhook.ValidWhatsAppMoment(webhook.WhatsAppHostCreated) ||
		!webhook.IsHostMoment(webhook.WhatsAppHostReminder5m) || webhook.IsHostMoment(webhook.WhatsAppReminder5m) {
		t.Errorf("moments = %v", webhook.WhatsAppMoments)
	}
	for _, m := range webhook.WhatsAppHostMoments {
		if webhook.DefaultWhatsAppMessage(m) == "" {
			t.Errorf("no default text for %s", m)
		}
	}
}

// hostEnv: seedWhatsAppBooking (host testUserID in Madrid, client María in Lima with a
// +51 number), the host's WhatsApp number and the real phone table.
func hostEnv(t *testing.T, tema string) *env {
	t.Helper()
	e := newEnv(t)
	seedWhatsAppBooking(t, e, tema)
	e.svc.SetPhoneTable(phoneTable(t))
	if _, err := e.db.Exec(`INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES (?, '+34612345678', 'x')`, testUserID); err != nil {
		t.Fatal(err)
	}
	return e
}

func hostPayload() webhook.BookingPayload {
	return webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", EndAt: "2026-09-29T15:40:00Z",
		Status: "confirmed", LocationValue: "https://meet.example.com/sala-cliente",
		ManageURL: "https://citas.example.com/manage/0a1b2c",
	}
}

// The audience is in the PAYLOAD: with EVERY field ticked (what the panel does for the
// owner), a host notice carries no client destination and no manage link, and a client
// event carries no host field.
func TestEnqueue_hostNotice_payloadExclusivity(t *testing.T) {
	e := hostEnv(t, "Ventas")
	ctx := context.Background()
	hostHook := waHook(t, e, []string{webhook.EventHostCreated}, webhook.AllFields)
	clientHook := waHook(t, e, []string{"booking.created"}, webhook.AllFields)

	if err := e.svc.Enqueue(ctx, webhook.EventHostCreated, hostPayload()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enqueue(ctx, "booking.created", hostPayload()); err != nil {
		t.Fatal(err)
	}
	host := lastData(t, e, hostHook)
	for _, k := range []string{"attendee_phone", "attendee_whatsapp", "whatsapp_message", "manage_url"} {
		if v, ok := host[k]; ok {
			t.Errorf("host notice carries %s = %v", k, v)
		}
	}
	if host["host_phone"] != "+34612345678" || host["host_whatsapp"] != "34612345678" {
		t.Errorf("host destination = %v / %v", host["host_phone"], host["host_whatsapp"])
	}
	if msg, _ := host["host_whatsapp_message"].(string); !strings.Contains(msg, "*Número:* +51987654321") {
		t.Errorf("the client's number belongs in the TEXT: %q", msg)
	}
	if host["location_value"] != "https://meet.example.com/sala-cliente" || host["attendee_email"] != "maria@example.com" {
		t.Errorf("host notice lost the ordinary fields: %v", host)
	}

	client := lastData(t, e, clientHook)
	for _, k := range []string{"host_phone", "host_whatsapp", "host_whatsapp_message"} {
		if v, ok := client[k]; ok {
			t.Errorf("client event carries %s = %v", k, v)
		}
	}
	if client["attendee_whatsapp"] != "51987654321" || client["whatsapp_message"] == nil || client["manage_url"] == nil {
		t.Errorf("client event lost its own fields: %v", client)
	}
}

// No number, or an archived host: nothing is queued, a debug line says why, and no log line
// carries the number.
func TestEnqueue_hostNotice_skipsWithoutNumberOrArchived(t *testing.T) {
	e := hostEnv(t, "Ventas")
	var buf bytes.Buffer
	e.svc.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ctx := context.Background()
	id := waHook(t, e, []string{webhook.EventHostCreated}, []string{webhook.FieldHostWhatsApp, webhook.FieldHostWhatsAppMessage})

	if _, err := e.db.Exec(`UPDATE users SET archived_at = '2026-09-01T00:00:00Z' WHERE id = ?`, testUserID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enqueue(ctx, webhook.EventHostCreated, hostPayload()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE users SET archived_at = NULL WHERE id = ?`, testUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`DELETE FROM fork_member_phones`); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enqueue(ctx, webhook.EventHostReminder5m, hostPayload()); err != nil {
		t.Fatal(err)
	}
	if n := deliveriesFor(t, e, id); n != 0 {
		t.Errorf("deliveries = %d; want 0", n)
	}
	var all int
	e.db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&all)
	if all != 0 {
		t.Errorf("webhook_deliveries rows = %d; want 0", all)
	}
	logs := buf.String()
	if strings.Count(logs, "host notice skipped: host has no WhatsApp number or is archived") != 1 {
		t.Errorf("want one skip line (the 5 min notice had no subscriber): %s", logs)
	}
	if strings.Contains(logs, "612345678") {
		t.Errorf("the number reached the log: %s", logs)
	}

	// Subscribed to the 5 min notice too: now it is logged as well.
	waHook(t, e, []string{webhook.EventHostReminder5m}, []string{webhook.FieldHostWhatsApp})
	if err := e.svc.Enqueue(ctx, webhook.EventHostReminder5m, hostPayload()); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "host notice skipped") != 2 {
		t.Errorf("no skip line for the subscribed 5 min notice: %s", buf.String())
	}
}

// The default texts, as the owner wrote them: the client's country beside the name, and the
// date in the HOST's zone with their country (Spain's zones tell two times: the city too).
func TestEnqueue_hostNotice_defaultTexts(t *testing.T) {
	e := hostEnv(t, "Ventas")
	ctx := context.Background()
	created := waHook(t, e, []string{webhook.EventHostCreated}, []string{webhook.FieldHostWhatsApp, webhook.FieldHostWhatsAppMessage})
	fiveMin := waHook(t, e, []string{webhook.EventHostReminder5m}, []string{webhook.FieldHostWhatsApp, webhook.FieldHostWhatsAppMessage})
	if err := e.svc.Enqueue(ctx, webhook.EventHostCreated, hostPayload()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enqueue(ctx, webhook.EventHostReminder5m, hostPayload()); err != nil {
		t.Fatal(err)
	}
	want := "*Nueva sesión agendada: Soporte 1 a 1*\n" +
		"\n" +
		"*Nombre:* María Pérez (Perú 🇵🇪)\n" +
		"\n" +
		"*Correo electrónico:* maria@example.com\n" +
		"\n" +
		"*Número:* +51987654321\n" +
		"\n" +
		"*Tema a tratar* \"Ventas\"\n" +
		"\n" +
		"*FECHA Y HORA:*\n" +
		"martes 29 de septiembre, 5:00 p. m.\n" + // 15:00 UTC in Madrid (CEST), not Lima's 10:00
		"Hora de España 🇪🇸 (Madrid)"
	if got := lastData(t, e, created)["host_whatsapp_message"]; got != want {
		t.Errorf("host_created =\n%v\n--- want ---\n%s", got, want)
	}
	want5 := "*FALTAN 5 MINUTOS:* Ya casi inicia tu sesión de Soporte 1 a 1\n" +
		"\n" +
		"*ENTRA AHORA:* https://meet.example.com/sala-cliente\n" + // a Meet booking: the meeting's own link
		"\n" +
		"*Nombre:* María Pérez\n" +
		"*Correo:* maria@example.com\n" +
		"\n" +
		"\"Ventas\""
	if got := lastData(t, e, fiveMin)["host_whatsapp_message"]; got != want5 {
		t.Errorf("host_reminder_5m =\n%v\n--- want ---\n%s", got, want5)
	}
}

// A copy's saved host text is its template's (the same table), {tema} unanswered drops its
// line, a client marker in a host text resolves to nothing, an unknown country leaves the
// bare name, and an in-person booking has no "ENTRA AHORA" line.
func TestEnqueue_hostNotice_savedTextAndFallbacks(t *testing.T) {
	e := hostEnv(t, "")
	ctx := context.Background()
	if err := e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{
		webhook.WhatsAppHostReminder5m: "*Nueva Mentoría agendada*\n{cliente}\nTema: {tema}\nEnlace del cliente: {enlace}\nEntra: {enlace_mentor}\n{nombre_corto} - {correo}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE booking_answers SET value = '987654321' WHERE id = 'a-phone'`); err != nil { // no country code
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE booking_attendees SET iana_timezone = 'UTC' WHERE booking_id = 'bk-wa'`); err != nil {
		t.Fatal(err)
	}
	id := waHook(t, e, []string{webhook.EventHostReminder5m}, []string{webhook.FieldHostWhatsAppMessage})
	p := hostPayload()
	p.LocationValue = "Av. Larco 123, Miraflores"
	if _, err := e.db.Exec(`UPDATE bookings SET location_type = 'in_person', location_value = 'Av. Larco 123, Miraflores' WHERE id = 'bk-wa'`); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enqueue(ctx, webhook.EventHostReminder5m, p); err != nil {
		t.Fatal(err)
	}
	want := "*Nueva Mentoría agendada*\nMaría Pérez\nMaría Pérez - maria@example.com"
	if got := lastData(t, e, id)["host_whatsapp_message"]; got != want {
		t.Errorf("host_reminder_5m = %q; want %q", got, want)
	}
}

// A host text written with the client's markers ({fecha}, {dia}, {hora}) shows the HOST's
// clock, never the client's: client María in Lima (10:00), host in Madrid (17:00 CEST).
func TestEnqueue_hostNotice_clientDateMarkersUseTheHostZone(t *testing.T) {
	e := hostEnv(t, "Ventas")
	ctx := context.Background()
	if err := e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{
		webhook.WhatsAppHostCreated: "El {dia} a las {hora}.\n{fecha}\n{fecha_mentor} - {pais_mentor}",
	}); err != nil {
		t.Fatal(err)
	}
	id := waHook(t, e, []string{webhook.EventHostCreated}, []string{webhook.FieldHostWhatsAppMessage})
	if err := e.svc.Enqueue(ctx, webhook.EventHostCreated, hostPayload()); err != nil {
		t.Fatal(err)
	}
	want := "El martes 29 de septiembre a las 5:00 p. m.\n" +
		"martes 29 de septiembre de 2026, 5:00 p. m.\n" +
		"martes 29 de septiembre, 5:00 p. m. - España 🇪🇸 (Madrid)"
	if got := lastData(t, e, id)["host_whatsapp_message"]; got != want {
		t.Errorf("host_created = %q; want %q (Lima's 10:00 a. m. is the client's clock)", got, want)
	}
}

var hostCodeRE = regexp.MustCompile(`https://citas\.example\.com/h/([a-z0-9]{8})\b`)

// A LiveKit booking: {enlace_mentor} is a NEW /h code per message, made for THIS host; with
// no base URL the handler's long host link is the fallback; without that either, the line
// goes.
func TestEnqueue_hostNotice_liveKitHostLink(t *testing.T) {
	e := hostEnv(t, "Ventas")
	ctx := context.Background()
	if _, err := e.db.Exec(`UPDATE bookings SET location_type = 'livekit', livekit_room = 'booking-bk-wa' WHERE id = 'bk-wa'`); err != nil {
		t.Fatal(err)
	}
	e.svc.SetShortLinkBaseURL(func() string { return "https://citas.example.com" })
	id := waHook(t, e, []string{webhook.EventHostReminder5m}, []string{webhook.FieldHostWhatsAppMessage})
	p := hostPayload()
	p.LocationValue = "https://citas.example.com/room/booking-bk-wa?t=attendee-token-attendee-token-attendee-token"
	send := func() string {
		t.Helper()
		if err := e.svc.Enqueue(ctx, webhook.EventHostReminder5m, p); err != nil {
			t.Fatal(err)
		}
		msg, _ := lastData(t, e, id)["host_whatsapp_message"].(string)
		return msg
	}
	msg := send()
	m := hostCodeRE.FindStringSubmatch(msg)
	if m == nil || !strings.Contains(msg, "*ENTRA AHORA:* https://citas.example.com/h/"+m[1]+"\n") || strings.Contains(msg, "/room/") {
		t.Fatalf("host_reminder_5m = %q; want a /h short link and never the attendee's room link", msg)
	}
	booking, user, err := e.svc.ResolveHostShortLink(ctx, m[1], time.Now())
	if err != nil || booking != "bk-wa" || user != testUserID {
		t.Errorf("ResolveHostShortLink = %q, %q, %v", booking, user, err)
	}
	if again := hostCodeRE.FindStringSubmatch(send()); again == nil || again[1] == m[1] {
		t.Errorf("a second message must draw a new code: %v then %v", m, again)
	}
	var rows int
	e.db.QueryRow(`SELECT COUNT(*) FROM fork_host_short_links WHERE booking_id = 'bk-wa' AND user_id = ?`, testUserID).Scan(&rows)
	if rows != 2 {
		t.Errorf("fork_host_short_links rows = %d; want 2", rows)
	}

	// No base URL: the handler's long host link.
	e.svc.SetShortLinkBaseURL(nil)
	var asked []string
	e.svc.SetHostRoomLinker(func(_ context.Context, bookingID, hostID string) string {
		asked = append(asked, bookingID+"/"+hostID)
		return "https://citas.example.com/room/booking-bk-wa?t=host-token"
	})
	if msg := send(); !strings.Contains(msg, "*ENTRA AHORA:* https://citas.example.com/room/booking-bk-wa?t=host-token\n") {
		t.Errorf("fallback = %q", msg)
	}
	if len(asked) != 1 || asked[0] != "bk-wa/"+testUserID {
		t.Errorf("linker asked %v; want once, for bk-wa and the host", asked)
	}
	// Neither: the line goes, the rest stays.
	e.svc.SetHostRoomLinker(nil)
	if msg := send(); strings.Contains(msg, "ENTRA AHORA") || !strings.Contains(msg, "FALTAN 5 MINUTOS") {
		t.Errorf("without any link = %q; want the ENTRA AHORA line dropped", msg)
	}
}

// RenderWhatsAppMoment keeps each audience's values to itself; RenderWhatsApp is unchanged.
func TestRenderWhatsAppMoment_audience(t *testing.T) {
	v := webhook.WhatsAppValues{
		Nombre: "María", Enlace: "https://e", Cancelar: "https://c", Motivo: "viaje",
		Cliente: "María (Perú 🇵🇪)", Correo: "m@x.com", Telefono: "+51987654321", NombreCorto: "María P",
		FechaMentor: "viernes 2 de octubre, 3:30 p. m.", PaisMentor: "Perú 🇵🇪", EnlaceMentor: "https://h",
		PaisCliente: "Perú 🇵🇪",
	}
	tmpl := "Hola {nombre}\nEntra: {enlace}\nCancela: {cancelar}\nMotivo: {motivo}\nCliente: {cliente}\nCorreo: {correo}\n" +
		"Tel: {teléfono}\nCorto: {nombre_corto}\nFecha: {fecha_mentor} ({país_mentor})\nHost: {enlace_mentor}\nPaís: {pais_cliente}"
	client := webhook.RenderWhatsAppMoment(webhook.WhatsAppCreated, tmpl, v)
	if client != "Hola María\nEntra: https://e\nCancela: https://c\nMotivo: viaje" {
		t.Errorf("client moment = %q", client)
	}
	host := webhook.RenderWhatsAppMoment(webhook.WhatsAppHostCreated, tmpl, v)
	want := "Hola María\nCliente: María (Perú 🇵🇪)\nCorreo: m@x.com\nTel: +51987654321\nCorto: María P\n" +
		"Fecha: viernes 2 de octubre, 3:30 p. m. (Perú 🇵🇪)\nHost: https://h\nPaís: Perú 🇵🇪"
	if host != want {
		t.Errorf("host moment = %q; want %q", host, want)
	}
}

func TestWhatsAppMarkerMisuse(t *testing.T) {
	for _, c := range []struct {
		moment, tmpl string
		bad          bool
	}{
		{webhook.WhatsAppCreated, "Entra: {enlace}", false},
		{webhook.WhatsAppCreated, "Entra: { Enlace_Mentor }", true},
		{webhook.WhatsAppReminder5m, "{enlace_mentor}", true},
		{webhook.WhatsAppHostCreated, "Entra: {enlace_mentor}", false},
		{webhook.WhatsAppHostCreated, "Cliente: {enlace}", true},
		{webhook.WhatsAppHostReminder5m, "{cancelar}", true},
		{webhook.WhatsAppHostReminder5m, "{motivo}", true},
		{webhook.WhatsAppHostReminder5m, "{correo} {telefono}", false},
		{webhook.WhatsAppHostCreated, "El {dia} a las {hora} ({fecha})", false}, // the host's clock in a host text
	} {
		if got := webhook.WhatsAppMarkerMisuse(c.moment, c.tmpl) != ""; got != c.bad {
			t.Errorf("WhatsAppMarkerMisuse(%s, %q) bad = %v; want %v", c.moment, c.tmpl, got, c.bad)
		}
	}
	// The message names the marker actually found (word for word in host-notices.ts).
	if got := webhook.WhatsAppMarkerMisuse(webhook.WhatsAppHostCreated, "Motivo: {motivo}"); got != "{motivo} es solo del mensaje de cancelación al cliente." {
		t.Errorf("motivo: %q", got)
	}
	if got := webhook.WhatsAppMarkerMisuse(webhook.WhatsAppHostCreated, "{motivo} {cancelar}"); got != "En los avisos al anfitrión usa {enlace_mentor}; {enlace} y {cancelar} son del cliente." {
		t.Errorf("cancelar + motivo: %q", got)
	}
}

func TestClientCountry(t *testing.T) {
	e := newEnv(t)
	e.svc.SetPhoneTable(phoneTable(t))
	for _, c := range []struct{ phone, tz, want string }{
		{"+51987654321", "Europe/Madrid", "Perú 🇵🇪"}, // the phone wins
		{"", "America/Bogota", "Colombia 🇨🇴"},        // no phone: the client's zone
		{"+1 809 555 1234", "", "República Dominicana 🇩🇴"},
		{"", "UTC", ""}, // unknown, and the host's zone is never borrowed
		{"987654321", "", ""},
	} {
		if got := e.svc.ClientCountryLabel(c.phone, c.tz); got != c.want {
			t.Errorf("ClientCountryLabel(%q, %q) = %q; want %q", c.phone, c.tz, got, c.want)
		}
	}
	if got := webhook.ClientWithCountry("María Pérez", "Perú 🇵🇪"); got != "María Pérez (Perú 🇵🇪)" {
		t.Errorf("ClientWithCountry = %q", got)
	}
	if webhook.ClientWithCountry("María Pérez", "") != "María Pérez" || webhook.ClientWithCountry("  ", "Perú 🇵🇪") != "" {
		t.Error("ClientWithCountry without a country keeps the name; without a name gives nothing")
	}
}

// {fecha_mentor} / {pais_mentor}: the host's profile zone, else their phone's country's main
// zone, else UTC; 12-hour clock; daylight saving time resolved by the zone at that instant.
func TestHostDateTexts(t *testing.T) {
	e := newEnv(t)
	e.svc.SetPhoneTable(phoneTable(t))
	for _, c := range []struct {
		name, start, tz, phone, fecha, pais string
	}{
		{"Peru profile", "2026-10-02T20:30:00Z", "America/Lima", "", "viernes 2 de octubre, 3:30 p. m.", "Perú 🇵🇪"},
		{"profile beats phone", "2026-10-02T20:30:00Z", "America/Lima", "+34612345678", "viernes 2 de octubre, 3:30 p. m.", "Perú 🇵🇪"},
		{"US city", "2026-10-02T20:30:00Z", "America/Chicago", "", "viernes 2 de octubre, 3:30 p. m.", "Estados Unidos 🇺🇸 (Chicago)"},
		{"New York before DST", "2027-03-13T15:00:00Z", "America/New_York", "", "sábado 13 de marzo, 10:00 a. m.", "Estados Unidos 🇺🇸 (Nueva York)"},
		{"New York after DST", "2027-03-15T14:00:00Z", "America/New_York", "", "lunes 15 de marzo, 10:00 a. m.", "Estados Unidos 🇺🇸 (Nueva York)"},
		{"India: aliases are one time", "2026-10-02T06:30:00Z", "Asia/Kolkata", "", "viernes 2 de octubre, 12:00 p. m.", "India 🇮🇳"},
		{"no zone: the phone's country", "2026-10-02T17:00:00Z", "", "+52 55 1234 5678", "viernes 2 de octubre, 11:00 a. m.", "México 🇲🇽 (Ciudad de México)"},
		{"UTC profile: the phone's country", "2026-10-02T17:00:00Z", "UTC", "+51987654321", "viernes 2 de octubre, 12:00 p. m.", "Perú 🇵🇪"},
		{"nothing known", "2026-10-02T17:00:00Z", "UTC", "", "viernes 2 de octubre, 5:00 p. m.", "UTC (hora universal)"},
	} {
		start, err := time.Parse(time.RFC3339, c.start)
		if err != nil {
			t.Fatal(err)
		}
		fecha, pais := e.svc.HostDateTexts(start, c.tz, c.phone)
		if fecha != c.fecha || pais != c.pais {
			t.Errorf("%s: HostDateTexts = %q, %q; want %q, %q", c.name, fecha, pais, c.fecha, c.pais)
		}
	}
	// Without a phone table (never a panic): the zone still tells the time.
	bare := newEnv(t)
	start, _ := time.Parse(time.RFC3339, "2026-10-02T20:30:00Z")
	if fecha, pais := bare.svc.HostDateTexts(start, "America/Lima", ""); fecha != "viernes 2 de octubre, 3:30 p. m." || pais != "America/Lima" {
		t.Errorf("no table: %q, %q", fecha, pais)
	}
}

// The host's message is scrubbed with the rest once the delivery is finished.
func TestScrubManageURL_removesTheHostWhatsAppMessage(t *testing.T) {
	e := hostEnv(t, "Ventas")
	ctx := context.Background()
	id := waHook(t, e, []string{webhook.EventHostCreated}, []string{webhook.FieldID, webhook.FieldHostWhatsApp, webhook.FieldHostWhatsAppMessage})
	if err := e.svc.Enqueue(ctx, webhook.EventHostCreated, hostPayload()); err != nil {
		t.Fatal(err)
	}
	var deliveryID string
	e.db.QueryRow(`SELECT id FROM webhook_deliveries WHERE webhook_id = ?`, id).Scan(&deliveryID)
	if err := e.svc.ScrubManageURL(ctx, deliveryID); err != nil { // still pending: kept
		t.Fatal(err)
	}
	if lastData(t, e, id)["host_whatsapp_message"] == nil {
		t.Fatal("a pending delivery lost its text")
	}
	if _, err := e.db.Exec(`UPDATE webhook_deliveries SET status = 'success' WHERE id = ?`, deliveryID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ScrubManageURL(ctx, deliveryID); err != nil {
		t.Fatal(err)
	}
	data := lastData(t, e, id)
	if _, ok := data["host_whatsapp_message"]; ok || data["host_whatsapp"] != "34612345678" || data["id"] != "bk-wa" {
		t.Errorf("after scrub: %v; want host_whatsapp_message gone, the rest kept", data)
	}
}

func TestMemberPhone(t *testing.T) {
	e := hostEnv(t, "")
	if got, err := e.svc.MemberPhone(context.Background(), testUserID); err != nil || got != "+34612345678" {
		t.Errorf("MemberPhone = %q, %v", got, err)
	}
	if got, err := e.svc.MemberPhone(context.Background(), otherUser); err != nil || got != "" {
		t.Errorf("MemberPhone(no number) = %q, %v", got, err)
	}
}

func TestHostShortLinks_storeResolveExpireAndPurge(t *testing.T) {
	e := hostEnv(t, "")
	ctx := context.Background()
	now := time.Now()
	code, err := e.svc.CreateHostShortLink(ctx, "bk-wa", testUserID, now)
	if err != nil || !webhook.ValidShortCode(code) {
		t.Fatalf("CreateHostShortLink = %q, %v", code, err)
	}
	var stored string
	e.db.QueryRow(`SELECT code_hash FROM fork_host_short_links`).Scan(&stored)
	if stored == "" || strings.Contains(stored, code) {
		t.Errorf("stored %q; want only a keyed hash", stored)
	}
	if b, u, err := e.svc.ResolveHostShortLink(ctx, code, now); err != nil || b != "bk-wa" || u != testUserID {
		t.Errorf("resolve = %q %q %v", b, u, err)
	}
	// A /h code is not a room or manage code, and vice versa.
	if _, err := e.svc.ResolveShortLink(ctx, code, webhook.ShortLinkRoom, now); err == nil {
		t.Error("a host code opened as a room code")
	}
	if _, _, err := e.svc.ResolveHostShortLink(ctx, "zzzzzzzz", now); err != webhook.ErrShortLinkNotFound {
		t.Errorf("unknown code: %v", err)
	}
	if _, _, err := e.svc.ResolveHostShortLink(ctx, code, now.Add(webhook.ShortLinkMaxTTL+time.Minute)); err != webhook.ErrShortLinkNotFound {
		t.Errorf("past the hard cap: %v", err)
	}
	if _, err := e.svc.CreateHostShortLink(ctx, "bk-wa", "", now); err == nil {
		t.Error("a code with no host was stored")
	}
	if got := webhook.ShortLinkURL("https://citas.example.com/", webhook.ShortLinkHost, code); got != "https://citas.example.com/h/"+code {
		t.Errorf("ShortLinkURL(host) = %q", got)
	}

	// The purge takes expired /h codes and host tokens older than the cap, nothing else.
	old := now.Add(-webhook.ShortLinkMaxTTL - time.Hour).UTC().Format(time.RFC3339)
	for _, q := range []string{
		`INSERT INTO fork_host_short_links (code_hash, booking_id, user_id, expires_at) VALUES ('old', 'bk-wa', '` + testUserID + `', '` + old + `')`,
		`INSERT INTO fork_livekit_host_tokens (token_hash, booking_id, user_id, created_at) VALUES ('old', 'bk-wa', 'u', '` + old + `')`,
		`INSERT INTO fork_livekit_host_tokens (token_hash, booking_id, user_id, created_at) VALUES ('new', 'bk-wa', 'u', '` + now.UTC().Format(time.RFC3339) + `')`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := webhook.PurgeExpiredShortLinks(ctx, e.db, now); err != nil {
		t.Fatal(err)
	}
	var codes, tokens int
	e.db.QueryRow(`SELECT COUNT(*) FROM fork_host_short_links`).Scan(&codes)
	e.db.QueryRow(`SELECT COUNT(*) FROM fork_livekit_host_tokens WHERE token_hash = 'new'`).Scan(&tokens)
	if codes != 1 || tokens != 1 {
		t.Errorf("after purge: %d host codes, new token kept = %d; want 1 and 1", codes, tokens)
	}
	var oldTokens int
	e.db.QueryRow(`SELECT COUNT(*) FROM fork_livekit_host_tokens WHERE token_hash = 'old'`).Scan(&oldTokens)
	if oldTokens != 0 {
		t.Error("the old host token survived the purge")
	}
	// A booking's deletion takes its codes and tokens (CASCADE).
	if _, err := e.db.Exec(`DELETE FROM bookings WHERE id = 'bk-wa'`); err != nil {
		t.Fatal(err)
	}
	e.db.QueryRow(`SELECT (SELECT COUNT(*) FROM fork_host_short_links) + (SELECT COUNT(*) FROM fork_livekit_host_tokens)`).Scan(&codes)
	if codes != 0 {
		t.Errorf("%d rows survived the booking's deletion", codes)
	}
}

func TestEnsureTeamSchema_hostTables(t *testing.T) {
	e := newEnv(t)
	for _, table := range []string{"fork_member_phones", "fork_livekit_host_tokens", "fork_host_short_links"} {
		var n int
		e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
		if n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
	if err := webhook.EnsureTeamSchema(e.db); err != nil {
		t.Errorf("second run: %v", err)
	}
	if err := webhook.EnsureForkSchema(e.db); err != nil {
		t.Errorf("second run: %v", err)
	}
	var triggers int
	e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND (sql LIKE '%fork_member_phones%' OR sql LIKE '%fork_host_short_links%' OR sql LIKE '%fork_livekit_host_tokens%')`).Scan(&triggers)
	if triggers != 0 {
		t.Errorf("%d triggers name the new tables; want none", triggers)
	}
}
