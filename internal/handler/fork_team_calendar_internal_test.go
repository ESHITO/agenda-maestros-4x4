package handler

// Fork (Agenda Maestros 4x4): the pure parts of the team calendar (fork_team_calendar.go,
// fork_team_coverage.go): wall-clock minutes per viewer day (DST included), the target as
// instants, the colours, the bounded cache map and the shared computation.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

type splitPiece struct {
	day  string
	s, e int
}

func split(t *testing.T, iv slots.Interval, loc *time.Location, from, to string) []splitPiece {
	t.Helper()
	f, _ := time.Parse("2006-01-02", from)
	tt, _ := time.Parse("2006-01-02", to)
	var out []splitPiece
	splitByViewerDay(iv, loc, f, tt, func(day string, s, e int) { out = append(out, splitPiece{day, s, e}) })
	return out
}

func TestSplitByViewerDay(t *testing.T) {
	lima := mustLoc(t, "America/Lima")
	at := func(loc *time.Location, y int, m time.Month, d, hh, mm int) time.Time {
		return time.Date(y, m, d, hh, mm, 0, 0, loc)
	}

	// A session crossing the viewer's midnight lands on both days; the first piece ends at 1440.
	got := split(t, slots.Interval{Start: at(lima, 2026, 10, 6, 23, 0), End: at(lima, 2026, 10, 7, 1, 30)}, lima, "2026-10-01", "2026-10-31")
	want := []splitPiece{{"2026-10-06", 1380, 1440}, {"2026-10-07", 0, 90}}
	if !slices.Equal(got, want) {
		t.Errorf("crossing midnight = %v; want %v", got, want)
	}
	// Pieces outside [from, to] are dropped.
	got = split(t, slots.Interval{Start: at(lima, 2026, 10, 6, 23, 0), End: at(lima, 2026, 10, 7, 1, 30)}, lima, "2026-10-07", "2026-10-07")
	if !slices.Equal(got, []splitPiece{{"2026-10-07", 0, 90}}) {
		t.Errorf("clipped to the range = %v", got)
	}
	// The same instant seen from Madrid: another day and other minutes (the viewer's clock).
	madrid := mustLoc(t, "Europe/Madrid")
	got = split(t, slots.Interval{Start: at(lima, 2026, 10, 6, 18, 0), End: at(lima, 2026, 10, 6, 19, 0)}, madrid, "2026-10-01", "2026-10-31")
	if !slices.Equal(got, []splitPiece{{"2026-10-07", 60, 120}}) {
		t.Errorf("Lima 18:00 seen from Madrid = %v; want 7 Oct 01:00-02:00", got)
	}

	// Fall back (New York, 1 Nov 2026, 02:00 EDT -> 01:00 EST): 01:30 EDT to 01:15 EST is 45
	// real minutes, so the end is written start + 45, not the repeated clock.
	ny := mustLoc(t, "America/New_York")
	s := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC) // 01:30 EDT
	e := time.Date(2026, 11, 1, 6, 15, 0, 0, time.UTC) // 01:15 EST
	got = split(t, slots.Interval{Start: s, End: e}, ny, "2026-11-01", "2026-11-01")
	if !slices.Equal(got, []splitPiece{{"2026-11-01", 90, 135}}) {
		t.Errorf("repeated hour = %v; want [90 135]", got)
	}
	// Santiago, 4 Apr 2026: 24:00 -03 -> 23:00 -04. A span in the repeated hour whose end
	// clock reads before its start is capped at the day's end.
	scl := mustLoc(t, "America/Santiago")
	s = time.Date(2026, 4, 5, 2, 10, 0, 0, time.UTC) // 23:10 -03
	e = time.Date(2026, 4, 5, 3, 5, 0, 0, time.UTC)  // 23:05 -04
	got = split(t, slots.Interval{Start: s, End: e}, scl, "2026-04-01", "2026-04-30")
	if !slices.Equal(got, []splitPiece{{"2026-04-04", 1390, 1440}}) {
		t.Errorf("Santiago repeated hour = %v; want 4 Apr [1390 1440]", got)
	}
	// Santiago, 6 Sep 2026: midnight does not exist (00:00 -> 01:00), so the day starts at
	// minute 60.
	s = time.Date(2026, 9, 5, 22, 0, 0, 0, scl)
	e = time.Date(2026, 9, 6, 3, 0, 0, 0, scl)
	got = split(t, slots.Interval{Start: s, End: e}, scl, "2026-09-01", "2026-09-30")
	want = []splitPiece{{"2026-09-05", 1320, 1440}, {"2026-09-06", 60, 180}}
	if !slices.Equal(got, want) {
		t.Errorf("Santiago skipped midnight = %v; want %v", got, want)
	}
	// Empty interval: nothing.
	if got := split(t, slots.Interval{Start: s, End: s}, scl, "2026-09-01", "2026-09-30"); len(got) != 0 {
		t.Errorf("empty interval = %v", got)
	}
}

