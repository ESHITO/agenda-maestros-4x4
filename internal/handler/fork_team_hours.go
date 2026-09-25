package handler

// Fork (Agenda Maestros 4x4): who has working hours. A person joins the Soporte shared
// type's rotation only when their weekly availability can open a window on it - a global
// rule (event_type_id NULL) or one for S itself, the same rules loadHostSchedule feeds the
// slot engine. Round robin only offers slots where some host is free, so a single host
// with no rules makes the public page show nothing: that is how putting an admin with no
// schedule in área soporte once took S from 319 slots to 0.
//
// Date overrides are left out on purpose: an override replaces one day, it never adds
// weekly hours, and a person whose only hours are a few overrides would drain S the day
// after their last one.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

// teamHHMM parses "HH:MM" the way slots.parseWallClock does (hour 0-23, minute 0-59) and
// returns minutes since midnight.
func teamHHMM(s string) (int, bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	hh, err := strconv.Atoi(parts[0])
	if err != nil || hh < 0 || hh > 23 {
		return 0, false
	}
	mm, err := strconv.Atoi(parts[1])
	if err != nil || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// teamRuleOpens reports whether a weekly rule can open a window at all: the slot engine
// can parse both times and it starts before it ends.
func teamRuleOpens(start, end string) bool {
	s, ok1 := teamHHMM(start)
	e, ok2 := teamHHMM(end)
	return ok1 && ok2 && s < e
}

// teamRule is one weekly rule the slot engine can parse and that starts before it ends.
type teamRule struct {
	etID       string // "" = global
	dow        int
	start, end string
}

// teamUserHours is one person's weekly rules as the slot engine sees them.
type teamUserHours struct {
	loc   *time.Location
	rules []teamRule
	// broken holds the scopes ("" = global, else an event type id) with a rule whose times
	// slots.parseWallClock rejects. The POST does not validate times, and resolveDay fails
	// the WHOLE slot request on such a rule (GetSlots answers 500), so a person with one is
	// never counted for that scope, whatever their other rules. A backwards rule
	// (start >= end) is only an empty window, not an error: it is just skipped.
	broken map[string]bool
}

// teamHours maps user id -> their rules. A user with no rule at all is absent.
type teamHours map[string]*teamUserHours

// usable reports whether userID's rules for event type etID ("" = only global rules)
// can reach the slot engine without failing it.
func (th teamHours) usable(userID, etID string) *teamUserHours {
	u := th[userID]
	if u == nil || u.broken[""] || (etID != "" && u.broken[etID]) {
		return nil
	}
	return u
}

// forType reports whether userID has a usable weekly rule for event type etID ("" = only
// a global rule counts).
func (th teamHours) forType(userID, etID string) bool {
	return th.opensFor(userID, etID, teamSlotShape{})
}

// opensFor is forType where a rule counts only if at least one slot of shape fits in it
// (a zero shape skips that check). The slot engine does not merge rules: each one is its
// own window, the first start is aligned up to the slot interval (epoch-aligned, in UTC),
// and a slot is offered only if it ends inside the window. A Monday 09:00-09:05 rule on a
// 30-minute type opens nothing, so it must not put its owner alone in S's rotation.
func (th teamHours) opensFor(userID, etID string, shape teamSlotShape) bool {
	u := th.usable(userID, etID)
	if u == nil {
		return false
	}
	for _, r := range u.rules {
		if r.etID != "" && r.etID != etID {
			continue
		}
		if shape.dur <= 0 || teamRuleFits(u.loc, r, shape, time.Now()) {
			return true
		}
	}
	return false
}

// any reports whether userID has any rule that opens a window (on some type).
func (th teamHours) any(userID string) bool {
	u := th[userID]
	return u != nil && len(u.rules) > 0
}

// teamSlotShape is what the slot engine needs to know whether a window holds a slot.
type teamSlotShape struct {
	dur, interval time.Duration
}

// loadTeamSlotShape reads event type etID's duration and slot interval.
func loadTeamSlotShape(ctx context.Context, q teamQuerier, etID string) (teamSlotShape, error) {
	var dur, interval int
	if err := q.QueryRowContext(ctx,
		`SELECT duration_minutes, slot_interval_minutes FROM event_types WHERE id = ?`, etID).Scan(&dur, &interval); err != nil {
		return teamSlotShape{}, err
	}
	return teamSlotShape{dur: time.Duration(dur) * time.Minute, interval: time.Duration(interval) * time.Minute}, nil
}

// teamRuleFits reports whether rule r, on its next occurrence from now in loc, holds at
// least one slot of shape - the same window and alignment slots.hostsByStart computes.
func teamRuleFits(loc *time.Location, r teamRule, shape teamSlotShape, now time.Time) bool {
	day := now.UTC().Truncate(24 * time.Hour)
	for int(day.Weekday()) != r.dow {
		day = day.AddDate(0, 0, 1)
	}
	windows, err := slots.ResolveDayWindows(loc, day,
		[]slots.AvailabilityRule{{DayOfWeek: time.Weekday(r.dow), StartTime: r.start, EndTime: r.end}}, nil)
	if err != nil || len(windows) == 0 {
		return false
	}
	w := windows[0]
	t := w.Start
	if secs := int64(shape.interval / time.Second); secs > 0 {
		if rem := t.Unix() % secs; rem != 0 {
			if rem < 0 {
				rem += secs
			}
			t = t.Add(time.Duration(secs-rem) * time.Second)
		}
	}
	return !t.Add(shape.dur).After(w.End)
}

// loadTeamHours reads every availability rule once (a handful per person) with its
// owner's timezone. The cursor is drained before returning (single-connection pool).
func loadTeamHours(ctx context.Context, q teamQuerier) (teamHours, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT r.user_id, COALESCE(r.event_type_id, ''), r.day_of_week, r.start_time, r.end_time, u.iana_timezone
		FROM availability_rules r JOIN users u ON u.id = r.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := teamHours{}
	for rows.Next() {
		var userID, etID, start, end, tz string
		var dow int
		if err := rows.Scan(&userID, &etID, &dow, &start, &end, &tz); err != nil {
			return nil, err
		}
		u := out[userID]
		if u == nil {
			loc, err := time.LoadLocation(tz)
			if err != nil {
				loc = time.UTC // loadHostSchedule's fallback
			}
			u = &teamUserHours{loc: loc, broken: map[string]bool{}}
			out[userID] = u
		}
		_, ok1 := teamHHMM(start)
		_, ok2 := teamHHMM(end)
		switch {
		case !ok1 || !ok2:
			u.broken[etID] = true
		case teamRuleOpens(start, end) && dow >= 0 && dow <= 6:
			u.rules = append(u.rules, teamRule{etID: etID, dow: dow, start: start, end: end})
		}
	}
	return out, rows.Err()
}

// teamStaffMember is an active área-soporte person.
type teamStaffMember struct {
	id, name string
}

// loadSoporteStaff splits the active área-soporte people, in the rotation's stable order
// (created_at, id), into those whose hours reach S (the rotation) and those left waiting
// for a schedule.
func loadSoporteStaff(ctx context.Context, q teamQuerier, supID string) (rotation, waiting []teamStaffMember, err error) {
	rows, err := q.QueryContext(ctx, `
		SELECT u.id, u.name FROM users u JOIN fork_member_areas a ON a.user_id = u.id
		WHERE a.area = ? AND u.archived_at IS NULL
		ORDER BY u.created_at, u.id`, areaSoporte)
	if err != nil {
		return nil, nil, err
	}
	var staff []teamStaffMember
	for rows.Next() {
		var s teamStaffMember
		if err := rows.Scan(&s.id, &s.name); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error
			return nil, nil, err
		}
		staff = append(staff, s)
	}
	rows.Close() // #nosec G104 -- drained
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(staff) == 0 {
		return nil, nil, nil
	}
	shape, err := loadTeamSlotShape(ctx, q, supID)
	if err != nil {
		return nil, nil, err
	}
	hours, err := loadTeamHours(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range staff {
		if hours.opensFor(s.id, supID, shape) {
			rotation = append(rotation, s)
		} else {
			waiting = append(waiting, s)
		}
	}
	return rotation, waiting, nil
}

// teamHoursChecker returns hasHours(userID, area): whether the person's weekly rules can
// open a window on what they attend - Soporte: a global rule or one for S; Mentoría: a
// global rule or one for their copy (the template, for its owner); no área: any rule.
// Best effort for the members list: on a read error it logs and answers true (no false
// "Sin horario" alarm).
func (h *Handler) teamHoursChecker(ctx context.Context) func(userID, area string) bool {
	yes := func(string, string) bool { return true }
	st, err := loadTeamSettings(ctx, h.db)
	if err != nil {
		h.logger.ErrorContext(ctx, "team hours: settings", "error", err)
		return yes
	}
	personal := map[string]string{} // user id -> their Mentoría type id
	if st.templateID != "" {
		rows, err := h.db.QueryContext(ctx, `
			SELECT user_id, copy_id FROM fork_event_type_links WHERE kind = ? AND template_id = ?
			UNION ALL
			SELECT user_id, id FROM event_types WHERE id = ?`, linkKindCopy, st.templateID, st.templateID)
		if err != nil {
			h.logger.ErrorContext(ctx, "team hours: links", "error", err)
			return yes
		}
		for rows.Next() {
			var owner, etID string
			if rows.Scan(&owner, &etID) == nil {
				personal[owner] = etID
			}
		}
		rows.Close() // #nosec G104 -- drained
	}
	var supShape teamSlotShape // zero (no fit check) while no Soporte type is set
	if st.soporteID != "" {
		if supShape, err = loadTeamSlotShape(ctx, h.db, st.soporteID); err != nil {
			h.logger.ErrorContext(ctx, "team hours: soporte shape", "error", err)
			return yes
		}
	}
	hours, err := loadTeamHours(ctx, h.db)
	if err != nil {
		h.logger.ErrorContext(ctx, "team hours: rules", "error", err)
		return yes
	}
	return func(userID, area string) bool {
		switch area {
		case areaSoporte:
			// The same test loadSoporteStaff uses for the rotation, so the note and the
			// "Esperando horario" list agree.
			return hours.opensFor(userID, st.soporteID, supShape)
		case areaMentoria:
			return hours.forType(userID, personal[userID])
		default:
			return hours.any(userID)
		}
	}
}
