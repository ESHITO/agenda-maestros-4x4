package handler_test

// Fork (Agenda Maestros 4x4): GET /v1/team/calendar and GET|PUT /v1/team/coverage-target
// (fork_team_calendar.go, fork_team_coverage.go) - the Panel's Mes / 15 días / Semana team
// calendar. Free starts must be exactly the public page's (computeSlots through GET
// /slots), sessions never leak a booking id or a client, and "Sin cubrir" is the target
// minus everybody's working hours.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/slots"
	"github.com/calnode/calnode/internal/uid"
)

type calSpan [2]int

type calBusy struct {
	Key   string `json:"key"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Type  string `json:"type"`
	Area  string `json:"area"`
}

type calDay struct {
	Hours []calSpan `json:"hours"`
	Free  []int     `json:"free"`
	Busy  []calBusy `json:"busy"`
}

type calPerson struct {
	Key         string `json:"key"`
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	Area        string `json:"area"`
	Color       string `json:"color"`
	ColorCustom bool   `json:"color_custom"`
	IsOwner     bool   `json:"is_owner"`
	IsYou       bool   `json:"is_you"`
	Link        struct {
		Slug string `json:"slug"`
		URL  string `json:"url"`
	} `json:"link"`
	DurationMin   int                `json:"duration_min"`
	NoticeUntil   string             `json:"notice_until"`
	BookableUntil string             `json:"bookable_until"`
	Error         bool               `json:"error"`
	ErrorKind     string             `json:"error_kind"`
	Days          map[string]*calDay `json:"days"`
}

type calCoverageDay struct {
	Target    []calSpan `json:"target"`
	Uncovered []calSpan `json:"uncovered"`
	OwnerOnly []calSpan `json:"owner_only"`
}

type calTarget struct {
	V    int    `json:"v"`
	TZ   string `json:"tz"`
	Days []struct {
		Dow   int    `json:"dow"`
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"days"`
	IsDefault bool `json:"is_default"`
	CanEdit   bool `json:"can_edit"`
}

type calAnswer struct {
	TZ           string      `json:"tz"`
	From         string      `json:"from"`
	To           string      `json:"to"`
	Today        string      `json:"today"`
	GeneratedAt  string      `json:"generated_at"`
	FreeIncluded bool        `json:"free_included"`
	Target       calTarget   `json:"target"`
	People       []calPerson `json:"people"`
	Coverage     map[string]struct {
		Days map[string]*calCoverageDay `json:"days"`
	} `json:"coverage"`
}

func (a calAnswer) person(key string) calPerson {
	for _, p := range a.People {
		if p.Key == key {
			return p
		}
	}
	return calPerson{}
}

func (a calAnswer) keys() []string {
	var out []string
	for _, p := range a.People {
		out = append(out, p.Key)
	}
	return out
}

func (f *teamFixture) calendarReq(key, query string) *httptest.ResponseRecorder {
	return f.call(f.h.GetTeamCalendar, http.MethodGet, "/v1/team/calendar?"+query, "", key)
}

func (f *teamFixture) mustCalendar(key, query string) (calAnswer, string) {
	f.t.Helper()
	rec := f.calendarReq(key, query)
	mustStatus(f.t, rec, http.StatusOK, "GET team calendar "+query)
	var out calAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		f.t.Fatalf("decode: %v - %s", err, rec.Body.String())
	}
	return out, rec.Body.String()
}

func (f *teamFixture) putTarget(key, body string) *httptest.ResponseRecorder {
	return f.call(f.h.PutTeamCoverageTarget, http.MethodPut, "/v1/team/coverage-target", body, key)
}

func (f *teamFixture) getTarget(key string) *httptest.ResponseRecorder {
	return f.call(f.h.GetTeamCoverageTarget, http.MethodGet, "/v1/team/coverage-target", "", key)
}

func utcDay(n int) string { return time.Now().UTC().AddDate(0, 0, n).Format("2006-01-02") }

