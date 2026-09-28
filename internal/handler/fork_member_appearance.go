package handler

// Fork (Agenda Maestros 4x4): the owner and the admins set another person's look - the
// booking accent colour and the profile photo - from the Members page.
//
//	PATCH  /v1/users/{id}/appearance  {"booking_accent": "#rrggbb" | ""}   ("" = back to the default)
//	POST   /v1/users/{id}/avatar      multipart "avatar" (the /me limits, resize and storage)
//	DELETE /v1/users/{id}/avatar
//
// Who may edit whom (canEditAppearance): the owner -> any active user, themselves included;
// an admin -> non-admin members and themselves (never the owner, never another admin);
// everybody else -> 403 (they keep using /v1/users/me). Archived or unknown -> 404.
//
// The processing is the /me endpoints' own: storeAvatar/removeAvatar are UploadAvatar's
// and DeleteAvatar's bodies, and parseAccent is the check PatchMe runs.
//
// GET /v1/users carries booking_accent, accent_custom and can_edit_appearance (this very
// matrix, for the viewer) so the Members page shows the controls only where they work.
//
// Where the colour is used: a team copy (fork_event_type_links kind copy - owned by the
// templates' owner, hosted by the mentor or support person) shows its HOST's accent on
// book.html, manage.html and the embed payload (teamHostAccent, called from book.go and
// manage_handler.go); a host still on the default falls back to the owner's, as before.
// The name and photo on those surfaces already come from the hosts (event_type_hosts for
// book.html and /public, booking_hosts for manage.html), which on a copy is that person;
// the e-mails name the booking's host and carry no colour or photo; the LiveKit room has
// its own fixed palette and shows no photo. So the accent was the only owner-sourced piece.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// parseAccent validates a booking accent the way PatchMe always has (six hex digits after
// "#") and returns it lower-cased. Shared by PatchMe and PatchMemberAppearance.
func parseAccent(s string) (string, bool) {
	if !validAccentColor(s) {
		return "", false
	}
	return strings.ToLower(s), true
}

// accentIsCustom reports whether a stored accent is one somebody chose: valid and not the
// column default (accentFallback, which is also what a reset writes back).
func accentIsCustom(stored string) bool {
	return validAccentColor(stored) && !strings.EqualFold(stored, accentFallback)
}

// canEditAppearance is the permission matrix, pure so the list and the endpoints agree.
func canEditAppearance(actor AuthUser, targetID string, targetIsAdmin, targetIsOwner bool) bool {
	switch {
	case actor.IsOwner:
		return true
	case actor.IsAdmin:
		if targetID == actor.ID {
			return true
		}
		return !targetIsOwner && !targetIsAdmin
	default:
		return false
	}
}

