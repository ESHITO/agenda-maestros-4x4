package handler

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
)

// Fork: the morning reminder in the owner's FIXED zone (fork_settings.go). With 07:00
// America/Lima the reminder is 07:00 Lima on the meeting's day as seen in Lima, whatever
// the client's own zone - and the ordering rule (strictly before the 1 h reminder, still
// in the future) still decides whether there is one at all.
func TestPlanWebhookReminders_fixedZoneLimaForClientsInMadridAndMexicoCity(t *testing.T) {
	lima := mustZone(t, "America/Lima")
	madrid := mustZone(t, "Europe/Madrid")
	mexico := mustZone(t, "America/Mexico_City")
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	morningOf := func(plan []plannedWebhookReminder) (time.Time, bool) {
		for _, p := range plan {
			if p.Kind == reminderKindMorning {
				return p.RunAt, true
			}
		}
		return time.Time{}, false
	}

	for _, tc := range []struct {
		name  string
		start time.Time
		want  time.Time // zero = no morning reminder
	}{
		// Summer: Madrid is UTC+2. 18:00 Madrid = 11:00 Lima: 07:00 Lima (14:00 Madrid) fits.
		{"Madrid 18:00 in summer", time.Date(2026, 7, 14, 18, 0, 0, 0, madrid), time.Date(2026, 7, 14, 7, 0, 0, 0, lima)},
		// 10:00 Madrid = 03:00 Lima: 07:00 Lima is 14:00 in Madrid, after the meeting.
		{"Madrid 10:00 in summer", time.Date(2026, 7, 14, 10, 0, 0, 0, madrid), time.Time{}},
		// Winter: Madrid is UTC+1. 18:00 Madrid = 12:00 Lima: 07:00 Lima is 13:00 Madrid.
		{"Madrid 18:00 in winter", time.Date(2026, 12, 1, 18, 0, 0, 0, madrid), time.Date(2026, 12, 1, 7, 0, 0, 0, lima)},
		// 13:00 Madrid in winter = 07:00 Lima: the 1 h reminder (06:00 Lima) comes first.
		{"Madrid 13:00 in winter", time.Date(2026, 12, 1, 13, 0, 0, 0, madrid), time.Time{}},
		// 01:00 Madrid (summer) on the 15th = 18:00 Lima on the 14th: the Lima day is the 14th.
		{"Madrid 01:00, previous day in Lima", time.Date(2026, 7, 15, 1, 0, 0, 0, madrid), time.Date(2026, 7, 14, 7, 0, 0, 0, lima)},
		// Mexico City (UTC-6, no DST since 2022): 10:00 there = 11:00 Lima; 07:00 Lima = 06:00 CDMX.
		{"Mexico City 10:00", time.Date(2026, 7, 14, 10, 0, 0, 0, mexico), time.Date(2026, 7, 14, 7, 0, 0, 0, lima)},
		// 07:30 CDMX = 08:30 Lima: 07:00 Lima is 06:00 CDMX, 1 h 30 before - allowed.
		{"Mexico City 07:30", time.Date(2026, 7, 14, 7, 30, 0, 0, mexico), time.Date(2026, 7, 14, 7, 0, 0, 0, lima)},
		// 07:00 CDMX = 08:00 Lima: 07:00 Lima would coincide with the 1 h reminder - none.
		{"Mexico City 07:00", time.Date(2026, 7, 14, 7, 0, 0, 0, mexico), time.Time{}},
	} {
		plan := planWebhookReminders(tc.start, lima, 7, 0, now)
		got, ok := morningOf(plan)
		switch {
		case tc.want.IsZero() && ok:
			t.Errorf("%s: morning at %s; want none", tc.name, got.In(lima))
		case !tc.want.IsZero() && !ok:
			t.Errorf("%s: no morning reminder; want %s", tc.name, tc.want)
		case !tc.want.IsZero() && !got.Equal(tc.want):
			t.Errorf("%s: morning at %s; want %s", tc.name, got.In(lima), tc.want.In(lima))
		}
		// The order property holds whatever the zone: morning → 1 h → 5 min.
		for i := 1; i < len(plan); i++ {
			if !plan[i-1].RunAt.Before(plan[i].RunAt) {
				t.Errorf("%s: plan out of order: %+v", tc.name, plan)
			}
		}
	}
}

