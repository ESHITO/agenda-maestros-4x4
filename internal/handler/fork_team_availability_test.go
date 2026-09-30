package handler_test

// Fork (Agenda Maestros 4x4): GET /v1/team/availability (fork_team_availability.go) - the
// Panel's "Disponibilidad del equipo". Every person's times must be exactly what their
// public link offers (computeSlots through GET /slots), so the owner never hands out a
// link for a time the client cannot book.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/slots"
	"github.com/calnode/calnode/internal/uid"
)

type availSlot struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type availPerson struct {
	UserID    string  `json:"user_id"`
	Name      string  `json:"name"`
	Area      string  `json:"area"`
	AvatarURL string  `json:"avatar_url"`
	Color     *string `json:"color"`
	IsYou     bool    `json:"is_you"`
	Link      struct {
		Slug string `json:"slug"`
		URL  string `json:"url"`
	} `json:"link"`
	Slots     []availSlot `json:"slots"`
	Error     bool        `json:"error"`
	ErrorKind string      `json:"error_kind"`
}

type availAnswer struct {
	TZ          string        `json:"tz"`
	From        string        `json:"from"`
	To          string        `json:"to"`
	GeneratedAt string        `json:"generated_at"`
	People      []availPerson `json:"people"`
}

func (f *teamFixture) availability(key, query string) *httptest.ResponseRecorder {
	return f.call(f.h.GetTeamAvailability, http.MethodGet, "/v1/team/availability?"+query, "", key)
}

func (f *teamFixture) mustAvailability(key, query string) availAnswer {
	f.t.Helper()
	rec := f.availability(key, query)
	mustStatus(f.t, rec, http.StatusOK, "GET team availability "+query)
	var out availAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		f.t.Fatalf("decode: %v - %s", err, rec.Body.String())
	}
	return out
}

// publicSlots is GET /v1/event-types/{slug}/slots, the booking page's own source.
func (f *teamFixture) publicSlots(slug, from, to, tz string) []availSlot {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/slots?from="+from+"&to="+to+"&tz="+tz, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	f.h.GetSlots(rec, req)
	mustStatus(f.t, rec, http.StatusOK, "public slots "+slug)
	var resp struct {
		Slots []availSlot `json:"slots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		f.t.Fatal(err)
	}
	return resp.Slots
}

// addWeeklyRule gives userID a global weekly rule on every day.
func addWeeklyRule(t *testing.T, f *teamFixture, userID, start, end string) {
	t.Helper()
	for day := 0; day < 7; day++ {
		mustExec(t, f.db, `INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time) VALUES (?, ?, ?, ?, ?)`,
			uid.New(), userID, day, start, end)
	}
}

// availTeam: T and S set; m1 and m2 mentors (m1 09:00-12:00 Lima, m2 14:00-18:00 Madrid),
// s1 support (10:00-13:00 Bogotá), adm an admin with no área, plain a member with none.
type availTeam struct {
	*teamFixture
	m1Key, m2Key, s1Key, admKey, plainKey string
}

func newAvailTeam(t *testing.T) *availTeam {
	t.Helper()
	f := newTeamFixture(t)
	a := &availTeam{teamFixture: f}
	a.m1Key = addMember(t, f.db, "m1", "America/Lima")
	a.m2Key = addMember(t, f.db, "m2", "Europe/Madrid")
	a.s1Key = addMember(t, f.db, "s1", "America/Bogota")
	a.plainKey = addMember(t, f.db, "plain", "UTC")
	a.admKey = supAddAdmin(t, f, "adm")
	mustExec(t, f.db, `UPDATE users SET name = 'Zoe Mentora' WHERE id = 'm2'`)
	addWeeklyRule(t, f, "m1", "09:00", "12:00")
	addWeeklyRule(t, f, "m2", "14:00", "18:00")
	addWeeklyRule(t, f, "s1", "10:00", "13:00")
	f.mustRole("m1", "member", "mentoria")
	f.mustRole("m2", "member", "mentoria")
	f.mustRole("s1", "member", "soporte")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_template_id":"` + f.sID + `"}`)
	return a
}

