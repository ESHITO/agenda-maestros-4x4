package handler

// Fork (Agenda Maestros 4x4): the team's free time in one place - the Panel's
// "Disponibilidad del equipo", so the owner (and the support people) can answer
// "¿quién me atiende el martes a las 10?" and hand over the right personal link.
//
//	GET /v1/team/availability?from=YYYY-MM-DD&to=YYYY-MM-DD&tz=<IANA>&area=all|mentoria|soporte[&fresh=1]
//
// Who: the owner and the área-soporte people (an admin only if they also attend Soporte);
// anybody else 403 - owner decision: mentors and plain admins do not see the team calendar. from/to are days in tz (the VIEWER's chosen
// zone, exactly like the public /slots: the booker's days), at most 7 days (400 beyond);
// from defaults to today in tz and to to from+6; tz defaults to the viewer's profile zone.
//
// People: for each área asked, the template's owner with the template itself (T for
// Mentoría, S for Soporte - their own links) and the host of every ACTIVE copy of it whose
// user is active. Each person's slots are computeSlots for THAT link with slotsWanted{} -
// the very function the public booking page uses - so the panel never offers a time the
// client could not book there. A link the public page would 404 (paused, not public,
// archived) is left out. A person whose computation fails, or whose external calendar
// could not be checked (booking is fail-closed then: calendarFreeHosts rejects every
// time, so the fail-open slots would be times nobody can book), is kept with error=true
// and no slots, and the others still answer. No client data: only free starts.
//
// People are computed one after the other (each computeSlots already overlaps its own
// provider calls; the DB is one connection). Each person gets at most teamAvailPersonTimeout
// and the whole loop teamAvailTotalBudget - well inside the server's 30 s WriteTimeout -
// because a provider free/busy call has no timeout of its own: a slow calendar marks that
// person (error_kind "calendar", or "timeout" for the people the budget no longer reached)
// instead of stalling everybody's answer. A whole answer with no error is cached for
// teamAvailabilityTTL per range+tz+área on the Handler (h.teamAvail); fresh=1 skips the
// cache (the panel's "Actualizar"). Every successful write moves the cache's generation
// and makes all of it stale at once (fork_free_time_changes.go): a mentor's new hours show
// on the next read, not a minute later.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	teamAvailabilityMaxDays = 7
	teamAvailabilityTTL     = 60 * time.Second
)

// Time limits of one answer (vars so the tests can shorten them).
var (
	teamAvailPersonTimeout = 5 * time.Second
	teamAvailTotalBudget   = 20 * time.Second
)

type teamAvailSlotJSON struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type teamAvailLinkJSON struct {
	Slug string `json:"slug"`
	URL  string `json:"url"`
}

type teamAvailPersonJSON struct {
	UserID    string              `json:"user_id"`
	Name      string              `json:"name"`
	Area      string              `json:"area"`
	AvatarURL string              `json:"avatar_url"`
	Color     *string             `json:"color"`
	IsYou     bool                `json:"is_you"`
	Link      teamAvailLinkJSON   `json:"link"`
	Slots     []teamAvailSlotJSON `json:"slots"`
	// Error: this person's times could not be computed now; ErrorKind says why -
	// "calendar" (their external calendar did not answer, or not in time), "timeout" (the
	// answer's time budget ran out before this person) or "internal".
	Error     bool   `json:"error,omitempty"`
	ErrorKind string `json:"error_kind,omitempty"`
}

type teamAvailabilityJSON struct {
	TZ          string                `json:"tz"`
	From        string                `json:"from"`
	To          string                `json:"to"`
	GeneratedAt string                `json:"generated_at"`
	People      []teamAvailPersonJSON `json:"people"`
}

// teamAvailCache holds recent answers, keyed by range + tz + área (IsYou is set per viewer
// on the way out, so one entry serves everybody). It lives on the Handler (the zero value
// is ready), so two Handlers - every test builds its own over its own database - never
// share an answer.
type teamAvailCache struct {
	mu sync.Mutex
	m  map[string]teamAvailCacheEntry
	// gen moves on every change that may alter somebody's free time
	// (fork_free_time_changes.go); an entry stored under an older generation is a miss.
	gen atomic.Uint64
}

type teamAvailCacheEntry struct {
	at  time.Time
	gen uint64
	out teamAvailabilityJSON
}

func teamAvailCacheKey(from, to, tz, area string) string {
	return from + "|" + to + "|" + tz + "|" + area
}

func (c *teamAvailCache) get(key string, now time.Time) (teamAvailabilityJSON, bool) {
	gen := c.gen.Load()
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || e.gen != gen || now.Sub(e.at) >= teamAvailabilityTTL {
		return teamAvailabilityJSON{}, false
	}
	return e.out, true
}