func newForkSettingsHandler(t *testing.T) *Handler {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database, slog.Default()) // webhook.New creates fork_settings
}

// A saved value wins over the env; a missing or no-longer-valid one falls back to it.
func TestLoadMorningSetting_savedOverEnv(t *testing.T) {
	h := newForkSettingsHandler(t)
	ctx := context.Background()

	ms := h.loadMorningSetting(ctx)
	if ms.hhmm != "08:00" || ms.tzName != "" || ms.zone != nil {
		t.Errorf("defaults = %+v; want 08:00, each client's zone", ms)
	}

	if !h.SetReminderMorningHour("09:15") || !h.SetReminderMorningTimezone("Europe/Madrid") {
		t.Fatal("env setters refused valid values")
	}
	if ms = h.loadMorningSetting(ctx); ms.hhmm != "09:15" || ms.hour != 9 || ms.minute != 15 || ms.tzName != "Europe/Madrid" || ms.zone == nil {
		t.Errorf("env only = %+v", ms)
	}

	if err := h.setForkSettings(ctx, map[string]string{forkKeyMorningHour: "07:00", forkKeyMorningTZ: "America/Lima"}); err != nil {
		t.Fatal(err)
	}
	if ms = h.loadMorningSetting(ctx); ms.hhmm != "07:00" || ms.tzName != "America/Lima" || ms.zone.String() != "America/Lima" {
		t.Errorf("saved = %+v; want 07:00 America/Lima over the env", ms)
	}

	// "" saved = each client's zone, even with an env zone set.
	if err := h.setForkSettings(ctx, map[string]string{forkKeyMorningTZ: ""}); err != nil {
		t.Fatal(err)
	}
	if ms = h.loadMorningSetting(ctx); ms.tzName != "" || ms.zone != nil {
		t.Errorf("saved \"\" = %+v; want each client's zone", ms)
	}

	// A saved value that is not valid (hand-edited, or a zone renamed by tzdata) is ignored.
	if err := h.setForkSettings(ctx, map[string]string{forkKeyMorningHour: "7am", forkKeyMorningTZ: "Atlantis/Capital"}); err != nil {
		t.Fatal(err)
	}
	if ms = h.loadMorningSetting(ctx); ms.hhmm != "09:15" || ms.tzName != "Europe/Madrid" {
		t.Errorf("invalid saved values = %+v; want the env ones", ms)
	}
}

func TestSetReminderMorningTimezone(t *testing.T) {
	h := &Handler{}
	if !h.SetReminderMorningTimezone("") || h.reminderMorningTZ != "" {
		t.Error(`"" (each client's zone) refused`)
	}
	if !h.SetReminderMorningTimezone(" America/Lima ") || h.reminderMorningTZ != "America/Lima" {
		t.Errorf("America/Lima: got %q", h.reminderMorningTZ)
	}
	for _, bad := range []string{"Lima", "Local", "../etc/passwd", "Mars/Olympus"} {
		if h.SetReminderMorningTimezone(bad) || h.reminderMorningTZ != "" {
			t.Errorf("SetReminderMorningTimezone(%q) accepted (now %q)", bad, h.reminderMorningTZ)
		}
	}
}

func bookingForNotices(start time.Time) booking.Booking {
	return booking.Booking{ID: "b", EventTypeID: "et", HostID: "host", Status: "confirmed", StartAt: start, EndAt: start.Add(30 * time.Minute)}
}

