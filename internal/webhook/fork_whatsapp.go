package webhook

// Fork (Agenda Maestros 4x4): the WhatsApp message is composed HERE, not in FunnelChat.
//
// FunnelChat only receives a payload and forwards a message: it cannot work out a time in
// the client's zone, branch on the type of appointment, or drop a line whose value is
// empty. So the agenda renders the finished text - per event type ("tipo de atención") and
// per moment - and sends it as data.whatsapp_message; each FunnelChat flow maps that one
// key. The owner edits the texts in the event-type editor (GET/PUT
// /v1/event-types/{slug}/whatsapp-messages); an unset moment uses the built-in default
// below. Rendering is Go-only (RenderWhatsApp), for deliveries and for the panel's
// "Ver ejemplo" alike, per CLAUDE.md "Where a translated sentence gets assembled".

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/calnode/calnode/internal/i18n"
)

// WhatsApp moments: one saved text per event type and moment. Each maps to exactly one
// webhook event (WhatsAppMomentForEvent).
const (
	WhatsAppCreated         = "created"
	WhatsAppReminderMorning = "reminder_morning"
	WhatsAppReminder1h      = "reminder_1h"
	WhatsAppReminder5m      = "reminder_5m"
	WhatsAppCancelled       = "cancelled"
	WhatsAppRescheduled     = "rescheduled"

	// Host moments (fork_host.go): the notices to the person who attends the booking.
	WhatsAppHostCreated    = "host_created"
	WhatsAppHostReminder5m = "host_reminder_5m"
)

// WhatsAppClientMoments are the moments whose text goes to the CLIENT (whatsapp_message).
var WhatsAppClientMoments = []string{
	WhatsAppCreated, WhatsAppReminderMorning, WhatsAppReminder1h, WhatsAppReminder5m,
	WhatsAppCancelled, WhatsAppRescheduled,
}

// WhatsAppHostMoments are the moments whose text goes to the HOST (host_whatsapp_message).
var WhatsAppHostMoments = []string{WhatsAppHostCreated, WhatsAppHostReminder5m}

// WhatsAppMoments lists every moment, in the order the panel shows them: the client's six,
// then the host's two. GET/PUT/preview, the inherited texts and the validation walk it.
var WhatsAppMoments = append(append([]string{}, WhatsAppClientMoments...), WhatsAppHostMoments...)

// IsHostMoment reports whether m is one of WhatsAppHostMoments.
func IsHostMoment(m string) bool {
	return m == WhatsAppHostCreated || m == WhatsAppHostReminder5m
}

// hostWhatsAppMomentByEvent maps the host notices to their moment. Separate from
// whatsAppMomentByEvent on purpose: WhatsAppMomentForEvent (the client's text, and with it
// the manage-link minting of WhatsAppNeedsManageURL) never answers for a host event.
var hostWhatsAppMomentByEvent = map[string]string{
	EventHostCreated:    WhatsAppHostCreated,
	EventHostReminder5m: WhatsAppHostReminder5m,
}

// HostWhatsAppMomentForEvent is the host moment whose text a delivery of event carries, or
// "" for every event that is not a host notice.
func HostWhatsAppMomentForEvent(event string) string { return hostWhatsAppMomentByEvent[event] }

var whatsAppMomentByEvent = map[string]string{
	"booking.created":     WhatsAppCreated,
	EventReminderMorning:  WhatsAppReminderMorning,
	EventReminder1h:       WhatsAppReminder1h,
	EventReminder5m:       WhatsAppReminder5m,
	"booking.cancelled":   WhatsAppCancelled,
	"booking.rescheduled": WhatsAppRescheduled, // also a host reassignment (same time, new host)
}

// WhatsAppMomentForEvent is the moment whose text a delivery of event carries, or "" for
// events that carry none (recording.completed, transcript.ready, notes.ready).
func WhatsAppMomentForEvent(event string) string { return whatsAppMomentByEvent[event] }