// availRange is a 7-day window starting in two days (clear of the minimum notice).
func availRange() (string, string) {
	now := time.Now().UTC()
	return now.AddDate(0, 0, 2).Format("2006-01-02"), now.AddDate(0, 0, 8).Format("2006-01-02")
}

func personKeys(ps []availPerson) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Area+":"+p.UserID)
	}
	return out
}

func TestTeamAvailability_permissions(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=America/Lima"
	for _, c := range []struct {
		who  string
		key  string
		want int
	}{
		{"owner", a.ownerKey, http.StatusOK},
		{"admin without área", a.admKey, http.StatusForbidden},
		{"soporte", a.s1Key, http.StatusOK},
		{"mentor", a.m1Key, http.StatusForbidden},
		{"member without área", a.plainKey, http.StatusForbidden},
	} {
		if rec := a.availability(c.key, q); rec.Code != c.want {
			t.Errorf("%s: status %d; want %d - %s", c.who, rec.Code, c.want, rec.Body.String())
		}
	}
	// An admin who also attends Soporte sees it.
	a.mustRole("adm", "admin", "soporte")
	if rec := a.availability(a.admKey, q); rec.Code != http.StatusOK {
		t.Errorf("admin with área soporte: status %d; want 200 - %s", rec.Code, rec.Body.String())
	}
	// No key at all: RequireAuth's 401.
	req := httptest.NewRequest(http.MethodGet, "/v1/team/availability?"+q, nil)
	rec := httptest.NewRecorder()
	a.h.RequireAuth(a.h.GetTeamAvailability)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: status %d; want 401", rec.Code)
	}
}

func TestTeamAvailability_validation(t *testing.T) {
	a := newAvailTeam(t)
	today := time.Now().UTC()
	d := func(n int) string { return today.AddDate(0, 0, n).Format("2006-01-02") }
	for _, q := range []string{
		"from=" + d(1) + "&to=" + d(8) + "&tz=UTC",  // 8 days
		"from=" + d(1) + "&to=" + d(40) + "&tz=UTC", // far beyond
		"from=" + d(5) + "&to=" + d(4) + "&tz=UTC",  // backwards
		"from=2026-13-01&to=" + d(3) + "&tz=UTC",
		"from=" + d(1) + "&to=mañana&tz=UTC",
		"from=" + d(1) + "&to=" + d(3) + "&tz=Mars/Olympus",
		"from=" + d(1) + "&to=" + d(3) + "&tz=Local",
		"from=" + d(1) + "&to=" + d(3) + "&tz=UTC&area=ventas",
	} {
		if rec := a.availability(a.ownerKey, q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d; want 400 - %s", q, rec.Code, rec.Body.String())
		}
	}
	// Exactly 7 days is fine; defaults fill a missing range and zone.
	mustStatus(t, a.availability(a.ownerKey, "from="+d(1)+"&to="+d(7)+"&tz=UTC"), http.StatusOK, "7 days")
	got := a.mustAvailability(a.ownerKey, "")
	if got.TZ != "UTC" || got.From != d(0) || got.To != d(6) {
		t.Errorf("defaults: tz %q from %q to %q; want the owner's UTC and today..today+6", got.TZ, got.From, got.To)
	}
}

