package handler

// Fork (Agenda Maestros 4x4): the WhatsApp texts of an event type ("tipo de atención"),
// one per moment, edited in the event-type editor. Storage and rendering live in
// internal/webhook/fork_whatsapp.go; this file holds the three endpoints and who may use
// them - the same rule as editing the event type itself (its owner: eventTypeIDForOwner,
// as PatchEventType, questions and the test e-mail).
//
//	GET  /v1/event-types/{slug}/whatsapp-messages          saved texts ("" = default) + defaults
//	PUT  /v1/event-types/{slug}/whatsapp-messages          save; "" = back to the default
//	POST /v1/event-types/{slug}/whatsapp-messages/preview  render the given texts with sample data
//
// The preview renders in Go (webhook.RenderWhatsApp), never in the browser: the panel shows
// exactly what a delivery would carry, including the dropped lines.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/calnode/calnode/internal/webhook"
)

// whatsAppMomentLabels names each moment in the panel's words, for error messages.
var whatsAppMomentLabels = map[string]string{
	webhook.WhatsAppCreated:         "Confirmación",
	webhook.WhatsAppReminderMorning: "Recordatorio de la mañana",
	webhook.WhatsAppReminder1h:      "1 hora antes",
	webhook.WhatsAppReminder5m:      "5 minutos antes",
	webhook.WhatsAppCancelled:       "Cancelación",
	webhook.WhatsAppRescheduled:     "Reprogramación",
	webhook.WhatsAppHostCreated:     "Aviso al anfitrión: nueva sesión",
	webhook.WhatsAppHostReminder5m:  "Aviso al anfitrión: faltan 5 minutos",
	webhook.WhatsAppHostCancelled:   "Aviso al anfitrión: sesión cancelada",
}

// whatsAppMessagesBody is the PUT and preview body. A nil field is "not sent": PUT leaves
// that moment as it is, preview renders the saved text (else the default).
type whatsAppMessagesBody struct {
	Created         *string `json:"created"`
	ReminderMorning *string `json:"reminder_morning"`
	Reminder1h      *string `json:"reminder_1h"`
	Reminder5m      *string `json:"reminder_5m"`
	Cancelled       *string `json:"cancelled"`
	Rescheduled     *string `json:"rescheduled"`
	// Host notices (webhook/fork_host.go).
	HostCreated    *string `json:"host_created"`
	HostReminder5m *string `json:"host_reminder_5m"`
	HostCancelled  *string `json:"host_cancelled"`
}

// sent returns the moments present in the body, moment → text.
func (b whatsAppMessagesBody) sent() map[string]string {
	out := map[string]string{}
	for moment, p := range map[string]*string{
		webhook.WhatsAppCreated:         b.Created,
		webhook.WhatsAppReminderMorning: b.ReminderMorning,
		webhook.WhatsAppReminder1h:      b.Reminder1h,
		webhook.WhatsAppReminder5m:      b.Reminder5m,
		webhook.WhatsAppCancelled:       b.Cancelled,
		webhook.WhatsAppRescheduled:     b.Rescheduled,
		webhook.WhatsAppHostCreated:     b.HostCreated,
		webhook.WhatsAppHostReminder5m:  b.HostReminder5m,
		webhook.WhatsAppHostCancelled:   b.HostCancelled,
	} {
		if p != nil {
			out[moment] = *p
		}
	}
	return out
}

// whatsAppMessagesMaxBody: nine texts (the client's six, the host's three) of
// MaxWhatsAppMessageLen characters, up to four bytes each, plus JSON escaping.
const whatsAppMessagesMaxBody = 160 << 10

// decodeWhatsAppMessages reads and checks a PUT/preview body; msg != "" is a 400.
func decodeWhatsAppMessages(w http.ResponseWriter, r *http.Request) (map[string]string, string) {
	r.Body = http.MaxBytesReader(w, r.Body, whatsAppMessagesMaxBody)
	var body whatsAppMessagesBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, "invalid JSON"
	}
	msgs := body.sent()
	for _, moment := range webhook.WhatsAppMoments {
		text, ok := msgs[moment]
		if !ok {
			continue
		}
		if n := utf8.RuneCountInString(webhook.NormalizeWhatsAppMessage(text)); n > webhook.MaxWhatsAppMessageLen {
			return nil, fmt.Sprintf("El mensaje «%s» es demasiado largo: %d caracteres (máximo %d).",
				whatsAppMomentLabels[moment], n, webhook.MaxWhatsAppMessageLen)
		}
	}
	return msgs, ""
}