// put stores out under gen, the generation read BEFORE out was computed: a change that
// landed while it was being computed has moved the generation on, so the entry is born
// stale instead of hiding that change for a whole TTL.
func (c *teamAvailCache) put(key string, now time.Time, gen uint64, out teamAvailabilityJSON) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]teamAvailCacheEntry{}
	}
	cur := c.gen.Load()
	for k, e := range c.m { // a handful of entries: purge the stale ones here
		if e.gen != cur || now.Sub(e.at) >= teamAvailabilityTTL {
			delete(c.m, k)
		}
	}
	if gen != cur {
		return
	}
	c.m[key] = teamAvailCacheEntry{at: now, gen: gen, out: out}
}

// teamMayViewAvailability: the owner and the área-soporte people (owner decision: not the
// mentors, and not an admin who does not attend Soporte).
func (h *Handler) teamMayViewAvailability(ctx context.Context, user AuthUser) (bool, error) {
	if user.IsOwner {
		return true, nil
	}
	var n int
	err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fork_member_areas WHERE user_id = ? AND area = ?`, user.ID, areaSoporte).Scan(&n)
	return n > 0, err
}

// parseTeamAvailabilityRange validates from/to (days in loc, both optional) and the 7-day
// cap. The message is the Spanish 400 ("" = fine).
func parseTeamAvailabilityRange(fromStr, toStr string, now time.Time, loc *time.Location) (string, string, string) {
	y, m, d := now.In(loc).Date()
	from := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if fromStr != "" {
		t, err := time.Parse("2006-01-02", fromStr)
		if err != nil {
			return "", "", "La fecha «from» debe tener el formato AAAA-MM-DD."
		}
		from = t
	}
	to := from.AddDate(0, 0, teamAvailabilityMaxDays-1)
	if toStr != "" {
		t, err := time.Parse("2006-01-02", toStr)
		if err != nil {
			return "", "", "La fecha «to» debe tener el formato AAAA-MM-DD."
		}
		to = t
	}
	if to.Before(from) {
		return "", "", "La fecha «to» no puede ser anterior a «from»."
	}
	if to.Sub(from) > (teamAvailabilityMaxDays-1)*24*time.Hour {
		return "", "", fmt.Sprintf("El rango máximo es de %d días.", teamAvailabilityMaxDays)
	}
	return from.Format("2006-01-02"), to.Format("2006-01-02"), ""
}

// GetTeamAvailability handles GET /v1/team/availability.
func (h *Handler) GetTeamAvailability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, _ := userFromContext(ctx)
	ok, err := h.teamMayViewAvailability(ctx, user)
	if err != nil {
		h.logger.ErrorContext(ctx, "team availability: permission", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		h.writeError(w, http.StatusForbidden, "Solo el propietario y el equipo de soporte ven la disponibilidad del equipo.")
		return
	}
	q := r.URL.Query()
	area := q.Get("area")
	switch area {
	case "", "all":
		area = "all"
	case areaMentoria, areaSoporte:
	default:
		h.writeError(w, http.StatusBadRequest, "El área debe ser 'all', 'mentoria' o 'soporte'.")
		return
	}
	tzName := strings.TrimSpace(q.Get("tz"))
	if tzName == "" {
		tzName = user.IANATZ
	}
	if tzName == "" {
		tzName = "UTC"
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil || tzName == "Local" {
		h.writeError(w, http.StatusBadRequest, "Zona horaria inválida: "+tzName)
		return
	}
	now := time.Now()
	from, to, msg := parseTeamAvailabilityRange(q.Get("from"), q.Get("to"), now, loc)
	if msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}

	key := teamAvailCacheKey(from, to, tzName, area)
	out, hit := teamAvailabilityJSON{}, false
	if q.Get("fresh") != "1" {
		out, hit = h.teamAvail.get(key, now)
	}
	if !hit {
		gen := h.teamAvailGeneration() // before computing: see teamAvailCache.put
		out, err = h.teamAvailability(ctx, from, to, tzName, area)
		if err != nil {
			h.logger.ErrorContext(ctx, "team availability: build", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out.GeneratedAt = now.UTC().Format(time.RFC3339)
		clean := true
		for _, p := range out.People {
			clean = clean && !p.Error
		}
		if clean { // an error is worth retrying at once, never cached
			h.teamAvail.put(key, now, gen, out)
		}
	}
	people := make([]teamAvailPersonJSON, len(out.People))
	copy(people, out.People)
	for i := range people {
		people[i].IsYou = people[i].UserID == user.ID
	}
	out.People = people
	h.writeJSON(w, http.StatusOK, out)
}

// teamAvailEntry is one person's link to compute.
type teamAvailEntry struct {
	userID, area, slug string
}

// teamAvailability builds the answer (without IsYou or GeneratedAt).
func (h *Handler) teamAvailability(ctx context.Context, from, to, tzName, area string) (teamAvailabilityJSON, error) {
	out := teamAvailabilityJSON{TZ: tzName, From: from, To: to, People: []teamAvailPersonJSON{}}
	idx, err := h.loadTeamIndex(ctx)
	if err != nil {
		return out, err
	}
	var entries []teamAvailEntry
	for _, a := range []string{areaMentoria, areaSoporte} {
		if area != "all" && area != a {
			continue
		}
		t, err := loadTeamType(ctx, h.db, idx.st.templateFor(a))
		if err != nil {
			return out, err
		}
		if t == nil {
			continue
		}
		if !t.archived {
			entries = append(entries, teamAvailEntry{userID: t.userID, area: a, slug: t.slug})
		}
		for _, li := range idx.copiesOf(t.id) {
			if li.active {
				entries = append(entries, teamAvailEntry{userID: li.userID, area: a, slug: li.copySlug})
			}
		}
	}
	users, err := h.teamAvailUsers(ctx, entries)
	if err != nil {
		return out, err
	}
	budget, cancelBudget := context.WithTimeout(ctx, teamAvailTotalBudget)
	defer cancelBudget()
	for _, e := range entries {
		u, ok := users[e.userID]
		if !ok { // archived or gone
			continue
		}
		p := teamAvailPersonJSON{
			UserID: e.userID, Name: u.name, Area: e.area, AvatarURL: u.avatar,
			Link:  teamAvailLinkJSON{Slug: e.slug, URL: h.bookURL(e.slug)},
			Slots: []teamAvailSlotJSON{},
		}
		if accentIsCustom(u.accent) {
			c := strings.ToLower(u.accent)
			p.Color = &c
		}
		if budget.Err() != nil && ctx.Err() == nil {
			// The slow ones before used up the answer's time: say so, keep the link.
			p.Error, p.ErrorKind = true, "timeout"
			out.People = append(out.People, p)
			continue
		}
		// Sequential on purpose (see the file comment); computeSlots drains every cursor.
		pctx, cancelPerson := context.WithTimeout(budget, teamAvailPersonTimeout)
		res, err := h.computeSlots(pctx, e.slug, tzName, from, to, slotsWanted{})
		late := pctx.Err() != nil && ctx.Err() == nil // this person's limit (or the budget) ran out
		cancelPerson()
		switch {
		case errors.Is(err, errEventTypeNotFound):
			continue // the public page would 404: not a link to hand out
		case errors.Is(err, errBadDateRange):
			// The range lies past this link's booking window: nothing to offer.
		case err != nil && late:
			h.logger.WarnContext(ctx, "team availability: person timed out", "error", err, "user", e.userID, "slug", e.slug)
			p.Error, p.ErrorKind = true, "calendar"
		case err != nil:
			h.logger.ErrorContext(ctx, "team availability: person", "error", err, "user", e.userID, "slug", e.slug)
			p.Error, p.ErrorKind = true, "internal"
		case res.Degraded:
			p.Error, p.ErrorKind = true, "calendar"
		default:
			for _, s := range res.Slots {
				p.Slots = append(p.Slots, teamAvailSlotJSON{Start: s.Start, End: s.End})
			}
		}
		out.People = append(out.People, p)
	}
	sort.SliceStable(out.People, func(i, j int) bool {
		a, b := out.People[i], out.People[j]
		if a.Area != b.Area {
			return a.Area == areaMentoria
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out, nil
}

type teamAvailUser struct {
	name, avatar, accent string
}

// teamAvailUsers loads the active users among entries (one query, drained before return).
func (h *Handler) teamAvailUsers(ctx context.Context, entries []teamAvailEntry) (map[string]teamAvailUser, error) {
	out := map[string]teamAvailUser{}
	if len(entries) == 0 {
		return out, nil
	}
	seen := map[string]bool{}
	var ph []string
	var args []any
	for _, e := range entries {
		if !seen[e.userID] {
			seen[e.userID] = true
			ph = append(ph, "?")
			args = append(args, e.userID)
		}
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(avatar_url, ''), COALESCE(booking_accent, '')
		FROM users WHERE archived_at IS NULL AND id IN (`+strings.Join(ph, ",")+`)`, args...) // #nosec G202 -- ph holds only literal "?" placeholders; values are bound
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var u teamAvailUser
		if err := rows.Scan(&id, &u.name, &u.avatar, &u.accent); err != nil {
			return nil, err
		}
		out[id] = u
	}
	return out, rows.Err()
}
