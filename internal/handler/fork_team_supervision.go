package handler

// Fork (Agenda Maestros 4x4): supervision of the team's sessions - the owner and admins see
// every booking; this file lets them read the client's answers and pass a session to
// another person of the SAME área, and keeps the video room's host capability with whoever
// attends it now. Attendance of the room is fork_attendance.go.
//
//	GET  /v1/bookings/{id}/answers               primary host, any booking_hosts seat, owner, admins
//	POST /v1/bookings/{id}/reassign              admins; TeamReassignGuard (same-área rule, Spanish)
//	GET  /v1/bookings/{id}/reassign-candidates   admins: [{id, name, area}]
//
// Same-área rule, derived from the booking's TYPE (fork_team_bookings.go does the same):
//   - Mentoría (the template T or a mentor's copy of it): only to the template's owner, or
//     to a mentor with an ACTIVE copy of that template. The booking then moves to the new
//     host's family member - their copy, or T itself for T's owner - so it books, lists,
//     notifies and reads {tema} as theirs; the answers follow through fork_question_links.
//   - Soporte (the shared type S): only to an active person whose área is soporte.
//   - Any other type: the upstream rule (any active user).
//
// ReassignBooking keeps its single call site: teamReassignHost replaces the one call to
// bookingSvc.ReassignHost and does, in ONE transaction with the same busy check, host_id,
// the booking_hosts primary seat, event_type_id, the answers and a fresh host link.
//
// Host link: the host's room link (role=host token) is minted when the booking is made and
// emailed to that host only. Its hash is kept in fork_livekit_host_links; after a reassign
// a fresh link is minted for the new host and its hash replaces the old one, so the
// previous host's link degrades to an attendee link (a signed-in current host is still
// promoted by LiveKitToken's own rule). Bookings without a row keep the upstream behaviour.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/mailer"
)

// ---- Answers --------------------------------------------------------------------------

// teamMayReadAnswers is GetBookingAnswers' widened check (the primary host is allowed
// before it is called): any booking_hosts seat, the owner and the admins.
func (h *Handler) teamMayReadAnswers(ctx context.Context, user AuthUser, bookingID string) bool {
	return user.IsOwner || user.IsAdmin || h.userHostsBooking(ctx, user.ID, bookingID)
}

// ---- Same-área rule -------------------------------------------------------------------

// teamReassignRule is the same-área rule that applies to a booking's type.
type teamReassignRule struct {
	area       string // areaMentoria | areaSoporte | "" (the upstream rule)
	templateID string // mentoria: the template of the booking's family
}

// errTeamNotInFamily: the new host has no member of the booking's Mentoría family (no
// active copy, not the template's owner). The guard refuses it first, in Spanish; inside
// the transaction it only means the copy was deactivated in between.
var errTeamNotInFamily = errors.New("team: the new host has no active copy of the template")

// teamRuleFor classifies etID: a mentor's copy (of any template, the current one or an
// old one) or the current template T is Mentoría, with the copy's own template as its
// family; the current S is Soporte; anything else (a holder included) is neither.
func teamRuleFor(ctx context.Context, q teamQuerier, st teamSettings, etID string) (teamReassignRule, error) {
	var templateID, kind string
	err := q.QueryRowContext(ctx,
		`SELECT template_id, kind FROM fork_event_type_links WHERE copy_id = ?`, etID).Scan(&templateID, &kind)
	switch {
	case err == nil:
		if kind == linkKindCopy {
			return teamReassignRule{area: areaMentoria, templateID: templateID}, nil
		}
		return teamReassignRule{}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return teamReassignRule{}, err
	}
	switch {
	case etID != "" && etID == st.templateID:
		return teamReassignRule{area: areaMentoria, templateID: etID}, nil
	case etID != "" && etID == st.soporteID:
		return teamReassignRule{area: areaSoporte}, nil
	}
	return teamReassignRule{}, nil
}

