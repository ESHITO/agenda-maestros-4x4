package handler

// Fork (Agenda Maestros 4x4): the transition time between sessions.
//
// Owner decision (30 Sep 2026): after each session there is a "dead" transition time before
// the next one can start, and clients only see starts that respect it.
//
//   - Mentoría: template-wide. The owner sets T's buffer_after_minutes (the transition) and
//     slot_interval_minutes ("empezar una sesión cada", 60 = one mentoría per hour) in the
//     event-type editor, and every mentor's copy follows through the reconcile's generic
//     field sync. Nothing here applies to it.
//   - Soporte: per person. Each support person may choose their own transition
//     (fork_member_transition, created by EnsureTeamSchema). The reconcile then writes it
//     into THEIR copy of S: buffer_after_minutes = minutes and slot_interval_minutes =
//     S's duration + minutes, leaving both columns out of the generic sync for that copy
//     only (syncTeamCopy). No row = the copy keeps S's values, exactly as before.
//
// API (Spanish errors, like the rest of the team feature):
//
//	GET /v1/users/me/transition          the caller's setting (applies = área soporte with an active S copy)
//	PUT /v1/users/me/transition          {minutes: n|null}; área soporte only (403); null = back to S
//	PUT /v1/users/{id}/transition        owner, or an admin for a non-admin member; the target must be área soporte
//	GET /v1/team/transitions             owner and admins: every chosen value, for Miembros
//
// The PUTs are wrapped with a reconcile of that person after a 2xx (server.go), and the
// FreeTimeChanges generation drops the cached team calendar on any successful write.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
)

// transitionChoices are the minutes a support person may choose between sessions.
var transitionChoices = []int{0, 5, 10, 15, 20, 25, 30, 45, 60}

// teamTransitionColumns are the event_types columns a Soporte copy with a chosen transition
// does not take from S (they stay in the generic sync for every other copy).
var teamTransitionColumns = map[string]bool{"buffer_after_minutes": true, "slot_interval_minutes": true}

// withoutColumns returns cols minus drop, keeping the order.
func withoutColumns(cols []string, drop map[string]bool) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		if !drop[c] {
			out = append(out, c)
		}
	}
	return out
}

// validTransition reports whether minutes is one of transitionChoices.
func validTransition(minutes int) bool { return slices.Contains(transitionChoices, minutes) }

// loadTeamTransitions returns every stored transition by user id. A row outside
// transitionChoices (only a hand-written one could be) is ignored, so the copy keeps S's
// values: whatever the reconcile writes must be valid by construction.
func loadTeamTransitions(ctx context.Context, q teamQuerier) (map[string]int, error) {
	rows, err := q.QueryContext(ctx, `SELECT user_id, minutes FROM fork_member_transition`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var m int
		if err := rows.Scan(&id, &m); err != nil {
			return nil, err
		}
		if validTransition(m) {
			out[id] = m
		}
	}
	return out, rows.Err()
}

// transitionJSON is one person's transition as the API shows it.
type transitionJSON struct {
	UserID string `json:"user_id"`
	// Applies: the person is área soporte and has an active copy of S, so the choice
	// changes what their clients can book.
	Applies bool `json:"applies"`
	// Minutes is the person's choice; null = S's own buffer after (template_minutes).
	Minutes          *int `json:"minutes"`
	TemplateMinutes  int  `json:"template_minutes"`
	TemplateInterval int  `json:"template_interval"`
	Duration         int  `json:"duration"`
	// IntervalEffective is how often their sessions start: duration + minutes with a
	// choice, else S's slot interval.
	IntervalEffective int `json:"interval_effective"`
}

// soporteTemplateTimes reads S's duration, buffer after and slot interval; ok=false when
// S is unset or gone.
func (h *Handler) soporteTemplateTimes(ctx context.Context) (st teamSettings, duration, buffer, interval int, ok bool, err error) {
	st, err = loadTeamSettings(ctx, h.db)
	if err != nil || st.soporteID == "" {
		return st, 0, 0, 0, false, err
	}
	err = h.db.QueryRowContext(ctx,
		`SELECT duration_minutes, buffer_after_minutes, slot_interval_minutes FROM event_types WHERE id = ?`, st.soporteID).
		Scan(&duration, &buffer, &interval)
	if errors.Is(err, sql.ErrNoRows) {
		return st, 0, 0, 0, false, nil
	}
	if err != nil {
		return st, 0, 0, 0, false, err
	}
	return st, duration, buffer, interval, true, nil
}

// effectiveInterval is how often a Soporte copy's sessions start for minutes (nil = S's).
func effectiveInterval(duration, templateInterval int, minutes *int) int {
	if minutes == nil {
		return templateInterval
	}
	return max(1, duration+*minutes)
}

