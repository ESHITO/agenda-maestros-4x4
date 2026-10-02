package handler

// Fork (Agenda Maestros 4x4): each person's WhatsApp number, for the notices to the host
// (webhook/fork_host.go: "nueva sesión agendada" and "faltan 5 minutos").
//
//	GET /v1/users/me/whatsapp      the caller's number (any signed-in user)
//	PUT /v1/users/me/whatsapp      {"phone": "+51 987 654 321" | null | ""}; null/"" = remove it
//	PUT /v1/users/{id}/whatsapp    the same for someone else: the appearance matrix
//	                               (canEditAppearance) - the owner for anyone active, themselves
//	                               included; an admin for non-admin members and themselves
//
// The number must carry its country code ("+51...", "0051..."): without it nobody can tell
// the country, and FunnelChat/wa.me would read its first digits as one - the same rule as
// attendee_phone (webhook.NormalizePhone). It is stored E.164 in fork_member_phones
// (EnsureTeamSchema). GET /v1/users shows a number only to those who may change it
// (users.go). Logs never carry the number.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/webhook"
)

// memberWhatsAppJSON is one person's number as the API shows it; the pointers are null when
// the person has none.
type memberWhatsAppJSON struct {
	UserID      string  `json:"user_id"`
	Phone       *string `json:"phone"`        // E.164, "+51987654321"
	WhatsApp    *string `json:"whatsapp"`     // digits, wa.me form, "51987654321"
	Country     *string `json:"country"`      // ISO, "PE"
	CountryName *string `json:"country_name"` // "Perú"
	UpdatedAt   *string `json:"updated_at"`
}

// memberWhatsApp builds userID's answer (their zone settles a dial code several countries
// share: +1 with America/Toronto is Canada).
func (h *Handler) memberWhatsApp(ctx context.Context, userID string) (memberWhatsAppJSON, error) {
	out := memberWhatsAppJSON{UserID: userID}
	var phone, updated, tz string
	err := h.db.QueryRowContext(ctx, `
		SELECT p.phone, p.updated_at, COALESCE(u.iana_timezone, '')
		FROM fork_member_phones p LEFT JOIN users u ON u.id = p.user_id
		WHERE p.user_id = ?`, userID).Scan(&phone, &updated, &tz)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	e164, digits := webhook.NormalizePhone(phone)
	cc := phoneTable.CountryFor(e164, tz)
	name := webhook.CountryNameES(cc)
	out.Phone, out.WhatsApp, out.UpdatedAt = &e164, &digits, &updated
	if cc != "" {
		out.Country, out.CountryName = &cc, &name
	}
	return out, nil
}

// parseMemberPhone validates a PUT body's "phone": (e164, remove, "") or a Spanish 400.
func parseMemberPhone(raw json.RawMessage) (e164 string, remove bool, msg string) {
	const needCode = "Escribe el número con el código de país, por ejemplo +51 987 654 321. Sin el código no sabemos de qué país es."
	if len(raw) == 0 {
		return "", false, "Falta «phone» (el número con código de país, o null para quitarlo)."
	}
	if string(raw) == "null" {
		return "", true, ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false, needCode
	}
	if strings.TrimSpace(s) == "" {
		return "", true, ""
	}
	e164, digits := webhook.NormalizePhone(s)
	if digits == "" {
		return "", false, needCode
	}
	if phoneTable.CountryFor(e164, "") == "" {
		return "", false, "Ese código de país no existe."
	}
	return e164, false, ""
}

// GetMyWhatsApp handles GET /v1/users/me/whatsapp.
func (h *Handler) GetMyWhatsApp(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	out, err := h.memberWhatsApp(r.Context(), user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "member whatsapp: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, out)
}

// PutMyWhatsApp handles PUT /v1/users/me/whatsapp: anyone signed in, a member with no área
// included ("mentores, soporte o usuarios lo ponen en su perfil").
func (h *Handler) PutMyWhatsApp(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	h.putMemberWhatsApp(w, r, user, user.ID)
}

// PutUserWhatsApp handles PUT /v1/users/{id}/whatsapp: the owner sets anyone's number (the
// "superadministrador" of the request), an admin a non-admin member's or their own.
func (h *Handler) PutUserWhatsApp(w http.ResponseWriter, r *http.Request) {
	actor, targetID, ok := h.whatsAppTarget(w, r)
	if !ok {
		return
	}
	h.putMemberWhatsApp(w, r, actor, targetID)
}

// whatsAppTarget resolves {id} and checks the appearance matrix, answering the error itself
// (appearanceTarget's rule and wording; the id names no file here, so any id shape is
// looked up).
func (h *Handler) whatsAppTarget(w http.ResponseWriter, r *http.Request) (AuthUser, string, bool) {
	actor, _ := userFromContext(r.Context())
	if !actor.IsOwner && !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "Solo el propietario y los administradores cambian el WhatsApp de otra persona; el tuyo se cambia en tu perfil.")
		return actor, "", false
	}
	targetID := r.PathValue("id")
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
		h.logger.ErrorContext(r.Context(), "member whatsapp: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return actor, "", false
	}
	if !canEditAppearance(actor, targetID, isAdmin, isOwner) {
		msg := "Solo el propietario cambia el WhatsApp de un administrador."
		if isOwner {
			msg = "Solo el propietario cambia su propio WhatsApp."
		}
		h.writeError(w, http.StatusForbidden, msg)
		return actor, "", false
	}
	return actor, targetID, true
}

// putMemberWhatsApp validates and stores (or removes) targetID's number, then answers like GET.
func (h *Handler) putMemberWhatsApp(w http.ResponseWriter, r *http.Request, actor AuthUser, targetID string) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		Phone json.RawMessage `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	e164, remove, msg := parseMemberPhone(req.Phone)
	if msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	var err error
	if remove {
		_, err = h.db.ExecContext(r.Context(), `DELETE FROM fork_member_phones WHERE user_id = ?`, targetID)
	} else {
		_, err = h.db.ExecContext(r.Context(), `
			INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (user_id) DO UPDATE SET phone = excluded.phone, updated_at = excluded.updated_at`,
			targetID, e164, teamNow())
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "member whatsapp: save", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out, err := h.memberWhatsApp(r.Context(), targetID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "member whatsapp: reload", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	country := ""
	if out.Country != nil {
		country = *out.Country
	}
	h.logger.InfoContext(r.Context(), "member whatsapp: changed",
		"actor_id", actor.ID, "user_id", targetID, "removed", remove, "country", country)
	h.writeJSON(w, http.StatusOK, out)
}

// memberPhones returns every stored number by user id, for GET /v1/users. Best effort: a
// failure leaves the numbers out, never the list. The cursor is closed on return
// (single-connection pool).
func (h *Handler) memberPhones(ctx context.Context) map[string]string {
	out := map[string]string{}
	rows, err := h.db.QueryContext(ctx, `SELECT user_id, phone FROM fork_member_phones`)
	if err != nil {
		h.logger.ErrorContext(ctx, "list users: whatsapp numbers", "error", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, phone string
		if rows.Scan(&id, &phone) == nil {
			out[id] = phone
		}
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(ctx, "list users: whatsapp numbers rows", "error", err)
	}
	return out
}
