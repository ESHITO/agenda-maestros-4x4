package handler

// Fork (Agenda Maestros 4x4): the pure parts of the host notices - the host's "faltan 5
// minutos" plan and lateness rule (webhook_reminders.go), the member number's validation
// (fork_member_whatsapp.go) and the Spanish name of every country the phone picker lists.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/webhook"
)

func TestPlanHostReminders(t *testing.T) {
	start := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		now  time.Time
		want map[string]string
	}{
		{"booked days ahead", start.Add(-72 * time.Hour), map[string]string{"host_5m": "2026-10-12T14:55:00Z"}},
		{"booked 10 minutes ahead", start.Add(-10 * time.Minute), map[string]string{"host_5m": "2026-10-12T14:55:00Z"}},
		{"booked 5 minutes ahead: already due", start.Add(-5 * time.Minute), map[string]string{}},
		{"booked 2 minutes ahead", start.Add(-2 * time.Minute), map[string]string{}},
	} {
		if got := planKinds(planHostReminders(start, c.now)); len(got) != len(c.want) || got["host_5m"] != c.want["host_5m"] {
			t.Errorf("%s: plan = %v; want %v", c.name, got, c.want)
		}
	}
	// The client's planner is untouched: it never plans host_5m.
	for _, p := range planWebhookReminders(start, time.UTC, 8, 0, start.Add(-72*time.Hour)) {
		if p.Kind == reminderKindHost5m {
			t.Error("planWebhookReminders planned the host's notice")
		}
	}
	if webhookReminderEvents[reminderKindHost5m] != webhook.EventHostReminder5m {
		t.Errorf("host_5m fires %q", webhookReminderEvents[reminderKindHost5m])
	}
	for _, k := range noticeKinds {
		if k == reminderKindHost5m {
			t.Error("host_5m must never be one of the bookings list's four notices")
		}
	}
}

// The host's notice stays true until the session starts, and never after.
func TestReminderSuperseded_host5m(t *testing.T) {
	start := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		now  time.Time
		want bool
	}{
		{start.Add(-5 * time.Minute), false},
		{start.Add(-time.Second), false},
		{start, true},
		{start.Add(10 * time.Minute), true},
	} {
		if got := reminderSuperseded(reminderKindHost5m, start, c.now); got != c.want {
			t.Errorf("reminderSuperseded(host_5m, now = start%+v) = %v; want %v", c.now.Sub(start), got, c.want)
		}
	}
}

func TestParseMemberPhone(t *testing.T) {
	for _, c := range []struct {
		raw          string
		e164         string
		remove, fail bool
	}{
		{`"+51 987 654 321"`, "+51987654321", false, false},
		{`"0051 (987) 654-321"`, "+51987654321", false, false},
		{`"+1 809 555 1234"`, "+18095551234", false, false},
		{`null`, "", true, false},
		{`"   "`, "", true, false},
		{``, "", false, true},                // the key is missing
		{`"987654321"`, "", false, true},     // no country code
		{`"+999 123 4567"`, "", false, true}, // no such country code
		{`123`, "", false, true},             // not text
		{`"+51 98"`, "", false, true},        // too short
	} {
		e164, remove, msg := parseMemberPhone(json.RawMessage(c.raw))
		if e164 != c.e164 || remove != c.remove || (msg != "") != c.fail {
			t.Errorf("parseMemberPhone(%s) = %q, %v, %q", c.raw, e164, remove, msg)
		}
	}
}

// Every country the phone picker lists has a Spanish name for {pais_cliente} and
// {pais_mentor} (webhook/fork_countries.go is generated from this very file).
func TestPhoneData_everyCountryHasASpanishName(t *testing.T) {
	var countries [][]string
	if err := json.Unmarshal(phoneData.Countries, &countries); err != nil {
		t.Fatal(err)
	}
	if len(countries) == 0 {
		t.Fatal("no countries")
	}
	for _, c := range countries {
		if webhook.CountryNameES(c[0]) == "" {
			t.Errorf("%s has no Spanish name: re-run internal/webhook/fork_countries.gen.mjs", c[0])
		}
	}
	if phoneTable.CountryFor("+51987654321", "") != "PE" {
		t.Error("phoneTable is not phone-data.json")
	}
}
