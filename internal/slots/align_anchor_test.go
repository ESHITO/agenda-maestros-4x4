package slots

// Fork (Agenda Maestros 4x4): starts are anchored at the start of each availability
// window, in host time, not at the Unix epoch (audit, Sep 2026). With the epoch grid a
// Lima host on a 45-minute interval lost 09:00 (first start 09:15), on 90 minutes the
// first start was 10:00, and a 09:30 window on 40 minutes started at 09:40.

import (
	"sort"
	"testing"
	"time"
)

func limaOrSkip(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Lima")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	return loc
}

// startsOn runs Generate for one Lima host with the given rules on one day and returns
// the starts as Lima "15:04".
func startsOn(t *testing.T, lima *time.Location, day time.Time, dur, interval int, rules []AvailabilityRule, busy []Interval) []string {
	t.Helper()
	out, err := Generate(Request{
		Event:    EventConfig{DurationMinutes: dur, SlotIntervalMinutes: interval, RoutingMode: "fixed"},
		Hosts:    []HostAvailability{{HostID: "h", Location: lima, Rules: rules, Busy: busy}},
		DateFrom: day, DateTo: day, BookerTZ: lima,
		Now: day.AddDate(0, 0, -7),
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range out {
		got = append(got, s.Start.In(lima).Format("15:04"))
	}
	return got
}

func TestAlignment_anchoredAtTheWindowStart(t *testing.T) {
	lima := limaOrSkip(t)
	thu := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC) // a Thursday
	for _, c := range []struct {
		interval   int
		start, end string
		want       []string // first three starts, Lima time
	}{
		{30, "09:00", "17:40", []string{"09:00", "09:30", "10:00"}},
		{40, "09:30", "17:40", []string{"09:30", "10:10", "10:50"}}, // epoch: 09:40
		{45, "09:00", "17:40", []string{"09:00", "09:45", "10:30"}}, // epoch: 09:15
		{50, "09:30", "17:40", []string{"09:30", "10:20", "11:10"}}, // epoch: 09:50
		{90, "09:00", "17:40", []string{"09:00", "10:30", "12:00"}}, // epoch: 10:00
		{60, "20:30", "23:00", []string{"20:30", "21:30"}},          // epoch: 21:00
	} {
		rules := []AvailabilityRule{{DayOfWeek: time.Thursday, StartTime: c.start, EndTime: c.end}}
		got := startsOn(t, lima, thu, 40, c.interval, rules, nil)
		if len(got) > len(c.want) {
			got = got[:len(c.want)]
		}
		if len(got) != len(c.want) || !equalStrings(got, c.want) {
			t.Errorf("interval %d, window %s-%s: first starts %v; want %v", c.interval, c.start, c.end, got, c.want)
		}
	}
}

// The grid is fixed before busy time is cut out: after a 12:00-12:40 booking the next
// start is 13:00 (the 09:00 grid), never 12:40. On a 45-minute grid, 12:45 fits after it.
func TestAlignment_busyResumesOnTheWindowGrid(t *testing.T) {
	lima := limaOrSkip(t)
	thu := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	rules := []AvailabilityRule{{DayOfWeek: time.Thursday, StartTime: "09:00", EndTime: "17:40"}}
	booked := []Interval{{
		Start: time.Date(2026, 10, 8, 12, 0, 0, 0, lima),
		End:   time.Date(2026, 10, 8, 12, 40, 0, 0, lima),
	}}
	for _, c := range []struct {
		interval int
		after    string // first start at or after 12:00
	}{{30, "13:00"}, {45, "12:45"}, {90, "13:30"}} {
		got := startsOn(t, lima, thu, 40, c.interval, rules, booked)
		first := ""
		for _, s := range got {
			if s >= "12:00" {
				first = s
				break
			}
		}
		if first != c.after {
			t.Errorf("interval %d: first start after the 12:00 booking = %q; want %q (all: %v)", c.interval, first, c.after, got)
		}
	}
}

// epochReference is the pre-fix engine for one fixed host: merge the day's windows,
// subtract busy, align every free stretch up to the Unix-epoch grid.
func epochReference(t *testing.T, loc *time.Location, from, to time.Time, dur, interval time.Duration, rules []AvailabilityRule, busy []Interval, now time.Time) []time.Time {
	t.Helper()
	var out []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		windows, err := resolveDay(loc, d, rules, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range subtract(windows, busy) {
			start := f.Start
			secs := int64(interval / time.Second)
			if rem := start.Unix() % secs; rem != 0 {
				start = start.Add(time.Duration(secs-rem) * time.Second)
			}
			for ts := start; !ts.Add(dur).After(f.End); ts = ts.Add(interval) {
				if !ts.Before(now) {
					out = append(out, ts)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// The production shape - Lima host, 40-minute sessions every 30 minutes, windows starting
// on :00 or :30, bookings and calendar busy at arbitrary minutes - gives exactly the same
// starts as the old epoch grid over two months, for every visitor zone.
func TestAlignment_productionThirtyMinuteCaseUnchanged(t *testing.T) {
	lima := limaOrSkip(t)
	rules := []AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "17:00"},
		{DayOfWeek: time.Tuesday, StartTime: "09:00", EndTime: "13:00"},
		{DayOfWeek: time.Tuesday, StartTime: "12:30", EndTime: "17:40"}, // overlaps: merged
		{DayOfWeek: time.Wednesday, StartTime: "20:30", EndTime: "21:10"},
		{DayOfWeek: time.Wednesday, StartTime: "09:30", EndTime: "12:00"},
		{DayOfWeek: time.Thursday, StartTime: "09:00", EndTime: "17:40"},
		{DayOfWeek: time.Friday, StartTime: "00:30", EndTime: "02:00"},
		{DayOfWeek: time.Saturday, StartTime: "10:30", EndTime: "13:00"},
	}
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 60)
	var busy []Interval
	for i := 0; i < 60; i++ {
		d := from.AddDate(0, 0, i)
		// A booking ending off the grid, a Google event at odd minutes.
		busy = append(busy,
			Interval{Start: time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, lima), End: time.Date(d.Year(), d.Month(), d.Day(), 12, 40, 0, 0, lima)},
			Interval{Start: time.Date(d.Year(), d.Month(), d.Day(), 15, 10+i%20, 0, 0, lima), End: time.Date(d.Year(), d.Month(), d.Day(), 15, 55, 0, 0, lima)},
		)
	}
	now := from.Add(20*time.Hour + 20*time.Minute)
	want := epochReference(t, lima, from, to, 40*time.Minute, 30*time.Minute, rules, busy, now)
	if len(want) < 300 {
		t.Fatalf("reference produced only %d starts; the fixture is wrong", len(want))
	}
	for _, tz := range []string{"America/Lima", "Europe/Madrid", "America/Mexico_City", "UTC"} {
		booker, err := time.LoadLocation(tz)
		if err != nil {
			t.Skip("no tzdata:", err)
		}
		got, err := Generate(Request{
			Event:    EventConfig{DurationMinutes: 40, SlotIntervalMinutes: 30, MinNoticeMinutes: 0, MaxFutureDays: 90, RoutingMode: "fixed"},
			Hosts:    []HostAvailability{{HostID: "h", Location: lima, Rules: rules, Busy: busy}},
			DateFrom: from, DateTo: to, BookerTZ: booker, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d starts; the epoch grid gave %d", tz, len(got), len(want))
		}
		for i := range got {
			if !got[i].Start.Equal(want[i]) {
				t.Fatalf("%s: start %d = %s; the epoch grid gave %s", tz, i, got[i].Start.UTC(), want[i].UTC())
			}
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// multiStarts runs Generate over [from, to] for several hosts in one routing mode and
// returns the offered starts as Lima "01-02 15:04".
func multiStarts(t *testing.T, lima *time.Location, mode string, dur, interval int, hosts []HostAvailability, from, to time.Time) []string {
	t.Helper()
	out, err := Generate(Request{
		Event:    EventConfig{DurationMinutes: dur, SlotIntervalMinutes: interval, RoutingMode: mode},
		Hosts:    hosts,
		DateFrom: from, DateTo: to, BookerTZ: lima,
		Now: from.AddDate(0, 0, -7),
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range out {
		got = append(got, s.Start.In(lima).Format("01-02 15:04"))
	}
	return got
}

// Several hosts compared at one start share ONE grid (midnight in the reference host's
// zone), so windows starting at different offsets still meet. With a grid per window, a
// required 09:00 host and a 14:30 rotation host on 60 minutes never shared a start and
// the event offered nothing (review of the anchor fix, Sep 2026); the epoch grid gave
// 15:00 and 16:00.
func TestAlignment_multiHostModesShareOneGrid(t *testing.T) {
	lima := limaOrSkip(t)
	thu := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	rule := func(start, end string) []AvailabilityRule {
		return []AvailabilityRule{{DayOfWeek: time.Thursday, StartTime: start, EndTime: end}}
	}
	reqAndRot := []HostAvailability{
		{HostID: "a", Location: lima, Role: "required", Rules: rule("09:00", "17:00")},
		{HostID: "b", Location: lima, Role: "rotation", Rules: rule("14:30", "18:00")},
	}
	nineAndNineThirty := []HostAvailability{
		{HostID: "a", Location: lima, Rules: rule("09:00", "13:00")},
		{HostID: "b", Location: lima, Rules: rule("09:30", "13:00")},
	}
	for _, c := range []struct {
		name          string
		mode          string
		dur, interval int
		hosts         []HostAvailability
		want          []string
	}{
		{"collective 30", "collective", 60, 30, reqAndRot, []string{"10-08 14:30", "10-08 15:00", "10-08 15:30", "10-08 16:00"}},
		{"collective 60", "collective", 60, 60, reqAndRot, []string{"10-08 15:00", "10-08 16:00"}},
		{"round robin + required 60", "round_robin", 60, 60, reqAndRot, []string{"10-08 15:00", "10-08 16:00"}},
		{"collective 09:00/09:30 on 60", "collective", 60, 60, nineAndNineThirty, []string{"10-08 10:00", "10-08 11:00", "10-08 12:00"}},
		// New types default the interval to the duration: 40/40 steps from Lima midnight
		// (09:20, 10:00, ...), common to both hosts.
		{"collective 09:00/09:30 on 40", "collective", 40, 40, nineAndNineThirty, []string{"10-08 10:00", "10-08 10:40", "10-08 11:20", "10-08 12:00"}},
		// Merged lists still step by the interval, not every 30 minutes alternating hosts.
		{"round robin rotation-only 60", "round_robin", 60, 60, nineAndNineThirty, []string{"10-08 09:00", "10-08 10:00", "10-08 11:00", "10-08 12:00"}},
		{"priority 60", "priority", 60, 60, nineAndNineThirty, []string{"10-08 09:00", "10-08 10:00", "10-08 11:00", "10-08 12:00"}},
	} {
		got := multiStarts(t, lima, c.mode, c.dur, c.interval, c.hosts, thu, thu)
		if !equalStrings(got, c.want) {
			t.Errorf("%s: starts %v; want %v", c.name, got, c.want)
		}
	}
}

// Across zones the grid is the reference host's (the first one's): a Lima host free Wed
// 20:00-23:59 and a Kolkata host free Thu 06:00-12:00 IST (Wed 19:30 - Thu 01:30 Lima)
// meet on the Lima hour, as they did on the epoch grid.
func TestAlignment_multiHostAcrossZones(t *testing.T) {
	lima := limaOrSkip(t)
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	wed := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	hosts := []HostAvailability{
		{HostID: "lima", Location: lima, Rules: []AvailabilityRule{{DayOfWeek: time.Wednesday, StartTime: "20:00", EndTime: "23:59"}}},
		{HostID: "india", Location: kolkata, Rules: []AvailabilityRule{{DayOfWeek: time.Thursday, StartTime: "06:00", EndTime: "12:00"}}},
	}
	got := multiStarts(t, lima, "collective", 60, 60, hosts, wed, wed.AddDate(0, 0, 1))
	want := []string{"10-07 20:00", "10-07 21:00", "10-07 22:00"}
	if !equalStrings(got, want) {
		t.Errorf("collective Lima+Kolkata: starts %v; want %v", got, want)
	}
}
