package handler

// Fork (Agenda Maestros 4x4): the hours the team wants covered - the "meta de cobertura"
// the team calendar (fork_team_calendar.go) measures every área against, so the owner and
// the support people SEE the hours nobody attends yet ("Sin cubrir") and can fill them
// with new people.
//
//	GET /v1/team/coverage-target   owner + área soporte (teamMayViewAvailability)
//	PUT /v1/team/coverage-target   owner only    {"tz": "<IANA>", "days": [{"dow", "start", "end"}]}
//
// ONE target for the whole team, applied to each área separately. Stored in fork_settings
// under team_coverage_target as {"v":1,"tz":…,"days":[…]} (no goose migration, no new
// table). "v" leaves room for a per-área target later without breaking the stored value.
// dow is Go's weekday (0 = Sunday … 6 = Saturday), at most one span per day; times are
// "HH:MM" on 30-minute steps, start < end, and end may be "24:00" (until midnight).
// days: [] is valid and means "no target" (no gap is ever marked). With no saved row - or
// a saved value that stopped being valid (a zone renamed by a tzdata update), which is
// ignored like loadMorningSetting does - the default is Monday to Saturday 08:00-20:00 in
// America/Lima, Sunday free. A failed READ of the setting is never the default: both
// endpoints answer 500 (never cached) instead.
//
// A PUT is a 2xx write, so FreeTimeChanges moves the team cache's generation and every
// cached calendar answer (which carries the target) goes stale.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const forkKeyTeamCoverageTarget = "team_coverage_target"