func TestTeamCalendar_permissionsAndValidation(t *testing.T) {
	a := newAvailTeam(t)
	q := "from=" + utcDay(0) + "&to=" + utcDay(30) + "&tz=UTC&free=0"
	for _, c := range []struct {
		who  string
		key  string
		want int
	}{
		{"owner", a.ownerKey, http.StatusOK},
		{"soporte", a.s1Key, http.StatusOK},
		{"admin without área", a.admKey, http.StatusForbidden},
		{"mentor", a.m1Key, http.StatusForbidden},
		{"member without área", a.plainKey, http.StatusForbidden},
	} {
		if rec := a.calendarReq(c.key, q); rec.Code != c.want {
			t.Errorf("%s: status %d; want %d - %s", c.who, rec.Code, c.want, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/team/calendar?"+q, nil)
	rec := httptest.NewRecorder()
	a.h.RequireAuth(a.h.GetTeamCalendar)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: status %d; want 401", rec.Code)
	}

	for q, msg := range map[string]string{
		"from=" + utcDay(0) + "&to=" + utcDay(31) + "&tz=UTC":         "El rango máximo es de 31 días.",
		"from=" + utcDay(5) + "&to=" + utcDay(4) + "&tz=UTC":          "",
		"from=2026-13-01&to=" + utcDay(3) + "&tz=UTC":                 "",
		"from=" + utcDay(1) + "&to=" + utcDay(3) + "&tz=Mars/Olympus": "",
		"from=" + utcDay(1) + "&to=" + utcDay(3) + "&tz=Local":        "",
		"from=" + utcDay(1) + "&to=" + utcDay(3) + "&area=ventas":     "",
		"from=" + utcDay(1) + "&to=" + utcDay(3) + "&free=2":          "",
		"from=" + utcDay(360) + "&to=" + utcDay(367) + "&tz=UTC":      "No se puede consultar más allá de un año.",
	} {
		rec := a.calendarReq(a.ownerKey, q)
		if rec.Code != http.StatusBadRequest || (msg != "" && !strings.Contains(rec.Body.String(), msg)) {
			t.Errorf("%s: status %d - %s; want 400 %q", q, rec.Code, rec.Body.String(), msg)
		}
	}
	// A whole month (31 days) and a year ahead are fine; a long way back is cheap and fine.
	mustStatus(t, a.calendarReq(a.ownerKey, "from="+utcDay(336)+"&to="+utcDay(366)+"&tz=UTC&free=0"), http.StatusOK, "up to a year ahead")
	mustStatus(t, a.calendarReq(a.ownerKey, "from="+utcDay(-400)+"&to="+utcDay(-370)+"&tz=UTC"), http.StatusOK, "a past month")
	got, _ := a.mustCalendar(a.ownerKey, "")
	if got.TZ != "UTC" || got.From != utcDay(0) || got.To != utcDay(6) || got.Today != utcDay(0) {
		t.Errorf("defaults: tz %q from %q to %q today %q", got.TZ, got.From, got.To, got.Today)
	}
}

// Free starts are the public page's, minute for minute, in the viewer's zone; people,
// links, colours and the booking window ride along.
func TestTeamCalendar_freeMatchesPublicPages(t *testing.T) {
	a := newAvailTeam(t)
	mustExec(t, a.db, `UPDATE users SET booking_accent = '#FF0000' WHERE id = 'm1'`)
	mustExec(t, a.db, `UPDATE event_types SET min_notice_minutes = 120 WHERE id = ?`, a.tID)
	from, to := availRange()
	tz := "America/Lima"
	got, _ := a.mustCalendar(a.ownerKey, "from="+from+"&to="+to+"&tz="+tz)
	if !got.FreeIncluded || got.GeneratedAt == "" || got.TZ != tz {
		t.Errorf("header = %+v", got)
	}
	want := []string{"mentoria:m1", "mentoria:" + a.ownerID, "mentoria:m2", "soporte:s1", "soporte:" + a.ownerID}
	if !slices.Equal(got.keys(), want) {
		t.Fatalf("people = %v; want %v", got.keys(), want)
	}
	colors := map[string]string{}
	for _, p := range got.People {
		if p.Error {
			t.Errorf("%s: error %s", p.Key, p.ErrorKind)
		}
		pub := a.publicSlots(p.Link.Slug, from, to, tz)
		if len(pub) == 0 {
			t.Fatalf("%s: the public page offers nothing; the fixture is wrong", p.Key)
		}
		wantFree := map[string][]int{}
		for _, s := range pub {
			hh, _ := strconv.Atoi(s.Start[11:13])
			mm, _ := strconv.Atoi(s.Start[14:16])
			wantFree[s.Start[:10]] = append(wantFree[s.Start[:10]], hh*60+mm)
		}
		gotFree := map[string][]int{}
		for day, d := range p.Days {
			if len(d.Free) > 0 {
				gotFree[day] = d.Free
				if len(d.Hours) == 0 {
					t.Errorf("%s %s: free starts but no working hours", p.Key, day)
				}
				for _, m := range d.Free { // every free start lies inside the hours
					inside := false
					for _, h := range d.Hours {
						inside = inside || (m >= h[0] && m+p.DurationMin <= h[1])
					}
					if !inside {
						t.Errorf("%s %s: free start %d outside hours %v", p.Key, day, m, d.Hours)
					}
				}
			}
		}
		if len(gotFree) != len(wantFree) {
			t.Errorf("%s: free days %d; public %d", p.Key, len(gotFree), len(wantFree))
		}
		for day, w := range wantFree {
			if !slices.Equal(gotFree[day], w) {
				t.Errorf("%s %s: free %v; public %v", p.Key, day, gotFree[day], w)
			}
		}
		if p.Link.URL == "" || p.DurationMin != 30 || p.IsYou != (p.UserID == a.ownerID) || p.IsOwner != (p.UserID == a.ownerID) {
			t.Errorf("%s: link %+v duration %d is_you %v is_owner %v", p.Key, p.Link, p.DurationMin, p.IsYou, p.IsOwner)
		}
		bu, err := time.Parse(time.RFC3339, p.BookableUntil)
		if err != nil || bu.Sub(time.Now()) < 364*24*time.Hour || !strings.HasSuffix(p.BookableUntil, "-05:00") {
			t.Errorf("%s: bookable_until %q; want about a year ahead, on the viewer's clock", p.Key, p.BookableUntil)
		}
		if prev, ok := colors[p.UserID]; ok && prev != p.Color {
			t.Errorf("%s: colour %s differs from the same person's other row %s", p.Key, p.Color, prev)
		}
		colors[p.UserID] = p.Color
	}
	if o := got.person("mentoria:" + a.ownerID); o.Color != "#4fd7ff" || o.ColorCustom {
		t.Errorf("owner colour %q custom %v; want turquoise", o.Color, o.ColorCustom)
	}
	if m1 := got.person("mentoria:m1"); m1.Color != "#ff0000" || !m1.ColorCustom {
		t.Errorf("m1 colour %q custom %v; want its own #ff0000", m1.Color, m1.ColorCustom)
	}
	if colors["m2"] == colors["s1"] || colors["m2"] == "" || got.person("mentoria:m2").ColorCustom {
		t.Errorf("palette colours m2 %q s1 %q; want two distinct defaults", colors["m2"], colors["s1"])
	}
	// The notice window, on the viewer's clock: about now + 120 min for T (its owner's row).
	nu, err := time.Parse(time.RFC3339, got.person("mentoria:"+a.ownerID).NoticeUntil)
	if err != nil || nu.Sub(time.Now()) < 115*time.Minute || nu.Sub(time.Now()) > 125*time.Minute {
		t.Errorf("notice_until = %q; want now + 2 h", got.person("mentoria:"+a.ownerID).NoticeUntil)
	}
	// Filtering an área never repaints anybody.
	sup, _ := a.mustCalendar(a.s1Key, "from="+from+"&to="+to+"&tz="+tz+"&area=soporte")
	if !slices.Equal(sup.keys(), []string{"soporte:s1", "soporte:" + a.ownerID}) {
		t.Errorf("area=soporte people = %v", sup.keys())
	}
	for _, p := range sup.People {
		if p.Color != colors[p.UserID] || p.IsYou != (p.UserID == "s1") {
			t.Errorf("area=soporte %s: colour %s (was %s) is_you %v", p.Key, p.Color, colors[p.UserID], p.IsYou)
		}
	}
	if _, ok := sup.Coverage["mentoria"]; ok || sup.Target.CanEdit {
		t.Errorf("area=soporte: coverage %v can_edit %v; want only soporte, not editable by support", sup.Coverage, sup.Target.CanEdit)
	}
}

// Sessions: every row of the person carries all of them, under opaque keys, with nothing
// that identifies the booking or the client; past days show sessions only.
func TestTeamCalendar_sessions(t *testing.T) {
	a := newAvailTeam(t)
	ins := func(id, etID, host, start, end, status string) {
		mustExec(t, a.db, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES (?,?,?,?,?,?)`, id, etID, host, start, end, status)
		mustExec(t, a.db, `INSERT INTO booking_hosts (id, booking_id, user_id, is_primary) VALUES (?,?,?,1)`, uid.New(), id, host)
	}
	at := func(day string, hhmm string) string { return day + "T" + hhmm + ":00Z" }
	m1Copy := a.copyOf(a.tID, "m1").id
	mustExec(t, a.db, `INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et-other','m1','cita-juan','Cita con Juan Pérez',60)`)
	ins("bk-secret-5", m1Copy, "m1", at(utcDay(-1), "10:00"), at(utcDay(-1), "11:00"), "confirmed")
	ins("bk-secret-1", m1Copy, "m1", at(utcDay(2), "10:00"), at(utcDay(2), "10:30"), "confirmed")
	ins("bk-secret-2", a.sID, a.ownerID, at(utcDay(2), "16:00"), at(utcDay(2), "16:30"), "confirmed")
	ins("bk-secret-3", "et-other", "m1", at(utcDay(2), "23:30"), at(utcDay(3), "00:30"), "confirmed")
	ins("bk-secret-4", m1Copy, "m1", at(utcDay(2), "12:00"), at(utcDay(2), "12:30"), "cancelled")

	got, body := a.mustCalendar(a.ownerKey, "from="+utcDay(-1)+"&to="+utcDay(3)+"&tz=UTC")
	for _, leak := range []string{"bk-secret", "Juan", "et-other", "cita-juan"} {
		if strings.Contains(body, leak) {
			t.Errorf("the answer contains %q", leak)
		}
	}
	m1 := got.person("mentoria:m1")
	busy := func(p calPerson, day string) []calBusy {
		if d := p.Days[day]; d != nil {
			return d.Busy
		}
		return nil
	}
	if b := busy(m1, utcDay(-1)); len(b) != 1 || b[0] != (calBusy{Key: "b1", Start: 600, End: 660, Type: "Mentoría privada", Area: "mentoria"}) {
		t.Errorf("m1 yesterday = %+v", b)
	}
	if d := m1.Days[utcDay(-1)]; d != nil && (len(d.Hours) > 0 || len(d.Free) > 0) {
		t.Errorf("past day carries hours %v / free %v; want sessions only", d.Hours, d.Free)
	}
	if d := m1.Days[utcDay(0)]; d == nil || len(d.Hours) == 0 {
		t.Errorf("today has no hours: %+v", d)
	}
	wantDay2 := []calBusy{
		{Key: "b2", Start: 600, End: 630, Type: "Mentoría privada", Area: "mentoria"},
		{Key: "b4", Start: 1410, End: 1440, Type: "Otra reunión", Area: ""},
	}
	if b := busy(m1, utcDay(2)); !slices.Equal(b, wantDay2) {
		t.Errorf("m1 day+2 = %+v; want %+v (cancelled left out)", b, wantDay2)
	}
	if b := busy(m1, utcDay(3)); len(b) != 1 || b[0] != (calBusy{Key: "b4", Start: 0, End: 30, Type: "Otra reunión", Area: ""}) {
		t.Errorf("m1 day+3 = %+v; want the rest of the session crossing midnight, same key", b)
	}
	// The owner's Soporte session shows on both of the owner's rows.
	wantOwner := []calBusy{{Key: "b3", Start: 960, End: 990, Type: "Soporte 1 a 1", Area: "soporte"}}
	for _, k := range []string{"mentoria:" + a.ownerID, "soporte:" + a.ownerID} {
		if b := busy(got.person(k), utcDay(2)); !slices.Equal(b, wantOwner) {
			t.Errorf("%s day+2 = %+v; want %+v", k, b, wantOwner)
		}
	}
	// Coverage and hours only from today on.
	for a2, c := range got.Coverage {
		if _, ok := c.Days[utcDay(-1)]; ok {
			t.Errorf("coverage %s has the past day", a2)
		}
	}
}

// "Sin cubrir" = the target minus everybody's hours; "owner_only" = covered by the owner
// alone; a window shorter than one session covers nothing.
func TestTeamCalendar_coverage(t *testing.T) {
	a := newAvailTeam(t)
	mustExec(t, a.db, `UPDATE users SET iana_timezone = 'UTC' WHERE id IN ('m1','m2','s1')`)
	mustExec(t, a.db, `DELETE FROM availability_rules WHERE user_id = ?`, a.ownerID)
	addWeeklyRule(t, a.teamFixture, a.ownerID, "11:00", "15:00")
	body := `{"tz":"UTC","days":[` + strings.Join([]string{
		`{"dow":0,"start":"08:00","end":"20:00"}`, `{"dow":1,"start":"08:00","end":"20:00"}`,
		`{"dow":2,"start":"08:00","end":"20:00"}`, `{"dow":3,"start":"08:00","end":"20:00"}`,
		`{"dow":4,"start":"08:00","end":"20:00"}`, `{"dow":5,"start":"08:00","end":"20:00"}`,
		`{"dow":6,"start":"08:00","end":"20:00"}`}, ",") + `]}`
	mustStatus(t, a.putTarget(a.ownerKey, body), http.StatusOK, "PUT target")
	// m2 works only 19:00-19:20 on day+3: shorter than a 30-minute session.
	mustExec(t, a.db, `INSERT INTO availability_overrides (id, user_id, date, is_available, start_time, end_time) VALUES (?, 'm2', ?, 1, '19:00', '19:20')`, uid.New(), utcDay(3))

	got, _ := a.mustCalendar(a.ownerKey, "from="+utcDay(2)+"&to="+utcDay(3)+"&tz=UTC&free=0")
	if got.FreeIncluded || got.Target.TZ != "UTC" || got.Target.IsDefault || !got.Target.CanEdit {
		t.Errorf("header: free_included %v target %+v", got.FreeIncluded, got.Target)
	}
	d2, d3 := utcDay(2), utcDay(3)
	if h := got.person("mentoria:m1").Days[d2]; h == nil || !slices.Equal(h.Hours, []calSpan{{540, 720}}) || len(h.Free) != 0 {
		t.Errorf("m1 hours day+2 = %+v; want [[540 720]] and no free with free=0", h)
	}
	if h := got.person("mentoria:m2").Days[d3]; h != nil && len(h.Hours) > 0 {
		t.Errorf("m2 hours day+3 = %v; want none (window shorter than a session)", h.Hours)
	}
	check := func(area, day string, uncovered, ownerOnly []calSpan) {
		t.Helper()
		c := got.Coverage[area].Days[day]
		if c == nil {
			t.Fatalf("coverage %s %s missing: %+v", area, day, got.Coverage[area])
		}
		if !slices.Equal(c.Target, []calSpan{{480, 1200}}) || !slices.Equal(c.Uncovered, uncovered) || !slices.Equal(c.OwnerOnly, ownerOnly) {
			t.Errorf("coverage %s %s = target %v uncovered %v owner_only %v; want uncovered %v owner_only %v",
				area, day, c.Target, c.Uncovered, c.OwnerOnly, uncovered, ownerOnly)
		}
	}
	// Mentoría: m1 09-12, m2 14-18, owner 11-15 -> covered 09-18, owner alone 12-14.
	check("mentoria", d2, []calSpan{{480, 540}, {1080, 1200}}, []calSpan{{720, 840}})
	// Without m2 (day+3): covered 09-15, owner alone 12-15.
	check("mentoria", d3, []calSpan{{480, 540}, {900, 1200}}, []calSpan{{720, 900}})
	// Soporte: s1 10-13, owner 11-15 -> covered 10-15, owner alone 13-15.
	check("soporte", d2, []calSpan{{480, 600}, {900, 1200}}, []calSpan{{780, 900}})

	// Nobody but the owner hosts Mentoría any more: everything the owner does not work is
	// uncovered, and the rest is the owner's alone.
	a.mustRole("m1", "member", "")
	a.mustRole("m2", "member", "")
	got, _ = a.mustCalendar(a.ownerKey, "from="+d2+"&to="+d2+"&tz=UTC&free=0&fresh=1")
	check("mentoria", d2, []calSpan{{480, 660}, {900, 1200}}, []calSpan{{660, 900}})

	// An empty target marks nothing.
	mustStatus(t, a.putTarget(a.ownerKey, `{"tz":"UTC","days":[]}`), http.StatusOK, "PUT empty target")
	got, _ = a.mustCalendar(a.ownerKey, "from="+d2+"&to="+d2+"&tz=UTC&free=0")
	if c, ok := got.Coverage["mentoria"]; !ok || len(c.Days) != 0 {
		t.Errorf("empty target: coverage %+v; want the área with no days", got.Coverage)
	}
}

// A provider that hangs costs only that person their free starts (hours and sessions
// stay); free=0 never calls a provider; and the budget counts from the request.
func TestTeamCalendar_slowProvidersAndFreeZero(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&fresh=1"

	restore := handler.SetTeamCalendarLimitsForTest(5*time.Second, 10*time.Second)
	svc := calendar.NewService(a.db)
	svc.Register(slowCalendar{slow: map[string]bool{"m1": true, "m2": true, "s1": true, a.ownerID: true}})
	a.h.SetCalendar(svc)
	start := time.Now()
	got, _ := a.mustCalendar(a.ownerKey, q+"&free=0")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("free=0 took %v with every calendar hanging; it must not call them", d)
	}
	for _, p := range got.People {
		if p.Error || !hasHours(p) {
			t.Errorf("free=0 %s: error %v (%s) hours %v", p.Key, p.Error, p.ErrorKind, hasHours(p))
		}
	}
	restore()

	restore = handler.SetTeamCalendarLimitsForTest(150*time.Millisecond, 5*time.Second)
	defer restore()
	svc = calendar.NewService(a.db)
	svc.Register(slowCalendar{slow: map[string]bool{"m1": true}})
	a.h.SetCalendar(svc)
	start = time.Now()
	got, _ = a.mustCalendar(a.ownerKey, q)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("answer took %v with one slow calendar", d)
	}
	for _, p := range got.People {
		switch {
		case p.UserID == "m1" && (!p.Error || p.ErrorKind != "calendar" || hasFree(p) || !hasHours(p)):
			t.Errorf("m1 (slow) = error %v kind %q free %v hours %v; want calendar, no free, hours kept", p.Error, p.ErrorKind, hasFree(p), hasHours(p))
		case p.UserID != "m1" && (p.Error || !hasFree(p)):
			t.Errorf("%s: error %v (%s) free %v; want its starts", p.Key, p.Error, p.ErrorKind, hasFree(p))
		}
	}

	// A budget already spent when the request arrives: everybody is "timeout", with links,
	// hours and coverage still there (they come from the DB).
	restore()
	restore = handler.SetTeamCalendarLimitsForTest(time.Second, time.Nanosecond)
	got, _ = a.mustCalendar(a.ownerKey, q)
	if len(got.People) != 5 {
		t.Fatalf("people = %v", got.keys())
	}
	for _, p := range got.People {
		if !p.Error || p.ErrorKind != "timeout" || p.Link.Slug == "" || !hasHours(p) || hasFree(p) {
			t.Errorf("%s after the budget = error %v kind %q link %q hours %v free %v", p.Key, p.Error, p.ErrorKind, p.Link.Slug, hasHours(p), hasFree(p))
		}
	}
	if len(got.Coverage["mentoria"].Days) == 0 {
		t.Error("coverage missing when the budget ran out; it must not wait for providers")
	}
}

func hasHours(p calPerson) bool {
	for _, d := range p.Days {
		if len(d.Hours) > 0 {
			return true
		}
	}
	return false
}

func hasFree(p calPerson) bool {
	for _, d := range p.Days {
		if len(d.Free) > 0 {
			return true
		}
	}
	return false
}

// trackingCalendar records how many free/busy calls run at once, overall and per user.
type trackingCalendar struct {
	calendar.Provider
	mu         *sync.Mutex
	active     map[string]int
	maxPerUser map[string]int
	calls      map[string]int
	cur, max   *int
}

func (trackingCalendar) Name() string { return "google" }
func (c trackingCalendar) FreeBusy(ctx context.Context, userID string, _, _ time.Time) ([]slots.Interval, error) {
	c.mu.Lock()
	c.active[userID]++
	c.calls[userID]++
	c.maxPerUser[userID] = max(c.maxPerUser[userID], c.active[userID])
	*c.cur++
	*c.max = max(*c.max, *c.cur)
	c.mu.Unlock()
	select {
	case <-time.After(80 * time.Millisecond):
	case <-ctx.Done():
	}
	c.mu.Lock()
	c.active[userID]--
	*c.cur--
	c.mu.Unlock()
	return nil, nil
}

// Providers run in parallel across people, never twice at once for one user (the owner's
// two rows share one token source).
func TestTeamCalendar_oneUserNeverTwiceAtOnce(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	var cur, mx int
	tc := trackingCalendar{mu: &sync.Mutex{}, active: map[string]int{}, maxPerUser: map[string]int{}, calls: map[string]int{}, cur: &cur, max: &mx}
	svc := calendar.NewService(a.db)
	svc.Register(tc)
	a.h.SetCalendar(svc)
	got, _ := a.mustCalendar(a.ownerKey, "from="+from+"&to="+to+"&tz=UTC&fresh=1")
	for _, p := range got.People {
		if p.Error {
			t.Errorf("%s: error %s", p.Key, p.ErrorKind)
		}
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.calls[a.ownerID] != 2 || tc.maxPerUser[a.ownerID] != 1 {
		t.Errorf("owner: %d calls, %d at once; want 2 calls one after the other", tc.calls[a.ownerID], tc.maxPerUser[a.ownerID])
	}
	if mx < 2 || mx > 3 {
		t.Errorf("calls at once = %d; want parallel people on at most 3 workers", mx)
	}
}

// A rule the engine cannot read costs that person their row's hours and free starts (and
// their part of the coverage); a paused link is left out.
func TestTeamCalendar_brokenRuleAndPausedLink(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&fresh=1"
	mustExec(t, a.db, `INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time) VALUES (?, 'm2', 1, '25:00', '26:00')`, uid.New())
	got, _ := a.mustCalendar(a.ownerKey, q)
	m2 := got.person("mentoria:m2")
	if !m2.Error || m2.ErrorKind != "internal" || hasHours(m2) || hasFree(m2) {
		t.Errorf("m2 with a broken rule = error %v kind %q hours %v free %v", m2.Error, m2.ErrorKind, hasHours(m2), hasFree(m2))
	}
	for _, k := range []string{"mentoria:m1", "mentoria:" + a.ownerID, "soporte:s1", "soporte:" + a.ownerID} {
		if p := got.person(k); p.Error || !hasFree(p) {
			t.Errorf("%s: error %v (%s) free %v; want its starts despite m2", k, p.Error, p.ErrorKind, hasFree(p))
		}
	}
	mustStatus(t, a.patchET(a.tSlug, `{"is_active":false}`), http.StatusOK, "pause T")
	got, _ = a.mustCalendar(a.ownerKey, q)
	if slices.Contains(got.keys(), "mentoria:"+a.ownerID) {
		t.Errorf("people with T paused = %v; the owner's Mentoría row must go", got.keys())
	}
}

func TestTeamCoverageTarget_endpoints(t *testing.T) {
	a := newAvailTeam(t)
	var tg calTarget
	rec := a.getTarget(a.ownerKey)
	mustStatus(t, rec, http.StatusOK, "GET target")
	json.Unmarshal(rec.Body.Bytes(), &tg)
	if !tg.IsDefault || !tg.CanEdit || tg.TZ != "America/Lima" || len(tg.Days) != 6 || tg.Days[0].Dow != 1 || tg.Days[0].Start != "08:00" || tg.Days[5].End != "20:00" {
		t.Errorf("default target = %+v", tg)
	}
	rec = a.getTarget(a.s1Key)
	mustStatus(t, rec, http.StatusOK, "GET target as soporte")
	json.Unmarshal(rec.Body.Bytes(), &tg)
	if tg.CanEdit {
		t.Error("support person may edit the target")
	}
	if rec := a.getTarget(a.m1Key); rec.Code != http.StatusForbidden {
		t.Errorf("mentor GET: %d; want 403", rec.Code)
	}
	ok := `{"tz":"America/Bogota","days":[{"dow":6,"start":"09:30","end":"24:00"},{"dow":1,"start":"07:00","end":"13:00"}]}`
	for who, key := range map[string]string{"soporte": a.s1Key, "admin": a.admKey, "mentor": a.m1Key} {
		if rec := a.putTarget(key, ok); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Solo el propietario cambia el horario a cubrir.") {
			t.Errorf("%s PUT: %d %s; want 403", who, rec.Code, rec.Body.String())
		}
	}
	for _, bad := range []string{
		`{"tz":"Mars/Olympus","days":[]}`,
		`{"tz":"UTC"}`,
		`{"days":[]}`,
		`{"tz":"UTC","days":[{"dow":1,"start":"08:15","end":"10:00"}]}`,
		`{"tz":"UTC","days":[{"dow":1,"start":"10:00","end":"09:00"}]}`,
		`{"tz":"UTC","days":[{"dow":1,"start":"08:00","end":"10:00"},{"dow":1,"start":"12:00","end":"14:00"}]}`,
		`{"tz":"UTC","days":[{"dow":9,"start":"08:00","end":"10:00"}]}`,
		`not json`,
	} {
		if rec := a.putTarget(a.ownerKey, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: %d %s; want 400", bad, rec.Code, rec.Body.String())
		}
	}
	mustStatus(t, a.putTarget(a.ownerKey, ok), http.StatusOK, "PUT target")
	rec = a.getTarget(a.ownerKey)
	tg = calTarget{}
	json.Unmarshal(rec.Body.Bytes(), &tg)
	if tg.IsDefault || tg.TZ != "America/Bogota" || len(tg.Days) != 2 || tg.Days[0].Dow != 1 || tg.Days[1].End != "24:00" {
		t.Errorf("saved target = %+v", tg)
	}
	// A stored value that stopped being valid is ignored: the default again.
	mustExec(t, a.db, `UPDATE fork_settings SET value = '{"v":1,"tz":"Mars/Olympus","days":[]}' WHERE key = 'team_coverage_target'`)
	rec = a.getTarget(a.ownerKey)
	tg = calTarget{}
	json.Unmarshal(rec.Body.Bytes(), &tg)
	if !tg.IsDefault || tg.TZ != "America/Lima" {
		t.Errorf("invalid stored target = %+v; want the default", tg)
	}
}

// Within the TTL one answer serves every viewer (is_you and can_edit follow the caller);
// fresh=1 recomputes; a saved target is visible at once.
func TestTeamCalendar_cacheAndViewerFields(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&area=mentoria&free=0"
	first, _ := a.mustCalendar(a.ownerKey, q)
	a.mustRole("m2", "member", "") // through the handler only: the generation does not move
	cached, _ := a.mustCalendar(a.s1Key, q)
	if !slices.Equal(cached.keys(), first.keys()) || cached.GeneratedAt != first.GeneratedAt {
		t.Errorf("cached answer changed: %v vs %v", cached.keys(), first.keys())
	}
	if cached.Target.CanEdit {
		t.Error("cached answer lets the support person edit the target")
	}
	for _, p := range cached.People {
		if p.IsYou {
			t.Errorf("cached answer for the support person marks %s as is_you", p.Key)
		}
	}
	again, _ := a.mustCalendar(a.ownerKey, q)
	if !again.Target.CanEdit || !again.person("mentoria:"+a.ownerID).IsYou {
		t.Error("the owner lost can_edit / is_you on the cached answer")
	}
	fresh, _ := a.mustCalendar(a.ownerKey, q+"&fresh=1")
	if slices.Contains(fresh.keys(), "mentoria:m2") {
		t.Errorf("fresh answer still lists m2: %v", fresh.keys())
	}
	mustStatus(t, a.putTarget(a.ownerKey, `{"tz":"UTC","days":[]}`), http.StatusOK, "PUT target")
	after, _ := a.mustCalendar(a.ownerKey, q)
	if after.Target.TZ != "UTC" || len(after.Target.Days) != 0 {
		t.Errorf("target after PUT = %+v; want the saved one at once", after.Target)
	}
}

// A failed read of the target answers 500 on both endpoints (never the default, never cached).
func TestTeamCoverageTarget_readErrorIs500(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	mustExec(t, a.db, `ALTER TABLE fork_settings RENAME TO fork_settings_gone`)
	if rec := a.getTarget(a.ownerKey); rec.Code != http.StatusInternalServerError {
		t.Errorf("GET target with the settings unreadable: %d %s; want 500", rec.Code, rec.Body.String())
	}
	if rec := a.calendarReq(a.ownerKey, "from="+from+"&to="+to+"&tz=UTC&free=0&fresh=1"); rec.Code != http.StatusInternalServerError {
		t.Errorf("GET calendar with the settings unreadable: %d; want 500", rec.Code)
	}
	mustExec(t, a.db, `ALTER TABLE fork_settings_gone RENAME TO fork_settings`)
	got, _ := a.mustCalendar(a.ownerKey, "from="+from+"&to="+to+"&tz=UTC&free=0")
	if len(got.People) == 0 || !got.Target.IsDefault {
		t.Errorf("after the table is back: %d people, target %+v", len(got.People), got.Target)
	}
}