func TestTeamTargetIntervals(t *testing.T) {
	madrid := mustLoc(t, "Europe/Madrid")
	tgt := teamCoverageTargetJSON{V: 1, TZ: "America/Lima", Days: []teamCoverageDayJSON{
		{Dow: 2, Start: "08:00", End: "20:00"}, // Tuesday
		{Dow: 3, Start: "22:00", End: "24:00"}, // Wednesday until midnight
	}}
	tloc, rules, err := coverageRules(tgt)
	if err != nil {
		t.Fatal(err)
	}
	// Viewer in Madrid, Tuesday 6 Oct 2026 .. Thursday 8 Oct (CEST, Lima +7 h behind).
	from := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	start, end := viewerMidnight(from, madrid), viewerMidnight(to.AddDate(0, 0, 1), madrid)
	ivs := teamTargetIntervals(tloc, rules, start, end)
	var got []splitPiece
	for _, iv := range ivs {
		splitByViewerDay(iv, madrid, from, to, func(day string, s, e int) { got = append(got, splitPiece{day, s, e}) })
	}
	// Tue 08-20 Lima = Tue 15:00 - Wed 03:00 Madrid; Wed 22-24 Lima = Thu 05:00-07:00 Madrid.
	want := []splitPiece{{"2026-10-06", 900, 1440}, {"2026-10-07", 0, 180}, {"2026-10-08", 300, 420}}
	if !slices.Equal(got, want) {
		t.Errorf("target seen from Madrid = %v; want %v", got, want)
	}
}

func TestValidateCoverageTarget(t *testing.T) {
	ok := []teamCoverageDayJSON{{Dow: 6, Start: "09:30", End: "24:00"}, {Dow: 0, Start: "00:00", End: "12:00"}}
	days, msg := validateCoverageTarget("America/Lima", ok)
	if msg != "" || len(days) != 2 || days[0].Dow != 0 {
		t.Errorf("valid target: %v %q; want sorted by weekday", days, msg)
	}
	if days, msg := validateCoverageTarget("UTC", []teamCoverageDayJSON{}); msg != "" || len(days) != 0 {
		t.Errorf("empty target: %v %q; want valid", days, msg)
	}
	for _, c := range []struct {
		tz   string
		days []teamCoverageDayJSON
	}{
		{"Mars/Olympus", nil},
		{"Local", nil},
		{"", nil},
		{"UTC", []teamCoverageDayJSON{{Dow: 7, Start: "08:00", End: "09:00"}}},
		{"UTC", []teamCoverageDayJSON{{Dow: -1, Start: "08:00", End: "09:00"}}},
		{"UTC", []teamCoverageDayJSON{{Dow: 1, Start: "08:00", End: "09:00"}, {Dow: 1, Start: "10:00", End: "11:00"}}},
		{"UTC", []teamCoverageDayJSON{{Dow: 1, Start: "08:15", End: "09:00"}}},
		{"UTC", []teamCoverageDayJSON{{Dow: 1, Start: "8:00", End: "09:00"}}},
		{"UTC", []teamCoverageDayJSON{{Dow: 1, Start: "24:00", End: "24:00"}}},
		{"UTC", []teamCoverageDayJSON{{Dow: 1, Start: "10:00", End: "10:00"}}},
		{"UTC", []teamCoverageDayJSON{{Dow: 1, Start: "11:00", End: "10:00"}}},
		{"UTC", []teamCoverageDayJSON{{Dow: 1, Start: "10:00", End: "24:30"}}},
	} {
		if _, msg := validateCoverageTarget(c.tz, c.days); msg == "" {
			t.Errorf("tz %q days %v: accepted; want a 400 message", c.tz, c.days)
		}
	}
}

