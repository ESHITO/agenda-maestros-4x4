package webhook

import (
	"testing"
	"time"

	"github.com/calnode/calnode/internal/i18n"
)

func TestNormalizePhone(t *testing.T) {
	for _, tc := range []struct {
		in, e164, digits string
	}{
		// The phone question stores "+<dial code> <number>" (00067_question_type_phone.sql).
		{"+51 987654321", "+51987654321", "51987654321"},
		{"+51 987-654-321", "+51987654321", "51987654321"},
		{" +1 (415) 555-0100 ", "+14155550100", "14155550100"},
		{"+34 612.345.678", "+34612345678", "34612345678"},
		// Telephone bookings (allow_phone_call) store the number as location "tel:...".
		{"tel:+31 6 12345678", "+31612345678", "31612345678"},
		{"TEL:+31612345678", "+31612345678", "31612345678"},
		// 00 is the international prefix outside North America.
		{"0051 987 654 321", "+51987654321", "51987654321"},
		// validPhone accepts an extension marker; WhatsApp cannot dial one.
		{"+51 987654321 x12", "+51987654321", "51987654321"},
		// No country marker: omitted, never a guessed country code. Read as
		// international, Mexico's "55 1234 5678" would dial Brazil (+55) and Peru's
		// "987654321" Iran (+98) - a stranger would get the client's appointment.
		{"987 654 321", "", ""},
		{"987654321", "", ""},
		{"tel:55 1234 5678", "", ""},
		{"51987654321", "", ""}, // a country code typed without "+" is indistinguishable
		// Not a phone number.
		{"", "", ""},
		{"call me", "", ""},
		{"+51 123", "", ""},           // < 6 digits
		{"+1234567890123456", "", ""}, // > 15 digits (E.164 max)
		{"tel:", "", ""},
	} {
		e164, digits := NormalizePhone(tc.in)
		if e164 != tc.e164 || digits != tc.digits {
			t.Errorf("NormalizePhone(%q) = (%q, %q); want (%q, %q)", tc.in, e164, digits, tc.e164, tc.digits)
		}
	}
}

func TestAttendeeZone_fallbacks(t *testing.T) {
	for _, tc := range []struct {
		attendee, host, want string
	}{
		{"America/Lima", "Europe/Madrid", "America/Lima"},
		{"", "Europe/Madrid", "Europe/Madrid"},
		// "UTC" is the column default / what booking.Create writes for "no zone sent".
		{"UTC", "Europe/Madrid", "Europe/Madrid"},
		{"Not/AZone", "America/Bogota", "America/Bogota"},
		{"", "", "UTC"},
		{"UTC", "UTC", "UTC"},
	} {
		if got := AttendeeZone(tc.attendee, tc.host).String(); got != tc.want {
			t.Errorf("AttendeeZone(%q, %q) = %s; want %s", tc.attendee, tc.host, got, tc.want)
		}
	}
}

func TestEnrichForkFields_phoneAndLocalStart(t *testing.T) {
	s := &Service{}
	bd := enrichedBooking{core: BookingPayload{StartAt: "2026-09-25T14:00:00Z"}}
	s.enrichForkFields(&bd, forkInputs{
		// The first USABLE phone answer wins; an empty optional one is skipped.
		phoneAnswers:   []string{"", "+51 987-654-321", "+34 600 000 000"},
		attendeeTZ:     "America/Lima", // UTC-5: 14:00Z is 09:00 there
		hostTZ:         "Europe/Madrid",
		attendeeLocale: "es",
	})
	if bd.attendeePhone != "+51987654321" || bd.attendeeWhatsApp != "51987654321" {
		t.Errorf("phone = (%q, %q)", bd.attendeePhone, bd.attendeeWhatsApp)
	}
	// Spanish CLDR abbreviates September as "sept".
	if bd.startLocal != "vie 25 sept 2026, 09:00" {
		t.Errorf("start_local = %q; want the attendee's (Lima) wall clock, not the host's", bd.startLocal)
	}
	if bd.startLocalDate != "vie 25 sept 2026" || bd.startLocalTime != "09:00" {
		t.Errorf("start_local_date/time = %q / %q", bd.startLocalDate, bd.startLocalTime)
	}
	if bd.startLocalLong != "viernes 25 de septiembre de 2026, 09:00" {
		t.Errorf("start_local_long = %q", bd.startLocalLong)
	}
	if bd.startLocalTZ != "America/Lima" {
		t.Errorf("start_local_timezone = %q; want America/Lima", bd.startLocalTZ)
	}
}

