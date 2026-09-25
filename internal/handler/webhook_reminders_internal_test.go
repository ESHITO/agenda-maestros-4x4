package handler

import (
	"testing"
	"time"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%s): %v", name, err)
	}
	return loc
}

// planKinds renders a plan as kind→RunAt (RFC3339 UTC) for compact assertions.
func planKinds(plan []plannedWebhookReminder) map[string]string {
	m := map[string]string{}
	for _, p := range plan {
		m[p.Kind] = p.RunAt.UTC().Format(time.RFC3339)
	}
	return m
}

func TestPlanWebhookReminders(t *testing.T) {
	lima := mustZone(t, "America/Lima")    // UTC-5, no DST
	madrid := mustZone(t, "Europe/Madrid") // UTC+2 in September
	day := func(h, m int) time.Time { return time.Date(2026, 9, 25, h, m, 0, 0, time.UTC) }
	earlier := day(0, 0).Add(-48 * time.Hour) // booked two days ahead

	for _, tc := range []struct {
		name  string
		start time.Time
		zone  *time.Location
		now   time.Time
		want  map[string]string
	}{
		{
			// 15:00Z = 10:00 in Lima. Morning = 08:00 LIMA = 13:00Z.
			name:  "all three, morning in the attendee's zone",
			start: day(15, 0), zone: lima, now: earlier,
			want: map[string]string{"morning": "2026-09-25T13:00:00Z", "1h": "2026-09-25T14:00:00Z", "5m": "2026-09-25T14:55:00Z"},
		},
		{
			// The same instant for a Madrid attendee is 17:00 local: morning = 08:00 MADRID
			// = 06:00Z. Proves the zone, not the host's or UTC, picks the morning.
			name:  "same start, different attendee zone",
			start: day(15, 0), zone: madrid, now: earlier,
			want: map[string]string{"morning": "2026-09-25T06:00:00Z", "1h": "2026-09-25T14:00:00Z", "5m": "2026-09-25T14:55:00Z"},
		},
		{
			// 04:00Z on the 26th is still the 25th (23:00) in Lima: the morning belongs
			// to the attendee's LOCAL day, i.e. 08:00 Lima on the 25th.
			name:  "local day differs from the UTC day",
			start: time.Date(2026, 9, 26, 4, 0, 0, 0, time.UTC), zone: lima, now: earlier,
			want: map[string]string{"morning": "2026-09-25T13:00:00Z", "1h": "2026-09-26T03:00:00Z", "5m": "2026-09-26T03:55:00Z"},
		},
		{
			// 12:30Z = 07:30 Lima: 08:00 is after the start → no morning reminder.
			name:  "meeting before the morning hour",
			start: day(12, 30), zone: lima, now: earlier,
			want: map[string]string{"1h": "2026-09-25T11:30:00Z", "5m": "2026-09-25T12:25:00Z"},
		},
		{
			// 13:30Z = 08:30 Lima. The 1 h reminder (07:30) would come BEFORE a morning one
			// at 08:00, which would then read as a correction: no morning reminder.
			name:  "meeting within the hour after the morning hour",
			start: day(13, 30), zone: lima, now: earlier,
			want: map[string]string{"1h": "2026-09-25T12:30:00Z", "5m": "2026-09-25T13:25:00Z"},
		},
		{
			// 14:00Z = 09:00 Lima: morning and 1 h would both go at 08:00. The 1 h one wins.
			name:  "meeting exactly one hour after the morning hour",
			start: day(14, 0), zone: lima, now: earlier,
			want: map[string]string{"1h": "2026-09-25T13:00:00Z", "5m": "2026-09-25T13:55:00Z"},
		},
		{
			// 14:05Z = 09:05 Lima: morning 08:00, 1 h 08:05, 5 min 09:00 - in that order.
			name:  "meeting just over an hour after the morning hour",
			start: day(14, 5), zone: lima, now: earlier,
			want: map[string]string{"morning": "2026-09-25T13:00:00Z", "1h": "2026-09-25T13:05:00Z", "5m": "2026-09-25T14:00:00Z"},
		},
		{
			// Booked at 10:00 Lima (15:00Z) for 15:00 Lima the same day: 08:00 has passed.
			name:  "booked the same day after the morning hour",
			start: day(20, 0), zone: lima, now: day(15, 0),
			want: map[string]string{"1h": "2026-09-25T19:00:00Z", "5m": "2026-09-25T19:55:00Z"},
		},
		{
			name:  "less than an hour away: only 5m",
			start: day(15, 0), zone: lima, now: day(14, 30),
			want: map[string]string{"5m": "2026-09-25T14:55:00Z"},
		},
		{
			name:  "less than five minutes away: nothing",
			start: day(15, 0), zone: lima, now: day(14, 57),
			want: map[string]string{},
		},
		{
			name:  "nil zone means UTC",
			start: day(15, 0), zone: nil, now: earlier,
			want: map[string]string{"morning": "2026-09-25T08:00:00Z", "1h": "2026-09-25T14:00:00Z", "5m": "2026-09-25T14:55:00Z"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := planKinds(planWebhookReminders(tc.start, tc.zone, 8, 0, tc.now))
			if len(got) != len(tc.want) {
				t.Fatalf("plan = %v; want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s at %s; want %s (plan %v)", k, got[k], v, got)
				}
			}
		})
	}
}

