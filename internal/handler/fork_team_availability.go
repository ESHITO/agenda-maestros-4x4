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

// teamAvailCache holds recent answers of both team endpoints - the 7-day list (avail) and
// the calendar (cal, fork_team_calendar.go) - keyed by range + tz + área (IsYou and the
// other per-viewer fields are set on the way out, so one entry serves everybody). It
// lives on the Handler (the zero value is ready), so two Handlers - every test builds its
// own over its own database - never share an answer. ONE generation covers both maps.
type teamAvailCache struct {
	mu sync.Mutex
	// gen moves on every change that may alter somebody's free time
	// (fork_free_time_changes.go); an entry stored under an older generation is a miss.
	gen   atomic.Uint64
	avail teamGenMap[teamAvailabilityJSON]
	cal   teamGenMap[teamCalendarJSON]
	// fl shares one calendar computation among identical requests in flight (the owner
	// and three support people with the same month open launch one set of provider
	// calls, not four).
	fl teamFlight[teamCalendarJSON]
}

// teamCalCacheMax bounds the calendar map (a month answer weighs tens of KB): the oldest
// entry leaves first.
const teamCalCacheMax = 16

type teamGenEntry[T any] struct {
	at  time.Time
	gen uint64
	out T
}

// teamGenMap is one cache map; its methods run under teamAvailCache.mu.
type teamGenMap[T any] struct {
	m map[string]teamGenEntry[T]
}

func (g *teamGenMap[T]) get(key string, now time.Time, gen uint64) (T, bool) {
	e, ok := g.m[key]
	if !ok || e.gen != gen || now.Sub(e.at) >= teamAvailabilityTTL {
		var zero T
		return zero, false
	}
	return e.out, true
}

// put stores out under gen, the generation read BEFORE out was computed: a change that
// landed while it was being computed has moved the generation on (cur), so the entry is
// born stale instead of hiding that change for a whole TTL. max > 0 caps the entries.
func (g *teamGenMap[T]) put(key string, now time.Time, gen, cur uint64, out T, max int) {
	if g.m == nil {
		g.m = map[string]teamGenEntry[T]{}
	}
	for k, e := range g.m { // a handful of entries: purge the stale ones here
		if e.gen != cur || now.Sub(e.at) >= teamAvailabilityTTL {
			delete(g.m, k)
		}
	}
	if gen != cur {
		return
	}
	if _, exists := g.m[key]; !exists && max > 0 {
		for len(g.m) >= max {
			oldest := ""
			for k, e := range g.m {
				if oldest == "" || e.at.Before(g.m[oldest].at) {
					oldest = k
				}
			}
			delete(g.m, oldest)
		}
	}
	g.m[key] = teamGenEntry[T]{at: now, gen: gen, out: out}
}

func teamAvailCacheKey(from, to, tz, area string) string {
	return from + "|" + to + "|" + tz + "|" + area
}

func (c *teamAvailCache) get(key string, now time.Time) (teamAvailabilityJSON, bool) {
	gen := c.gen.Load()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.avail.get(key, now, gen)
}

func (c *teamAvailCache) put(key string, now time.Time, gen uint64, out teamAvailabilityJSON) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.avail.put(key, now, gen, c.gen.Load(), out, 0)
}

func (c *teamAvailCache) getCal(key string, now time.Time) (teamCalendarJSON, bool) {
	gen := c.gen.Load()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cal.get(key, now, gen)
}

func (c *teamAvailCache) putCal(key string, now time.Time, gen uint64, out teamCalendarJSON) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cal.put(key, now, gen, c.gen.Load(), out, teamCalCacheMax)
}

// teamFlight is a minimal singleflight (no new module dependency): while one request
// computes a key, identical requests wait for its result instead of computing again. The
// computation runs on the FIRST request's context; if that request goes away the result is
// marked abandoned, the key is freed, and a waiter still interested computes it again. A
// waiter that goes away leaves on its own ctx.Done().
type teamFlight[T any] struct {
	mu sync.Mutex
	m  map[string]*teamFlightCall[T]
}

type teamFlightCall[T any] struct {
	done      chan struct{}
	out       T
	err       error
	abandoned bool
}

// do runs fn once per key among concurrent callers. shared reports that the result came
// from another caller's computation.
func (f *teamFlight[T]) do(ctx context.Context, key string, fn func() (T, error)) (out T, shared bool, err error) {
	for {
		f.mu.Lock()
		if c, ok := f.m[key]; ok {
			f.mu.Unlock()
			select {
			case <-c.done:
				if c.abandoned {
					if ctx.Err() != nil {
						return out, false, ctx.Err()
					}
					continue // the first caller left: compute it ourselves
				}
				return c.out, true, c.err
			case <-ctx.Done():
				return out, false, ctx.Err()
			}
		}
		c := &teamFlightCall[T]{done: make(chan struct{})}
		if f.m == nil {
			f.m = map[string]*teamFlightCall[T]{}
		}
		f.m[key] = c
		f.mu.Unlock()
		finished := false
		func() {
			defer func() {
				// A panic or a departed first caller: waiters must not take this result.
				c.abandoned = !finished || ctx.Err() != nil
				f.mu.Lock()
				delete(f.m, key)
				f.mu.Unlock()
				close(c.done)
			}()
			c.out, c.err = fn()
			finished = true
		}()
		return c.out, false, c.err
	}
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
	return parseTeamRange(fromStr, toStr, now, loc, teamAvailabilityMaxDays)
}

