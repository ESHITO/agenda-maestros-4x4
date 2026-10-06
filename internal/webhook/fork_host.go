package webhook

// Fork (Agenda Maestros 4x4): WhatsApp notices to the HOST - the person who attends a
// booking now (bookings.host_id: a mentor, a support person, or the owner).
//
// Owner request (2 Oct 2026): when a client books, the mentor or support person gets a
// WhatsApp confirmation ("*Nueva Mentoría agendada*", the client's name, country, e-mail,
// number and topic, and the date and time IN THE HOST'S OWN ZONE, with that country's
// flag), and 5 minutes before the start another one with the link to enter. Owner request
// (5 Oct 2026): when a session is cancelled, the host is told too, with the reason.
//
// Three events (FunnelChat cannot branch on "event"; the three share one recipient and the
// same two keys, so they may share a webhook - TeamWebhookGuard refuses only host + client):
//
//	booking.host_created      every creation path (handler.dispatchBookingConfirmation)
//	                          and "Pasar a otra persona" (handler.ReassignBooking, new host)
//	booking.host_reminder_5m  the "webhook.reminder" job of kind host_5m, at start - 5 min,
//	                          to whoever hosts the booking WHEN it runs
//	booking.host_cancelled    every cancel (handler.cancelSideEffects: panel, /manage and
//	                          its /c short link, MCP), to whoever hosted it at that moment,
//	                          with {motivo}; never for an unpaid Stripe hold (no host_created)
//
// The FunnelChat flow sends data.host_whatsapp_message to data.host_whatsapp. The number is
// the host's own (fork_member_phones: set in their profile, or by the owner/an admin from
// Miembros - handler/fork_member_whatsapp.go). Nothing is queued for a host with no number,
// or archived (hostNotice): the notice would have no destination - or would hand a former
// member a client's data.
//
// Audience is enforced in the PAYLOAD (applyAudience), not by what the operator ticks - the
// panel creates the owner's webhooks with every field ticked: a host event never carries
// attendee_phone, attendee_whatsapp, whatsapp_message or manage_url (a flow mapped to
// data.attendee_whatsapp by mistake fails instead of messaging the client the host's link),
// and a client event never carries host_phone, host_whatsapp or host_whatsapp_message. The
// TEXTS are kept apart too (RenderWhatsAppMoment).

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// hostContext is what hostNotice found for a host notice: the host's WhatsApp number
// (E.164). Zero for every other event.
type hostContext struct {
	phone string
}