// writeWhatsAppMessages answers GET/PUT: every moment's saved text ("" = none saved, the
// default is sent) and, under "defaults", the built-in texts the panel shows as the
// placeholder of an empty box.
func (h *Handler) writeWhatsAppMessages(w http.ResponseWriter, r *http.Request, eventTypeID string) {
	saved, err := h.webhookSvc.WhatsAppMessages(r.Context(), eventTypeID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "whatsapp messages: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make(map[string]any, len(saved)+1)
	defaults := make(map[string]string, len(webhook.WhatsAppMoments))
	for _, m := range webhook.WhatsAppMoments {
		out[m] = saved[m]
		defaults[m] = webhook.DefaultWhatsAppMessage(m)
	}
	out["defaults"] = defaults
	h.writeJSON(w, http.StatusOK, out)
}

// GetWhatsAppMessages handles GET /v1/event-types/{slug}/whatsapp-messages.
func (h *Handler) GetWhatsAppMessages(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	// A copy (of T or S) sends its template's texts: shown read-only (fork_team_guards.go).
	if h.writeInheritedWhatsApp(w, r, user) {
		return
	}
	etID := h.eventTypeIDForOwner(w, r, r.PathValue("slug"), user.ID)
	if etID == "" {
		return
	}
	if h.webhookSvc == nil {
		h.writeError(w, http.StatusServiceUnavailable, "webhooks are not available")
		return
	}
	h.writeWhatsAppMessages(w, r, etID)
}

// PutWhatsAppMessages handles PUT /v1/event-types/{slug}/whatsapp-messages. Body:
// {"created": "...", "reminder_morning": "...", ...}; an empty text puts that moment back
// on the default, an omitted one is left as it is. Answers like GET.
func (h *Handler) PutWhatsAppMessages(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	etID := h.eventTypeIDForOwner(w, r, r.PathValue("slug"), user.ID)
	if etID == "" {
		return
	}
	if h.webhookSvc == nil {
		h.writeError(w, http.StatusServiceUnavailable, "webhooks are not available")
		return
	}
	msgs, bad := decodeWhatsAppMessages(w, r)
	if bad != "" {
		h.writeError(w, http.StatusBadRequest, bad)
		return
	}
	if bad, err := h.whatsAppMarkerMisuse(r, etID, msgs); err != nil {
		h.logger.ErrorContext(r.Context(), "whatsapp messages: load for validation", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	} else if bad != "" {
		h.writeError(w, http.StatusBadRequest, bad)
		return
	}
	if err := h.webhookSvc.SetWhatsAppMessages(r.Context(), etID, msgs); err != nil {
		if errors.Is(err, webhook.ErrInvalidWhatsAppMessage) {
			h.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.logger.ErrorContext(r.Context(), "whatsapp messages: save", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeWhatsAppMessages(w, r, etID)
}

// whatsAppMarkerMisuse refuses a marker outside its audience (webhook.WhatsAppMarkerMisuse:
// {enlace_mentor} in a client text or in host_cancelled; {enlace} or {cancelar} in a host
// text; {motivo} outside the two cancellation texts) - only
// in the texts this PUT CHANGES against what is stored (validate on change, not on
// mention: the editor sends every box). "" = fine.
func (h *Handler) whatsAppMarkerMisuse(r *http.Request, etID string, msgs map[string]string) (string, error) {
	saved, err := h.webhookSvc.WhatsAppMessages(r.Context(), etID)
	if err != nil {
		return "", err
	}
	for _, moment := range webhook.WhatsAppMoments {
		text, ok := msgs[moment]
		if !ok {
			continue
		}
		text = webhook.NormalizeWhatsAppMessage(text)
		if text == "" || text == webhook.NormalizeWhatsAppMessage(saved[moment]) {
			continue
		}
		if why := webhook.WhatsAppMarkerMisuse(moment, text); why != "" {
			return "«" + whatsAppMomentLabels[moment] + "»: " + why, nil
		}
	}
	return "", nil
}

// Sample data for the preview. Plausible, and obviously an example.
const (
	sampleWhatsAppName   = "María Pérez"
	sampleWhatsAppTopic  = "Quiero ordenar mis finanzas y armar un presupuesto"
	sampleWhatsAppReason = "Me surgió un imprevisto en el trabajo"
	sampleWhatsAppZone   = "America/Lima"
	// The client's details a host notice shows.
	sampleWhatsAppCountry = "PE"
	sampleWhatsAppEmail   = "maria@ejemplo.com"
	sampleWhatsAppPhone   = "+51987654321"
	// sampleShortCodeHost is the preview's /h code (see sampleShortCodeRoom below).
	sampleShortCodeHost = "ejemplo3"
)

// sampleHostLink is {enlace_mentor} for the preview, as a delivery would carry it: the /h
// short link for the built-in video room (the long sample when there is no base URL), the
// meeting's own link - enlace, already shortened like the client's - for another web
// meeting, and nothing for a call or an in-person meeting.
func (h *Handler) sampleHostLink(locType, enlace string) string {
	if locType == "livekit" {
		if base := strings.TrimRight(h.publicURL(), "/"); base != "" {
			return webhook.ShortLinkURL(base, webhook.ShortLinkHost, sampleShortCodeHost)
		}
		return enlace
	}
	if webhook.IsWebLink(enlace) {
		return enlace
	}
	return ""
}

// sampleZoomJoinURL has the shape and length of the join_url Zoom returns for a meeting
// made at booking time (host subdomain, 11-digit id, ?pwd=), so the preview shortens it
// exactly when a delivery would shorten the real one.
const sampleZoomJoinURL = "https://us02web.zoom.us/j/81234567890?pwd=EjemploEjemploEjemploEjemplo1234.1"

// sampleJoinLink is {enlace} for the preview: the event type's own link when it has one,
// else what a booking of a self-generating location would get.
func (h *Handler) sampleJoinLink(locType, locValue string) string {
	if v := strings.TrimSpace(locValue); v != "" && locType != "livekit" {
		return v
	}
	switch locType {
	case "livekit":
		return h.publicURL() + "/room/ejemplo"
	case "zoom":
		return sampleZoomJoinURL
	case "google_meet":
		return "https://meet.google.com/abc-defg-hij"
	case "teams":
		return "https://teams.microsoft.com/l/meetup-join/ejemplo"
	}
	return "" // no link for this type: the line holding {enlace} is dropped, as it would be
}

// Sample codes of the preview's short links: the real length, obviously an example, and
// never stored (a delivery draws its own, internal/webhook/fork_short_links.go).
const (
	sampleShortCodeRoom   = "ejemplo1"
	sampleShortCodeManage = "ejemplo2"
)

// sampleWhatsAppLinks are {enlace} and {cancelar} for the preview, as a delivery would carry
// them: the short /e and /c links (https://citas.clubmaestros4x4.com/e/ejemplo1) wherever a
// delivery would shorten. The manage link and a LiveKit link always are (the real ones are
// ~100 and ~200 characters; the samples stand for them); another web link only when the
// short one is shorter (a Meet link stays as it is, a Zoom join_url with ?pwd= does not);
// an address or phone number never. No
// base URL = the long samples, as a delivery would send.
func (h *Handler) sampleWhatsAppLinks(locType, locValue string) (enlace, cancelar string) {
	enlace = h.sampleJoinLink(locType, locValue)
	base := strings.TrimRight(h.publicURL(), "/")
	if base == "" {
		return enlace, "/manage/ejemplo"
	}
	if locType == "livekit" && enlace != "" {
		enlace = webhook.ShortLinkURL(base, webhook.ShortLinkRoom, sampleShortCodeRoom)
	} else {
		enlace = webhook.ShortLinkForPreview(base, webhook.ShortLinkRoom, enlace, sampleShortCodeRoom)
	}
	return enlace, webhook.ShortLinkURL(base, webhook.ShortLinkManage, sampleShortCodeManage)
}

// PreviewWhatsAppMessages handles POST /v1/event-types/{slug}/whatsapp-messages/preview:
// the given texts (the editor's current, unsaved ones; an empty one = the default, as
// saving it would make it; an omitted one = the saved text, else the default) rendered
// with sample data by the same code as a delivery. The
// sample meeting is tomorrow at 10:00 in the zone a host notice to the signed-in user would
// use (profile zone, else their WhatsApp number's country, else America/Lima),
// with them as {mentor} (for the team's two templates, a placeholder name: those are sent
// in the name of whoever attends each copy). {tema} has a sample answer only when the type asks a text
// question - without one, real messages drop that line too. Answers each moment's text
// plus "timezone" (the sample's zone) and "has_text_question".
func (h *Handler) PreviewWhatsAppMessages(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	etID := h.eventTypeIDForOwner(w, r, r.PathValue("slug"), user.ID)
	if etID == "" {
		return
	}
	if h.webhookSvc == nil {
		h.writeError(w, http.StatusServiceUnavailable, "webhooks are not available")
		return
	}
	given, bad := decodeWhatsAppMessages(w, r)
	if bad != "" {
		h.writeError(w, http.StatusBadRequest, bad)
		return
	}
	saved, err := h.webhookSvc.WhatsAppMessages(r.Context(), etID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "whatsapp preview: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var etName, locType, locValue string
	var textQuestions int
	if err := h.db.QueryRowContext(r.Context(), `
		SELECT et.name, et.location_type, COALESCE(et.location_value, ''),
		       (SELECT COUNT(*) FROM event_type_questions q WHERE q.event_type_id = et.id AND q.type = 'text')
		FROM event_types et WHERE et.id = ?`, etID).Scan(&etName, &locType, &locValue, &textQuestions); err != nil {
		h.logger.ErrorContext(r.Context(), "whatsapp preview: load event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// The sample's zone is the one a real host notice to the signed-in user would use
	// (webhook HostZone: their profile zone, else - while it says UTC - their WhatsApp
	// number's country), so «Ver ejemplo» agrees with the profile card and the delivery.
	// America/Lima only when neither tells anything.
	hostTZ, hostPhone := user.IANATZ, ""
	if p, err := h.webhookSvc.MemberPhone(r.Context(), user.ID); err == nil {
		hostPhone = p
	}
	zone := h.webhookSvc.HostZone(hostTZ, hostPhone)
	if zone == nil || zone.String() == "UTC" {
		zone, hostTZ, hostPhone = time.UTC, sampleWhatsAppZone, ""
		if loc, err := time.LoadLocation(sampleWhatsAppZone); err == nil {
			zone = loc
		}
	}
	tomorrow := time.Now().In(zone).AddDate(0, 0, 1)
	start := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 10, 0, 0, 0, zone)
	fecha, dia, hora := h.webhookSvc.WhatsAppDateTexts(start, zone)
	mentor := strings.TrimSpace(user.Name)
	if mentor == "" {
		mentor = "tu mentor"
	}
	// The team's templates are sent in the name of the person attending each copy, never
	// the owner's (fork_team_guards.go).
	if name := h.teamPreviewMentor(r.Context(), etID); name != "" {
		mentor = name
	}
	enlace, cancelar := h.sampleWhatsAppLinks(locType, locValue)
	base := webhook.WhatsAppValues{
		Nombre:   sampleWhatsAppName,
		Mentor:   mentor,
		Tipo:     etName,
		Fecha:    fecha,
		Dia:      dia,
		Hora:     hora,
		Enlace:   enlace,
		Cancelar: cancelar,
	}
	if textQuestions > 0 {
		base.Tema = sampleWhatsAppTopic
	}
	// Host moments (webhook/fork_host.go): the client as the host sees them, and the dates -
	// {fecha_mentor} and, in a host text, {fecha}/{dia}/{hora} too - by the delivery's own rule
	// for the signed-in user (they stand for the host), with its country.
	base.PaisCliente = webhook.CountryLabel(sampleWhatsAppCountry)
	base.Cliente = webhook.ClientWithCountry(sampleWhatsAppName, base.PaisCliente)
	base.NombreCorto = webhook.ShortName(sampleWhatsAppName)
	base.Correo = sampleWhatsAppEmail
	base.Telefono = sampleWhatsAppPhone
	hostDates := h.webhookSvc.HostDateValues(start, hostTZ, hostPhone)
	base.EnlaceMentor = h.sampleHostLink(locType, enlace)

	out := make(map[string]any, len(webhook.WhatsAppMoments)+2)
	for _, moment := range webhook.WhatsAppMoments {
		// Sent = the editor's box, where empty means "the default" (what saving it does).
		// Not sent = what is saved now.
		tmpl, sent := given[moment]
		if !sent {
			tmpl = saved[moment]
		}
		if tmpl = webhook.NormalizeWhatsAppMessage(tmpl); tmpl == "" {
			tmpl = webhook.DefaultWhatsAppMessage(moment)
		}
		v := base
		if webhook.IsHostMoment(moment) {
			hostDates.Apply(&v)
		}
		if webhook.IsCancellationMoment(moment) {
			v.Motivo = sampleWhatsAppReason // the client's cancellation and the host's
		}
		out[moment] = webhook.RenderWhatsAppMoment(moment, tmpl, v)
	}
	out["timezone"] = zone.String()
	out["has_text_question"] = textQuestions > 0
	h.writeJSON(w, http.StatusOK, out)
}
