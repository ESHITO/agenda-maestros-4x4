package handler

// Fork (Agenda Maestros 4x4): the team calendar - the Panel's Mes / 15 días / Semana view of
// who works when, who is booked, what is still free and which hours NOBODY covers.
//
//	GET /v1/team/calendar?from=YYYY-MM-DD&to=YYYY-MM-DD&tz=<IANA>&area=all|mentoria|soporte[&free=0][&fresh=1]
//
// Who: teamMayViewAvailability (the owner and the área-soporte people), like
// /v1/team/availability; the same people (teamEntries + teamAvailUsers), the same error
// classification (teamClassify) and the same generation cache (h.teamAvail). That endpoint
// stays as it was; this one answers all three panel views with ONE compact shape: per
// person and per day of the VIEWER (tz):
//
//   - hours: the person's working hours on their link, [start, end] in minutes since the
//     viewer's midnight (end may be 1440). Read from the DB only (loadHostSchedule +
//     slots.ResolveDayWindows, the loader and resolver computeSlots uses - never a second
//     slot engine), merged, and a span shorter than one session dropped (teamRuleFits'
//     rule). Only from today on: today's rules painted over past days would invent a false
//     history.
//   - free: the bookable starts, minutes - computeSlots(slug, tz, max(from, today), to,
//     slotsWanted{}), exactly what the public page offers. Ends are start + duration_min.
//   - busy: the person's sessions (status != 'cancelled', any type: computeSlots subtracts
//     them all), with an opaque per-answer key ("b1", "b2"...), the type's name (or "Otra
//     reunión" for a type outside the team - it could name a client) and its área. Every
//     row of a person carries ALL their sessions (the owner has a Mentoría and a Soporte
//     row); the panel de-duplicates by key. Past days carry only these.
//
// No booking id ever leaves (GET /v1/bookings/{id} is public) and nothing about the client.
//
// coverage: per requested área whose template is set, per day from today on, the target
// (fork_team_coverage.go) and the parts of it that no person of that área works
// ("uncovered") or only the owner works ("owner_only", only where the owner is a person of
// that área - the owner's global hours cover both áreas and would hide the gaps). Hours,
// not free starts, decide coverage, so it never waits for a calendar provider.
//
// Range: at most 31 days ("Mes" asks the 1st to the last day; book.html already asks one
// month per person, so the provider windows are ones production already uses) and never
// past today + 366. Days are the viewer's, like /slots.
//
// free=0 answers only what the DB knows (milliseconds): the panel paints that first, then
// asks the full answer. Provider calls run on teamCalendarWorkers workers, a person's rows
// always on the SAME worker one after the other (each FreeBusy builds its own token source
// from the DB: two calls at once for one user would refresh the token twice, and Microsoft
// rotates refresh tokens). The DB still serializes on its one connection; only the network
// overlaps (computeSlots' own comment). Each person gets teamCalPersonTimeout and the whole
// answer teamCalTotalBudget counted from the request's arrival (< the 30 s WriteTimeout).
// Clean answers are cached teamAvailabilityTTL; identical requests in flight share one
// computation (teamFlight); fresh=1 skips the cache read.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

const (
	teamCalendarMaxDays = 31
	teamCalendarWorkers = 3
	// teamCalendarMaxAhead: to may not lie further than this many days after today.
	teamCalendarMaxAhead = 366
	// teamCalOtherMeeting names a session of a type outside the team.
	teamCalOtherMeeting = "Otra reunión"
	// teamOwnerDefaultColor: the owner on the default accent is the brand turquoise.
	teamOwnerDefaultColor = "#4fd7ff"
)

// Time limits of one answer (vars so the tests can shorten them).
var (
	teamCalPersonTimeout = 6 * time.Second  // a month of free/busy: Graph may page
	teamCalTotalBudget   = 22 * time.Second // from request arrival; < 30 s WriteTimeout
)