// transitionFor builds userID's answer and returns their área.
func (h *Handler) transitionFor(ctx context.Context, userID string) (*transitionJSON, string, error) {
	var area string
	err := h.db.QueryRowContext(ctx, `SELECT area FROM fork_member_areas WHERE user_id = ?`, userID).Scan(&area)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	out := &transitionJSON{UserID: userID}
	var m int
	switch err := h.db.QueryRowContext(ctx, `SELECT minutes FROM fork_member_transition WHERE user_id = ?`, userID).Scan(&m); {
	case err == nil:
		if validTransition(m) {
			out.Minutes = &m
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, "", err
	}
	st, duration, buffer, interval, ok, err := h.soporteTemplateTimes(ctx)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return out, area, nil
	}
	out.TemplateMinutes, out.TemplateInterval, out.Duration = buffer, interval, duration
	out.IntervalEffective = effectiveInterval(duration, interval, out.Minutes)
	if area == areaSoporte {
		var one int
		err := h.db.QueryRowContext(ctx, `
			SELECT 1 FROM fork_event_type_links l JOIN event_types c ON c.id = l.copy_id
			WHERE l.kind = ? AND l.template_id = ? AND l.user_id = ?
			  AND c.is_active = 1 AND c.archived_at IS NULL`, linkKindCopy, st.soporteID, userID).Scan(&one)
		switch {
		case err == nil:
			out.Applies = true
		case !errors.Is(err, sql.ErrNoRows):
			return nil, "", err
		}
	}
	return out, area, nil
}

// GetMyTransition handles GET /v1/users/me/transition.
func (h *Handler) GetMyTransition(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	out, _, err := h.transitionFor(r.Context(), user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "transition: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, out)
}

// PutMyTransition handles PUT /v1/users/me/transition (área soporte only).
func (h *Handler) PutMyTransition(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	h.putTransition(w, r, user.ID, true)
}

// PutUserTransition handles PUT /v1/users/{id}/transition: the owner for anyone, an admin
// for a non-admin member (and for themselves, like /me); the target must be área soporte.
func (h *Handler) PutUserTransition(w http.ResponseWriter, r *http.Request) {
	actor, _ := userFromContext(r.Context())
	targetID := r.PathValue("id")
	if actor.ID != targetID {
		if !actor.IsAdmin {
			h.writeError(w, http.StatusForbidden, "Solo el propietario y los administradores cambian el tiempo entre sesiones de otra persona.")
			return
		}
		var isAdmin, isOwner bool
		err := h.db.QueryRowContext(r.Context(), `SELECT is_admin, is_owner FROM users WHERE id = ?`, targetID).Scan(&isAdmin, &isOwner)
		if errors.Is(err, sql.ErrNoRows) {
			h.writeError(w, http.StatusNotFound, "No existe esa persona.")
			return
		}
		if err != nil {
			h.logger.ErrorContext(r.Context(), "transition: load target", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !actor.IsOwner && (isAdmin || isOwner) {
			h.writeError(w, http.StatusForbidden, "Solo el propietario cambia el tiempo entre sesiones de un administrador.")
			return
		}
	}
	h.putTransition(w, r, targetID, actor.ID == targetID)
}

// putTransition validates and stores targetID's choice. self picks the wording (and the
// status) of the "not área soporte" refusal: 403 for the caller themselves, 400 when an
// admin names someone who is not of Soporte.
func (h *Handler) putTransition(w http.ResponseWriter, r *http.Request, targetID string, self bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		Minutes json.RawMessage `json:"minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if len(req.Minutes) == 0 {
		h.writeError(w, http.StatusBadRequest, "Falta «minutes» (un número de minutos, o null para usar el de la plantilla).")
		return
	}
	var minutes *int
	if string(req.Minutes) != "null" {
		var m int
		if err := json.Unmarshal(req.Minutes, &m); err != nil || !validTransition(m) {
			h.writeError(w, http.StatusBadRequest, "El tiempo entre sesiones debe ser 0, 5, 10, 15, 20, 25, 30, 45 o 60 minutos.")
			return
		}
		minutes = &m
	}

	_, area, err := h.transitionFor(r.Context(), targetID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "transition: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if area != areaSoporte {
		if self {
			h.writeError(w, http.StatusForbidden, "Solo el personal de Soporte elige su tiempo entre sesiones; en Mentoría lo decide el propietario en la plantilla.")
		} else {
			h.writeError(w, http.StatusBadRequest, "Esa persona no es de Soporte: el tiempo entre sesiones de Mentoría se decide en la plantilla.")
		}
		return
	}

	if minutes == nil {
		_, err = h.db.ExecContext(r.Context(), `DELETE FROM fork_member_transition WHERE user_id = ?`, targetID)
	} else {
		_, err = h.db.ExecContext(r.Context(), `
			INSERT INTO fork_member_transition (user_id, minutes, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (user_id) DO UPDATE SET minutes = excluded.minutes, updated_at = excluded.updated_at`,
			targetID, *minutes, teamNow())
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "transition: save", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out, _, err := h.transitionFor(r.Context(), targetID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "transition: reload", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, out)
}

// ListTeamTransitions handles GET /v1/team/transitions (owner and admins): S's times and
// every stored choice ({user_id: minutes}), for the Soporte cards in Miembros.
func (h *Handler) ListTeamTransitions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	_, duration, buffer, interval, hasTemplate, err := h.soporteTemplateTimes(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "transitions: template", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	items, err := loadTeamTransitions(r.Context(), h.db)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "transitions: list", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"has_template":      hasTemplate,
		"template_minutes":  buffer,
		"template_interval": interval,
		"duration":          duration,
		"choices":           transitionChoices,
		"items":             items,
	})
}