// appearanceTarget resolves {id} and checks the matrix, answering the error itself.
func (h *Handler) appearanceTarget(w http.ResponseWriter, r *http.Request) (AuthUser, string, bool) {
	actor, _ := userFromContext(r.Context())
	if !actor.IsOwner && !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "Solo el propietario y los administradores cambian la apariencia de otra persona; tu color y tu foto se cambian en tu perfil.")
		return actor, "", false
	}
	targetID := r.PathValue("id")
	// The id names the avatar file: the same rule ServeAvatar applies.
	if !validUserID.MatchString(targetID) {
		h.writeError(w, http.StatusNotFound, "No existe esa persona.")
		return actor, "", false
	}
	var isAdmin, isOwner bool
	var archivedAt sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT is_admin, is_owner, archived_at FROM users WHERE id = ?`, targetID).
		Scan(&isAdmin, &isOwner, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && archivedAt.Valid) {
		h.writeError(w, http.StatusNotFound, "No existe esa persona.")
		return actor, "", false
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "member appearance: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return actor, "", false
	}
	if !canEditAppearance(actor, targetID, isAdmin, isOwner) {
		msg := "Solo el propietario cambia la apariencia de un administrador."
		if isOwner {
			msg = "Solo el propietario cambia su propia apariencia."
		}
		h.writeError(w, http.StatusForbidden, msg)
		return actor, "", false
	}
	return actor, targetID, true
}

// PatchMemberAppearance handles PATCH /v1/users/{id}/appearance.
func (h *Handler) PatchMemberAppearance(w http.ResponseWriter, r *http.Request) {
	actor, targetID, ok := h.appearanceTarget(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		BookingAccent *string `json:"booking_accent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if req.BookingAccent == nil {
		h.writeError(w, http.StatusBadRequest, "Falta booking_accent (\"#rrggbb\", o \"\" para volver al color predeterminado).")
		return
	}
	accent := accentFallback // "" = reset: the column default, which booking pages treat as "not chosen"
	if *req.BookingAccent != "" {
		v, valid := parseAccent(*req.BookingAccent)
		if !valid {
			h.writeError(w, http.StatusBadRequest, "El color debe tener el formato #rrggbb (seis dígitos hexadecimales).")
			return
		}
		accent = v
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET booking_accent = ? WHERE id = ?`, accent, targetID); err != nil {
		h.logger.ErrorContext(r.Context(), "member appearance: update accent", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.logger.InfoContext(r.Context(), "member appearance: accent changed",
		"actor_id", actor.ID, "user_id", targetID, "booking_accent", accent)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"id":             targetID,
		"booking_accent": accent,
		"accent_custom":  accentIsCustom(accent),
	})
}

// UploadMemberAvatar handles POST /v1/users/{id}/avatar.
func (h *Handler) UploadMemberAvatar(w http.ResponseWriter, r *http.Request) {
	actor, targetID, ok := h.appearanceTarget(w, r)
	if !ok {
		return
	}
	sw := &teamStatusWriter{ResponseWriter: w}
	h.storeAvatar(sw, r, targetID)
	if sw.ok() {
		h.logger.InfoContext(r.Context(), "member appearance: avatar uploaded", "actor_id", actor.ID, "user_id", targetID)
	}
}

// DeleteMemberAvatar handles DELETE /v1/users/{id}/avatar.
func (h *Handler) DeleteMemberAvatar(w http.ResponseWriter, r *http.Request) {
	actor, targetID, ok := h.appearanceTarget(w, r)
	if !ok {
		return
	}
	sw := &teamStatusWriter{ResponseWriter: w}
	h.removeAvatar(sw, r, targetID)
	if sw.ok() {
		h.logger.InfoContext(r.Context(), "member appearance: avatar removed", "actor_id", actor.ID, "user_id", targetID)
	}
}

// memberAccents returns every user's accent by id (lower-cased, the fallback when the row
// holds something invalid), for GET /v1/users. Best effort: a failure leaves the colours
// out, never the list. The cursor is closed on return (single-connection pool).
func (h *Handler) memberAccents(ctx context.Context) map[string]string {
	out := map[string]string{}
	rows, err := h.db.QueryContext(ctx, `SELECT id, booking_accent FROM users`)
	if err != nil {
		h.logger.ErrorContext(ctx, "list users: accents", "error", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, accent string
		if rows.Scan(&id, &accent) == nil {
			out[id] = strings.ToLower(accentOrDefault(accent))
		}
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(ctx, "list users: accents rows", "error", err)
	}
	return out
}

// teamHostAccent is the accent a booking surface shows for event type etID, given the one
// it already resolved (ownerAccent, the event type owner's). A team copy shows its host's
// colour when the host chose one; anything else - templates, ordinary types, a host on the
// default, an archived host, a failed read - keeps ownerAccent.
func (h *Handler) teamHostAccent(ctx context.Context, etID, ownerAccent string) string {
	var accent string
	err := h.db.QueryRowContext(ctx, `
		SELECT u.booking_accent
		FROM fork_event_type_links l JOIN users u ON u.id = l.user_id
		WHERE l.copy_id = ? AND l.kind = ? AND u.archived_at IS NULL`, etID, linkKindCopy).Scan(&accent)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			h.logger.WarnContext(ctx, "booking accent: team copy host", "error", err, "event_type_id", etID)
		}
		return ownerAccent
	}
	if accentIsCustom(accent) {
		return strings.ToLower(accent)
	}
	return ownerAccent
}