// teamCalPalette colours the people who did not choose one, in name order over the whole
// team (so filtering an área never repaints anybody). It holds none of the coverage hues:
// no red (red hatch = "Sin cubrir"), no orange or amber (amber dots = "Solo el
// propietario"; a session in a person's colour is drawn as stripes and would read as it)
// and no green or teal (green = "Cubierto"). A colour somebody chose is skipped while
// others remain; an accent a person CHOSE may still be one of those hues.
var teamCalPalette = []string{
	"#2563eb", "#9333ea", "#db2777", "#0891b2", "#4f46e5", "#c026d3",
	"#78716c", "#1e3a8a", "#be185d", "#7c3aed", "#475569", "#0369a1",
}

type teamCalSpan [2]int // minutes since the viewer's midnight; end may be 1440

type teamCalBusyJSON struct {
	Key   string `json:"key"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Type  string `json:"type"`
	Area  string `json:"area"`
}

type teamCalDayJSON struct {
	Hours []teamCalSpan     `json:"hours,omitempty"`
	Free  []int             `json:"free,omitempty"`
	Busy  []teamCalBusyJSON `json:"busy,omitempty"`
}

type teamCalPersonJSON struct {
	Key           string            `json:"key"`
	UserID        string            `json:"user_id"`
	Name          string            `json:"name"`
	Area          string            `json:"area"`
	AvatarURL     string            `json:"avatar_url"`
	Color         string            `json:"color"`
	ColorCustom   bool              `json:"color_custom"`
	IsOwner       bool              `json:"is_owner"`
	IsYou         bool              `json:"is_you"`
	Link          teamAvailLinkJSON `json:"link"`
	DurationMin   int               `json:"duration_min"`
	NoticeUntil   string            `json:"notice_until,omitempty"`
	BookableUntil string            `json:"bookable_until,omitempty"`
	// Error / ErrorKind: "calendar" and "timeout" (free starts unknown; hours and sessions
	// still there, and the hours count for coverage) as in /v1/team/availability - any
	// computeSlots failure of a row whose hours were read is one of these; "internal" ONLY
	// when the hours themselves could not be read (a rule or override the engine cannot
	// parse: no hours either, and the row does not count for coverage).
	Error     bool                       `json:"error,omitempty"`
	ErrorKind string                     `json:"error_kind,omitempty"`
	Days      map[string]*teamCalDayJSON `json:"days"`
}

type teamCalCoverageDayJSON struct {
	Target    []teamCalSpan `json:"target"`
	Uncovered []teamCalSpan `json:"uncovered,omitempty"`
	OwnerOnly []teamCalSpan `json:"owner_only,omitempty"`
}

type teamCalCoverageJSON struct {
	Days map[string]*teamCalCoverageDayJSON `json:"days"`
}

type teamCalendarJSON struct {
	TZ           string                         `json:"tz"`
	From         string                         `json:"from"`
	To           string                         `json:"to"`
	Today        string                         `json:"today"`
	GeneratedAt  string                         `json:"generated_at"`
	FreeIncluded bool                           `json:"free_included"`
	Target       teamCoverageTargetJSON         `json:"target"`
	People       []teamCalPersonJSON            `json:"people"`
	Coverage     map[string]teamCalCoverageJSON `json:"coverage"`
}

func teamCalCacheKey(from, to, tz, area string, withFree bool) string {
	f := "0"
	if withFree {
		f = "1"
	}
	return "cal|" + from + "|" + to + "|" + tz + "|" + area + "|" + f
}

// GetTeamCalendar handles GET /v1/team/calendar.
func (h *Handler) GetTeamCalendar(w http.ResponseWriter, r *http.Request) {
	arrival := time.Now()
	ctx := r.Context()
	user, _ := userFromContext(ctx)
	ok, err := h.teamMayViewAvailability(ctx, user)
	if err != nil {
		h.logger.ErrorContext(ctx, "team calendar: permission", "error", err)
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
	from, to, msg := parseTeamRange(q.Get("from"), q.Get("to"), arrival, loc, teamCalendarMaxDays)
	if msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	toD, _ := time.Parse("2006-01-02", to)
	if toD.After(viewerToday(arrival, loc).AddDate(0, 0, teamCalendarMaxAhead)) {
		h.writeError(w, http.StatusBadRequest, "No se puede consultar más allá de un año.")
		return
	}
	withFree := true
	switch q.Get("free") {
	case "", "1":
	case "0":
		withFree = false
	default:
		h.writeError(w, http.StatusBadRequest, "«free» debe ser 0 o 1.")
		return
	}

	key := teamCalCacheKey(from, to, tzName, area, withFree)
	out, hit := teamCalendarJSON{}, false
	if q.Get("fresh") != "1" {
		out, hit = h.teamAvail.getCal(key, arrival)
	}
	if !hit {
		gen := h.teamAvailGeneration() // before computing: see teamGenMap.put
		deadline := arrival.Add(teamCalTotalBudget)
		// The generation is part of the flight key: a request that comes after a write never
		// joins a computation that started before it.
		out, _, err = h.teamAvail.fl.do(ctx, key+"|"+strconv.FormatUint(gen, 10), func() (teamCalendarJSON, error) {
			res, err := h.teamCalendar(ctx, deadline, from, to, tzName, area, withFree)
			if err != nil {
				return res, err
			}
			res.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
			clean := true
			for _, p := range res.People {
				clean = clean && !p.Error
			}
			if clean && ctx.Err() == nil { // an error is worth retrying at once, never cached
				h.teamAvail.putCal(key, arrival, gen, res)
			}
			return res, nil
		})
		if err != nil {
			if ctx.Err() == nil {
				h.logger.ErrorContext(ctx, "team calendar: build", "error", err)
			}
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	// Per viewer, on a copy: the cached answer is shared.
	people := make([]teamCalPersonJSON, len(out.People))
	copy(people, out.People)
	for i := range people {
		people[i].IsYou = people[i].UserID == user.ID
	}
	out.People = people
	out.Target.CanEdit = user.IsOwner
	h.writeJSON(w, http.StatusOK, out)
}

// teamCalRow is one person's row while the answer is built.
type teamCalRow struct {
	entry teamAvailEntry
	p     teamCalPersonJSON
	hours []slots.Interval
	// hoursOK: hours were computed (the range reaches today and the rules could be read).
	hoursOK bool
	drop    bool
}

func (row *teamCalRow) day(key string) *teamCalDayJSON {
	d := row.p.Days[key]
	if d == nil {
		d = &teamCalDayJSON{}
		row.p.Days[key] = d
	}
	return d
}

// viewerToday is today in loc as a UTC midnight naming the day.
func viewerToday(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// viewerMidnight is the first instant of the day named by day (a UTC midnight) in loc. When
// a DST change skips midnight (America/Santiago, early September), time.Date may land on
// 23:00 of the PREVIOUS day (Go does not guarantee the side); the day then really starts
// at the transition, which is the end of the zone in effect at that instant - 01:00 on the
// new clock, so that day starts at minute 60.
func viewerMidnight(day time.Time, loc *time.Location) time.Time {
	t := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	if y, m, d := t.In(loc).Date(); y != day.Year() || m != day.Month() || d != day.Day() {
		if _, end := t.ZoneBounds(); !end.IsZero() {
			t = end
		}
	}
	return t
}

// viewerMinutes is t's wall clock in loc, in minutes since midnight.
func viewerMinutes(t time.Time, loc *time.Location) int {
	lt := t.In(loc)
	return lt.Hour()*60 + lt.Minute()
}

// splitByViewerDay cuts iv at every midnight of loc and calls f with each piece that falls
// on a day in [from, to] (UTC midnights naming the viewer's days), as wall-clock minutes:
// a piece that reaches the next midnight ends at 1440. When a DST change repeats an hour
// and the end's clock reads at or before the start's, the end is start + elapsed minutes
// (capped at 1440), so the span keeps its real length.
func splitByViewerDay(iv slots.Interval, loc *time.Location, from, to time.Time, f func(day string, s, e int)) {
	if iv.IsEmpty() {
		return
	}
	day := viewerToday(iv.Start, loc)
	if day.Before(from) {
		day = from
	}
	for ; !day.After(to); day = day.AddDate(0, 0, 1) {
		mid := viewerMidnight(day, loc)
		if !mid.Before(iv.End) {
			return
		}
		next := viewerMidnight(day.AddDate(0, 0, 1), loc)
		s, e := iv.Start, iv.End
		if s.Before(mid) {
			s = mid
		}
		if e.After(next) {
			e = next
		}
		if !e.After(s) {
			continue
		}
		sm := viewerMinutes(s, loc)
		em := 1440
		if !e.Equal(next) {
			em = viewerMinutes(e, loc)
		}
		if em <= sm {
			em = min(sm+int(e.Sub(s)/time.Minute), 1440)
		}
		f(day.Format("2006-01-02"), sm, em)
	}
}

// teamCalendar builds the answer (without IsYou, CanEdit or GeneratedAt). Every query is
// drained before the next one opens (single-connection pool) and everything the DB knows
// is read BEFORE any provider call.
func (h *Handler) teamCalendar(ctx context.Context, deadline time.Time, from, to, tzName, area string, withFree bool) (teamCalendarJSON, error) {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return teamCalendarJSON{}, err
	}
	now := time.Now()
	fromD, _ := time.Parse("2006-01-02", from)
	toD, _ := time.Parse("2006-01-02", to)
	todayD := viewerToday(now, loc)
	out := teamCalendarJSON{
		TZ: tzName, From: from, To: to, Today: todayD.Format("2006-01-02"), FreeIncluded: withFree,
		People: []teamCalPersonJSON{}, Coverage: map[string]teamCalCoverageJSON{},
	}

	// 1. The team, its users, the target.
	idx, err := h.loadTeamIndex(ctx)
	if err != nil {
		return out, err
	}
	entries, err := h.teamEntries(ctx, idx, "all") // colours over the whole team
	if err != nil {
		return out, err
	}
	users, err := h.teamAvailUsers(ctx, entries)
	if err != nil {
		return out, err
	}
	if out.Target, err = h.loadCoverageTarget(ctx); err != nil {
		return out, err
	}
	colors := teamCalColors(entries, users)
	configured := map[string]bool{}
	for _, a := range []string{areaMentoria, areaSoporte} {
		if area != "all" && area != a {
			continue
		}
		t, err := loadTeamType(ctx, h.db, idx.st.templateFor(a))
		if err != nil {
			return out, err
		}
		configured[a] = t != nil && !t.archived
	}

	// 2. The rows: a link the public page 404s (paused, not public) is left out before any
	// computation, and counts for nobody's coverage.
	var rows []*teamCalRow
	for _, e := range entries {
		if area != "all" && e.area != area {
			continue
		}
		u, ok := users[e.userID]
		if !ok { // archived or gone
			continue
		}
		et, err := h.teamBookableEventType(ctx, e.slug)
		if err != nil {
			return out, err // never cached; a dropped row would read as «Sin cubrir»
		}
		if et == nil {
			continue
		}
		c := colors[e.userID]
		row := &teamCalRow{entry: e, p: teamCalPersonJSON{
			Key: e.area + ":" + e.userID, UserID: e.userID, Name: u.name, Area: e.area,
			AvatarURL: u.avatar, Color: c.color, ColorCustom: c.custom, IsOwner: u.isOwner,
			Link:        teamAvailLinkJSON{Slug: e.slug, URL: h.bookURL(e.slug)},
			DurationMin: et.DurationMinutes,
			Days:        map[string]*teamCalDayJSON{},
		}}
		if et.MinNoticeMinutes > 0 {
			row.p.NoticeUntil = now.Add(time.Duration(et.MinNoticeMinutes) * time.Minute).In(loc).Format(time.RFC3339)
		}
		maxFuture := et.MaxFutureDays
		if maxFuture <= 0 {
			maxFuture = 365 // the engine's guard (slots.newParams)
		}
		row.p.BookableUntil = now.Add(time.Duration(maxFuture) * 24 * time.Hour).In(loc).Format(time.RFC3339)
		rows = append(rows, row)
	}

	// 3. Working hours, from today on.
	hasFuture := !toD.Before(todayD)
	futureFrom := fromD
	if futureFrom.Before(todayD) {
		futureFrom = todayD
	}
	rangeStart, rangeEnd := viewerMidnight(futureFrom, loc), viewerMidnight(toD.AddDate(0, 0, 1), loc)
	if hasFuture {
		for _, row := range rows {
			dur := time.Duration(row.p.DurationMin) * time.Minute
			hours, err := h.teamWorkingHours(ctx, row.entry.userID, row.entry.etID, dur, rangeStart, rangeEnd)
			if err != nil {
				if ctx.Err() != nil {
					return out, ctx.Err()
				}
				// The same rule that makes GetSlots answer 500: no hours, no free starts.
				h.logger.ErrorContext(ctx, "team calendar: hours", "error", err, "user", row.entry.userID, "slug", row.entry.slug)
				row.p.Error, row.p.ErrorKind = true, "internal"
				continue
			}
			row.hours, row.hoursOK = hours, true
			for _, iv := range hours {
				splitByViewerDay(iv, loc, futureFrom, toD, func(day string, s, e int) {
					d := row.day(day)
					d.Hours = append(d.Hours, teamCalSpan{s, e})
				})
			}
		}
	}

	// 4. Sessions: one query for everybody, drained.
	var ids []string
	seen := map[string]bool{}
	for _, row := range rows {
		if !seen[row.entry.userID] {
			seen[row.entry.userID] = true
			ids = append(ids, row.entry.userID)
		}
	}
	busy, err := h.teamBusyRows(ctx, ids, viewerMidnight(fromD, loc), rangeEnd)
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		for _, b := range busy {
			if b.userID != row.entry.userID {
				continue
			}
			typ, bArea := teamCalOtherMeeting, idx.areaOf(b.etID)
			if bArea != "" {
				typ = b.etName
			}
			splitByViewerDay(slots.Interval{Start: b.start, End: b.end}, loc, fromD, toD, func(day string, s, e int) {
				d := row.day(day)
				d.Busy = append(d.Busy, teamCalBusyJSON{Key: b.key, Start: s, End: e, Type: typ, Area: bArea})
			})
		}
	}

	// 5. Free starts (provider calls), from today on.
	if withFree && hasFuture {
		var todo []*teamCalRow
		for _, row := range rows {
			if row.hoursOK {
				todo = append(todo, row)
			}
		}
		futureFromStr := futureFrom.Format("2006-01-02")
		h.teamRunFree(ctx, deadline, todo, tzName, futureFromStr, to, func(i int, res slotsResult, kind string, drop bool) {
			row := todo[i]
			switch {
			case drop:
				row.drop = true
			case kind != "":
				row.p.Error, row.p.ErrorKind = true, teamCalFreeErrorKind(kind)
			default:
				for _, s := range res.Slots {
					t, err := time.Parse(time.RFC3339, s.Start)
					if err != nil {
						continue
					}
					day := viewerToday(t, loc)
					if day.Before(futureFrom) || day.After(toD) {
						continue
					}
					d := row.day(day.Format("2006-01-02"))
					d.Free = append(d.Free, viewerMinutes(t, loc))
				}
			}
		})
	}

	// 6. Keep, order, and measure coverage.
	kept := rows[:0]
	for _, row := range rows {
		if !row.drop {
			kept = append(kept, row)
		}
	}
	rows = kept
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].p, rows[j].p
		if a.Area != b.Area {
			return a.Area == areaMentoria
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	for _, row := range rows {
		out.People = append(out.People, row.p)
	}
	if hasFuture {
		tloc, rules, err := coverageRules(out.Target)
		if err != nil {
			return out, err
		}
		target := teamTargetIntervals(tloc, rules, rangeStart, rangeEnd)
		for _, a := range []string{areaMentoria, areaSoporte} {
			if !configured[a] {
				continue
			}
			out.Coverage[a] = teamAreaCoverage(rows, a, target, loc, futureFrom, toD)
		}
	}
	return out, nil
}

// teamCalFreeErrorKind is the error_kind of a row whose free starts failed (step 5). Only
// rows whose hours WERE read get there, so teamClassify's "internal" (a quick computeSlots
// failure: a DB or host-availability error) means "free starts unknown", not "the schedule
// cannot be read": it becomes "calendar". "internal" stays step 3's, the one the panel
// blames on the person's rules, and such a row never counts for coverage.
func teamCalFreeErrorKind(kind string) string {
	if kind == "internal" {
		return "calendar"
	}
	return kind
}

// teamBookableEventType is loadBookableEventType with "not bookable" told apart from "could
// not read". loadBookableEventType maps EVERY failure, a DB error included, to
// errEventTypeNotFound; here a transient read error must not drop a real person (their
// hours would then read as «Sin cubrir» in an answer cached as clean and shared). nil, nil
// = the public page 404s this link (no row, paused or not public); an error = 500, never
// cached. Two small queries, each drained (QueryRow) before the next.
func (h *Handler) teamBookableEventType(ctx context.Context, slug string) (*bookableEventType, error) {
	var active, public int
	err := h.db.QueryRowContext(ctx, `SELECT is_active, is_public FROM event_types WHERE slug = ?`, slug).Scan(&active, &public)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("team calendar: read event type %q: %w", slug, err)
	case active == 0 || public == 0:
		return nil, nil
	}
	et, err := h.loadBookableEventType(ctx, slug)
	if err != nil {
		// Bookable a moment ago: a failure now is a read error (or a pause in between, which
		// the retry shows) - never a silent drop.
		return nil, fmt.Errorf("team calendar: load event type %q: %w", slug, err)
	}
	return et, nil
}

// teamCalColor is one person's colour in the calendar.
type teamCalColor struct {
	color  string
	custom bool
}

// teamCalColors gives every person of the team one colour: the accent they chose; the
// owner on the default, the brand turquoise; everybody else a palette colour in name order
// (then id), skipping colours already taken while some remain.
func teamCalColors(entries []teamAvailEntry, users map[string]teamAvailUser) map[string]teamCalColor {
	out := map[string]teamCalColor{}
	used := map[string]bool{}
	var rest []string
	for _, e := range entries {
		u, ok := users[e.userID]
		if !ok {
			continue
		}
		if _, done := out[e.userID]; done || slices.Contains(rest, e.userID) {
			continue
		}
		switch {
		case accentIsCustom(u.accent):
			c := strings.ToLower(u.accent)
			out[e.userID] = teamCalColor{color: c, custom: true}
			used[c] = true
		case u.isOwner:
			out[e.userID] = teamCalColor{color: teamOwnerDefaultColor}
			used[teamOwnerDefaultColor] = true
		default:
			rest = append(rest, e.userID)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		a, b := strings.ToLower(users[rest[i]].name), strings.ToLower(users[rest[j]].name)
		if a != b {
			return a < b
		}
		return rest[i] < rest[j]
	})
	next := 0
	for _, id := range rest {
		c := ""
		for tries := 0; tries < len(teamCalPalette); tries++ {
			cand := teamCalPalette[(next+tries)%len(teamCalPalette)]
			if !used[cand] {
				c = cand
				next = (next + tries + 1) % len(teamCalPalette)
				break
			}
		}
		if c == "" { // every palette colour taken: reuse in order
			c = teamCalPalette[next%len(teamCalPalette)]
			next++
		}
		used[c] = true
		out[id] = teamCalColor{color: c}
	}
	return out
}

// teamWorkingHours is a person's working hours on event type etID within [from, to) - the
// windows the slot engine walks: loadHostSchedule's rules and overrides resolved per host
// day (one extra host day each side, like computeSlots), merged, and every window shorter
// than one session (dur) dropped, as has_availability's teamRuleFits does. An unreadable
// rule or override is an error (GetSlots answers 500 on it).
func (h *Handler) teamWorkingHours(ctx context.Context, userID, etID string, dur time.Duration, from, to time.Time) ([]slots.Interval, error) {
	hostLoc, rules, overrides, err := h.loadHostSchedule(ctx, userID, etID)
	if err != nil {
		return nil, err
	}
	first := viewerToday(from, hostLoc).AddDate(0, 0, -1)
	last := viewerToday(to, hostLoc).AddDate(0, 0, 1)
	var all []slots.Interval
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		w, err := slots.ResolveDayWindows(hostLoc, d, rules, overrides)
		if err != nil {
			return nil, err
		}
		all = append(all, w...)
	}
	var out []slots.Interval
	for _, iv := range slots.MergeIntervals(all) {
		if iv.End.Sub(iv.Start) < dur {
			continue
		}
		if iv.Start.Before(from) {
			iv.Start = from
		}
		if iv.End.After(to) {
			iv.End = to
		}
		if !iv.IsEmpty() {
			out = append(out, iv)
		}
	}
	return out, nil
}

// teamBusyRow is one session of one person. key is opaque and per answer: the booking id
// never leaves teamBusyRows.
type teamBusyRow struct {
	key, userID  string
	start, end   time.Time
	etID, etName string
}

// teamBusyRows loads every non-cancelled session (Stripe holds pending payment included,
// like hostAvailability) of userIDs overlapping [fromUTC, toUTC), in ONE query drained
// before returning. Keys "b1", "b2"... follow (start, id) order, one per booking.
func (h *Handler) teamBusyRows(ctx context.Context, userIDs []string, fromUTC, toUTC time.Time) ([]teamBusyRow, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	idsJSON, err := json.Marshal(userIDs)
	if err != nil {
		return nil, err
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT b.id, bh.user_id, b.start_at, b.end_at, COALESCE(b.event_type_id, ''), COALESCE(et.name, '')
		FROM bookings b JOIN booking_hosts bh ON bh.booking_id = b.id
		LEFT JOIN event_types et ON et.id = b.event_type_id
		WHERE b.status != 'cancelled' AND b.start_at < ? AND b.end_at > ?
		  AND bh.user_id IN (SELECT value FROM json_each(?))
		ORDER BY b.start_at, b.id`,
		toUTC.UTC().Format(time.RFC3339), fromUTC.UTC().Format(time.RFC3339), string(idsJSON))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]string{}
	var out []teamBusyRow
	for rows.Next() {
		var id, startStr, endStr string
		var r teamBusyRow
		if err := rows.Scan(&id, &r.userID, &startStr, &endStr, &r.etID, &r.etName); err != nil {
			return nil, err
		}
		s, err1 := time.Parse(time.RFC3339Nano, startStr)
		e, err2 := time.Parse(time.RFC3339Nano, endStr)
		if err1 != nil || err2 != nil || !e.After(s) {
			continue
		}
		r.start, r.end = s, e
		k, ok := keys[id]
		if !ok {
			k = "b" + strconv.Itoa(len(keys)+1)
			keys[id] = k
		}
		r.key = k
		out = append(out, r)
	}
	return out, rows.Err()
}

