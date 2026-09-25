package webhook

// Fork (Agenda Maestros 4x4) additions to the webhook payload, kept out of webhook.go so
// upstream merges touch as little of it as possible. The owner sends WhatsApp messages
// through FunnelChat: each webhook points at a separate FunnelChat flow, which maps JSON
// keys (data.attendee_phone, data.start_local, ...) into the message. FunnelChat cannot
// branch on "event", so every moment (confirmation, morning, 1 h, 5 min) is its own event.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/i18n"
)

// Reminder events (fork). Fired by the handler's "webhook.reminder" jobs, scheduled when a
// booking is confirmed and rescheduled with it; the payload is the booking.created shape.
const (
	EventReminderMorning = "booking.reminder_morning" // same day, REMINDER_MORNING_HOUR in the attendee's zone
	EventReminder1h      = "booking.reminder_1h"      // start - 60 min
	EventReminder5m      = "booking.reminder_5m"      // start - 5 min
)

// fallbackLocaleCode is the start_local* locale when the booking stores none this build
// ships. The audience is Spanish-speaking (see FORCE_LOCALE in CLAUDE.md).
const fallbackLocaleCode = "es"

// SetForceLocale pins the locale of the start_local* fields, mirroring the handler's
// FORCE_LOCALE. Bookings made through the API or MCP without a language are stored as
// "en" (the column default), which would put an English date inside a Spanish WhatsApp
// message; a forced locale wins over that. Unsupported or empty codes clear the pin and
// report false. Call during boot, before the worker starts.
func (s *Service) SetForceLocale(code string) bool {
	if code == "" || i18n.Get(code) == nil {
		s.forceLocale = ""
		return false
	}
	s.forceLocale = code
	return true
}

// forkInputs is what enrich reads for the fork fields beyond BookingPayload.
type forkInputs struct {
	phoneAnswers   []string // answers to 'phone' questions, in question order
	locationType   string   // bookings.location_type ("phone" = attendee call-back number)
	locationValue  string   // bookings.location_value ("tel:+51 ..." for that case)
	attendeeTZ     string
	hostTZ         string
	attendeeLocale string
}

// enrichForkFields fills the attendee phone and the localised start time.
func (s *Service) enrichForkFields(bd *enrichedBooking, in forkInputs) {
	// Phone: the first 'phone' question answered with something usable, else the
	// call-back number of a telephone booking (allow_phone_call → location "tel:...").
	for _, a := range in.phoneAnswers {
		if e164, digits := NormalizePhone(a); digits != "" {
			bd.attendeePhone, bd.attendeeWhatsApp = e164, digits
			break
		}
	}
	if bd.attendeeWhatsApp == "" && in.locationType == "phone" &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(in.locationValue)), "tel:") {
		bd.attendeePhone, bd.attendeeWhatsApp = NormalizePhone(in.locationValue)
	}

	start, err := parseTimestamp(bd.core.StartAt)
	if err != nil {
		return
	}
	loc := s.localeFor(in.attendeeLocale)
	zone := AttendeeZone(in.attendeeTZ, in.hostTZ)
	local := start.In(zone)
	bd.startLocal = loc.FormatDateTime(local)
	bd.startLocalDate = loc.FormatDate(local)
	bd.startLocalTime = loc.FormatTimeOfDay(local)
	bd.startLocalLong = longDateTime(loc, local)
	bd.startLocalDay = longDay(loc, local) // {dia} of the WhatsApp message (fork_whatsapp.go)
	// The zone the start_local* texts are really in. attendee_timezone is the raw stored
	// value and may say "UTC" when the zone was unknown and the host's was used instead.
	bd.startLocalTZ = zone.String()
}

// Spanish long names for start_local_long. The locale JSONs carry only short names
// ("mar" is both martes and marzo); keeping these here leaves upstream's files untouched.
var (
	esWeekdaysLong = [7]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}
	esMonthsLong   = [13]string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio",
		"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
)

// longDateTime is start_local_long: an unambiguous date for a WhatsApp message, e.g.
// "martes 9 de marzo de 2027, 09:00" in Spanish (this fork's audience - FORCE_LOCALE=es).
// Other locales have no long-name table and get start_local's text.
func longDateTime(loc *i18n.Locale, t time.Time) string {
	if loc == nil || (loc.Code != "es" && !strings.HasPrefix(loc.Code, "es-")) {
		return loc.FormatDateTime(t)
	}
	return esWeekdaysLong[t.Weekday()] + " " + strconv.Itoa(t.Day()) + " de " +
		esMonthsLong[t.Month()] + " de " + strconv.Itoa(t.Year()) + ", " + loc.FormatTimeOfDay(t)
}