// parseTeamRange validates from/to (days in loc, both optional) against a cap of maxDays
// days. from defaults to today in loc and to to from+6 (a week, whatever the cap). The
// message is the Spanish 400 ("" = fine).
func parseTeamRange(fromStr, toStr string, now time.Time, loc *time.Location, maxDays int) (string, string, string) {
	y, m, d := now.In(loc).Date()
	from := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if fromStr != "" {
		t, err := time.Parse("2006-01-02", fromStr)
		if err != nil {
			return "", "", "La fecha «from» debe tener el formato AAAA-MM-DD."
		}
		from = t
	}
	to := from.AddDate(0, 0, min(maxDays, 7)-1)
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
	if to.Sub(from) > time.Duration(maxDays-1)*24*time.Hour {
		return "", "", fmt.Sprintf("El rango máximo es de %d días.", maxDays)
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

// teamAvailEntry is one person's link to compute: etID is that link's event type (the
// template itself for its owner, else the person's copy).
type teamAvailEntry struct {
	userID, area, slug, etID string
}

// teamEntries lists the people of área ("all" = both): for each área whose template is
// set, the template's owner with the template (unless archived) and the host of every
// ACTIVE copy of it. Archived users are filtered later (teamAvailUsers).
func (h *Handler) teamEntries(ctx context.Context, idx *teamIndex, area string) ([]teamAvailEntry, error) {
	var entries []teamAvailEntry
	for _, a := range []string{areaMentoria, areaSoporte} {
		if area != "all" && area != a {
			continue
		}
		t, err := loadTeamType(ctx, h.db, idx.st.templateFor(a))
		if err != nil {
			return nil, err
		}
		if t == nil {
			continue
		}
		if !t.archived {
			entries = append(entries, teamAvailEntry{userID: t.userID, area: a, slug: t.slug, etID: t.id})
		}
		for _, li := range idx.copiesOf(t.id) {
			if li.active {
				entries = append(entries, teamAvailEntry{userID: li.userID, area: a, slug: li.copySlug, etID: li.copyID})
			}
		}
	}
	return entries, nil
}

// teamClassify turns one person's computeSlots outcome into the answer's terms: drop = the
// public page would 404 (not a link to hand out); kind = "" (fine - possibly no slots, when
// the range lies past the link's booking window), "calendar" (their external calendar did
// not answer, not in time, or Degraded: booking is fail-closed then) or "internal". late
// means this person's limit (or the answer's budget) ran out while it was computing.
func teamClassify(err error, late bool, res slotsResult) (kind string, drop bool) {
	switch {
	case errors.Is(err, errEventTypeNotFound):
		return "", true
	case errors.Is(err, errBadDateRange):
		return "", false
	case err != nil && late:
		return "calendar", false
	case err != nil:
		return "internal", false
	case res.Degraded:
		return "calendar", false
	}
	return "", false
}

// teamAvailability builds the answer (without IsYou or GeneratedAt).
func (h *Handler) teamAvailability(ctx context.Context, from, to, tzName, area string) (teamAvailabilityJSON, error) {
	out := teamAvailabilityJSON{TZ: tzName, From: from, To: to, People: []teamAvailPersonJSON{}}
	idx, err := h.loadTeamIndex(ctx)
	if err != nil {
		return out, err
	}
	entries, err := h.teamEntries(ctx, idx, area)
	if err != nil {
		return out, err
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
		kind, drop := teamClassify(err, late, res)
		if drop {
			continue // the public page would 404: not a link to hand out
		}
		h.logTeamPersonError(ctx, "team availability", err, kind, e)
		if kind != "" {
			p.Error, p.ErrorKind = true, kind
		} else {
			for _, s := range res.Slots { // none when the range lies past the booking window
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

// logTeamPersonError logs one person's failed computation the way the classification
// reads it (a timeout is a warning, an internal failure an error).
func (h *Handler) logTeamPersonError(ctx context.Context, what string, err error, kind string, e teamAvailEntry) {
	if err == nil {
		return
	}
	switch kind {
	case "calendar":
		h.logger.WarnContext(ctx, what+": person timed out", "error", err, "user", e.userID, "slug", e.slug)
	case "internal":
		h.logger.ErrorContext(ctx, what+": person", "error", err, "user", e.userID, "slug", e.slug)
	}
}

type teamAvailUser struct {
	name, avatar, accent string
	isOwner              bool
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
		SELECT id, name, COALESCE(avatar_url, ''), COALESCE(booking_accent, ''), is_owner
		FROM users WHERE archived_at IS NULL AND id IN (`+strings.Join(ph, ",")+`)`, args...) // #nosec G202 -- ph holds only literal "?" placeholders; values are bound
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var u teamAvailUser
		if err := rows.Scan(&id, &u.name, &u.avatar, &u.accent, &u.isOwner); err != nil {
			return nil, err
		}
		out[id] = u
	}
	return out, rows.Err()
}
