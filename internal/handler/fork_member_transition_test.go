package handler_test

// Fork (Agenda Maestros 4x4): the transition time between sessions
// (fork_member_transition.go). Mentoría follows its template; each Soporte person may
// choose their own, which the reconcile writes into their copy of S only.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/slots"
	"github.com/calnode/calnode/internal/uid"
)

// transitionFixture: T and S as in production (40 min, a start every 30, 15 after), one
// mentor m1 and one support person s1, both with copies.
func newTransitionFixture(t *testing.T) (*teamFixture, string, string) {
	t.Helper()
	f := newTeamFixture(t)
	mustExec(t, f.db, `UPDATE event_types SET duration_minutes = 40, slot_interval_minutes = 30, buffer_after_minutes = 15 WHERE id IN (?, ?)`, f.tID, f.sID)
	m1Key := addMember(t, f.db, "m1", "UTC")
	s1Key := addMember(t, f.db, "s1", "UTC")
	f.mustRole("m1", "member", "mentoria")
	f.mustRole("s1", "member", "soporte")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_template_id":"` + f.sID + `"}`)
	return f, m1Key, s1Key
}

// times is a copy's (duration, slot interval, buffer after).
func (f *teamFixture) times(etID string) string {
	f.t.Helper()
	return f.scalar(`SELECT duration_minutes || '/' || slot_interval_minutes || '/' || buffer_after_minutes FROM event_types WHERE id = ?`, etID)
}

func (f *teamFixture) putMyTransition(key, body string) *httptest.ResponseRecorder {
	return f.call(f.h.TeamReconcileAfterCaller(f.h.PutMyTransition), http.MethodPut, "/v1/users/me/transition", body, key)
}

func (f *teamFixture) putUserTransition(key, userID, body string) *httptest.ResponseRecorder {
	return f.call(f.h.TeamReconcileAfter(f.h.PutUserTransition), http.MethodPut, "/v1/users/"+userID+"/transition", body, key, "id", userID)
}