// ValidWhatsAppMoment reports whether m is one of WhatsAppMoments.
func ValidWhatsAppMoment(m string) bool {
	for _, v := range WhatsAppMoments {
		if v == m {
			return true
		}
	}
	return false
}

// MaxWhatsAppMessageLen bounds a saved text, in characters. WhatsApp itself allows 4096;
// the rendered text is longer than the template once names and links are filled in.
const MaxWhatsAppMessageLen = 3000

// ErrInvalidWhatsAppMessage wraps every refusal of SetWhatsAppMessages (the handler
// answers 400 with the message).
var ErrInvalidWhatsAppMessage = errors.New("mensaje de WhatsApp no válido")

// defaultWhatsAppMessages are used for any moment an event type has no text saved for.
// Written so they stay true whatever the setup: they never say "hoy" or "buenos días"
// (with a fixed morning-reminder zone the reminder can reach a client in another country
// in the afternoon, or on the day before), and a line whose marker is empty disappears
// (a type with no text question has no {tema}; an in-person type may have no {enlace}).
var defaultWhatsAppMessages = map[string]string{
	WhatsAppCreated: "Hola {nombre} 👋\n" +
		"Tu sesión de *{tipo}* con {mentor} quedó agendada.\n" +
		"📅 {dia}, a las {hora}\n" +
		"Tema: _{tema}_\n" +
		"Para entrar a la sesión: {enlace}\n" +
		"Si necesitas cancelar o cambiar la fecha: {cancelar}",
	WhatsAppReminderMorning: "Hola {nombre} ☀️\n" +
		"Te recordamos tu sesión de *{tipo}* con {mentor}: {dia}, a las {hora}.\n" +
		"Para entrar a la sesión: {enlace}\n" +
		"Si no puedes asistir, cancela o cambia la fecha aquí: {cancelar}",
	WhatsAppReminder1h: "Hola {nombre}, tu sesión de *{tipo}* con {mentor} empieza en 1 hora (a las {hora}).\n" +
		"Para entrar a la sesión: {enlace}",
	WhatsAppReminder5m: "{nombre}, tu sesión de *{tipo}* con {mentor} empieza en 5 minutos.\n" +
		"Entra aquí: {enlace}",
	WhatsAppCancelled: "Hola {nombre}, tu sesión de *{tipo}* con {mentor} del {dia}, a las {hora}, fue cancelada.\n" +
		"Motivo: _{motivo}_\n" +
		"Cuando quieras, puedes agendar una nueva fecha.",
	WhatsAppRescheduled: "Hola {nombre}, hubo un cambio en tu sesión de *{tipo}* 🔄\n" +
		"📅 Ahora es el {dia}, a las {hora}, con {mentor}.\n" +
		"Para entrar a la sesión: {enlace}\n" +
		"Si necesitas cancelar o cambiar la fecha: {cancelar}",

	// Host moments (fork_host.go), after the owner's own wording (2 Oct 2026). The owner
	// writes the literal headers ("*Nueva Mentoría agendada*" on Mentoría privada) in each
	// template's WhatsApp tab; copies inherit them. Neither says "hoy" nor "mañana", and
	// {fecha_mentor} is already in the host's own zone, so both stay true anywhere.
	WhatsAppHostCreated: "*Nueva sesión agendada: {tipo}*\n" +
		"\n" +
		"*Nombre:* {cliente}\n" +
		"\n" +
		"*Correo electrónico:* {correo}\n" +
		"\n" +
		"*Número:* {telefono}\n" +
		"\n" +
		"*Tema a tratar* \"{tema}\"\n" +
		"\n" +
		"*FECHA Y HORA:*\n" +
		"{fecha_mentor}\n" +
		"Hora de {pais_mentor}",
	WhatsAppHostReminder5m: "*FALTAN 5 MINUTOS:* Ya casi inicia tu sesión de {tipo}\n" +
		"\n" +
		"*ENTRA AHORA:* {enlace_mentor}\n" +
		"\n" +
		"*Nombre:* {nombre_corto}\n" +
		"*Correo:* {correo}\n" +
		"\n" +
		"\"{tema}\"",
}