// teamFamilyTarget is the event type a Mentoría booking must move to when userID takes
// it: the template itself for its owner, else userID's ACTIVE copy of it (an active user,
// a copy neither paused nor archived). "" = userID may not take it.
func teamFamilyTarget(ctx context.Context, q teamQuerier, templateID, userID string) (string, error) {
	var owner string
	err := q.QueryRowContext(ctx, `SELECT user_id FROM event_types WHERE id = ?`, templateID).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil && owner == userID {
		return templateID, nil
	}
	var copyID string
	err = q.QueryRowContext(ctx, `
		SELECT l.copy_id FROM fork_event_type_links l
		JOIN event_types c ON c.id = l.copy_id
		JOIN users u ON u.id = l.user_id
		WHERE l.template_id = ? AND l.user_id = ? AND l.kind = ?
		  AND c.is_active = 1 AND c.archived_at IS NULL AND u.archived_at IS NULL`,
		templateID, userID, linkKindCopy).Scan(&copyID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return copyID, err
}

// teamIsSoporteStaff reports whether userID is active with the área soporte.
func teamIsSoporteStaff(ctx context.Context, q teamQuerier, userID string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users u JOIN fork_member_areas a ON a.user_id = u.id
		WHERE u.id = ? AND u.archived_at IS NULL AND a.area = ?`, userID, areaSoporte).Scan(&n)
	return n > 0, err
}

// ---- POST /v1/bookings/{id}/reassign: the guard ----------------------------------------

// teamReassignSpanish maps ReassignBooking's English errors to the panel's Spanish.
var teamReassignSpanish = map[string]string{
	"admin access required":                              "Solo el propietario y los administradores pasan una sesión a otra persona.",
	"host_id is required":                                "Elige a la persona que atenderá la sesión.",
	"new host not found or archived":                     "Esa persona no existe o está archivada.",
	"booking not found":                                  "No existe esa reserva.",
	"the chosen host already has a booking at that time": "Esa persona ya tiene otra sesión a esa hora.",
	"this booking has been cancelled":                    "Esta reserva está cancelada.",
}

// TeamReassignGuard wraps POST /v1/bookings/{id}/reassign: the same-área rule BEFORE any
// write (Spanish 4xx), then the upstream handler with the body restored, whose English
// errors are answered in Spanish. A success passes through untouched.
func (h *Handler) TeamReassignGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := userFromContext(r.Context())
		if actor.IsAdmin {
			body, err := readBody(w, r, 1<<10)
			if err != nil {
				h.writeError(w, http.StatusBadRequest, "JSON inválido")
				return
			}
			var req struct {
				HostID string `json:"host_id"`
			}
			// A body the handler's decoder rejects (or no host_id) gets its own 400.
			if decodeBody(body, &req) == nil && req.HostID != "" {
				status, msg, err := h.teamReassignCheck(r.Context(), r.PathValue("id"), req.HostID)
				if err != nil {
					h.logger.ErrorContext(r.Context(), "reassign guard", "error", err)
					h.writeError(w, http.StatusInternalServerError, "internal error")
					return
				}
				if msg != "" {
					h.writeError(w, status, msg)
					return
				}
			}
		}
		buf := newTeamBufferWriter()
		next(buf, r)
		out := buf.body.Bytes()
		if !buf.ok() {
			var e struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(out, &e) == nil {
				if es, ok := teamReassignSpanish[e.Error]; ok {
					out, _ = json.Marshal(map[string]string{"error": es})
				}
			}
		}
		buf.flush(w, out)
	}
}

// teamReassignCheck is the guard's decision: (status, Spanish message) to refuse, or ""
// to let the handler run. The handler repeats its own checks; these answer first, in
// Spanish, and add the same-área rule.
func (h *Handler) teamReassignCheck(ctx context.Context, bookingID, newHostID string) (int, string, error) {
	var etID, hostID, status string
	err := h.db.QueryRowContext(ctx,
		`SELECT event_type_id, host_id, status FROM bookings WHERE id = ?`, bookingID).Scan(&etID, &hostID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusNotFound, "No existe esa reserva.", nil
	}
	if err != nil {
		return 0, "", err
	}
	if status == "cancelled" {
		return http.StatusConflict, "Esta reserva está cancelada.", nil
	}
	var name string
	err = h.db.QueryRowContext(ctx,
		`SELECT name FROM users WHERE id = ? AND archived_at IS NULL`, newHostID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusBadRequest, "Esa persona no existe o está archivada.", nil
	}
	if err != nil {
		return 0, "", err
	}
	if newHostID == hostID {
		return 0, "", nil // the handler's no-op
	}
	st, err := loadTeamSettings(ctx, h.db)
	if err != nil {
		return 0, "", err
	}
	rule, err := teamRuleFor(ctx, h.db, st, etID)
	if err != nil {
		return 0, "", err
	}
	switch rule.area {
	case areaMentoria:
		target, err := teamFamilyTarget(ctx, h.db, rule.templateID, newHostID)
		if err != nil {
			return 0, "", err
		}
		if target == "" {
			return http.StatusBadRequest, "«" + name + "» no tiene un enlace de Mentoría activo: esta sesión solo puede pasarse " +
				"al propietario de la plantilla o a un mentor con su enlace activo.", nil
		}
	case areaSoporte:
		ok, err := teamIsSoporteStaff(ctx, h.db, newHostID)
		if err != nil {
			return 0, "", err
		}
		if !ok {
			return http.StatusBadRequest, "«" + name + "» no es del personal de soporte: esta sesión solo puede pasarse " +
				"a otra persona de Soporte.", nil
		}
	}
	return 0, "", nil
}

// ---- The one transaction of a reassign ------------------------------------------------

// teamReassignHost replaces ReassignBooking's call to bookingSvc.ReassignHost. In ONE
// transaction, with ReassignHost's busy check (booking.ReassignHostWith): host_id; the
// booking_hosts primary seat; for a Mentoría booking, event_type_id moved to the new
// host's family member and the answers remapped to its questions; for a LiveKit booking, a
// fresh host link whose hash replaces the previous one. Returns the updated booking (its
// EventTypeID already the new one, so the emails and booking.rescheduled carry it) and the
// new host link ("" when the booking has no room), which the goroutine emails to the new
// host instead of the attendee link.
func (h *Handler) teamReassignHost(ctx context.Context, bookingID, newHostID string) (*booking.Booking, string, error) {
	var hostLink string
	b, err := h.bookingSvc.ReassignHostWith(ctx, bookingID, newHostID, func(ctx context.Context, tx *sql.Tx, b *booking.Booking) error {
		// The primary seat follows host_id. A seat the new host already held (a Group
		// booking) makes way for the primary one: booking_hosts is unique per person.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM booking_hosts WHERE booking_id = ? AND user_id = ? AND is_primary = 0`, b.ID, newHostID); err != nil {
			return fmt.Errorf("reassign: free seat: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE booking_hosts SET user_id = ?, external_event_id = NULL WHERE booking_id = ? AND is_primary = 1`,
			newHostID, b.ID); err != nil {
			return fmt.Errorf("reassign: primary seat: %w", err)
		}
		st, err := loadTeamSettings(ctx, tx)
		if err != nil {
			return err
		}
		rule, err := teamRuleFor(ctx, tx, st, b.EventTypeID)
		if err != nil {
			return err
		}
		if rule.area == areaMentoria {
			target, err := teamFamilyTarget(ctx, tx, rule.templateID, newHostID)
			if err != nil {
				return err
			}
			if target == "" {
				return errTeamNotInFamily
			}
			if target != b.EventTypeID {
				if _, err := tx.ExecContext(ctx,
					`UPDATE bookings SET event_type_id = ? WHERE id = ?`, target, b.ID); err != nil {
					return fmt.Errorf("reassign: move event type: %w", err)
				}
				if err := remapTeamAnswers(ctx, tx, b.ID, b.EventTypeID, target); err != nil {
					return err
				}
				b.EventTypeID = target
			}
		}
		link, err := h.freshTeamHostLink(ctx, tx, b)
		if err != nil {
			return err
		}
		hostLink = link
		return nil
	})
	return b, hostLink, err
}