// teamRunFree computes the free starts of rows on teamCalendarWorkers workers. The rows of
// one user always go to the same worker, one after the other (see the file comment). Each
// row gets teamCalPersonTimeout; a row the deadline no longer reaches is "timeout". each
// is called once per row (never concurrently).
func (h *Handler) teamRunFree(ctx context.Context, deadline time.Time, rows []*teamCalRow, tz, from, to string, each func(i int, res slotsResult, kind string, drop bool)) {
	if len(rows) == 0 {
		return
	}
	var groups [][]int
	byUser := map[string]int{}
	for i, row := range rows {
		g, ok := byUser[row.entry.userID]
		if !ok {
			g = len(groups)
			byUser[row.entry.userID] = g
			groups = append(groups, nil)
		}
		groups[g] = append(groups[g], i)
	}
	budget, cancelBudget := context.WithDeadline(ctx, deadline)
	defer cancelBudget()
	var mu sync.Mutex
	report := func(i int, res slotsResult, kind string, drop bool) {
		mu.Lock()
		defer mu.Unlock()
		each(i, res, kind, drop)
	}
	work := make(chan []int)
	var wg sync.WaitGroup
	for w := 0; w < min(teamCalendarWorkers, len(groups)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range work {
				for _, i := range g {
					row := rows[i]
					if budget.Err() != nil && ctx.Err() == nil {
						report(i, slotsResult{}, "timeout", false)
						continue
					}
					pctx, cancelPerson := context.WithTimeout(budget, teamCalPersonTimeout)
					res, err := h.computeSlots(pctx, row.entry.slug, tz, from, to, slotsWanted{})
					late := pctx.Err() != nil && ctx.Err() == nil
					cancelPerson()
					kind, drop := teamClassify(err, late, res)
					h.logTeamPersonError(ctx, "team calendar", err, kind, row.entry)
					report(i, res, kind, drop)
				}
			}
		}()
	}
	for _, g := range groups {
		work <- g
	}
	close(work)
	wg.Wait()
}