func TestTeamAvailability_slotsMatchPublicPages(t *testing.T) {
	a := newAvailTeam(t)
	mustExec(t, a.db, `UPDATE users SET booking_accent = '#FF0000' WHERE id = 'm1'`)
	from, to := availRange()
	tz := "America/Lima"
	got := a.mustAvailability(a.ownerKey, "from="+from+"&to="+to+"&tz="+tz+"&area=all")
	if got.TZ != tz || got.From != from || got.To != to || got.GeneratedAt == "" {
		t.Errorf("header = %+v", got)
	}
	// Mentoría first, then by name: "Mentor m1", "Test Host" (T), "Zoe Mentora"; Soporte:
	// "Mentor s1" (named by addMember), "Test Host" (S).
	want := []string{"mentoria:m1", "mentoria:" + a.ownerID, "mentoria:m2", "soporte:s1", "soporte:" + a.ownerID}
	if keys := personKeys(got.People); !slices.Equal(keys, want) {
		t.Fatalf("people = %v; want %v", keys, want)
	}
	links := map[string]string{
		"mentoria:m1": a.copyOf(a.tID, "m1").slug, "mentoria:m2": a.copyOf(a.tID, "m2").slug,
		"soporte:s1": a.copyOf(a.sID, "s1").slug, "mentoria:" + a.ownerID: a.tSlug, "soporte:" + a.ownerID: a.sSlug,
	}
	for _, p := range got.People {
		k := p.Area + ":" + p.UserID
		if p.Link.Slug != links[k] || p.Link.URL == "" || p.Error {
			t.Errorf("%s: link %+v error %v; want slug %q", k, p.Link, p.Error, links[k])
		}
		pub := a.publicSlots(p.Link.Slug, from, to, tz)
		if len(pub) == 0 {
			t.Errorf("%s: the public page offers nothing; the fixture is wrong", k)
		}
		if !slices.Equal(p.Slots, pub) {
			t.Errorf("%s: panel slots (%d) differ from the public page's (%d)\npanel: %v\npublic: %v", k, len(p.Slots), len(pub), p.Slots, pub)
		}
		if p.IsYou != (p.UserID == a.ownerID) {
			t.Errorf("%s: is_you = %v", k, p.IsYou)
		}
		switch {
		case p.UserID == "m1" && (p.Color == nil || *p.Color != "#ff0000"):
			t.Errorf("m1 color = %v; want #ff0000", p.Color)
		case p.UserID != "m1" && p.Color != nil:
			t.Errorf("%s: color = %q; want null (default accent)", k, *p.Color)
		}
	}

	// The área filter, as a support person sees it.
	sup := a.mustAvailability(a.s1Key, "from="+from+"&to="+to+"&tz="+tz+"&area=soporte")
	if keys := personKeys(sup.People); !slices.Equal(keys, []string{"soporte:s1", "soporte:" + a.ownerID}) {
		t.Errorf("area=soporte people = %v", keys)
	}
	for _, p := range sup.People {
		if p.IsYou != (p.UserID == "s1") {
			t.Errorf("area=soporte %s: is_you = %v", p.UserID, p.IsYou)
		}
	}
}

func TestTeamAvailability_inactiveCopiesAndPausedLinksExcluded(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&area=mentoria&fresh=1"
	a.mustRole("m2", "member", "") // m2's copy is deactivated, not deleted
	if c := a.copyOf(a.tID, "m2"); c.id == "" || c.active {
		t.Fatalf("m2's copy = %+v; want it kept and inactive", c)
	}
	mustStatus(t, a.call(a.h.TeamReconcileAfter(a.h.ArchiveUser), http.MethodPost, "/v1/users/m1/archive", "", a.ownerKey, "id", "m1"), http.StatusOK, "archive m1")
	got := a.mustAvailability(a.ownerKey, q)
	if keys := personKeys(got.People); !slices.Equal(keys, []string{"mentoria:" + a.ownerID}) {
		t.Errorf("people = %v; want only the owner with T", keys)
	}
	// A paused template is a link the public page 404s: not handed out either.
	mustStatus(t, a.patchET(a.tSlug, `{"is_active":false}`), http.StatusOK, "pause T")
	got = a.mustAvailability(a.ownerKey, q)
	if len(got.People) != 0 {
		t.Errorf("people with T paused = %v; want none", personKeys(got.People))
	}
}

// userFailingCalendar fails free/busy for one user only.
type userFailingCalendar struct {
	calendar.Provider
	failFor string
}

func (userFailingCalendar) Name() string { return "google" }
func (c userFailingCalendar) FreeBusy(_ context.Context, userID string, _, _ time.Time) ([]slots.Interval, error) {
	if userID == c.failFor {
		return nil, errTestProviderDown
	}
	return nil, nil
}

