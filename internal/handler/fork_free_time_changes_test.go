package handler_test

// Fork (Agenda Maestros 4x4): the Panel's team calendar shows a change on the next read
// (fork_free_time_changes.go). Owner report: a mentor setting his free hours had to reload
// again and again to see them.

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// viaMux runs fn behind FreeTimeChanges, the way server.New wraps the whole mux.
func (f *teamFixture) viaMux(fn http.HandlerFunc) http.HandlerFunc {
	return f.h.FreeTimeChanges(fn).ServeHTTP
}

func slotCount(a availAnswer, userID string) int {
	n := 0
	for _, p := range a.People {
		if p.UserID == userID {
			n += len(p.Slots)
		}
	}
	return n
}

// A new availability rule (the mentor's own POST) reaches the cached team calendar at once.
func TestFreeTimeChanges_ruleInvalidatesTeamCalendar(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&area=mentoria"
	before := a.mustAvailability(a.ownerKey, q)
	n0 := slotCount(before, "m1")
	if n0 == 0 {
		t.Fatalf("m1 has no slots to start with: %+v", before.People)
	}
	body := `{"day_of_week":1,"start_time":"15:00","end_time":"20:00"}`
	rec := a.call(a.viaMux(a.h.TeamReconcileAfterCaller(a.h.CreateAvailabilityRule)), http.MethodPost, "/v1/availability-rules", body, a.m1Key)
	mustStatus(t, rec, http.StatusCreated, "POST availability rule")
	after := a.mustAvailability(a.ownerKey, q) // no fresh=1: what the Panel asks by itself
	if n := slotCount(after, "m1"); n <= n0 {
		t.Errorf("m1 still has %d slots after adding Monday 15:00-20:00 (before %d): the cached answer was served", n, n0)
	}
}

// A role change through the mux drops the person from the next (non-fresh) answer.
func TestFreeTimeChanges_roleInvalidatesTeamCalendar(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&area=mentoria"
	first := a.mustAvailability(a.ownerKey, q)
	if !slices.Contains(personKeys(first.People), "mentoria:m2") {
		t.Fatalf("m2 missing to start with: %v", personKeys(first.People))
	}
	rec := a.call(a.viaMux(a.h.SetTeamRole), http.MethodPut, "/v1/users/m2/team-role", `{"tier":"member","area":""}`, a.ownerKey, "id", "m2")
	mustStatus(t, rec, http.StatusOK, "team-role m2")
	next := a.mustAvailability(a.ownerKey, q)
	if slices.Contains(personKeys(next.People), "mentoria:m2") {
		t.Errorf("m2 still listed after losing the área: %v", personKeys(next.People))
	}
}

// Reads and refused writes keep the cache; only a success moves the generation.
func TestFreeTimeChanges_onlySuccessfulWrites(t *testing.T) {
	a := newAvailTeam(t)
	from, to := availRange()
	q := "from=" + from + "&to=" + to + "&tz=UTC&area=mentoria"
	first := a.mustAvailability(a.ownerKey, q)

	// A refused write (a mentor may not change roles) and a GET through the wrapper.
	rec := a.call(a.viaMux(a.h.SetTeamRole), http.MethodPut, "/v1/users/m2/team-role", `{"tier":"member","area":""}`, a.m1Key, "id", "m2")
	if rec.Code < 400 {
		t.Fatalf("mentor changed a role: %d %s", rec.Code, rec.Body.String())
	}
	mustStatus(t, a.call(a.viaMux(a.h.ListUsers), http.MethodGet, "/v1/users", "", a.ownerKey), http.StatusOK, "GET users")
	if got := a.mustAvailability(a.ownerKey, q); got.GeneratedAt != first.GeneratedAt {
		t.Errorf("cache dropped by a refused write or a read: %s vs %s", got.GeneratedAt, first.GeneratedAt)
	}

	// The video room's endpoints never touch free time.
	lk := httptest.NewRecorder()
	a.h.FreeTimeChanges(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).
		ServeHTTP(lk, httptest.NewRequest(http.MethodPost, "/v1/livekit/token", nil))
	if got := a.mustAvailability(a.ownerKey, q); got.GeneratedAt != first.GeneratedAt {
		t.Errorf("cache dropped by a /v1/livekit write")
	}

	// Any other success does, even one that writes nothing (implicit 200).
	ok := httptest.NewRecorder()
	a.h.FreeTimeChanges(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(ok, httptest.NewRequest(http.MethodDelete, "/v1/availability-overrides/x", nil))
	a.mustRole("m2", "member", "") // direct call: bypasses the wrapper, like the old test
	if got := a.mustAvailability(a.ownerKey, q); slices.Contains(personKeys(got.People), "mentoria:m2") {
		t.Errorf("cache survived a successful write: %v", personKeys(got.People))
	}
}

// The wrapper stays transparent to streamed answers (the assistant, MCP).
func TestFreeTimeChanges_keepsFlusher(t *testing.T) {
	a := newAvailTeam(t)
	flushed := false
	rec := httptest.NewRecorder()
	a.h.FreeTimeChanges(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err == nil {
			flushed = true
		}
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/event-types/x/assistant", nil))
	if !flushed || !rec.Flushed {
		t.Errorf("flush did not reach the recorder (flushed=%v, rec.Flushed=%v)", flushed, rec.Flushed)
	}
}