// teamTargetIntervals is the target as instants within [start, end): each target-zone day
// that can touch the range, its weekday's span resolved in the target zone ("24:00" = the
// next midnight there), clipped and merged.
func teamTargetIntervals(tloc *time.Location, rules map[time.Weekday]teamCoverageRule, start, end time.Time) []slots.Interval {
	var out []slots.Interval
	first := viewerToday(start, tloc).AddDate(0, 0, -1)
	last := viewerToday(end, tloc).AddDate(0, 0, 1)
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		r, ok := rules[d.Weekday()]
		if !ok {
			continue
		}
		s := time.Date(d.Year(), d.Month(), d.Day(), r.startMin/60, r.startMin%60, 0, 0, tloc)
		if r.startMin == 0 {
			s = viewerMidnight(d, tloc) // a skipped midnight must not reach into the day before
		}
		var e time.Time
		if r.endMin == 1440 {
			e = viewerMidnight(d.AddDate(0, 0, 1), tloc)
		} else {
			e = time.Date(d.Year(), d.Month(), d.Day(), r.endMin/60, r.endMin%60, 0, 0, tloc)
		}
		if s.Before(start) {
			s = start
		}
		if e.After(end) {
			e = end
		}
		if e.After(s) {
			out = append(out, slots.Interval{Start: s.UTC(), End: e.UTC()})
		}
	}
	return slots.MergeIntervals(out)
}