func TestTransition_reconcileSoporteCopiesOnly(t *testing.T) {
	f, _, s1Key := newTransitionFixture(t)
	mc, sc := f.copyOf(f.tID, "m1"), f.copyOf(f.sID, "s1")
	if mc.id == "" || sc.id == "" {
		t.Fatal("fixture: copies missing")
	}
	if got := f.times(sc.id); got != "40/30/15" {
		t.Fatalf("s1's copy before any choice = %s; want S's 40/30/15", got)
	}

	// s1 chooses 10: their copy starts a session every 50, with 10 of transition.
	got := mustJSON(t, f.putMyTransition(s1Key, `{"minutes":10}`), http.StatusOK, "s1 PUT 10")
	if got["minutes"] != float64(10) || got["interval_effective"] != float64(50) || got["applies"] != true {
		t.Errorf("PUT answer = %v; want minutes 10, interval_effective 50, applies", got)
	}
	if got := f.times(sc.id); got != "40/50/10" {
		t.Errorf("s1's copy = %s; want 40/50/10", got)
	}
	if got := f.times(mc.id); got != "40/30/15" {
		t.Errorf("m1's Mentoría copy = %s; want T's 40/30/15 untouched", got)
	}

	// A stray row for a mentor (the API refuses it; a hand-written one) changes nothing:
	// Mentoría copies never read the table.
	mustExec(t, f.db, `INSERT INTO fork_member_transition (user_id, minutes, updated_at) VALUES ('m1', 5, 'x')`)
	if _, err := f.h.ReconcileTeam(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if got := f.times(mc.id); got != "40/30/15" {
		t.Errorf("m1's copy with a stray row = %s; want T's 40/30/15", got)
	}
	// A row outside the choices is ignored (the copy keeps S's values).
	addMember(t, f.db, "s2", "UTC")
	f.mustRole("s2", "member", "soporte")
	mustExec(t, f.db, `INSERT INTO fork_member_transition (user_id, minutes, updated_at) VALUES ('s2', -30, 'x')`)
	if _, err := f.h.ReconcileTeam(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	s2c := f.copyOf(f.sID, "s2")
	if got := f.times(s2c.id); got != "40/30/15" {
		t.Errorf("s2's copy with an invalid row = %s; want S's 40/30/15", got)
	}
	mustExec(t, f.db, `DELETE FROM fork_member_transition WHERE user_id = 's2'`)

	// The owner edits S: copies without a choice take everything; s1's keeps their
	// transition on S's new duration.
	mustStatus(t, f.patchET(f.sSlug, `{"duration_minutes":45,"slot_interval_minutes":60,"buffer_after_minutes":5}`), http.StatusOK, "patch S")
	if got := f.times(sc.id); got != "45/55/10" {
		t.Errorf("s1's copy after the S edit = %s; want 45/55/10", got)
	}
	if got := f.times(s2c.id); got != "45/60/5" {
		t.Errorf("s2's copy (no choice) after the S edit = %s; want S's 45/60/5", got)
	}
	// And the owner edits T: the mentor's copy follows, as always.
	mustStatus(t, f.patchET(f.tSlug, `{"slot_interval_minutes":60,"buffer_after_minutes":10}`), http.StatusOK, "patch T")
	if got := f.times(mc.id); got != "40/60/10" {
		t.Errorf("m1's copy after the T edit = %s; want 40/60/10", got)
	}

	// null = back to the template.
	got = mustJSON(t, f.putMyTransition(s1Key, `{"minutes":null}`), http.StatusOK, "s1 PUT null")
	if got["minutes"] != nil || got["interval_effective"] != float64(60) {
		t.Errorf("PUT null answer = %v; want minutes null, interval_effective = S's 60", got)
	}
	if got := f.times(sc.id); got != "45/60/5" {
		t.Errorf("s1's copy after null = %s; want S's 45/60/5", got)
	}
	if n := f.scalar(`SELECT COUNT(*) FROM fork_member_transition WHERE user_id = 's1'`); n != "0" {
		t.Errorf("null left %s rows", n)
	}
}

func TestTransition_permissionsAndValidation(t *testing.T) {
	f, m1Key, s1Key := newTransitionFixture(t)
	s2Key := addMember(t, f.db, "s2", "UTC")
	f.mustRole("s2", "member", "soporte")
	adminKey := addMember(t, f.db, "a1", "UTC")
	f.mustRole("a1", "admin", "")
	addMember(t, f.db, "a2", "UTC")
	f.mustRole("a2", "admin", "soporte")

	for _, c := range []struct {
		name, key, target, body string
		want                    int
	}{
		{"a mentor on /me", m1Key, "", `{"minutes":10}`, http.StatusForbidden},
		{"an admin with no área on /me", adminKey, "", `{"minutes":10}`, http.StatusForbidden},
		{"a value off the list", s1Key, "", `{"minutes":7}`, http.StatusBadRequest},
		{"over an hour", s1Key, "", `{"minutes":90}`, http.StatusBadRequest},
		{"a string", s1Key, "", `{"minutes":"10"}`, http.StatusBadRequest},
		{"no minutes", s1Key, "", `{}`, http.StatusBadRequest},
		{"broken JSON", s1Key, "", `{`, http.StatusBadRequest},
		{"support on /me", s1Key, "", `{"minutes":0}`, http.StatusOK},
		{"support on their own id", s1Key, "s1", `{"minutes":5}`, http.StatusOK},
		{"support on someone else", s1Key, "s2", `{"minutes":5}`, http.StatusForbidden},
		{"a mentor on a support person", m1Key, "s1", `{"minutes":5}`, http.StatusForbidden},
		{"the owner on a support person", f.ownerKey, "s1", `{"minutes":20}`, http.StatusOK},
		{"the owner on a mentor", f.ownerKey, "m1", `{"minutes":20}`, http.StatusBadRequest},
		{"the owner on nobody", f.ownerKey, "ghost", `{"minutes":20}`, http.StatusNotFound},
		{"an admin on a support member", adminKey, "s2", `{"minutes":45}`, http.StatusOK},
		{"an admin on an admin of Soporte", adminKey, "a2", `{"minutes":45}`, http.StatusForbidden},
		{"an admin on the owner", adminKey, f.ownerID, `{"minutes":45}`, http.StatusForbidden},
		{"the owner on an admin of Soporte", f.ownerKey, "a2", `{"minutes":30}`, http.StatusOK},
		{"an admin on a mentor", adminKey, "m1", `{"minutes":30}`, http.StatusBadRequest},
	} {
		var rec *httptest.ResponseRecorder
		if c.target == "" {
			rec = f.putMyTransition(c.key, c.body)
		} else {
			rec = f.putUserTransition(c.key, c.target, c.body)
		}
		if rec.Code != c.want {
			t.Errorf("%s: status %d; want %d - %s", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	// What stuck: s1 = 20 (owner, last), s2 = 45 (admin), a2 = 30 (owner); nothing for m1.
	for id, want := range map[string]string{"s1": "20", "s2": "45", "a2": "30", "m1": ""} {
		if got := f.scalar(`SELECT minutes FROM fork_member_transition WHERE user_id = ?`, id); got != want {
			t.Errorf("%s stored %q; want %q", id, got, want)
		}
	}
	if got := f.times(f.copyOf(f.sID, "s2").id); got != "40/85/45" {
		t.Errorf("s2's copy after the admin's PUT = %s; want 40/85/45", got)
	}

	// GET /me: applies only with an active S copy.
	got := mustJSON(t, f.call(f.h.GetMyTransition, http.MethodGet, "/v1/users/me/transition", "", s1Key), http.StatusOK, "GET s1")
	want := map[string]any{"applies": true, "minutes": float64(20), "template_minutes": float64(15),
		"template_interval": float64(30), "duration": float64(40), "interval_effective": float64(60)}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("GET s1 %s = %v; want %v", k, got[k], v)
		}
	}
	got = mustJSON(t, f.call(f.h.GetMyTransition, http.MethodGet, "/v1/users/me/transition", "", m1Key), http.StatusOK, "GET m1")
	if got["applies"] != false || got["minutes"] != nil {
		t.Errorf("GET m1 = %v; want applies false, minutes null", got)
	}
	got = mustJSON(t, f.call(f.h.GetMyTransition, http.MethodGet, "/v1/users/me/transition", "", s2Key), http.StatusOK, "GET s2")
	if got["applies"] != true || got["minutes"] != float64(45) {
		t.Errorf("GET s2 = %v; want applies, 45", got)
	}
	// S unset: nobody's choice applies (their copies are inactive).
	f.mustSettings(`{"soporte_template_id":null}`)
	got = mustJSON(t, f.call(f.h.GetMyTransition, http.MethodGet, "/v1/users/me/transition", "", s1Key), http.StatusOK, "GET s1 without S")
	if got["applies"] != false {
		t.Errorf("GET s1 without S: applies = %v; want false", got["applies"])
	}
	f.mustSettings(`{"soporte_template_id":"` + f.sID + `"}`)

	// GET /v1/team/transitions: owner and admins only.
	mustStatus(t, f.call(f.h.ListTeamTransitions, http.MethodGet, "/v1/team/transitions", "", s1Key), http.StatusForbidden, "list as support")
	list := mustJSON(t, f.call(f.h.ListTeamTransitions, http.MethodGet, "/v1/team/transitions", "", adminKey), http.StatusOK, "list as admin")
	items, _ := list["items"].(map[string]any)
	if list["has_template"] != true || list["template_minutes"] != float64(15) || items["s2"] != float64(45) || len(items) != 3 {
		t.Errorf("list = %v; want S's 15 and the three choices (s1, s2, a2)", list)
	}
	choices, _ := json.Marshal(list["choices"])
	if string(choices) != "[0,5,10,15,20,25,30,45,60]" {
		t.Errorf("choices = %s", choices)
	}
}

// 40-min sessions + 10 of transition in a 9-12 window: starts 9:00, 9:50, 10:40; a booking
// at 9:00 keeps 9:50 bookable.
func TestTransition_slotsCadence(t *testing.T) {
	f, _, s1Key := newTransitionFixture(t)
	for day := 0; day < 7; day++ {
		mustExec(t, f.db, `INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time) VALUES (?, 's1', ?, '09:00', '12:00')`, uid.New(), day)
	}
	mustStatus(t, f.putMyTransition(s1Key, `{"minutes":10}`), http.StatusOK, "s1 PUT 10")
	sc := f.copyOf(f.sID, "s1")
	day := futureAt(3, 0, 0)
	ymd := day.Format("2006-01-02")
	starts := func() []string {
		t.Helper()
		var out []string
		for _, s := range getSlots(t, f.h, sc.slug, fmt.Sprintf("?from=%s&to=%s&tz=UTC", ymd, ymd)).Slots {
			ts, err := time.Parse(time.RFC3339, s.Start)
			if err != nil {
				t.Fatalf("slot start %q: %v", s.Start, err)
			}
			out = append(out, ts.UTC().Format("15:04"))
		}
		return out
	}
	if got := starts(); !slices.Equal(got, []string{"09:00", "09:50", "10:40"}) {
		t.Fatalf("s1's starts = %v; want 09:00 09:50 10:40", got)
	}
	bookInZone(t, f.h, sc.slug, day.Add(9*time.Hour), "UTC", "")
	if got := starts(); !slices.Equal(got, []string{"09:50", "10:40"}) {
		t.Errorf("after a 9:00 booking = %v; want 09:50 10:40 (the transition ends exactly at 9:50)", got)
	}

	// Without a choice the copy is back on S's grid (every 30, 15 after): the 9:00 booking
	// blocks until 9:55, so the next start is 10:00.
	mustStatus(t, f.putMyTransition(s1Key, `{"minutes":null}`), http.StatusOK, "s1 PUT null")
	if got := starts(); !slices.Equal(got, []string{"10:00", "10:30", "11:00"}) {
		t.Errorf("on S's grid after a 9:00 booking = %v; want 10:00 10:30 11:00", got)
	}
}

// The transition blocks the host until the session's end + the transition, and not a
// minute longer - shown on a fine 5-minute grid, where every start in between would fit.
func TestTransition_blocksUntilEndPlusTransition(t *testing.T) {
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 3)
	busyStart := day.Add(9 * time.Hour)
	got, err := slots.Generate(slots.Request{
		Event: slots.EventConfig{DurationMinutes: 40, SlotIntervalMinutes: 5, BufferAfterMinutes: 10, RoutingMode: "fixed"},
		Hosts: []slots.HostAvailability{{
			HostID: "s1", Location: time.UTC,
			Rules: []slots.AvailabilityRule{{DayOfWeek: day.Weekday(), StartTime: "09:00", EndTime: "11:00"}},
			Busy:  []slots.Interval{{Start: busyStart, End: busyStart.Add(40 * time.Minute)}},
		}},
		DateFrom: day, DateTo: day, BookerTZ: time.UTC, Now: day.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Start.Format("15:04") != "09:50" {
		first := "none"
		if len(got) > 0 {
			first = got[0].Start.Format("15:04")
		}
		t.Errorf("first start after a 9:00-9:40 session with 10 of transition = %s; want 09:50", first)
	}
}