// Phone numbers with no country code are never sent: not a local-only phone answer
// (the next usable one wins) and not a call-back number typed without "+".
func TestEnrichForkFields_skipsNumbersWithoutCountryCode(t *testing.T) {
	s := &Service{}
	bd := enrichedBooking{core: BookingPayload{StartAt: "2026-09-25T14:00:00Z"}}
	s.enrichForkFields(&bd, forkInputs{phoneAnswers: []string{"987654321", "+51 912 345 678"}})
	if bd.attendeePhone != "+51912345678" || bd.attendeeWhatsApp != "51912345678" {
		t.Errorf("phone = (%q, %q); want the answer that has a country code", bd.attendeePhone, bd.attendeeWhatsApp)
	}

	bd = enrichedBooking{core: BookingPayload{StartAt: "2026-09-25T14:00:00Z"}}
	s.enrichForkFields(&bd, forkInputs{locationType: "phone", locationValue: "tel:55 1234 5678"})
	if bd.attendeePhone != "" || bd.attendeeWhatsApp != "" {
		t.Errorf("call-back number without a country code sent as (%q, %q); want both omitted",
			bd.attendeePhone, bd.attendeeWhatsApp)
	}
	if d := buildData(bd, []string{FieldAttendeePhone, FieldAttendeeWhatsApp}); len(d) != 0 {
		t.Errorf("payload = %v; want both phone keys absent", d)
	}
}

// start_local_long spells weekday and month out: the short Spanish forms make "mar" both
// martes and marzo ("mar 10 mar 2027").
func TestLongDateTime(t *testing.T) {
	es, en := i18n.Get("es"), i18n.Get("en")
	wed := time.Date(2027, 3, 10, 9, 0, 0, 0, time.UTC)
	if got := longDateTime(es, wed); got != "miércoles 10 de marzo de 2027, 09:00" {
		t.Errorf("es = %q", got)
	}
	if got := longDateTime(es, time.Date(2027, 3, 9, 18, 5, 0, 0, time.UTC)); got != "martes 9 de marzo de 2027, 18:05" {
		t.Errorf("es = %q", got)
	}
	// No long table for other locales: start_local's text.
	if got, want := longDateTime(en, wed), en.FormatDateTime(wed); got != want {
		t.Errorf("en = %q; want %q", got, want)
	}
}

