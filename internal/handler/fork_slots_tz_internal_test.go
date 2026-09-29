package handler

// Fork (Agenda Maestros 4x4): regression tests for the slot/time-zone audit (Sep 2026).
// A Lima host, visitors in other zones:
//
//   - the external free/busy check must cover the host-local days the engine walks
//     (it stopped at 19:00 Lima on the last day, so evening slots were offered over real
//     Google busy time and bounced with 409 at booking);
//   - from/to are the VISITOR's days: a single-day fetch returns exactly that visitor
//     day, and a range returns every visitor day in it, including its first and last
//     (a Lima Wed 20:30 is Thu 03:30 in Madrid and used to be in neither month view).

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/slots"
)

// recordingCalendar is a "google" provider that behaves like Google freeBusy: it returns
// only the busy time overlapping the queried window, and it records every window asked.
type recordingCalendar struct {
	calendar.Provider
	busy []slots.Interval

	mu      sync.Mutex
	windows [][2]time.Time
}

func (*recordingCalendar) Name() string { return "google" }

func (c *recordingCalendar) FreeBusy(_ context.Context, _ string, from, to time.Time) ([]slots.Interval, error) {
	c.mu.Lock()
	c.windows = append(c.windows, [2]time.Time{from, to})
	c.mu.Unlock()
	var out []slots.Interval
	for _, b := range c.busy {
		if b.Start.Before(to) && b.End.After(from) {
			out = append(out, b)
		}
	}
	return out, nil
}

// tzFixture is a Lima host with evening, after-midnight and daytime hours on a
// 40-minute / 30-minute type with no notice and a one-year window, so real "now" works.
type tzFixture struct {
	t    *testing.T
	h    *Handler
	db   *sql.DB
	lima *time.Location
	slug string
}

func newTZFixture(t *testing.T) *tzFixture {
	t.Helper()
	lima, err := time.LoadLocation("America/Lima")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := New(database, slog.New(slog.DiscardHandler))
	for _, q := range []string{
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u1','m@example.com','Mentor','America/Lima',1,1)`,
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,slot_interval_minutes,min_notice_minutes,max_future_days)
		 VALUES ('et1','u1','mentoria','Mentoría',40,30,0,0)`,
		`INSERT INTO event_type_hosts (id,event_type_id,user_id,role,priority) VALUES ('eth1','et1','u1','required',0)`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	rules := [][3]any{}
	for dow := 0; dow <= 6; dow++ {
		rules = append(rules,
			[3]any{dow, "00:30", "01:10"}, // after midnight: the previous day for visitors west of Lima
			[3]any{dow, "09:00", "17:40"},
			[3]any{dow, "20:30", "21:10"}, // evening: the next day for visitors east of Lima
		)
	}
	for i, r := range rules {
		if _, err := database.Exec(`INSERT INTO availability_rules (id,user_id,day_of_week,start_time,end_time) VALUES (?,?,?,?,?)`,
			"r"+string(rune('a'+i/26))+string(rune('a'+i%26)), "u1", r[0], r[1], r[2]); err != nil {
			t.Fatalf("rule %v: %v", r, err)
		}
	}
	return &tzFixture{t: t, h: h, db: database, lima: lima, slug: "mentoria"}
}

// get calls GET /slots and returns the slot starts.
func (f *tzFixture) get(from, to, tz string) []string {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+f.slug+"/slots?from="+from+"&to="+to+"&tz="+tz, nil)
	req.SetPathValue("slug", f.slug)
	rec := httptest.NewRecorder()
	f.h.GetSlots(rec, req)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("slots %s..%s %s: %d %s", from, to, tz, rec.Code, rec.Body)
	}
	var body struct {
		Slots []slotJSON `json:"slots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		f.t.Fatal(err)
	}
	out := make([]string, len(body.Slots))
	for i, s := range body.Slots {
		out[i] = s.Start
	}
	return out
}

// nextWeekday is the first date >= now+14 days (in Lima) falling on wd, as YYYY-MM-DD.
func nextWeekday(lima *time.Location, wd time.Weekday) time.Time {
	y, m, d := time.Now().In(lima).AddDate(0, 0, 14).Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	for day.Weekday() != wd {
		day = day.AddDate(0, 0, 1)
	}
	return day
}

func ymdOf(t time.Time) string { return t.Format("2006-01-02") }

