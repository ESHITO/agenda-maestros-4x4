package handler

// Fork (Agenda Maestros 4x4): the Panel's team calendar must show a change at once.
//
// Owner report (sep 2026): a mentor configuring his free hours had to reload after every
// change to see it. One of the reasons was the team calendar's 60 s answer cache
// (fork_team_availability.go): whatever the mentor saved, the Panel kept showing the old
// answer until the entry expired.
//
// The cache now carries a generation (teamAvailCache.gen). Every answer is stored with the
// generation read BEFORE it was computed, and an entry of an older generation is a miss.
// The generation moves on every successful state-changing request (FreeTimeChanges, which
// server.New puts around the whole mux): availability rules and overrides of anybody,
// bookings created / cancelled / rescheduled / reassigned (admin, manage link, embed, the
// assistant, MCP), team settings and roles, calendar connections, event types... Anything
// that can change somebody's free time is a write, so "any successful write" is the simple
// rule that cannot forget a path added later. Over-invalidating only costs one recompute
// of the Panel's answer; the cache exists to absorb the Panel's reads, not the writes.
//
// Excluded, because they never touch free time and some are frequent: the video room's
// endpoints (/v1/livekit/...), login/logout (/v1/auth/...) and OAuth (/oauth/...).
//
// Background writers bump it by hand (StartTeamBoot's reconcile). External calendars
// changing on Google/Microsoft are not observable here: the TTL still bounds those, and the
// Panel asks with fresh=1 when it comes back to the foreground.

import (
	"net/http"
	"slices"
	"strings"
)

// InvalidateTeamAvailability makes every cached team-availability answer stale.
func (h *Handler) InvalidateTeamAvailability() {
	h.teamAvail.gen.Add(1)
}

// teamAvailGeneration is the cache's current generation.
func (h *Handler) teamAvailGeneration() uint64 {
	return h.teamAvail.gen.Load()
}

// freeTimeChangingGets are the GET routes that change free time: connecting a Google or
// Microsoft calendar ends on the provider's redirect (GET /v1/calendar/callback saves the
// new connection and answers 302), and the new calendar's busy events remove free time.
var freeTimeChangingGets = []string{"/v1/calendar/callback"}

// freeTimeWrite reports whether a request can change somebody's free time when it succeeds.
func freeTimeWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return !freeTimeNeverChanges(r.URL.Path)
	case http.MethodGet:
		return slices.Contains(freeTimeChangingGets, r.URL.Path)
	}
	return false
}

// freeTimeNeverChanges reports the write paths that cannot change anybody's free time.
func freeTimeNeverChanges(path string) bool {
	for _, p := range []string{"/v1/livekit/", "/v1/auth/", "/oauth/"} {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// FreeTimeChanges wraps the mux: a POST/PUT/PATCH/DELETE (or one of freeTimeChangingGets)
// that answers below 400 bumps the
// team-availability generation - as soon as the handler commits its status (the database
// change is done by then, and the client may refetch the moment it reads the answer) and
// once more when it returns (a Panel read that started between the commit and the status
// must not keep an answer computed half before it).
func (h *Handler) FreeTimeChanges(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !freeTimeWrite(r) {
			next.ServeHTTP(w, r)
			return
		}
		fw := &freeTimeWriter{ResponseWriter: w, h: h}
		next.ServeHTTP(fw, r)
		if fw.status == 0 || fw.status < http.StatusBadRequest {
			h.InvalidateTeamAvailability()
		}
	})
}

// freeTimeWriter notes the status and bumps the generation when a success is committed.
type freeTimeWriter struct {
	http.ResponseWriter
	h      *Handler
	status int
}

func (fw *freeTimeWriter) WriteHeader(status int) {
	if fw.status == 0 && status >= 200 { // 1xx (Continue, Early Hints) is not the answer yet
		fw.status = status
		if status < http.StatusBadRequest {
			fw.h.InvalidateTeamAvailability()
		}
	}
	fw.ResponseWriter.WriteHeader(status)
}

func (fw *freeTimeWriter) Write(b []byte) (int, error) {
	if fw.status == 0 {
		fw.WriteHeader(http.StatusOK)
	}
	return fw.ResponseWriter.Write(b)
}

// Flush keeps the wrapper transparent to streamed answers (the assistant, MCP).
func (fw *freeTimeWriter) Flush() {
	if f, ok := fw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer.
func (fw *freeTimeWriter) Unwrap() http.ResponseWriter {
	return fw.ResponseWriter
}