func TestEnrichForkFields_fallbacks(t *testing.T) {
	// No phone question: a telephone booking's call-back number is used instead.
	s := &Service{}
	bd := enrichedBooking{core: BookingPayload{StartAt: "2026-09-25T14:00:00.000000000Z"}}
	s.enrichForkFields(&bd, forkInputs{
		locationType:   "phone",
		locationValue:  "tel:+57 300 1234567",
		attendeeTZ:     "UTC", // unknown → host zone
		hostTZ:         "America/Bogota",
		attendeeLocale: "", // unknown → Spanish
	})
	if bd.attendeePhone != "+573001234567" || bd.attendeeWhatsApp != "573001234567" {
		t.Errorf("phone fallback = (%q, %q)", bd.attendeePhone, bd.attendeeWhatsApp)
	}
	if bd.startLocal != "vie 25 sept 2026, 09:00" {
		t.Errorf("start_local = %q; want host zone + Spanish", bd.startLocal)
	}
	// attendee_timezone still says the stored "UTC"; this says what start_local used.
	if bd.startLocalTZ != "America/Bogota" {
		t.Errorf("start_local_timezone = %q; want the host's America/Bogota", bd.startLocalTZ)
	}

	// Nothing to go on: no phone at all; FORCE_LOCALE beats a stored "en".
	s = &Service{}
	if !s.SetForceLocale("es") {
		t.Fatal("SetForceLocale(es) refused")
	}
	bd = enrichedBooking{core: BookingPayload{StartAt: "2026-09-25T14:00:00Z"}}
	s.enrichForkFields(&bd, forkInputs{locationType: "google_meet", locationValue: "https://meet.google.com/x", attendeeLocale: "en"})
	if bd.attendeePhone != "" || bd.attendeeWhatsApp != "" {
		t.Errorf("phone should be empty, got (%q, %q)", bd.attendeePhone, bd.attendeeWhatsApp)
	}
	if bd.startLocal != "vie 25 sept 2026, 14:00" {
		t.Errorf("start_local = %q; want UTC + forced Spanish", bd.startLocal)
	}
	if s.SetForceLocale("ja") {
		t.Error("SetForceLocale accepted a locale this build does not ship")
	}
}

func TestBuildData_forkFields(t *testing.T) {
	bd := enrichedBooking{
		core:             BookingPayload{ID: "b1", ManageURL: "https://citas.example.com/manage/tok"},
		attendeePhone:    "+51987654321",
		attendeeWhatsApp: "51987654321",
		startLocal:       "vie 25 sept 2026, 09:00",
		startLocalDate:   "vie 25 sept 2026",
		startLocalTime:   "09:00",
		startLocalLong:   "viernes 25 de septiembre de 2026, 09:00",
		startLocalTZ:     "America/Lima",
	}
	fork := []string{FieldAttendeePhone, FieldAttendeeWhatsApp, FieldStartLocal,
		FieldStartLocalDate, FieldStartLocalTime, FieldManageURL, FieldStartLocalLong, FieldStartLocalTZ}

	// The default set (unconfigured webhook) must not grow the fork fields.
	for _, k := range fork {
		if _, ok := buildData(bd, defaultFields)[k]; ok {
			t.Errorf("default payload must not include %q", k)
		}
	}
	d := buildData(bd, fork)
	want := map[string]string{
		FieldAttendeePhone: "+51987654321", FieldAttendeeWhatsApp: "51987654321",
		FieldStartLocal: "vie 25 sept 2026, 09:00", FieldStartLocalDate: "vie 25 sept 2026",
		FieldStartLocalTime: "09:00", FieldManageURL: "https://citas.example.com/manage/tok",
		FieldStartLocalLong: "viernes 25 de septiembre de 2026, 09:00", FieldStartLocalTZ: "America/Lima",
	}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("%s = %v; want %q", k, d[k], v)
		}
	}
	// Selected but unknown → absent, never "".
	empty := buildData(enrichedBooking{core: BookingPayload{ID: "b2"}}, fork)
	if len(empty) != 0 {
		t.Errorf("empty fork fields should be omitted, got %v", empty)
	}
	// All fork keys are selectable (ValidFields keeps them).
	if got := ValidFields(fork); len(got) != len(fork) {
		t.Errorf("ValidFields dropped fork fields: %v", got)
	}
}

func TestParseTimestamp_bothForms(t *testing.T) {
	a, err1 := parseTimestamp("2026-09-25T14:00:00Z")
	b, err2 := parseTimestamp("2026-09-25T14:00:00.000000000Z")
	if err1 != nil || err2 != nil || !a.Equal(b) || !a.Equal(time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC)) {
		t.Errorf("parseTimestamp: %v %v %v %v", a, b, err1, err2)
	}
}