// hostNotice is Enqueue's gate for the host notices, run once some webhook would receive
// event (after matchingWebhooks, so the debug line below means "someone was waiting for
// it"). For any other event it returns p untouched. For a host event it reads the host's
// number (an active host only) and either returns p without ManageURL - a host notice never
// carries the client's manage link - plus the number, or skip = true: no number, archived,
// or the table cannot be read. Logs never carry the number.
func (s *Service) hostNotice(ctx context.Context, event string, p BookingPayload) (BookingPayload, hostContext, bool) {
	if !IsHostEvent(event) {
		return p, hostContext{}, false
	}
	if p.HostID == "" {
		s.log().DebugContext(ctx, "host notice skipped: no host", "event", event, "booking_id", p.ID)
		return p, hostContext{}, true
	}
	var phone string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.phone FROM fork_member_phones p JOIN users u ON u.id = p.user_id
		WHERE p.user_id = ? AND u.archived_at IS NULL`, p.HostID).Scan(&phone)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		s.log().DebugContext(ctx, "host notice skipped: host has no WhatsApp number or is archived",
			"event", event, "booking_id", p.ID, "host_id", p.HostID)
		return p, hostContext{}, true
	case err != nil:
		s.log().ErrorContext(ctx, "host notice skipped: cannot read the host's number",
			"error", err, "event", event, "booking_id", p.ID, "host_id", p.HostID)
		return p, hostContext{}, true
	}
	p.ManageURL = ""
	return p, hostContext{phone: phone}, false
}

// applyAudience makes the payload address only the audience of event (see the file
// comment): on a host event the host's number is filled and every client destination is
// emptied - the client's number stays in the TEXT ({telefono}), never as a field a flow
// could send to; on a client event the host fields stay empty (nothing fills them).
// location_value (the attendee's join link, which the client already has) stays in both.
func applyAudience(event string, bd *enrichedBooking, hc hostContext) {
	if !IsHostEvent(event) {
		bd.hostPhone, bd.hostWhatsApp, bd.hostWhatsAppMessage = "", "", ""
		return
	}
	bd.hostPhone, bd.hostWhatsApp = NormalizePhone(hc.phone)
	bd.attendeePhone, bd.attendeeWhatsApp = "", ""
	bd.whatsappMessage = ""
	bd.core.ManageURL = ""
}

// MemberPhone is userID's stored WhatsApp number (E.164), "" when none.
func (s *Service) MemberPhone(ctx context.Context, userID string) (string, error) {
	var phone string
	err := s.db.QueryRowContext(ctx, `SELECT phone FROM fork_member_phones WHERE user_id = ?`, userID).Scan(&phone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return phone, err
}

// SetHostRoomLinker sets the fallback for {enlace_mentor} on a LiveKit booking when no /h
// code can be made (no base URL, or the insert failed): fn returns the host's long room
// link, already recorded as a valid host token for that host (handler/fork_host_notices.go),
// or "". nil = no fallback (the line is dropped).
func (s *Service) SetHostRoomLinker(fn func(ctx context.Context, bookingID, hostID string) string) {
	s.hostRoomLinker = fn
}

// enrichHostWhatsApp renders data.host_whatsapp_message for a host notice into bd - only
// when some receiving webhook selected the field. Runs before applyAudience (it still reads
// the client's number for {telefono}) and before Enqueue's transaction.
func (s *Service) enrichHostWhatsApp(ctx context.Context, event string, bd *enrichedBooking, matching []matchedWebhook, hc hostContext) {
	moment := HostWhatsAppMomentForEvent(event)
	if moment == "" || bd.core.ID == "" || !selectsField(matching, FieldHostWhatsAppMessage) {
		return
	}
	var hostTZ, room, locType string
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(u.iana_timezone, ''), COALESCE(b.livekit_room, ''), COALESCE(b.location_type, '')
		FROM bookings b JOIN users u ON u.id = b.host_id
		WHERE b.id = ?`, bd.core.ID).Scan(&hostTZ, &room, &locType)

	tmpl := s.whatsAppTemplate(ctx, bd.core.ID, moment)
	// {fecha}, {dia} and {hora} are NOT the client's start_local texts here: in a host text
	// every date is in the HOST's zone (HostDateValues), so a text written with the markers
	// of the client's six never shows the mentor the client's clock. Left empty (the line
	// goes) if the start cannot be read, never the client's time.
	v := WhatsAppValues{
		Nombre:      bd.attendeeName,
		Mentor:      bd.hostName,
		Tipo:        bd.eventTypeName,
		NombreCorto: ShortName(bd.attendeeName),
		Correo:      bd.attendeeEmail,
		Telefono:    bd.attendeePhone,
		PaisCliente: s.ClientCountryLabel(bd.attendeePhone, bd.attendeeTZ),
	}
	v.Cliente = ClientWithCountry(v.Nombre, v.PaisCliente)
	if start, err := parseTimestamp(bd.core.StartAt); err == nil {
		s.HostDateValues(start, hostTZ, hc.phone).Apply(&v)
	}
	if UsesMarker(tmpl, "tema") {
		v.Tema = s.firstTextAnswer(ctx, bd.core.ID)
	}
	if moment == WhatsAppHostCancelled {
		v.Motivo = bd.core.CancellationReason
	} else if UsesMarker(tmpl, "enlace_mentor") {
		// Never for a cancellation: there is nothing to enter, and no /h code or host room
		// token should be minted for a cancelled session (RenderWhatsAppMoment drops it too).
		v.EnlaceMentor = s.hostJoinLink(ctx, bd.core.ID, bd.core.HostID, room, locType, bd.core.LocationValue)
	}
	bd.hostWhatsAppMessage = RenderWhatsAppMoment(moment, tmpl, v)
}

// hostJoinLink is {enlace_mentor}: how the HOST enters the session.
//
//   - A built-in video room (LiveKit): a NEW /h code (CreateHostShortLink), which mints a
//     host room token for the booking's time when tapped (handler/fork_short_links.go). No
//     base URL, or a failed insert: the long host link from hostRoomLinker (logged, never
//     the link).
//   - Another web meeting (Zoom, Meet, Teams): the meeting's own link, shortened to /e as
//     for the client - the same meeting; Zoom recognises its host by their session.
//   - A call or an in-person meeting: "" (the "ENTRA AHORA" line goes).
func (s *Service) hostJoinLink(ctx context.Context, bookingID, hostID, room, locType, locValue string) string {
	if room != "" && (locType == "livekit" || locType == "") {
		if base := s.shortLinkBaseURL(); base != "" {
			code, err := s.CreateHostShortLink(ctx, bookingID, hostID, time.Now())
			if err == nil {
				return ShortLinkURL(base, ShortLinkHost, code)
			}
			s.log().ErrorContext(ctx, "host notice: host short link not created; sending the long host link",
				"error", err, "booking_id", bookingID)
		}
		if s.hostRoomLinker == nil {
			return ""
		}
		return s.hostRoomLinker(ctx, bookingID, hostID)
	}
	if IsWebLink(locValue) {
		return s.whatsAppShortLink(ctx, bookingID, ShortLinkRoom, strings.TrimSpace(locValue))
	}
	return ""
}