func TestTeamAvailability_oneFailureIsIsolated(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&fresh=1"

	// m1's calendar provider is down: booking is fail-closed for m1, so no times for m1.
	svc := calendar.NewService(a.db)
	svc.Register(userFailingCalendar{failFor: "m1"})
	a.h.SetCalendar(svc)
	got := a.mustAvailability(a.ownerKey, q)
	byKey := map[string]availPerson{}
	for _, p := range got.People {
		byKey[p.Area+":"+p.UserID] = p
	}
	if p := byKey["mentoria:m1"]; !p.Error || p.ErrorKind != "calendar" || len(p.Slots) != 0 {
		t.Errorf("m1 with its calendar down = error %v kind %q slots %d; want error, calendar, none", p.Error, p.ErrorKind, len(p.Slots))
	}
	for _, k := range []string{"mentoria:m2", "soporte:s1", "mentoria:" + a.ownerID} {
		if p := byKey[k]; p.Error || len(p.Slots) == 0 {
			t.Errorf("%s: error %v slots %d; want its times despite m1's failure", k, p.Error, len(p.Slots))
		}
	}

	// A rule the slot engine rejects fails m2's whole computation (GetSlots would 500):
	// m2 is marked, everyone else still answers.
	a.h.SetCalendar(nil)
	mustExec(t, a.db, `INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time) VALUES (?, 'm2', 1, '25:00', '26:00')`, uid.New())
	got = a.mustAvailability(a.ownerKey, q)
	byKey = map[string]availPerson{}
	for _, p := range got.People {
		byKey[p.Area+":"+p.UserID] = p
	}
	if p := byKey["mentoria:m2"]; !p.Error || p.ErrorKind != "internal" {
		t.Errorf("m2 with a broken rule = error %v kind %q; want error, internal", p.Error, p.ErrorKind)
	}
	for _, k := range []string{"mentoria:m1", "soporte:s1", "mentoria:" + a.ownerID, "soporte:" + a.ownerID} {
		if p := byKey[k]; p.Error || len(p.Slots) == 0 {
			t.Errorf("%s: error %v slots %d; want its times despite m2's failure", k, p.Error, len(p.Slots))
		}
	}
}

func TestTeamAvailability_cache(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&area=mentoria"
	first := a.mustAvailability(a.ownerKey, q)
	a.mustRole("m2", "member", "")
	// Within the TTL the same range is served from the cache (the viewer's is_you still
	// follows the caller)...
	cached := a.mustAvailability(a.s1Key, q)
	if !slices.Equal(personKeys(cached.People), personKeys(first.People)) || cached.GeneratedAt != first.GeneratedAt {
		t.Errorf("cached answer changed: %v vs %v", personKeys(cached.People), personKeys(first.People))
	}
	for _, p := range cached.People {
		if p.IsYou {
			t.Errorf("cached answer for the support person marks %s as is_you", p.UserID)
		}
	}
	// ...and fresh=1 recomputes it.
	fresh := a.mustAvailability(a.ownerKey, q+"&fresh=1")
	if slices.Contains(personKeys(fresh.People), "mentoria:m2") {
		t.Errorf("fresh answer still lists m2: %v", personKeys(fresh.People))
	}
}

// Two Handlers never share a cached answer: every test builds its own Handler over its own
// database with the same range/tz/área, and a package-wide cache keyed by the Handler's
// address served one test another's people once that address was reused.
func TestTeamAvailability_cacheIsPerHandler(t *testing.T) {
	a1 := newAvailTeam(t)
	a2 := newAvailTeam(t)
	if a1.ownerID == a2.ownerID {
		t.Fatalf("both fixtures have owner %s; the test needs two workspaces", a1.ownerID)
	}
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=America/Lima&area=all"
	first := a1.mustAvailability(a1.ownerKey, q) // cached on a1's Handler
	second := a2.mustAvailability(a2.ownerKey, q)
	for _, c := range []struct {
		name  string
		got   availAnswer
		owner string
		other string
	}{{"a1", first, a1.ownerID, a2.ownerID}, {"a2", second, a2.ownerID, a1.ownerID}} {
		keys := personKeys(c.got.People)
		if !slices.Contains(keys, "mentoria:"+c.owner) || slices.Contains(keys, "mentoria:"+c.other) {
			t.Errorf("%s: people = %v; want its own owner %s, never %s", c.name, keys, c.owner, c.other)
		}
	}
}