// DefaultWhatsAppMessage is the built-in text for moment ("" for an unknown moment).
func DefaultWhatsAppMessage(moment string) string { return defaultWhatsAppMessages[moment] }

// forkWhatsAppSchema is created by EnsureForkSchema with the rest of the fork's schema -
// in code, NOT by goose (see forkSchema for why). Plain tables and one index, no trigger:
// nothing here names another table from a trigger body, so an upstream table rebuild
// (CREATE x_new / DROP x / RENAME) cannot fail on it.
//
//   - event_type_whatsapp_messages: one saved text per event type and moment. CASCADE: a
//     deleted event type takes its texts with it. `moment` is validated in Go
//     (ValidWhatsAppMoment), not with a CHECK, so adding a moment needs no table rebuild.
//   - fork_settings: key/value settings the owner changes from the panel (the morning
//     reminder's hour and zone, handler/fork_settings.go). No row = the env default.
//   - the index makes the bookings list's per-page notice status (one aggregated query
//     over webhook_deliveries by booking_id) an index lookup. An index on an upstream
//     table is harmless to its rebuilds: DROP TABLE drops it, the next boot re-creates it.
var forkWhatsAppSchema = []string{
	`CREATE TABLE IF NOT EXISTS event_type_whatsapp_messages (
		event_type_id TEXT NOT NULL REFERENCES event_types(id) ON DELETE CASCADE,
		moment        TEXT NOT NULL,
		body          TEXT NOT NULL,
		PRIMARY KEY (event_type_id, moment)
	)`,
	`CREATE TABLE IF NOT EXISTS fork_settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_fork_webhook_deliveries_booking
		ON webhook_deliveries (booking_id, event)`,
}

// WhatsAppValues are the values the markers resolve to. Every value is attendee-facing
// text, already in the client's zone and language - except in a HOST text, where {fecha},
// {dia} and {hora} are in the host's zone like {fecha_mentor} (HostDateValues).
type WhatsAppValues struct {
	Nombre   string // {nombre}: the client's name
	Mentor   string // {mentor}: who attends (the booking's primary host)
	Tipo     string // {tipo}: the event type's name
	Tema     string // {tema}: answer to the event type's FIRST 'text' question
	Fecha    string // {fecha}: start_local_long, "martes 30 de septiembre de 2026, 10:00 a. m."
	Dia      string // {dia}: "martes 30 de septiembre"
	Hora     string // {hora}: "10:00 a. m." (12-hour clock, internal/i18n/fork_clock.go)
	Enlace   string // {enlace}: location_value, the ATTENDEE's join link (a /e short link in a delivery)
	Cancelar string // {cancelar}: manage_url, the cancel/reschedule link (a /c short link in a delivery)
	Motivo   string // {motivo}: the cancellation reason (cancelled only)

	// Host moments only (fork_host.go; RenderWhatsAppMoment empties them in a client text).
	Cliente      string // {cliente}: "María Pérez (Perú 🇵🇪)", or just the name when the country is unknown
	PaisCliente  string // {pais_cliente}: "Perú 🇵🇪" (from the client's phone, else their zone)
	NombreCorto  string // {nombre_corto}: first given name + first surname (ShortName)
	Correo       string // {correo}: the client's e-mail
	Telefono     string // {telefono}: the client's number, E.164 (WhatsApp makes it tappable)
	FechaMentor  string // {fecha_mentor}: "viernes 2 de octubre, 3:30 p. m." in the HOST's zone
	PaisMentor   string // {pais_mentor}: whose time that is, "Perú 🇵🇪"; never empty
	EnlaceMentor string // {enlace_mentor}: the HOST's way into the session (a /h short link)
}