// teamQuestionLinks returns etID's copy-question -> template-question map, and whether
// etID is a copy at all (a template's own question ids ARE the template ids).
func teamQuestionLinks(ctx context.Context, tx *sql.Tx, etID string) (map[string]string, bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fork_event_type_links WHERE copy_id = ? AND kind = ?`, etID, linkKindCopy).Scan(&n); err != nil {
		return nil, false, err
	}
	if n == 0 {
		return nil, false, nil
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT copy_question_id, template_question_id FROM fork_question_links WHERE copy_id = ?`, etID)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var cq, tq string
		if err := rows.Scan(&cq, &tq); err != nil {
			return nil, true, err
		}
		m[cq] = tq
	}
	return m, true, rows.Err()
}

// remapTeamAnswers points a moved booking's answers at the new type's questions: old copy
// question -> template question -> new copy question (a template's questions are the
// template ids themselves). An answer with no counterpart keeps its question - it stays
// readable (answers join by question id), it just no longer feeds {tema}; a retired
// old-copy question is parked on the template's holder (parkRetiredTeamQuestion).
func remapTeamAnswers(ctx context.Context, tx *sql.Tx, bookingID, fromET, toET string) error {
	fromLinks, fromCopy, err := teamQuestionLinks(ctx, tx, fromET)
	if err != nil {
		return err
	}
	toLinks, toCopy, err := teamQuestionLinks(ctx, tx, toET)
	if err != nil {
		return err
	}
	byTemplate := make(map[string]string, len(toLinks)) // template question -> new question
	for cq, tq := range toLinks {
		byTemplate[tq] = cq
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT a.id, a.question_id, COALESCE(q.event_type_id, '')
		FROM booking_answers a LEFT JOIN event_type_questions q ON q.id = a.question_id
		WHERE a.booking_id = ?`, bookingID)
	if err != nil {
		return err
	}
	type answer struct{ id, questionID, questionET string }
	var answers []answer
	for rows.Next() {
		var a answer
		if err := rows.Scan(&a.id, &a.questionID, &a.questionET); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error
			return err
		}
		answers = append(answers, a)
	}
	rows.Close() // #nosec G104 -- drained
	if err := rows.Err(); err != nil {
		return err
	}
	var unmapped []string // old-copy questions an answer could not follow
	for _, a := range answers {
		if a.questionET != fromET {
			continue // not a question of the old type (a parked one): leave it
		}
		tq := a.questionID
		if fromCopy {
			var ok bool
			if tq, ok = fromLinks[a.questionID]; !ok {
				unmapped = append(unmapped, a.questionID)
				continue
			}
		}
		nq := tq
		if toCopy {
			var ok bool
			if nq, ok = byTemplate[tq]; !ok {
				if fromCopy {
					unmapped = append(unmapped, a.questionID)
				}
				continue
			}
		} else {
			// Moving onto the template: its question must still exist there.
			var x int
			err := tx.QueryRowContext(ctx,
				`SELECT 1 FROM event_type_questions WHERE id = ? AND event_type_id = ?`, nq, toET).Scan(&x)
			if errors.Is(err, sql.ErrNoRows) {
				if fromCopy {
					unmapped = append(unmapped, a.questionID)
				}
				continue
			}
			if err != nil {
				return err
			}
		}
		if nq == a.questionID {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE booking_answers SET question_id = ? WHERE id = ?`, nq, a.id); err != nil {
			return fmt.Errorf("reassign: remap answer: %w", err)
		}
	}
	for _, qID := range unmapped {
		if err := parkRetiredTeamQuestion(ctx, tx, fromET, qID); err != nil {
			return err
		}
	}
	return nil
}