// ClientCountryLabel is {pais_cliente}: "Perú 🇵🇪" for the country of the client's phone
// (attendee_phone, the zone settling a shared dial code), else of the client's stored zone,
// else "". A stored "UTC" means unknown, and the host's zone is NEVER borrowed: it would
// name the host's country as the client's.
func (s *Service) ClientCountryLabel(attendeePhone, attendeeTZ string) string {
	cc := s.phones.CountryFor(attendeePhone, attendeeTZ)
	if cc == "" {
		cc = s.phones.CountryOfZone(attendeeTZ)
	}
	if cc == "" {
		return ""
	}
	return CountryLabel(cc)
}

// ClientWithCountry is {cliente}: "María Pérez (Perú 🇵🇪)", the name alone when the country
// is unknown, "" without a name (the line goes). The country lives inside this one marker so
// RenderWhatsApp's line rule needs no exception: an unknown country never drops the name.
func ClientWithCountry(name, country string) string {
	name = strings.TrimSpace(name)
	if name == "" || country == "" {
		return name
	}
	return name + " (" + country + ")"
}

// hostZone is the zone the host reads the time in, and its country:
//  1. their profile's zone (users.iana_timezone - where they set their availability), when
//     it is set, not UTC, and loads;
//  2. else the main zone of their phone's country (PhoneTable.MainZone);
//  3. else UTC, with no country.
func (s *Service) hostZone(hostTZ, hostPhone string) (*time.Location, string) {
	if tz := strings.TrimSpace(hostTZ); tz != "" && tz != "UTC" && tz != "Etc/UTC" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc, s.phones.CountryOfZone(tz)
		}
	}
	if cc := s.phones.CountryFor(hostPhone, ""); cc != "" {
		if z := s.phones.MainZone(cc); z != "" {
			if loc, err := time.LoadLocation(z); err == nil {
				return loc, cc
			}
		}
	}
	return time.UTC, ""
}

// HostZone is the zone hostZone resolves for a host (profile zone, else their phone's
// country, else UTC). For the editor's preview, so its sample uses the delivery's rule.
func (s *Service) HostZone(hostTZ, hostPhone string) *time.Location {
	zone, _ := s.hostZone(hostTZ, hostPhone)
	return zone
}

// HostDates are the date values of a HOST text, every one in the host's own zone.
type HostDates struct {
	Fecha, Dia, Hora        string // {fecha} {dia} {hora} - in a host text, the host's clock too
	FechaMentor, PaisMentor string // {fecha_mentor} {pais_mentor}
}

// Apply sets v's date markers to d (a host text's values).
func (d HostDates) Apply(v *WhatsAppValues) {
	v.Fecha, v.Dia, v.Hora, v.FechaMentor, v.PaisMentor = d.Fecha, d.Dia, d.Hora, d.FechaMentor, d.PaisMentor
}

// HostDateValues returns the date values of a host text for a session starting at start,
// for a host whose profile zone is hostTZ and whose WhatsApp number is hostPhone, in the
// payload's locale (FORCE_LOCALE, else Spanish): {fecha_mentor} = the day and 12-hour time
// in the host's own zone ("viernes 2 de octubre, 3:30 p. m."), {pais_mentor} = whose time
// that is ("Perú 🇵🇪", "Estados Unidos 🇺🇸 (Chicago)"), and {fecha}/{dia}/{hora} in that
// same zone (in a client text they are the client's). The zone resolves daylight saving
// time at that instant.
func (s *Service) HostDateValues(start time.Time, hostTZ, hostPhone string) HostDates {
	zone, cc := s.hostZone(hostTZ, hostPhone)
	loc := s.localeFor("")
	local := start.In(zone)
	day, hour := longDay(loc, local), loc.FormatTimeOfDay(local)
	return HostDates{
		Fecha: longDateTime(loc, local), Dia: day, Hora: hour,
		FechaMentor: day + ", " + hour, PaisMentor: s.hostZoneLabel(zone, cc, start),
	}
}