// lookup resolves a lower-cased marker name. known=false leaves the marker as written.
func (v WhatsAppValues) lookup(name string) (value string, known bool) {
	switch name {
	case "cliente":
		return v.Cliente, true
	case "pais_cliente", "país_cliente":
		return v.PaisCliente, true
	case "nombre_corto":
		return v.NombreCorto, true
	case "correo":
		return v.Correo, true
	case "telefono", "teléfono":
		return v.Telefono, true
	case "fecha_mentor":
		return v.FechaMentor, true
	case "pais_mentor", "país_mentor":
		return v.PaisMentor, true
	case "enlace_mentor":
		return v.EnlaceMentor, true
	case "nombre":
		return v.Nombre, true
	case "mentor":
		return v.Mentor, true
	case "tipo":
		return v.Tipo, true
	case "tema":
		return v.Tema, true
	case "fecha":
		return v.Fecha, true
	case "dia", "día":
		return v.Dia, true
	case "hora":
		return v.Hora, true
	case "enlace":
		return v.Enlace, true
	case "cancelar":
		return v.Cancelar, true
	case "motivo":
		return v.Motivo, true
	}
	return "", false
}

// markerRE matches "{nombre}", tolerating case and inner spaces ("{ Nombre }").
var markerRE = regexp.MustCompile(`\{\s*([\p{L}_]+)\s*\}`)

// RenderWhatsApp fills the markers of tmpl with v, line by line:
//
//   - a known marker is replaced by its value, squeezed onto one line (a multi-line {tema}
//     would otherwise break the line it sits in, and with it any _cursiva_ around it);
//   - if ANY known marker of a line resolves to "", the WHOLE line is dropped, so the
//     client never reads «Desea hablar de _""_» or «Motivo: __»;
//   - an unknown marker ("{precio}") is left exactly as written;
//   - WhatsApp formatting (*negrita*, _cursiva_, ~tachado~) and emojis are plain text
//     here and pass through untouched. Values are inserted once and never re-scanned, so
//     a client who types "{cancelar}" as their name cannot pull a link into the text;
//   - a value that already ends in "." eats ONE period written right after its marker: the
//     12-hour {hora} is "10:00 a. m." (internal/i18n/fork_clock.go), and "a las {hora}."
//     must read "a las 10:00 a. m.", not "a. m..".
//
// Blank lines left behind by a dropped line are collapsed, and the text is trimmed.
func RenderWhatsApp(tmpl string, v WhatsAppValues) string {
	tmpl = strings.ReplaceAll(tmpl, "\r\n", "\n")
	lines := strings.Split(tmpl, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		drop := false
		var b strings.Builder
		last := 0
		for _, m := range markerRE.FindAllStringSubmatchIndex(line, -1) {
			b.WriteString(line[last:m[0]])
			last = m[1]
			val, known := v.lookup(strings.ToLower(line[m[2]:m[3]]))
			if !known {
				b.WriteString(line[m[0]:m[1]])
				continue
			}
			val = strings.Join(strings.Fields(val), " ")
			if val == "" {
				drop = true
			}
			if strings.HasSuffix(val, ".") && strings.HasPrefix(line[last:], ".") {
				last++ // "{hora}." with "10:00 a. m." → one period, not two
			}
			b.WriteString(val)
		}
		b.WriteString(line[last:])
		if !drop {
			out = append(out, b.String())
		}
	}
	// Collapse runs of blank lines and trim blank lines at both ends.
	tidy := make([]string, 0, len(out))
	for _, line := range out {
		blank := strings.TrimSpace(line) == ""
		if blank && (len(tidy) == 0 || strings.TrimSpace(tidy[len(tidy)-1]) == "") {
			continue
		}
		tidy = append(tidy, line)
	}
	for len(tidy) > 0 && strings.TrimSpace(tidy[len(tidy)-1]) == "" {
		tidy = tidy[:len(tidy)-1]
	}
	return strings.Join(tidy, "\n")
}