// parkRetiredTeamQuestion moves a copy question an answer could not follow onto its
// template's holder when it is RETIRED (no link, or its template question is gone), as the
// reconcile does (rule 4): the moved booking's answer then no longer hangs off a copy that
// may later be deleted. A question still live on the template stays on the copy (it is
// still that copy's form and {tema}); the orphan rule keeps an answered copy.
func parkRetiredTeamQuestion(ctx context.Context, tx *sql.Tx, copyID, questionID string) error {
	var templateID string
	err := tx.QueryRowContext(ctx,
		`SELECT template_id FROM fork_event_type_links WHERE copy_id = ? AND kind = ?`, copyID, linkKindCopy).Scan(&templateID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var live int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM fork_question_links l
		JOIN event_type_questions q ON q.id = l.template_question_id AND q.event_type_id = ?
		WHERE l.copy_question_id = ?`, templateID, questionID).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return nil
	}
	tmpl, err := loadTeamType(ctx, tx, templateID)
	if err != nil || tmpl == nil {
		return err
	}
	cols, err := teamSyncColumns(ctx, tx)
	if err != nil {
		return err
	}
	holderID, err := ensureTeamHolder(ctx, tx, tmpl, cols)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE event_type_questions SET event_type_id = ? WHERE id = ? AND event_type_id = ?`, holderID, questionID, copyID); err != nil {
		return fmt.Errorf("reassign: park question: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fork_question_links WHERE copy_question_id = ?`, questionID); err != nil {
		return fmt.Errorf("reassign: unlink parked question: %w", err)
	}
	return nil
}

// ---- GET /v1/bookings/{id}/reassign-candidates -----------------------------------------

// teamCandidateJSON is one person a booking may pass to.
type teamCandidateJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Area string `json:"area"` // their área: "mentoria" | "soporte" | ""
}