// HostDateTexts is HostDateValues' {fecha_mentor} and {pais_mentor}.
func (s *Service) HostDateTexts(start time.Time, hostTZ, hostPhone string) (fecha, pais string) {
	d := s.HostDateValues(start, hostTZ, hostPhone)
	return d.FechaMentor, d.PaisMentor
}

// hostZoneLabel is {pais_mentor} - never empty: the country with its flag, plus the city
// when the country's zones tell different times at that instant ("Estados Unidos 🇺🇸
// (Chicago)"; India's two alias zones never add one); a zone with no country, its name;
// UTC, "UTC (hora universal)".
func (s *Service) hostZoneLabel(zone *time.Location, cc string, at time.Time) string {
	if cc != "" {
		label := CountryLabel(cc)
		if s.phones.MultiOffset(cc, at) {
			label += " (" + ZoneCityES(zone.String()) + ")"
		}
		return label
	}
	if name := zone.String(); name != "UTC" && name != "Etc/UTC" && name != "" {
		return name
	}
	return "UTC (hora universal)"
}

// CountryNameES is the Spanish name of the ISO country cc ("PE" → "Perú"), "" if unknown.
func CountryNameES(cc string) string { return countryNamesES[strings.ToUpper(cc)] }

// FlagEmoji is cc's flag as two regional indicator symbols ("PE" → 🇵🇪), "" for anything
// that is not two ASCII letters.
func FlagEmoji(cc string) string {
	cc = strings.ToUpper(cc)
	if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return ""
	}
	return string([]rune{0x1F1E6 + rune(cc[0]-'A'), 0x1F1E6 + rune(cc[1]-'A')})
}

// CountryLabel is "Perú 🇵🇪": the Spanish name and the flag (the code itself when the name
// is unknown), "" for "".
func CountryLabel(cc string) string {
	if cc == "" {
		return ""
	}
	name := CountryNameES(cc)
	if name == "" {
		name = strings.ToUpper(cc)
	}
	if flag := FlagEmoji(cc); flag != "" {
		return name + " " + flag
	}
	return name
}

// zoneCitiesES spells, in Spanish, the cities {pais_mentor} names for the countries whose
// zones tell different times. Anything else is the zone's last segment with spaces.
var zoneCitiesES = map[string]string{
	"America/Mexico_City":            "Ciudad de México",
	"America/Cancun":                 "Cancún",
	"America/Mazatlan":               "Mazatlán",
	"America/Merida":                 "Mérida",
	"America/Ciudad_Juarez":          "Ciudad Juárez",
	"America/Bahia_Banderas":         "Bahía de Banderas",
	"America/Sao_Paulo":              "São Paulo",
	"America/Belem":                  "Belém",
	"America/Maceio":                 "Maceió",
	"America/Cuiaba":                 "Cuiabá",
	"America/Noronha":                "Fernando de Noronha",
	"America/New_York":               "Nueva York",
	"America/Los_Angeles":            "Los Ángeles",
	"America/Indiana/Indianapolis":   "Indianápolis",
	"America/North_Dakota/Center":    "Dakota del Norte",
	"America/North_Dakota/New_Salem": "Dakota del Norte",
	"America/North_Dakota/Beulah":    "Dakota del Norte",
	"Pacific/Honolulu":               "Honolulu",
	"America/St_Johns":               "San Juan de Terranova",
	"America/Montreal":               "Montreal",
	"Pacific/Easter":                 "Isla de Pascua",
	"Pacific/Galapagos":              "Galápagos",
	"Atlantic/Canary":                "Canarias",
	"Atlantic/Azores":                "Azores",
	"Atlantic/Madeira":               "Madeira",
	"Europe/Lisbon":                  "Lisboa",
	"Europe/Moscow":                  "Moscú",
	"Europe/Kyiv":                    "Kiev",
	"Europe/Kiev":                    "Kiev",
	"Australia/Sydney":               "Sídney",
	"Asia/Shanghai":                  "Shanghái",
	"Asia/Jakarta":                   "Yakarta",
}

// ZoneCityES is the city of an IANA zone in Spanish: zoneCitiesES, else the last segment of
// the name with spaces ("America/Chicago" → "Chicago", "America/Argentina/Buenos_Aires" →
// "Buenos Aires").
func ZoneCityES(zone string) string {
	if c, ok := zoneCitiesES[zone]; ok {
		return c
	}
	if i := strings.LastIndex(zone, "/"); i >= 0 {
		zone = zone[i+1:]
	}
	return strings.ReplaceAll(zone, "_", " ")
}