// RenderWhatsAppMoment is RenderWhatsApp (unchanged) with the moment's audience enforced on
// the VALUES, so no text can cross over whatever it says:
//
//   - a host moment never resolves {enlace}, {cancelar} or {motivo} - the client's links;
//   - a client moment never resolves any host value ({cliente}, {pais_cliente},
//     {nombre_corto}, {correo}, {telefono}, {fecha_mentor}, {pais_mentor}, {enlace_mentor}).
//
// Those markers are known, so RenderWhatsApp's rule drops their whole line: a client text
// that writes {enlace_mentor} loses that line and never carries the host's link.
// Every delivery and the editor's preview render through here.
func RenderWhatsAppMoment(moment, tmpl string, v WhatsAppValues) string {
	if IsHostMoment(moment) {
		v.Enlace, v.Cancelar, v.Motivo = "", "", ""
	} else {
		v.Cliente, v.PaisCliente, v.NombreCorto, v.Correo, v.Telefono = "", "", "", "", ""
		v.FechaMentor, v.PaisMentor, v.EnlaceMentor = "", "", ""
	}
	return RenderWhatsApp(tmpl, v)
}

// WhatsAppMarkerMisuse returns why tmpl cannot be saved as the text of moment (Spanish, for
// a 400), or "": a client text may not use {enlace_mentor} (the host's link), and a host
// text may not use the client's {enlace}, {cancelar} or {motivo}. RenderWhatsAppMoment
// would drop those lines anyway; this says so when the owner saves, instead of a line that
// silently disappears. The handler asks it only for texts that CHANGE.
func WhatsAppMarkerMisuse(moment, tmpl string) string {
	if IsHostMoment(moment) {
		// Word for word in frontend/src/lib/host-notices.ts markerMisuse.
		if UsesMarker(tmpl, "enlace") || UsesMarker(tmpl, "cancelar") {
			return "En los avisos al anfitrión usa {enlace_mentor}; {enlace} y {cancelar} son del cliente."
		}
		if UsesMarker(tmpl, "motivo") {
			return "{motivo} es solo del mensaje de cancelación al cliente."
		}
		return ""
	}
	if UsesMarker(tmpl, "enlace_mentor") {
		return "{enlace_mentor} solo sirve en los avisos al anfitrión."
	}
	return ""
}

// UsesMarker reports whether tmpl contains the marker name ("cancelar" matches
// "{cancelar}", "{ Cancelar }").
func UsesMarker(tmpl, name string) bool {
	for _, m := range markerRE.FindAllStringSubmatch(tmpl, -1) {
		if strings.ToLower(m[1]) == name {
			return true
		}
	}
	return false
}

// longDay is {dia}: "martes 30 de septiembre" in Spanish (this fork's audience); other
// locales have no long-name table (see longDateTime) and get their short date.
func longDay(loc *i18n.Locale, t time.Time) string {
	if loc == nil || (loc.Code != "es" && !strings.HasPrefix(loc.Code, "es-")) {
		return loc.FormatDate(t)
	}
	return esWeekdaysLong[t.Weekday()] + " " + strconv.Itoa(t.Day()) + " de " + esMonthsLong[t.Month()]
}

// WhatsAppDateTexts returns {fecha}, {dia} and {hora} for t in zone, in the locale the
// payload uses (FORCE_LOCALE, else Spanish). For the panel's "Ver ejemplo", which has no
// booking to read them from.
func (s *Service) WhatsAppDateTexts(t time.Time, zone *time.Location) (fecha, dia, hora string) {
	if zone == nil {
		zone = time.UTC
	}
	loc := s.localeFor("")
	local := t.In(zone)
	return longDateTime(loc, local), longDay(loc, local), loc.FormatTimeOfDay(local)
}

// selectsField reports whether any of the matched webhooks explicitly selected field.
// An unconfigured webhook means defaultFields, which never holds a fork field.
func selectsField(matching []matchedWebhook, field string) bool {
	for _, wh := range matching {
		for _, f := range wh.fields {
			if f == field {
				return true
			}
		}
	}
	return false
}