// teamAreaCoverage measures área a: the target, the parts no person of a works
// (uncovered) and, when the owner is one of them, the parts only the owner works
// (owner_only). Rows whose hours could not be read do not count.
func teamAreaCoverage(rows []*teamCalRow, a string, target []slots.Interval, loc *time.Location, from, to time.Time) teamCalCoverageJSON {
	var all, others []slots.Interval
	ownerIn := false
	for _, row := range rows {
		if row.entry.area != a || !row.hoursOK {
			continue
		}
		all = append(all, row.hours...)
		if row.p.IsOwner {
			ownerIn = true
		} else {
			others = append(others, row.hours...)
		}
	}
	uncovered := slots.SubtractIntervals(target, all)
	var ownerOnly []slots.Interval
	if ownerIn {
		ownerOnly = slots.SubtractIntervals(slots.SubtractIntervals(target, others), uncovered)
	}
	cov := teamCalCoverageJSON{Days: map[string]*teamCalCoverageDayJSON{}}
	day := func(k string) *teamCalCoverageDayJSON {
		d := cov.Days[k]
		if d == nil {
			d = &teamCalCoverageDayJSON{Target: []teamCalSpan{}}
			cov.Days[k] = d
		}
		return d
	}
	for _, iv := range target {
		splitByViewerDay(iv, loc, from, to, func(k string, s, e int) { day(k).Target = append(day(k).Target, teamCalSpan{s, e}) })
	}
	for _, iv := range uncovered {
		splitByViewerDay(iv, loc, from, to, func(k string, s, e int) { day(k).Uncovered = append(day(k).Uncovered, teamCalSpan{s, e}) })
	}
	for _, iv := range ownerOnly {
		splitByViewerDay(iv, loc, from, to, func(k string, s, e int) { day(k).OwnerOnly = append(day(k).OwnerOnly, teamCalSpan{s, e}) })
	}
	return cov
}