// bookingNotices is pure: a pending job planned for an EARLIER start (a reschedule that
// raced the cleanup) does not count, and a running job reads as "sending".
func TestBookingNotices_ignoresJobsOfAnEarlierStart(t *testing.T) {
	start := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	b := bookingForNotices(start)
	hooks := []scopedWebhook{{userID: "host", events: map[string]bool{"booking.reminder_1h": true, "booking.reminder_5m": true}, eventTypeIDs: map[string]bool{}}}
	rows := []noticeRow{
		{bookingID: "b", src: "job", what: "1h", status: "pending", at: "2026-09-30T14:00:00Z", startAt: "2026-09-30T15:00:00Z"},
		{bookingID: "b", src: "job", what: "5m", status: "running", at: "2026-10-01T14:55:00Z", startAt: "2026-10-01T15:00:00Z"},
	}
	got := map[string]whatsAppNoticeJSON{}
	for _, n := range bookingNotices(b, rows, hooks, start.Add(-48*time.Hour)) {
		got[n.Kind] = n
	}
	if got["1h"].Status != noticeNotApplicable {
		t.Errorf("1h = %+v; a job for another start must not read as pending", got["1h"])
	}
	if got["5m"].Status != noticeSending {
		t.Errorf("5m = %+v; want sending", got["5m"])
	}
	// A filtered webhook for another type does not make a pending job "pending".
	hooks[0].eventTypeIDs = map[string]bool{"other-type": true}
	rows[0].startAt = "2026-10-01T15:00:00Z"
	for _, n := range bookingNotices(b, rows, hooks, start.Add(-48*time.Hour)) {
		if n.Kind == "1h" && n.Status != noticeNotApplicable {
			t.Errorf("1h with only another type's webhook = %+v; want not_applicable", n)
		}
	}
}

func noticesByKind(ns []whatsAppNoticeJSON) map[string]whatsAppNoticeJSON {
	got := map[string]whatsAppNoticeJSON{}
	for _, n := range ns {
		got[n.Kind] = n
	}
	return got
}

// A cancelled booking whose reminder jobs are still there (cancelSideEffects deletes them
// in a goroutine after CancelBooking answers, and only logs a failure) reads "cancelled",
// never "pending at 07:00": JobWebhookReminder would drop them anyway. A delivery that
// already went out still reads as sent.
func TestBookingNotices_cancelledBookingIgnoresItsLeftoverJobs(t *testing.T) {
	start := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	b := bookingForNotices(start)
	b.Status = "cancelled"
	hooks := []scopedWebhook{{userID: "host", events: map[string]bool{
		"booking.created": true, "booking.reminder_morning": true, "booking.reminder_1h": true, "booking.reminder_5m": true,
	}, eventTypeIDs: map[string]bool{}}}
	s := "2026-10-01T15:00:00Z"
	rows := []noticeRow{
		{bookingID: "b", src: "delivery", what: "booking.created", status: "success", at: "2026-09-20T10:00:00Z"},
		{bookingID: "b", src: "delivery", what: "booking.reminder_morning", status: "success", at: "2026-10-01T12:00:00Z"},
		{bookingID: "b", src: "job", what: "morning", status: "done", at: "2026-10-01T12:00:00Z", startAt: s, finishedAt: "2026-10-01T12:00:01Z"},
		{bookingID: "b", src: "job", what: "1h", status: "pending", at: "2026-10-01T14:00:00Z", startAt: s},
		{bookingID: "b", src: "job", what: "5m", status: "running", at: "2026-10-01T14:55:00Z", startAt: s},
	}
	got := noticesByKind(bookingNotices(b, rows, hooks, start.Add(-2*time.Hour)))
	for kind, want := range map[string]string{"created": noticeSent, "morning": noticeSent, "1h": noticeCancelled, "5m": noticeCancelled} {
		if got[kind].Status != want {
			t.Errorf("%s = %+v; want %s", kind, got[kind], want)
		}
	}
	if got["1h"].At != "" {
		t.Errorf("1h of a cancelled booking carries a time: %+v", got["1h"])
	}
}