// whatsAppTemplate is the text of moment for the booking's event type: the saved one, else
// the built-in default. A read error (the table missing) also falls back to the default:
// the client still gets a correct message.
//
// A mentor's copy of a team template (fork_team.go) has no texts of its own: it sends its
// TEMPLATE's saved texts (the link's template, even once it stopped being the setting, so
// existing bookings keep what they were booked with), then the default. Only the links
// are read, never fork_settings.
func (s *Service) whatsAppTemplate(ctx context.Context, bookingID, moment string) string {
	var body string
	err := s.db.QueryRowContext(ctx, `
		SELECT m.body FROM bookings b
		JOIN event_type_whatsapp_messages m
		  ON m.event_type_id = COALESCE((SELECT l.template_id FROM fork_event_type_links l
		                                 WHERE l.copy_id = b.event_type_id AND l.kind = 'copy'),
		                                b.event_type_id)
		WHERE b.id = ? AND m.moment = ?`, bookingID, moment).Scan(&body)
	if err == nil && strings.TrimSpace(body) != "" {
		return body
	}
	return DefaultWhatsAppMessage(moment)
}

// firstTextAnswer is {tema}: the booking's answer to its event type's FIRST question of
// type 'text' (by position). "" when that question was left blank or there is none - even
// if a later text question was answered, since that one asks something else.
func (s *Service) firstTextAnswer(ctx context.Context, bookingID string) string {
	var answer string
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT ba.value FROM booking_answers ba
		                 WHERE ba.booking_id = b.id AND ba.question_id = q.id), '')
		FROM bookings b
		JOIN event_type_questions q ON q.event_type_id = b.event_type_id AND q.type = 'text'
		WHERE b.id = ?
		ORDER BY q.position, q.id
		LIMIT 1`, bookingID).Scan(&answer)
	return answer
}

// enrichWhatsApp renders data.whatsapp_message for event into bd - only when the event has
// a moment and some receiving webhook selected the field, so an unconfigured install pays
// nothing. Called from Enqueue after enrich, before its transaction.
func (s *Service) enrichWhatsApp(ctx context.Context, event string, bd *enrichedBooking, matching []matchedWebhook) {
	moment := WhatsAppMomentForEvent(event)
	if moment == "" || bd.core.ID == "" || !selectsField(matching, FieldWhatsAppMessage) {
		return
	}
	tmpl := s.whatsAppTemplate(ctx, bd.core.ID, moment)
	v := WhatsAppValues{
		Nombre:   bd.attendeeName,
		Mentor:   bd.hostName,
		Tipo:     bd.eventTypeName,
		Fecha:    bd.startLocalLong,
		Dia:      bd.startLocalDay,
		Hora:     bd.startLocalTime,
		Enlace:   bd.core.LocationValue,
		Cancelar: bd.core.ManageURL,
	}
	if UsesMarker(tmpl, "tema") {
		v.Tema = s.firstTextAnswer(ctx, bd.core.ID)
	}
	if moment == WhatsAppCancelled {
		v.Motivo = bd.core.CancellationReason
	}
	// Short links (fork_short_links.go): one new code per marker the text really uses, the
	// long link when there is nothing to shorten or the code cannot be stored. The payload's
	// own location_value / manage_url keep the long links.
	if UsesMarker(tmpl, "enlace") {
		v.Enlace = s.whatsAppShortLink(ctx, bd.core.ID, ShortLinkRoom, v.Enlace)
	}
	if UsesMarker(tmpl, "cancelar") {
		v.Cancelar = s.whatsAppShortLink(ctx, bd.core.ID, ShortLinkManage, v.Cancelar)
	}
	bd.whatsappMessage = RenderWhatsAppMoment(moment, tmpl, v)
}

// enrichForkMessage is Enqueue's single fork hook after enrich: the text of a host notice
// (enrichHostWhatsApp) or of a client moment (enrichWhatsApp), then applyAudience, which
// decides whom the payload may address. hc is what hostNotice found (zero on client events).
func (s *Service) enrichForkMessage(ctx context.Context, event string, bd *enrichedBooking, matching []matchedWebhook, hc hostContext) {
	if IsHostEvent(event) {
		s.enrichHostWhatsApp(ctx, event, bd, matching, hc)
	} else {
		s.enrichWhatsApp(ctx, event, bd, matching)
	}
	applyAudience(event, bd, hc)
}

// WhatsAppNeedsManageURL reports whether the whatsapp_message of event for this booking
// needs the manage link: some receiving webhook selected FieldWhatsAppMessage AND the
// text for the booking's type and moment uses {cancelar}. The handler mints a manage
// token (additive IssueManageToken, never Rotate) only when this or WantsField(manage_url)
// says so - a webhook that selected only whatsapp_message still gets a working {cancelar}.
func (s *Service) WhatsAppNeedsManageURL(ctx context.Context, event, hostID, bookingID string) (bool, error) {
	moment := WhatsAppMomentForEvent(event)
	if moment == "" || bookingID == "" {
		return false, nil
	}
	want, err := s.WantsField(ctx, event, hostID, bookingID, FieldWhatsAppMessage)
	if err != nil || !want {
		return false, err
	}
	return UsesMarker(s.whatsAppTemplate(ctx, bookingID, moment), "cancelar"), nil
}

// WhatsAppMessages returns the texts saved for an event type, one entry per moment ("" =
// none saved, the default applies).
func (s *Service) WhatsAppMessages(ctx context.Context, eventTypeID string) (map[string]string, error) {
	out := make(map[string]string, len(WhatsAppMoments))
	for _, m := range WhatsAppMoments {
		out[m] = ""
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT moment, body FROM event_type_whatsapp_messages WHERE event_type_id = ?`, eventTypeID)
	if err != nil {
		return nil, fmt.Errorf("webhook: list whatsapp messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var moment, body string
		if err := rows.Scan(&moment, &body); err != nil {
			return nil, fmt.Errorf("webhook: scan whatsapp message: %w", err)
		}
		if ValidWhatsAppMoment(moment) {
			out[moment] = body
		}
	}
	return out, rows.Err()
}