// localeFor picks the start_local* locale: FORCE_LOCALE, else the booker's stored locale,
// else Spanish.
func (s *Service) localeFor(stored string) *i18n.Locale {
	if s.forceLocale != "" {
		if l := i18n.Get(s.forceLocale); l != nil {
			return l
		}
	}
	if l := i18n.Get(stored); l != nil {
		return l
	}
	if l := i18n.Get(fallbackLocaleCode); l != nil {
		return l
	}
	return i18n.Default()
}

// AttendeeZone resolves the zone an attendee-facing time is expressed in: the attendee's
// stored zone, else the host's, else UTC. Shared with the handler's reminder scheduling so
// "08:00 in the morning" and the start_local text always use the same zone.
//
// "UTC" counts as MISSING, not as a choice: booking_attendees.iana_timezone is NOT NULL
// DEFAULT 'UTC' and booking.Create writes "UTC" when the booker sent no zone (API/MCP
// bookings), so a stored "UTC" is almost always "unknown". Taken literally it would send
// a Lima client their morning reminder at 03:00. The booking pages always send the
// browser's zone, which is essentially never bare UTC for a person.
func AttendeeZone(attendeeTZ, hostTZ string) *time.Location {
	for _, name := range []string{attendeeTZ, hostTZ} {
		name = strings.TrimSpace(name)
		if name == "" || name == "UTC" || name == "Etc/UTC" {
			continue
		}
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.UTC
}

// parseTimestamp accepts both RFC3339 and the RFC3339Nano form bookings are stored in
// (bookingWebhookPayload passes the raw column through).
func parseTimestamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
}

// NormalizePhone turns a stored phone string into (e164, digits):
//
//	"+51 987-654-321" → ("+51987654321", "51987654321")
//	"tel:+31 6 12345678" → ("+31612345678", "31612345678")
//	"0051 (987) 654 321" → ("+51987654321", "51987654321")   00 = international prefix
//	"+51 987654321 x12" → ("+51987654321", "51987654321")    extension dropped
//	"55 1234 5678" → ("", "")                                 no country code: omitted
//
// Spaces, dashes, dots and parentheses go; a leading "+" (or "00") is kept as the
// international marker. A number with NEITHER is refused (both results ""): both fields
// are read as international numbers (FunnelChat, wa.me), so local digits would be dialled
// with their first digits as a country code - Mexico's "55 1234 5678" becomes a Brazilian
// number, Peru's "987654321" an Iranian one - and a stranger would receive the client's
// name, appointment and manage link. We cannot honestly add a country code either. Such
// numbers come from the free-text call-back field and API/MCP answers; the phone
// question's country picker always stores "+<code> ...". Fewer than 6 or more than 15
// digits (the E.164 maximum) is not a phone number either.
func NormalizePhone(raw string) (e164, digits string) {
	s := strings.TrimSpace(raw)
	if len(s) >= 4 && strings.EqualFold(s[:4], "tel:") {
		s = strings.TrimSpace(s[4:])
	}
	if i := strings.IndexAny(s, "xX"); i >= 0 {
		s = s[:i] // extension marker (validPhone allows "x123")
	}
	intl := strings.HasPrefix(s, "+")
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if !intl && strings.HasPrefix(d, "00") {
		d, intl = d[2:], true
	}
	if !intl || len(d) < 6 || len(d) > 15 {
		return "", ""
	}
	return "+" + d, d
}

// ScrubManageURL removes data.manage_url (and data.whatsapp_message, below) from a FINISHED
// delivery's stored payload (status 'success', or 'failed' with no attempts left). That
// link is a working credential for 60
// days - view, cancel, reschedule - and otherwise the only one kept in clear in the
// database: booking_manage_tokens stores hashes only, while webhook_deliveries rows live
// 30 days past their last attempt inside the file Litestream replicates offsite. The
// signature is computed at send time, so trimming the stored copy afterwards breaks
// nothing. A delivery still retrying keeps the link: its next attempt needs it.
// Called by the worker at both terminal transitions.
//
// data.whatsapp_message goes in the same statement, WHOLE. Its {enlace} and {cancelar} are
// short links (fork_short_links.go) whose codes are credentials - kept nowhere else in
// clear, the table holds keyed hashes - or, when a code could not be made, the long
// /room/...?t= and /manage/ links themselves. Removing the text is safer than redacting
// its links: no pattern to keep in step with every link format, present or future, and
// nothing reads the stored text back (the notice status in the bookings list uses the
// delivery's status; the owner previews a text in the editor). While the delivery is in
// flight the text stays - the stored payload IS what the worker signs and sends.
func (s *Service) ScrubManageURL(ctx context.Context, deliveryID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE webhook_deliveries
		SET payload = json_remove(payload, '$.data.manage_url', '$.data.whatsapp_message')
		WHERE id = ? AND status IN ('success', 'failed') AND json_valid(payload)
		  AND (json_extract(payload, '$.data.manage_url') IS NOT NULL
		       OR json_extract(payload, '$.data.whatsapp_message') IS NOT NULL)`,
		deliveryID)
	return err
}