// A reminder the job dropped as too late (worker down / instance asleep) is "missed", not
// "not_applicable": the job finished only once the next moment had come, and nothing was
// delivered. One that finished on time with no delivery, or with no webhook to receive
// it, stays not_applicable.
func TestBookingNotices_lateDroppedReminderIsMissed(t *testing.T) {
	start := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	b := bookingForNotices(start)
	s := "2026-10-01T15:00:00Z"
	hooks := []scopedWebhook{{userID: "host", events: map[string]bool{
		"booking.reminder_morning": true, "booking.reminder_1h": true, "booking.reminder_5m": true,
	}, eventTypeIDs: map[string]bool{}}}
	rows := []noticeRow{
		// planned 13:00Z, ran at 14:57Z: past the 1 h moment (14:00Z) → dropped
		{bookingID: "b", src: "job", what: "morning", status: "done", at: "2026-10-01T13:00:00Z", startAt: s, finishedAt: "2026-10-01T14:57:03.123456Z"},
		// planned 14:00Z, ran at 14:57Z: past the 5 min moment (14:55Z) → dropped
		{bookingID: "b", src: "job", what: "1h", status: "done", at: "2026-10-01T14:00:00Z", startAt: s, finishedAt: "2026-10-01T14:57:03Z"},
		// planned 14:55Z, ran at 14:57Z: still before the start → it fired, found no webhook then
		{bookingID: "b", src: "job", what: "5m", status: "done", at: "2026-10-01T14:55:00Z", startAt: s, finishedAt: "2026-10-01T14:57:04Z"},
	}
	now := start.Add(time.Hour)
	got := noticesByKind(bookingNotices(b, rows, hooks, now))
	if got["morning"].Status != noticeMissed || got["morning"].At != "2026-10-01T14:57:03Z" {
		t.Errorf("morning = %+v; want missed at 14:57:03Z", got["morning"])
	}
	if got["1h"].Status != noticeMissed {
		t.Errorf("1h = %+v; want missed", got["1h"])
	}
	if got["5m"].Status != noticeNotApplicable {
		t.Errorf("5m = %+v; want not_applicable (it ran in time)", got["5m"])
	}
	// No webhook would receive it: nothing was lost, so not_applicable.
	if n := noticesByKind(bookingNotices(b, rows, nil, now))["morning"]; n.Status != noticeNotApplicable {
		t.Errorf("morning without webhooks = %+v; want not_applicable", n)
	}
	// A delivery for it wins: it went out.
	rows = append(rows, noticeRow{bookingID: "b", src: "delivery", what: "booking.reminder_1h", status: "success", at: "2026-10-01T14:57:03Z"})
	if n := noticesByKind(bookingNotices(b, rows, hooks, now))["1h"]; n.Status != noticeSent {
		t.Errorf("1h with a delivery = %+v; want sent", n)
	}
}

// The worker purges finished deliveries and jobs after 30 days: past that, "no record" is
// "unknown", never "not_applicable" (a confirmation that did go out 45 days before a far
// booking would otherwise read "no se programó").
func TestBookingNotices_purgedRecordsReadUnknown(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	hooks := []scopedWebhook{{userID: "host", events: map[string]bool{"booking.created": true, "booking.reminder_1h": true}, eventTypeIDs: map[string]bool{}}}

	// Booked 45 days ago for next week: the confirmation's record may be gone; the reminders
	// are still ahead.
	far := bookingForNotices(now.Add(7 * 24 * time.Hour))
	far.CreatedAt = now.Add(-45 * 24 * time.Hour)
	got := noticesByKind(bookingNotices(far, nil, hooks, now))
	if got["created"].Status != noticeUnknown {
		t.Errorf("created of a 45-day-old booking = %+v; want unknown", got["created"])
	}
	if got["1h"].Status != noticeNotApplicable {
		t.Errorf("1h of a future booking = %+v; want not_applicable", got["1h"])
	}

	// A meeting 40 days ago: every record may be gone.
	old := bookingForNotices(now.Add(-40 * 24 * time.Hour))
	old.CreatedAt = old.StartAt.Add(-24 * time.Hour)
	for _, n := range bookingNotices(old, nil, hooks, now) {
		if n.Status != noticeUnknown {
			t.Errorf("%s of a 40-day-old meeting = %+v; want unknown", n.Kind, n)
		}
	}

	// Booked yesterday, no delivery: really not applicable.
	recent := bookingForNotices(now.Add(7 * 24 * time.Hour))
	recent.CreatedAt = now.Add(-24 * time.Hour)
	if n := noticesByKind(bookingNotices(recent, nil, hooks, now))["created"]; n.Status != noticeNotApplicable {
		t.Errorf("created of yesterday's booking = %+v; want not_applicable", n)
	}
}