// A single-day fetch returns exactly that VISITOR day, and it is the same list a wide
// range gives for that day - for visitors east of Lima, west of it and in it. This is the
// month view's first/last day and the pages' single-day fallback in one property.
func TestSlots_fromToAreTheVisitorsDays(t *testing.T) {
	f := newTZFixture(t)
	start := nextWeekday(f.lima, time.Monday)
	for _, tz := range []string{"Europe/Madrid", "Pacific/Honolulu", "America/Lima", "Asia/Tokyo"} {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			t.Skip("no tzdata:", err)
		}
		// The wide answer, grouped by visitor day.
		wide := map[string][]string{}
		for _, s := range f.get(ymdOf(start), ymdOf(start.AddDate(0, 0, 6)), tz) {
			ts, err := time.Parse(time.RFC3339, s)
			if err != nil {
				t.Fatal(err)
			}
			day := ts.In(loc).Format("2006-01-02")
			if day < ymdOf(start) || day > ymdOf(start.AddDate(0, 0, 6)) {
				t.Errorf("%s: range %s..%s returned %s, on visitor day %s outside it", tz, ymdOf(start), ymdOf(start.AddDate(0, 0, 6)), s, day)
			}
			wide[day] = append(wide[day], s)
		}
		for i := 0; i < 7; i++ {
			day := ymdOf(start.AddDate(0, 0, i))
			single := f.get(day, day, tz)
			if strings.Join(single, " ") != strings.Join(wide[day], " ") {
				t.Errorf("%s day %s:\n single-day fetch %v\n range, that day  %v", tz, day, single, wide[day])
			}
			// Every 24-hour visitor day holds the host's daily pattern once: 1 + 17 + 1
			// starts (09:00..17:00 every 30 min, the last one ending at 17:40). A missing
			// edge slot or a leaked neighbour shows up here too. (A DST changeover day is
			// 23 or 25 hours long and can hold one more or one less, so it is skipped.)
			d0 := start.AddDate(0, 0, i)
			midnight := time.Date(d0.Year(), d0.Month(), d0.Day(), 0, 0, 0, 0, loc)
			next := time.Date(d0.Year(), d0.Month(), d0.Day()+1, 0, 0, 0, 0, loc)
			if next.Sub(midnight) == 24*time.Hour && len(single) != 19 {
				t.Errorf("%s day %s: %d slots %v; want 19", tz, day, len(single), single)
			}
		}
	}
}

// The concrete production case: a Madrid visitor must see the Lima Wednesday 20:30
// session as Thursday 03:30 when that Thursday is the FIRST day asked for (the first of
// a month), and the Lima Thursday 17:00 start (Friday 00:00 in Madrid) must not leak in.
func TestSlots_madridFirstDayKeepsTheLimaEveningSlot(t *testing.T) {
	f := newTZFixture(t)
	thu := nextWeekday(f.lima, time.Thursday)
	got := f.get(ymdOf(thu), ymdOf(thu), "Europe/Madrid")
	madrid, _ := time.LoadLocation("Europe/Madrid")
	wed2030 := time.Date(thu.Year(), thu.Month(), thu.Day()-1, 20, 30, 0, 0, f.lima).In(madrid).Format(time.RFC3339)
	thu1700 := time.Date(thu.Year(), thu.Month(), thu.Day(), 17, 0, 0, 0, f.lima).In(madrid).Format(time.RFC3339)
	has := func(s string) bool {
		for _, g := range got {
			if g == s {
				return true
			}
		}
		return false
	}
	if !has(wed2030) {
		t.Errorf("Madrid %s: missing %s (Lima Wed 20:30); got %v", ymdOf(thu), wed2030, got)
	}
	if has(thu1700) {
		t.Errorf("Madrid %s: includes %s (Lima Thu 17:00 = Madrid Friday); got %v", ymdOf(thu), thu1700, got)
	}
}