// GetReassignCandidates handles GET /v1/bookings/{id}/reassign-candidates (admins): the
// active people the same-área rule allows, minus the current host, sorted by name - a bare
// JSON array. Busy people are not filtered out: the reassign itself answers that.
func (h *Handler) GetReassignCandidates(w http.ResponseWriter, r *http.Request) {
	actor, _ := userFromContext(r.Context())
	if !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "Solo el propietario y los administradores pasan una sesión a otra persona.")
		return
	}
	ctx := r.Context()
	var etID, hostID, status string
	err := h.db.QueryRowContext(ctx,
		`SELECT event_type_id, host_id, status FROM bookings WHERE id = ?`, r.PathValue("id")).Scan(&etID, &hostID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "No existe esa reserva.")
		return
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "reassign candidates: booking", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if status == "cancelled" {
		h.writeError(w, http.StatusConflict, "Esta reserva está cancelada.")
		return
	}
	out, err := h.teamReassignCandidates(ctx, etID, hostID)
	if err != nil {
		h.logger.ErrorContext(ctx, "reassign candidates", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, out)
}

// teamReassignCandidates lists who may take a booking of etID now hosted by hostID.
func (h *Handler) teamReassignCandidates(ctx context.Context, etID, hostID string) ([]teamCandidateJSON, error) {
	st, err := loadTeamSettings(ctx, h.db)
	if err != nil {
		return nil, err
	}
	rule, err := teamRuleFor(ctx, h.db, st, etID)
	if err != nil {
		return nil, err
	}
	query := `
		SELECT u.id, u.name, COALESCE(a.area, '')
		FROM users u LEFT JOIN fork_member_areas a ON a.user_id = u.id
		WHERE u.archived_at IS NULL AND u.id <> ?`
	args := []any{hostID}
	switch rule.area {
	case areaMentoria:
		query += ` AND (u.id = (SELECT user_id FROM event_types WHERE id = ?)
		            OR EXISTS (SELECT 1 FROM fork_event_type_links l JOIN event_types c ON c.id = l.copy_id
		                       WHERE l.template_id = ? AND l.user_id = u.id AND l.kind = ?
		                         AND c.is_active = 1 AND c.archived_at IS NULL))`
		args = append(args, rule.templateID, rule.templateID, linkKindCopy)
	case areaSoporte:
		query += ` AND a.area = ?`
		args = append(args, areaSoporte)
	}
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []teamCandidateJSON{}
	for rows.Next() {
		var c teamCandidateJSON
		if err := rows.Scan(&c.ID, &c.Name, &c.Area); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// ---- The host link of the video room --------------------------------------------------

// teamRoomTokenHash is how a host room token is stored: its SHA-256, hex.
func teamRoomTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// saveTeamHostLink stores the hash of bookingID's current host link.
func saveTeamHostLink(ctx context.Context, q teamQuerier, bookingID, token string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO fork_livekit_host_links (booking_id, token_hash, created_at) VALUES (?, ?, ?)
		ON CONFLICT (booking_id) DO UPDATE SET token_hash = excluded.token_hash, created_at = excluded.created_at`,
		bookingID, teamRoomTokenHash(token), teamNow())
	return err
}

// recordTeamHostLink is the hook where a booking's host link is minted (the booking's
// creation): it stores the hash of that link's room token. Best effort - without a row the
// room keeps the upstream rule (any role=host token of the room is host).
func (h *Handler) recordTeamHostLink(ctx context.Context, bookingID, hostURL string) {
	u, err := url.Parse(hostURL)
	if err != nil || u.Query().Get("t") == "" {
		return
	}
	if err := saveTeamHostLink(ctx, h.db, bookingID, u.Query().Get("t")); err != nil {
		h.logger.ErrorContext(ctx, "livekit: record host link", "error", err, "booking_id", bookingID)
	}
}

// freshTeamHostLink mints a new host link for a reassigned booking's room (a unique token:
// the upstream one is deterministic, so it would equal the previous host's) and stores its
// hash in tx. "" when the booking has no room or LiveKit is off.
func (h *Handler) freshTeamHostLink(ctx context.Context, tx *sql.Tx, b *booking.Booking) (string, error) {
	lk := h.getLiveKit()
	if lk == nil {
		return "", nil
	}
	var room string
	if err := tx.QueryRowContext(ctx, `SELECT livekit_room FROM bookings WHERE id = ?`, b.ID).Scan(&room); err != nil {
		return "", err
	}
	if room == "" {
		return "", nil
	}
	link := lk.BookingJoinURLUnique(h.baseURL, room, "host", b.EndAt.Add(liveKitJoinGrace))
	u, err := url.Parse(link)
	if err != nil {
		return "", err
	}
	if err := saveTeamHostLink(ctx, tx, b.ID, u.Query().Get("t")); err != nil {
		return "", fmt.Errorf("reassign: host link: %w", err)
	}
	return link, nil
}

// teamHostLinkCurrent reports whether a role=host room token of room is the booking's
// CURRENT host link: true when the booking has no stored hash (upstream rule), else only
// when the hash matches. A lookup error counts as not current (the signed-in current host
// is still recognised by the callers' own session check).
func (h *Handler) teamHostLinkCurrent(ctx context.Context, room, token string) bool {
	var stored string
	bookingID, err := h.bookingForRoom(ctx, room)
	if err == nil {
		err = h.db.QueryRowContext(ctx,
			`SELECT token_hash FROM fork_livekit_host_links WHERE booking_id = ?`, bookingID).Scan(&stored)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "livekit: host link lookup", "error", err)
		return false
	}
	return stored == teamRoomTokenHash(token)
}

// teamHostLinkRole is LiveKitToken's hook right after the room token is verified: a
// role=host token that is not the booking's current host link becomes an attendee token
// (""). LiveKitToken then promotes a signed-in current booking host as it always has.
func (h *Handler) teamHostLinkRole(ctx context.Context, room, token, role string) string {
	if role == "host" && !h.teamHostLinkCurrent(ctx, room, token) {
		return ""
	}
	return role
}

// withTeamHostLink is the reassign email to the new host: the fresh host link (controls
// enabled) instead of the attendee link, as the booking's creation does for its host.
func withTeamHostLink(d mailer.BookingData, hostLink string) mailer.BookingData {
	if hostLink != "" {
		d.LocationValue = hostLink
	}
	return d
}
