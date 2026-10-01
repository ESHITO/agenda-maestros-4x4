package slots_test

import (
	"slices"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

// Fork (transition between sessions, 30 Sep 2026): the transition (buffer after) and the
// margin before hold on BOTH sides of a busy interval, whichever order the sessions were
// booked in. Upstream widened busy time by before/after only, so a new session could end
// exactly when a later booking began.

func transitionStarts(t *testing.T, dur, interval, before, after int, rulesFrom, rulesTo string, busy ...slots.Interval) []string {
	t.Helper()
	date := utcDate(2026, 6, 15) // a Monday
	got, err := slots.Generate(slots.Request{
		Event: slots.EventConfig{
			DurationMinutes: dur, SlotIntervalMinutes: interval,
			BufferBeforeMinutes: before, BufferAfterMinutes: after,
			RoutingMode: "fixed", MaxFutureDays: 30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", time.UTC, monRules(rulesFrom, rulesTo), busy...)},
		DateFrom: date, DateTo: date, BookerTZ: time.UTC,
		Now: utcTime(2026, 6, 14, 0, 0, 0),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var out []string
	for _, st := range startTimes(got) {
		out = append(out, st.UTC().Format("15:04"))
	}
	return out
}

func session(h, m, dur int) slots.Interval {
	s := utcTime(2026, 6, 15, h, m, 0)
	return slots.Interval{Start: s, End: s.Add(time.Duration(dur) * time.Minute)}
}

// 40 min + 10 of transition on a 15-min grid, with a session already at 10:00: 9:15 would
// end at 9:55 and leave 5 min before the 10:00 session, so it is not offered; 9:00 (ends
// 9:40, 20 min free) is. After the session, 10:45 is hidden (5 min after 10:40) and 11:00
// is the first start, as before.
func TestTransition_holdsBeforeALaterSession(t *testing.T) {
	got := transitionStarts(t, 40, 15, 0, 10, "09:00", "12:00", session(10, 0, 40))
	want := []string{"09:00", "11:00", "11:15"}
	if !slices.Equal(got, want) {
		t.Errorf("starts around a 10:00 session = %v; want %v (9:15 leaves only 5 min before 10:00)", got, want)
	}
}

// The Soporte override cadence (40 + 10 → every 50) with a session booked off the grid at
// 9:45 (a block that starts there): 9:00 would end at 9:40, 5 min before it, so it is
// hidden. Booked the other way round, 9:45 would have been hidden too.
func TestTransition_offGridLaterSession(t *testing.T) {
	date := utcDate(2026, 6, 15)
	rules := []slots.AvailabilityRule{
		{DayOfWeek: date.Weekday(), StartTime: "09:00", EndTime: "09:40"},
		{DayOfWeek: date.Weekday(), StartTime: "09:45", EndTime: "12:00"},
	}
	got, err := slots.Generate(slots.Request{
		Event: slots.EventConfig{DurationMinutes: 40, SlotIntervalMinutes: 50, BufferAfterMinutes: 10, RoutingMode: "fixed", MaxFutureDays: 30},
		Hosts: []slots.HostAvailability{singleHost("h1", time.UTC, rules, session(9, 45, 40))},
		DateFrom: date, DateTo: date, BookerTZ: time.UTC, Now: utcTime(2026, 6, 14, 0, 0, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range startTimes(got) {
		if st.UTC().Format("15:04") == "09:00" {
			t.Errorf("9:00-9:40 offered with a session at 9:45: only 5 min of the 10-min transition")
		}
	}
}

// The margin is max(before, after) on each side: 40 min, 5 before, 10 after, a session at
// 10:00 on a 5-min grid → the last start before it is 9:10 (ends 9:50, 10 min free) and the
// first after it is 10:50 (10 after 10:40). The two buffers overlap, they do not add up.
func TestTransition_marginIsTheLargerBuffer(t *testing.T) {
	got := transitionStarts(t, 40, 5, 5, 10, "09:00", "12:00", session(10, 0, 40))
	if !slices.Contains(got, "09:10") || slices.Contains(got, "09:15") {
		t.Errorf("last start before a 10:00 session = %v; want 09:10 offered and 09:15 not", got)
	}
	if !slices.Contains(got, "10:50") || slices.Contains(got, "10:45") {
		t.Errorf("first start after a 10:00-10:40 session = %v; want 10:50 offered and 10:45 not", got)
	}
}

// No buffers: back-to-back sessions stay bookable on both sides (unchanged).
func TestTransition_noBuffersBackToBack(t *testing.T) {
	got := transitionStarts(t, 30, 30, 0, 0, "09:00", "11:00", session(10, 0, 30))
	want := []string{"09:00", "09:30", "10:30"}
	if !slices.Equal(got, want) {
		t.Errorf("starts with no buffers = %v; want %v", got, want)
	}
}