// slowCalendar answers free/busy only when the request's context ends, for the users in slow.
type slowCalendar struct {
	calendar.Provider
	slow map[string]bool
}

func (slowCalendar) Name() string { return "google" }
func (c slowCalendar) FreeBusy(ctx context.Context, userID string, _, _ time.Time) ([]slots.Interval, error) {
	if !c.slow[userID] {
		return nil, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, errTestProviderDown
	}
}

// A provider that never answers (the Google client has no timeout of its own) costs that
// person their time limit, not the whole answer; and once the answer's budget is spent the
// people still waiting are marked "timeout" instead of pushing past the server's
// WriteTimeout.
func TestTeamAvailability_slowCalendarIsBounded(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&fresh=1"
	byKey := func(got availAnswer) map[string]availPerson {
		m := map[string]availPerson{}
		for _, p := range got.People {
			m[p.Area+":"+p.UserID] = p
		}
		return m
	}

	// One slow person: only they are marked, within their own limit.
	restore := handler.SetTeamAvailabilityLimitsForTest(150*time.Millisecond, 5*time.Second)
	defer restore()
	svc := calendar.NewService(a.db)
	svc.Register(slowCalendar{slow: map[string]bool{"m1": true}})
	a.h.SetCalendar(svc)
	start := time.Now()
	got := byKey(a.mustAvailability(a.ownerKey, q))
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("answer took %v with one slow calendar; want about one person limit", d)
	}
	if p := got["mentoria:m1"]; !p.Error || p.ErrorKind != "calendar" || len(p.Slots) != 0 {
		t.Errorf("m1 (slow) = error %v kind %q slots %d; want error, calendar, none", p.Error, p.ErrorKind, len(p.Slots))
	}
	for _, k := range []string{"mentoria:m2", "soporte:s1", "mentoria:" + a.ownerID, "soporte:" + a.ownerID} {
		if p := got[k]; p.Error || len(p.Slots) == 0 {
			t.Errorf("%s: error %v (%s) slots %d; want its times despite m1's slow calendar", k, p.Error, p.ErrorKind, len(p.Slots))
		}
	}

	// Both mentors slow and a budget of about one and a half limits: the owner's T (first)
	// answers, the two mentors are "calendar", and the Soporte people - reached after the
	// budget is spent - are "timeout", with their links still there.
	restore()
	restore = handler.SetTeamAvailabilityLimitsForTest(200*time.Millisecond, 300*time.Millisecond)
	svc = calendar.NewService(a.db)
	svc.Register(slowCalendar{slow: map[string]bool{"m1": true, "m2": true}})
	a.h.SetCalendar(svc)
	start = time.Now()
	got = byKey(a.mustAvailability(a.ownerKey, q))
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("answer took %v; want it cut near the 300 ms budget", d)
	}
	if p := got["mentoria:"+a.ownerID]; p.Error || len(p.Slots) == 0 {
		t.Errorf("owner with T: error %v (%s) slots %d; want its times", p.Error, p.ErrorKind, len(p.Slots))
	}
	for _, k := range []string{"mentoria:m1", "mentoria:m2"} {
		if p := got[k]; !p.Error || p.ErrorKind != "calendar" {
			t.Errorf("%s (slow) = error %v kind %q; want error, calendar", k, p.Error, p.ErrorKind)
		}
	}
	for _, k := range []string{"soporte:s1", "soporte:" + a.ownerID} {
		if p, ok := got[k]; !ok || !p.Error || p.ErrorKind != "timeout" || p.Link.Slug == "" {
			t.Errorf("%s after the budget = present %v error %v kind %q link %q; want it listed as timeout with its link", k, ok, p.Error, p.ErrorKind, p.Link.Slug)
		}
	}
}