// NormalizeWhatsAppMessage is how a text is stored: CRLF → LF, trimmed.
func NormalizeWhatsAppMessage(body string) string {
	return strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
}

// SetWhatsAppMessages writes the texts in msgs (moment → text) for an event type, in one
// transaction; moments not in msgs are left as they are. An empty text (after trimming)
// deletes the saved one, so that moment goes back to the default. Every moment and length
// is checked before anything is written; a refusal wraps ErrInvalidWhatsAppMessage.
func (s *Service) SetWhatsAppMessages(ctx context.Context, eventTypeID string, msgs map[string]string) error {
	for moment, body := range msgs {
		if !ValidWhatsAppMoment(moment) {
			return fmt.Errorf("%w: momento desconocido %q", ErrInvalidWhatsAppMessage, moment)
		}
		if n := utf8.RuneCountInString(NormalizeWhatsAppMessage(body)); n > MaxWhatsAppMessageLen {
			return fmt.Errorf("%w: el mensaje %q tiene %d caracteres (máximo %d)",
				ErrInvalidWhatsAppMessage, moment, n, MaxWhatsAppMessageLen)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("webhook: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	// Fixed order: deterministic writes.
	for _, moment := range WhatsAppMoments {
		body, ok := msgs[moment]
		if !ok {
			continue
		}
		body = NormalizeWhatsAppMessage(body)
		if body == "" {
			_, err = tx.ExecContext(ctx,
				`DELETE FROM event_type_whatsapp_messages WHERE event_type_id = ? AND moment = ?`, eventTypeID, moment)
		} else {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO event_type_whatsapp_messages (event_type_id, moment, body) VALUES (?, ?, ?)
				ON CONFLICT (event_type_id, moment) DO UPDATE SET body = excluded.body`,
				eventTypeID, moment, body)
		}
		if err != nil {
			return fmt.Errorf("webhook: save whatsapp message %s: %w", moment, err)
		}
	}
	return tx.Commit()
}
