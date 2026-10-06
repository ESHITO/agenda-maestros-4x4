package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// An answer computed while a change landed is born stale: put stores it under the
// generation read before computing, so it never hides that change for a whole TTL.
func TestTeamAvailCache_generation(t *testing.T) {
	var c teamAvailCache
	now := time.Now()
	out := teamAvailabilityJSON{TZ: "UTC"}

	gen := c.gen.Load()
	c.put("k", now, gen, out)
	if _, ok := c.get("k", now); !ok {
		t.Fatal("fresh entry missed")
	}
	c.gen.Add(1) // a write
	if _, ok := c.get("k", now); ok {
		t.Error("entry of an older generation served")
	}

	gen = c.gen.Load()
	c.gen.Add(1) // a write lands while the answer is being computed
	c.put("k", now, gen, out)
	if _, ok := c.get("k", now); ok {
		t.Error("answer computed across a write was cached")
	}
	if len(c.avail.m) != 0 {
		t.Errorf("stale entries kept: %d", len(c.avail.m))
	}

	c.put("k", now, c.gen.Load(), out)
	if _, ok := c.get("k", now.Add(teamAvailabilityTTL)); ok {
		t.Error("entry served past the TTL")
	}
}

// Connecting a Google or Microsoft calendar ends on a GET (the provider's redirect to
// /v1/calendar/callback, answered with 302): a success moves the generation, a refused
// callback and any other GET do not.
func TestFreeTimeChanges_calendarCallback(t *testing.T) {
	var h Handler
	serve := func(method, path string, status int) {
		rec := httptest.NewRecorder()
		h.FreeTimeChanges(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status == http.StatusFound {
				http.Redirect(w, r, "/admin/calendar?connected=true", status)
				return
			}
			w.WriteHeader(status)
		})).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	}

	g := h.teamAvailGeneration()
	serve(http.MethodGet, "/v1/calendar/callback?code=x&state=y", http.StatusFound)
	if h.teamAvailGeneration() == g {
		t.Error("a connected calendar kept the cached team calendar")
	}

	g = h.teamAvailGeneration()
	serve(http.MethodGet, "/v1/calendar/callback?error=access_denied", http.StatusBadRequest)
	serve(http.MethodGet, "/v1/calendar/connections", http.StatusOK)
	serve(http.MethodGet, "/v1/team/availability", http.StatusOK)
	if h.teamAvailGeneration() != g {
		t.Error("a refused callback or a plain read moved the generation")
	}
}