// The morning is built with time.Date in the attendee's zone, so a DST change the night
// before moves it with the local clock instead of drifting by an hour.
func TestPlanWebhookReminders_DST(t *testing.T) {
	ny := mustZone(t, "America/New_York")
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	// 8 Mar 2026: New York springs forward at 02:00 (EST -5 → EDT -4). 10:00 EDT = 14:00Z.
	got := planKinds(planWebhookReminders(time.Date(2026, 3, 8, 14, 0, 0, 0, time.UTC), ny, 8, 0, now))
	if got["morning"] != "2026-03-08T12:00:00Z" { // 08:00 EDT
		t.Errorf("morning on the DST day = %s; want 2026-03-08T12:00:00Z (08:00 EDT)", got["morning"])
	}
	// The day before, still EST: 08:00 EST = 13:00Z.
	got = planKinds(planWebhookReminders(time.Date(2026, 3, 7, 15, 0, 0, 0, time.UTC), ny, 8, 0, now))
	if got["morning"] != "2026-03-07T13:00:00Z" {
		t.Errorf("morning before DST = %s; want 2026-03-07T13:00:00Z (08:00 EST)", got["morning"])
	}
}

// The owner asked for "in the morning ... then 1 hour before, and 5 minutes before".
// Whatever the start (every 5 minutes of a day, three zones), the planned reminders run
// strictly in that order and never two at the same moment.
func TestPlanWebhookReminders_alwaysInOrder(t *testing.T) {
	order := map[string]int{"morning": 0, "1h": 1, "5m": 2}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, zone := range []*time.Location{mustZone(t, "America/Lima"), mustZone(t, "Europe/Madrid"), time.UTC} {
		for m := 0; m < 24*60; m += 5 {
			start := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC).Add(time.Duration(m) * time.Minute)
			plan := planWebhookReminders(start, zone, 8, 0, now)
			for i := 1; i < len(plan); i++ {
				prev, cur := plan[i-1], plan[i]
				if order[prev.Kind] >= order[cur.Kind] || !prev.RunAt.Before(cur.RunAt) {
					t.Fatalf("start %s (%s): %s at %s then %s at %s; want morning < 1h < 5m, strictly",
						start.Format(time.RFC3339), zone, prev.Kind, prev.RunAt.Format(time.RFC3339),
						cur.Kind, cur.RunAt.Format(time.RFC3339))
				}
			}
		}
	}
}

// A reminder whose NEXT reminder's moment has arrived is false ("in 1 hour" three minutes
// before) and is dropped instead of sent in a burst after the worker was down.
func TestReminderSuperseded(t *testing.T) {
	start := time.Date(2026, 9, 25, 16, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		kind string
		now  time.Time
		want bool
	}{
		{"morning", start.Add(-61 * time.Minute), false},
		{"morning", start.Add(-time.Hour), true}, // the 1 h reminder is due now
		{"morning", start.Add(-3 * time.Minute), true},
		{"1h", start.Add(-time.Hour), false},
		{"1h", start.Add(-6 * time.Minute), false},
		{"1h", start.Add(-5 * time.Minute), true}, // the 5 min reminder is due now
		{"5m", start.Add(-5 * time.Minute), false},
		{"5m", start.Add(-time.Second), false},
		{"5m", start, true}, // the meeting has started
	} {
		if got := reminderSuperseded(tc.kind, start, tc.now); got != tc.want {
			t.Errorf("reminderSuperseded(%s, now = start%s) = %v; want %v",
				tc.kind, tc.now.Sub(start), got, tc.want)
		}
	}
}

func TestPlanWebhookReminders_customHour(t *testing.T) {
	lima := mustZone(t, "America/Lima")
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	got := planKinds(planWebhookReminders(time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC), lima, 7, 30, now))
	if got["morning"] != "2026-09-25T12:30:00Z" { // 07:30 Lima
		t.Errorf("morning at 07:30 = %s", got["morning"])
	}
}

func TestSetReminderMorningHour(t *testing.T) {
	h := &Handler{}
	if h.reminderMorningHour() != "08:00" {
		t.Errorf("default = %q; want 08:00", h.reminderMorningHour())
	}
	if !h.SetReminderMorningHour("07:30") || h.reminderMorningHour() != "07:30" {
		t.Errorf("07:30 not applied: %q", h.reminderMorningHour())
	}
	for _, bad := range []string{"", "7:30", "24:00", "08:60", "8am", "08.00", "08:00:00"} {
		if h.SetReminderMorningHour(bad) {
			t.Errorf("SetReminderMorningHour(%q) accepted", bad)
		}
		if h.reminderMorningHour() != "08:00" {
			t.Errorf("after bad %q: %q; want fallback 08:00", bad, h.reminderMorningHour())
		}
	}
}