func TestParseTeamRange_calendarCap(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if from, to, msg := parseTeamRange("2026-10-01", "2026-10-31", now, time.UTC, teamCalendarMaxDays); msg != "" || from != "2026-10-01" || to != "2026-10-31" {
		t.Errorf("31 days: %q %q %q", from, to, msg)
	}
	if _, _, msg := parseTeamRange("2026-10-01", "2026-11-01", now, time.UTC, teamCalendarMaxDays); msg != "El rango máximo es de 31 días." {
		t.Errorf("32 days: %q", msg)
	}
	// Defaults: today .. today+6, also with the 31-day cap.
	if from, to, msg := parseTeamRange("", "", now, time.UTC, teamCalendarMaxDays); msg != "" || from != "2026-10-06" || to != "2026-10-12" {
		t.Errorf("defaults: %q %q %q", from, to, msg)
	}
	// The 7-day endpoint keeps its message.
	if _, _, msg := parseTeamAvailabilityRange("2026-10-01", "2026-10-08", now, time.UTC); msg != "El rango máximo es de 7 días." {
		t.Errorf("7-day cap: %q", msg)
	}
}

func TestTeamCalColors(t *testing.T) {
	users := map[string]teamAvailUser{
		"own": {name: "Dueño", accent: accentFallback, isOwner: true},
		"zed": {name: "Zed", accent: accentFallback},
		"ana": {name: "ana", accent: ""},
		"cus": {name: "Custom", accent: "#2563EB"}, // took the palette's first colour
		"bea": {name: "Bea", accent: accentFallback},
	}
	entries := []teamAvailEntry{
		{userID: "own", area: areaMentoria}, {userID: "zed", area: areaMentoria}, {userID: "own", area: areaSoporte},
		{userID: "cus", area: areaMentoria}, {userID: "bea", area: areaSoporte}, {userID: "ana", area: areaSoporte},
		{userID: "gone", area: areaSoporte},
	}
	got := teamCalColors(entries, users)
	if c := got["own"]; c.color != teamOwnerDefaultColor || c.custom {
		t.Errorf("owner on the default = %+v; want turquoise, not custom", c)
	}
	if c := got["cus"]; c.color != "#2563eb" || !c.custom {
		t.Errorf("custom = %+v", c)
	}
	// ana, Bea, Zed in name order, skipping the taken #2563eb.
	if got["ana"].color != teamCalPalette[1] || got["bea"].color != teamCalPalette[2] || got["zed"].color != teamCalPalette[3] {
		t.Errorf("palette order = ana %s bea %s zed %s", got["ana"].color, got["bea"].color, got["zed"].color)
	}
	if _, ok := got["gone"]; ok {
		t.Error("an archived user got a colour")
	}
	// Filtering never repaints: the same team in another entry order gives the same colours.
	rev := slices.Clone(entries)
	slices.Reverse(rev)
	again := teamCalColors(rev, users)
	for id, c := range got {
		if again[id] != c {
			t.Errorf("%s: %+v then %+v", id, c, again[id])
		}
	}
}

func TestTeamGenMap_capAndGeneration(t *testing.T) {
	var c teamAvailCache
	now := time.Now()
	for i := 0; i < teamCalCacheMax+5; i++ {
		c.putCal(fmt.Sprintf("k%d", i), now.Add(time.Duration(i)*time.Millisecond), c.gen.Load(), teamCalendarJSON{From: fmt.Sprint(i)})
	}
	if n := len(c.cal.m); n != teamCalCacheMax {
		t.Errorf("entries = %d; want the cap %d", n, teamCalCacheMax)
	}
	if _, ok := c.getCal("k0", now); ok {
		t.Error("the oldest entry survived the cap")
	}
	if got, ok := c.getCal(fmt.Sprintf("k%d", teamCalCacheMax+4), now); !ok || got.From != fmt.Sprint(teamCalCacheMax+4) {
		t.Error("the newest entry is missing")
	}
	c.gen.Add(1) // a write makes both maps stale
	if _, ok := c.getCal(fmt.Sprintf("k%d", teamCalCacheMax+4), now); ok {
		t.Error("calendar entry of an older generation served")
	}
}