// The external calendar check covers the whole host-local last day: Google busy on the
// last Lima day 20:00-22:00 removes the 20:30 slot. It used to ask Google for
// [from, to+1) in UTC, which ends at 19:00 in Lima, so the slot was offered and 409'd.
func TestSlots_freeBusyCoversTheHostsLastDay(t *testing.T) {
	f := newTZFixture(t)
	wed := nextWeekday(f.lima, time.Wednesday)
	busyStart := time.Date(wed.Year(), wed.Month(), wed.Day(), 20, 0, 0, 0, f.lima)
	cal := &recordingCalendar{busy: []slots.Interval{{Start: busyStart, End: busyStart.Add(2 * time.Hour)}}}
	svc := calendar.NewService(f.db)
	svc.Register(cal)
	f.h.SetCalendar(svc)

	slot := time.Date(wed.Year(), wed.Month(), wed.Day(), 20, 30, 0, 0, f.lima).Format(time.RFC3339)
	for _, from := range []time.Time{wed, wed.AddDate(0, 0, -6)} { // single day, and a range ending on it
		got := f.get(ymdOf(from), ymdOf(wed), "America/Lima")
		for _, s := range got {
			if s == slot {
				t.Errorf("range %s..%s offers %s over Google busy 20:00-22:00 Lima", ymdOf(from), ymdOf(wed), slot)
			}
		}
	}
	// And the windows asked for reach past the end of the last Lima day.
	endOfWed := time.Date(wed.Year(), wed.Month(), wed.Day()+1, 0, 0, 0, 0, f.lima)
	cal.mu.Lock()
	defer cal.mu.Unlock()
	if len(cal.windows) == 0 {
		t.Fatal("free/busy never queried")
	}
	for _, w := range cal.windows {
		if w[1].Before(endOfWed) {
			t.Errorf("free/busy window %s..%s ends before the last Lima day does (%s)", w[0].UTC(), w[1].UTC(), endOfWed.UTC())
		}
	}
}

// hostBusyWindow is built in the host's zone: Lima days [from, to] plus a day each side.
func TestHostBusyWindow_isHostLocal(t *testing.T) {
	lima, err := time.LoadLocation("America/Lima")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	gotFrom, gotTo := hostBusyWindow(lima, from, to)
	if want := time.Date(2026, 8, 31, 0, 0, 0, 0, lima); !gotFrom.Equal(want) {
		t.Errorf("from = %s; want %s", gotFrom, want.UTC())
	}
	if want := time.Date(2026, 10, 2, 0, 0, 0, 0, lima); !gotTo.Equal(want) {
		t.Errorf("to = %s; want %s", gotTo, want.UTC())
	}
	if gotFrom.Location() != time.UTC || gotTo.Location() != time.UTC {
		t.Error("bounds must be UTC: the bookings query compares them as strings")
	}
}

// parseDateRangeStrIn's "today" is the visitor's: at 20:20Z on Sep 29 it is already
// Sep 30 in Tokyo and still Sep 29 in Lima; the cap moves with it.
func TestParseDateRangeStrIn_todayIsTheVisitors(t *testing.T) {
	now := time.Date(2026, 9, 29, 20, 20, 0, 0, time.UTC)
	for tz, want := range map[string]string{"Asia/Tokyo": "2026-09-30", "America/Lima": "2026-09-29", "UTC": "2026-09-29"} {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			t.Skip("no tzdata:", err)
		}
		from, to, ok := parseDateRangeStrIn("", "", now, 60, loc)
		if !ok {
			t.Fatal("ok = false")
		}
		if got := ymdOf(from); got != want {
			t.Errorf("%s: default from = %s; want %s", tz, got, want)
		}
		if got, wantTo := ymdOf(to), ymdOf(from.AddDate(0, 0, 60)); got != wantTo {
			t.Errorf("%s: default to = %s; want %s", tz, got, wantTo)
		}
	}
}

// keepBookerDays keeps only the starts on the visitor's days, whatever zone the engine
// rendered them in.
func TestKeepBookerDays(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	mk := func(s string) slots.Slot {
		ts, _ := time.Parse(time.RFC3339, s)
		return slots.Slot{Start: ts, End: ts.Add(40 * time.Minute)}
	}
	in := []slots.Slot{
		mk("2026-09-30T23:30:00+02:00"), // Sep 30 Madrid: out
		mk("2026-10-01T01:30:00Z"),      // 03:30 Oct 1 Madrid: in
		mk("2026-10-01T21:30:00Z"),      // 23:30 Oct 1 Madrid: in
		mk("2026-10-01T22:00:00Z"),      // 00:00 Oct 2 Madrid: out
	}
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var got []string
	for _, s := range keepBookerDays(in, madrid, day, day) {
		got = append(got, s.Start.UTC().Format(time.RFC3339))
	}
	sort.Strings(got)
	if strings.Join(got, " ") != "2026-10-01T01:30:00Z 2026-10-01T21:30:00Z" {
		t.Errorf("kept %v", got)
	}
}