// teamCoverageDayJSON is one weekday's span of the target.
type teamCoverageDayJSON struct {
	Dow   int    `json:"dow"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// teamCoverageTargetJSON is the target as the API shows it. IsDefault: nothing valid is
// saved; CanEdit: the viewer may PUT it (the owner) - set per viewer, never cached.
type teamCoverageTargetJSON struct {
	V         int                   `json:"v"`
	TZ        string                `json:"tz"`
	Days      []teamCoverageDayJSON `json:"days"`
	IsDefault bool                  `json:"is_default"`
	CanEdit   bool                  `json:"can_edit"`
}

// teamCoverageStored is the fork_settings value.
type teamCoverageStored struct {
	V    int                   `json:"v"`
	TZ   string                `json:"tz"`
	Days []teamCoverageDayJSON `json:"days"`
}

// defaultCoverageTarget: Monday to Saturday 08:00-20:00, Lima (the owner's clock).
func defaultCoverageTarget() teamCoverageTargetJSON {
	t := teamCoverageTargetJSON{V: 1, TZ: "America/Lima", Days: []teamCoverageDayJSON{}, IsDefault: true}
	for dow := 1; dow <= 6; dow++ {
		t.Days = append(t.Days, teamCoverageDayJSON{Dow: dow, Start: "08:00", End: "20:00"})
	}
	return t
}

// coverageClock parses a target time: "HH:MM" on a 30-minute step, "24:00" only when
// allow24. Returns minutes since midnight.
func coverageClock(s string, allow24 bool) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	hh, err1 := strconv.Atoi(s[:2])
	mm, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil || (mm != 0 && mm != 30) {
		return 0, false
	}
	if hh == 24 && mm == 0 && allow24 {
		return 1440, true
	}
	if hh < 0 || hh > 23 {
		return 0, false
	}
	return hh*60 + mm, true
}

// validateCoverageTarget checks a target and returns its days sorted by weekday. The
// message is the Spanish 400 ("" = fine).
func validateCoverageTarget(tz string, days []teamCoverageDayJSON) ([]teamCoverageDayJSON, string) {
	if !validFixedZone(tz) {
		return nil, "Zona horaria inválida: " + tz
	}
	seen := map[int]bool{}
	out := make([]teamCoverageDayJSON, 0, len(days))
	for _, d := range days {
		if d.Dow < 0 || d.Dow > 6 {
			return nil, "El día debe ir de 0 (domingo) a 6 (sábado)."
		}
		if seen[d.Dow] {
			return nil, "Cada día admite un solo horario a cubrir."
		}
		seen[d.Dow] = true
		s, ok1 := coverageClock(d.Start, false)
		e, ok2 := coverageClock(d.End, true)
		if !ok1 || !ok2 {
			return nil, "Las horas deben tener el formato HH:MM en pasos de 30 minutos (el fin puede ser 24:00)."
		}
		if s >= e {
			return nil, "La hora de inicio debe ser anterior a la de fin."
		}
		out = append(out, teamCoverageDayJSON{Dow: d.Dow, Start: d.Start, End: d.End})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dow < out[j].Dow })
	return out, ""
}

// forkSettingStrict reads one fork_settings key and, unlike forkSettings (whose callers
// fall back to an env value, so it swallows errors), reports a failed read: ok = false with
// a nil error means only "no row". For a value whose fallback would be WRONG data rather
// than a harmless default. One QueryRow, drained before it returns.
func (h *Handler) forkSettingStrict(ctx context.Context, key string) (value string, ok bool, err error) {
	err = h.db.QueryRowContext(ctx, `SELECT value FROM fork_settings WHERE key = ?`, key).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("read fork setting %q: %w", key, err)
	}
	return value, true, nil
}

// loadCoverageTarget returns the effective target: the saved one when it is valid, else
// the default - only for "no row" or "saved value invalid". A failed READ is an error: the
// default in its place would show false «Sin cubrir» gaps in a calendar answer cached as
// clean, and open the owner's «Cambiar» dialog on the default, ready to be saved over the
// real target. One small query; run it before opening a cursor (single-connection pool).
func (h *Handler) loadCoverageTarget(ctx context.Context) (teamCoverageTargetJSON, error) {
	raw, ok, err := h.forkSettingStrict(ctx, forkKeyTeamCoverageTarget)
	if err != nil {
		return teamCoverageTargetJSON{}, err
	}
	if !ok {
		return defaultCoverageTarget(), nil
	}
	var st teamCoverageStored
	if err := json.Unmarshal([]byte(raw), &st); err != nil || st.V != 1 || st.Days == nil {
		return defaultCoverageTarget(), nil
	}
	days, msg := validateCoverageTarget(st.TZ, st.Days)
	if msg != "" {
		return defaultCoverageTarget(), nil
	}
	return teamCoverageTargetJSON{V: 1, TZ: st.TZ, Days: days}, nil
}

// GetTeamCoverageTarget handles GET /v1/team/coverage-target.
func (h *Handler) GetTeamCoverageTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, _ := userFromContext(ctx)
	ok, err := h.teamMayViewAvailability(ctx, user)
	if err != nil {
		h.logger.ErrorContext(ctx, "team coverage target: permission", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		h.writeError(w, http.StatusForbidden, "Solo el propietario y el equipo de soporte ven la disponibilidad del equipo.")
		return
	}
	t, err := h.loadCoverageTarget(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "team coverage target: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	t.CanEdit = user.IsOwner
	h.writeJSON(w, http.StatusOK, t)
}

// PutTeamCoverageTarget handles PUT /v1/team/coverage-target (owner only).
func (h *Handler) PutTeamCoverageTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, _ := userFromContext(ctx)
	if !user.IsOwner {
		h.writeError(w, http.StatusForbidden, "Solo el propietario cambia el horario a cubrir.")
		return
	}
	body, err := readBody(w, r, 4<<10)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "El cuerpo es demasiado grande o no se pudo leer.")
		return
	}
	var req struct {
		TZ   *string                `json:"tz"`
		Days *[]teamCoverageDayJSON `json:"days"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if req.TZ == nil || req.Days == nil {
		h.writeError(w, http.StatusBadRequest, "Faltan «tz» y «days» (una lista; vacía = sin horario a cubrir).")
		return
	}
	tz := strings.TrimSpace(*req.TZ)
	days, msg := validateCoverageTarget(tz, *req.Days)
	if msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	raw, err := json.Marshal(teamCoverageStored{V: 1, TZ: tz, Days: days})
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.setForkSettings(ctx, map[string]string{forkKeyTeamCoverageTarget: string(raw)}); err != nil {
		h.logger.ErrorContext(ctx, "team coverage target: save", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// FreeTimeChanges already moves the generation after this 2xx; doing it here too keeps a
	// direct caller (tests, a future route without the wrapper) from reading a stale target.
	h.InvalidateTeamAvailability()
	h.logger.InfoContext(ctx, "team coverage target: saved", "actor_id", user.ID, "tz", tz, "days", len(days))
	h.writeJSON(w, http.StatusOK, teamCoverageTargetJSON{V: 1, TZ: tz, Days: days, CanEdit: true})
}

// teamCoverageRule is a target day ready for interval math.
type teamCoverageRule struct {
	startMin, endMin int // endMin may be 1440
}

// coverageRules maps weekday -> span and loads the target's zone (valid by construction:
// loadCoverageTarget only returns validated targets).
func coverageRules(t teamCoverageTargetJSON) (*time.Location, map[time.Weekday]teamCoverageRule, error) {
	loc, err := time.LoadLocation(t.TZ)
	if err != nil {
		return nil, nil, fmt.Errorf("coverage target zone %q: %w", t.TZ, err)
	}
	out := map[time.Weekday]teamCoverageRule{}
	for _, d := range t.Days {
		s, _ := coverageClock(d.Start, false)
		e, _ := coverageClock(d.End, true)
		out[time.Weekday(d.Dow)] = teamCoverageRule{startMin: s, endMin: e}
	}
	return loc, out, nil
}