func TestTeamFlight_sharesAndRecovers(t *testing.T) {
	var f teamFlight[int]
	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]int, 4)
	shared := make([]bool, 4)
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[0], shared[0], _ = f.do(context.Background(), "k", func() (int, error) {
			calls.Add(1)
			close(started)
			<-release
			return 42, nil
		})
	}()
	<-started
	for i := 1; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], shared[i], _ = f.do(context.Background(), "k", func() (int, error) {
				calls.Add(1)
				return -1, nil
			})
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // let the waiters queue on the call
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("computations = %d; want 1", calls.Load())
	}
	for i, r := range results {
		if r != 42 {
			t.Errorf("caller %d got %d", i, r)
		}
	}
	if shared[0] || !shared[1] {
		t.Errorf("shared flags = %v", shared)
	}

	// A waiter that leaves returns its own context error at once.
	block := make(chan struct{})
	go f.do(context.Background(), "slow", func() (int, error) { <-block; return 1, nil }) //nolint:errcheck
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := f.do(ctx, "slow", func() (int, error) { return 2, nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("departed waiter err = %v; want context.Canceled", err)
	}
	close(block)

	// The first caller leaves mid-computation: a waiter still interested computes again.
	firstCtx, firstCancel := context.WithCancel(context.Background())
	inFirst := make(chan struct{})
	go f.do(firstCtx, "abandon", func() (int, error) { //nolint:errcheck
		close(inFirst)
		<-firstCtx.Done()
		return 0, firstCtx.Err()
	})
	<-inFirst
	done := make(chan int)
	go func() {
		v, _, _ := f.do(context.Background(), "abandon", func() (int, error) { return 7, nil })
		done <- v
	}()
	time.Sleep(20 * time.Millisecond)
	firstCancel()
	select {
	case v := <-done:
		if v != 7 {
			t.Errorf("waiter after an abandoned computation got %d; want its own 7", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter stuck after the first caller left")
	}
}

// A row whose hours were read keeps them when its free starts fail quickly: the error is
// "calendar" (free starts unknown), never step 3's "internal" (schedule unreadable).
func TestTeamCalFreeErrorKind(t *testing.T) {
	for in, want := range map[string]string{"internal": "calendar", "calendar": "calendar", "timeout": "timeout"} {
		if got := teamCalFreeErrorKind(in); got != want {
			t.Errorf("teamCalFreeErrorKind(%q) = %q; want %q", in, got, want)
		}
	}
	kind, drop := teamClassify(errors.New("db busy"), false, slotsResult{})
	if got := teamCalFreeErrorKind(kind); drop || got != "calendar" {
		t.Errorf("quick computeSlots failure = %q (drop %v); want calendar", got, drop)
	}
}

// The palette keeps clear of the coverage hues (red, amber/orange, green/teal) and of the
// owner's turquoise.
func TestTeamCalPalette_noCoverageHues(t *testing.T) {
	banned := []string{"#dc2626", "#d97706", "#16a34a", "#ea580c", "#ca8a04", "#0d9488", "#65a30d", teamOwnerDefaultColor}
	seen := map[string]bool{}
	for _, c := range teamCalPalette {
		if slices.Contains(banned, c) {
			t.Errorf("palette holds %s", c)
		}
		if seen[c] {
			t.Errorf("palette repeats %s", c)
		}
		seen[c] = true
	}
}

// A failed read of the target is an error, never the default; no row and an invalid saved
// value are the default.
func TestLoadCoverageTarget_readErrorIsNotTheDefault(t *testing.T) {
	h := newForkSettingsHandler(t)
	ctx := context.Background()
	if tg, err := h.loadCoverageTarget(ctx); err != nil || !tg.IsDefault {
		t.Fatalf("no row = %+v, %v; want the default", tg, err)
	}
	if err := h.setForkSettings(ctx, map[string]string{forkKeyTeamCoverageTarget: `{"v":1,"tz":"UTC","days":[]}`}); err != nil {
		t.Fatal(err)
	}
	if tg, err := h.loadCoverageTarget(ctx); err != nil || tg.IsDefault || tg.TZ != "UTC" || len(tg.Days) != 0 {
		t.Errorf("saved no-target = %+v, %v", tg, err)
	}
	if err := h.setForkSettings(ctx, map[string]string{forkKeyTeamCoverageTarget: `garbage`}); err != nil {
		t.Fatal(err)
	}
	if tg, err := h.loadCoverageTarget(ctx); err != nil || !tg.IsDefault {
		t.Errorf("invalid saved value = %+v, %v; want the default", tg, err)
	}
	if _, err := h.db.ExecContext(ctx, `ALTER TABLE fork_settings RENAME TO fork_settings_gone`); err != nil {
		t.Fatal(err)
	}
	if tg, err := h.loadCoverageTarget(ctx); err == nil {
		t.Errorf("failed read = %+v, nil; want an error, never the default", tg)
	}
}

// "Not bookable" (no row) is nil, nil; a failed read is an error, never a silent drop.
func TestTeamBookableEventType_readErrorIsNotADrop(t *testing.T) {
	h := newForkSettingsHandler(t)
	ctx := context.Background()
	if et, err := h.teamBookableEventType(ctx, "nobody-has-this"); et != nil || err != nil {
		t.Errorf("unknown slug = %v, %v; want nil, nil", et, err)
	}
	h.db.Close()
	if et, err := h.teamBookableEventType(ctx, "nobody-has-this"); et != nil || err == nil {
		t.Errorf("closed DB = %v, %v; want an error", et, err)
	}
}
