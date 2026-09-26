package handler

// Fork (Agenda Maestros 4x4): the team feature's API (the engine is fork_team.go).
//
//	GET  /v1/team/settings            admins: the two templates (T and S) and their copies
//	PUT  /v1/team/settings            owner: choose / unset T and S, then reconcile
//	PUT  /v1/users/{id}/team-role     tier (admin|member) + área, per the permission matrix
//	GET  /v1/users, GET /v1/users/me  + area, personal_link (fillUserTeamInfo, teamInfoForUser)
//	POST/GET/DELETE /v1/invites       + role (route wrappers), applied on claim (applyInviteRole)
//
// Every error meant for the panel is Spanish (the admin SPA is hardcoded Spanish).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// teamLinkInfo is one fork_event_type_links row with the names the API shows.
type teamLinkInfo struct {
	copyID, templateID, userID, kind string
	templateSlug, templateName       string
	mentorName                       string // the person who attends the copy
	copySlug                         string
	area                             string // copies: the área of their template (teamTemplateArea)
	active                           bool
}

// teamIndex is the team feature's view of the workspace, loaded in three small queries:
// the settings, the recorded template áreas and every link (a workspace has a handful of
// people).
type teamIndex struct {
	st            teamSettings
	links         map[string]teamLinkInfo // by copy/holder id
	activeCopies  map[string]int          // template id -> active copies
	anyCopies     map[string]int          // template id -> copies, active or not
	templateAreas map[string]string       // fork_template_areas
}

func (h *Handler) loadTeamIndex(ctx context.Context) (*teamIndex, error) {
	st, err := loadTeamSettings(ctx, h.db)
	if err != nil {
		return nil, err
	}
	idx := &teamIndex{st: st, links: map[string]teamLinkInfo{}, activeCopies: map[string]int{},
		anyCopies: map[string]int{}, templateAreas: map[string]string{}}
	arows, err := h.db.QueryContext(ctx, `SELECT template_id, area FROM fork_template_areas`)
	if err != nil {
		return nil, err
	}
	for arows.Next() {
		var id, area string
		if err := arows.Scan(&id, &area); err != nil {
			arows.Close() // #nosec G104 -- already returning the scan error
			return nil, err
		}
		idx.templateAreas[id] = area
	}
	arows.Close() // #nosec G104 -- drained
	if err := arows.Err(); err != nil {
		return nil, err
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT l.copy_id, l.template_id, l.user_id, l.kind, c.slug, c.is_active,
		       COALESCE(t.slug, ''), COALESCE(t.name, ''), COALESCE(u.name, '')
		FROM fork_event_type_links l
		JOIN event_types c ON c.id = l.copy_id
		LEFT JOIN event_types t ON t.id = l.template_id
		LEFT JOIN users u ON u.id = l.user_id
		ORDER BY COALESCE(u.name, ''), l.copy_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var li teamLinkInfo
		if err := rows.Scan(&li.copyID, &li.templateID, &li.userID, &li.kind, &li.copySlug, &li.active,
			&li.templateSlug, &li.templateName, &li.mentorName); err != nil {
			return nil, err
		}
		li.area = idx.templateArea(li.templateID)
		idx.links[li.copyID] = li
		if li.kind == linkKindCopy {
			idx.anyCopies[li.templateID]++
			if li.active {
				idx.activeCopies[li.templateID]++
			}
		}
	}
	return idx, rows.Err()
}

// templateArea is teamTemplateArea over the loaded index.
func (idx *teamIndex) templateArea(templateID string) string {
	if a := idx.st.currentArea(templateID); a != "" {
		return a
	}
	if idx.templateAreas[templateID] == areaSoporte {
		return areaSoporte
	}
	return areaMentoria
}

// Kinds of the "team" object on event types (fork_team_guards.go) and internal ones.
const (
	teamKindMentoriaTemplate = "mentoria_template"
	teamKindMentoriaCopy     = "mentoria_copy"
	teamKindSoporteTemplate  = "soporte_template"
	teamKindSoporteCopy      = "soporte_copy"
	teamKindHolder           = "holder" // never shown: holders are omitted everywhere
)

// isTeamCopyKind reports whether kind is a copy of either template.
func isTeamCopyKind(kind string) bool {
	return kind == teamKindMentoriaCopy || kind == teamKindSoporteCopy
}

// isTeamTemplateKind reports whether kind is either template.
func isTeamTemplateKind(kind string) bool {
	return kind == teamKindMentoriaTemplate || kind == teamKindSoporteTemplate
}

// kindOf classifies an event type: "" = an ordinary type.
func (idx *teamIndex) kindOf(etID string) string {
	if li, ok := idx.links[etID]; ok {
		switch {
		case li.kind == linkKindHolder:
			return teamKindHolder
		case li.area == areaSoporte:
			return teamKindSoporteCopy
		}
		return teamKindMentoriaCopy
	}
	switch idx.st.currentArea(etID) {
	case areaMentoria:
		return teamKindMentoriaTemplate
	case areaSoporte:
		return teamKindSoporteTemplate
	}
	return ""
}

// areaOf is the área of an event type's bookings: T or a Mentoría copy is Mentoría, S or
// a Soporte copy is Soporte, anything else (a holder included) is "".
func (idx *teamIndex) areaOf(etID string) string {
	switch idx.kindOf(etID) {
	case teamKindMentoriaTemplate, teamKindMentoriaCopy:
		return areaMentoria
	case teamKindSoporteTemplate, teamKindSoporteCopy:
		return areaSoporte
	}
	return ""
}

// copiesOf lists the copy links of a template, sorted by the attending person's name.
func (idx *teamIndex) copiesOf(templateID string) []teamLinkInfo {
	var out []teamLinkInfo
	for _, li := range idx.links {
		if li.kind == linkKindCopy && li.templateID == templateID {
			out = append(out, li)
		}
	}
	sortTeamLinks(out)
	return out
}

func sortTeamLinks(ls []teamLinkInfo) {
	for i := 1; i < len(ls); i++ {
		for j := i; j > 0 && strings.ToLower(ls[j].mentorName) < strings.ToLower(ls[j-1].mentorName); j-- {
			ls[j], ls[j-1] = ls[j-1], ls[j]
		}
	}
}

// teamCopyLinkJSON is one person's personal link (their copy of a template), as the owner
// sees it. mentor_name is the attending person: a mentor or a support person.
type teamCopyLinkJSON struct {
	Slug       string `json:"slug"`
	URL        string `json:"url"`
	MentorName string `json:"mentor_name"`
	Active     bool   `json:"active"`
}

func (h *Handler) bookURL(slug string) string {
	return strings.TrimRight(h.publicURL(), "/") + "/book/" + slug
}

func (h *Handler) copyLinksJSON(links []teamLinkInfo) []teamCopyLinkJSON {
	out := make([]teamCopyLinkJSON, 0, len(links))
	for _, li := range links {
		out = append(out, teamCopyLinkJSON{Slug: li.copySlug, URL: h.bookURL(li.copySlug), MentorName: li.mentorName, Active: li.active})
	}
	return out
}

// teamSettingsJSON is GET/PUT /v1/team/settings. Both templates have the same shape.
type teamSettingsJSON struct {
	MentoriaTemplate *teamTemplateJSON `json:"mentoria_template"`
	SoporteTemplate  *teamTemplateJSON `json:"soporte_template"`
	CanEdit          bool              `json:"can_edit"`
	Warnings         *teamWarnings     `json:"warnings,omitempty"` // PUT only
}

type teamTemplateJSON struct {
	ID        string             `json:"id"`
	Slug      string             `json:"slug"`
	Name      string             `json:"name"`
	Copies    int                `json:"copies"`               // active copies (one per person of the área)
	CopyLinks []teamCopyLinkJSON `json:"copy_links,omitempty"` // owner only: every copy, with its state
}

// teamWarnings accompany a PUT: what the change did (the copies of both templates
// together) and what the owner may still need to do (a template no active webhook of
// theirs receives sends no WhatsApp).
type teamWarnings struct {
	CopiesCreated             int  `json:"copies_created"`
	CopiesDeactivated         int  `json:"copies_deactivated"`
	MentoriaTemplateNoWebhook bool `json:"mentoria_template_no_webhook"`
	SoporteTemplateNoWebhook  bool `json:"soporte_template_no_webhook"`
}

// teamSettingsFor builds the settings answer for user.
func (h *Handler) teamSettingsFor(ctx context.Context, user AuthUser) (*teamSettingsJSON, error) {
	idx, err := h.loadTeamIndex(ctx)
	if err != nil {
		return nil, err
	}
	out := &teamSettingsJSON{CanEdit: user.IsOwner}
	for _, c := range []struct {
		id  string
		dst **teamTemplateJSON
	}{{idx.st.templateID, &out.MentoriaTemplate}, {idx.st.soporteID, &out.SoporteTemplate}} {
		t, err := loadTeamType(ctx, h.db, c.id)
		if err != nil {
			return nil, err
		}
		if t == nil {
			continue
		}
		j := &teamTemplateJSON{ID: t.id, Slug: t.slug, Name: t.name, Copies: idx.activeCopies[t.id]}
		if user.IsOwner {
			j.CopyLinks = h.copyLinksJSON(idx.copiesOf(t.id))
		}
		*c.dst = j
	}
	return out, nil
}

// GetTeamSettings handles GET /v1/team/settings (admins; the owner also gets copy_links).
func (h *Handler) GetTeamSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	out, err := h.teamSettingsFor(r.Context(), user)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "team settings: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, out)
}

// optionalID decodes a settings field: omitted = unchanged (set=false), null or "" = unset.
type optionalID struct {
	set bool
	id  string
}

func (o *optionalID) UnmarshalJSON(b []byte) error {
	o.set = true
	if string(b) == "null" {
		o.id = ""
		return nil
	}
	return json.Unmarshal(b, &o.id)
}

// PutTeamSettings handles PUT /v1/team/settings (owner): {"mentoria_template_id":
// "<id>"|null, "soporte_template_id": "<id>"|null}; an omitted key is left as it is.
// "soporte_shared_id" (the old name) is still read when soporte_template_id is absent. A
// newly chosen type must be the owner's own, not a copy or holder, not archived, on the
// built-in video room, and the two must differ (400, Spanish). Saves, reconciles, and
// answers like GET plus "warnings".
func (h *Handler) PutTeamSettings(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if !user.IsOwner {
		h.writeError(w, http.StatusForbidden, "Solo el propietario elige los tipos predefinidos.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		MentoriaTemplateID optionalID `json:"mentoria_template_id"`
		SoporteTemplateID  optionalID `json:"soporte_template_id"`
		SoporteSharedID    optionalID `json:"soporte_shared_id"` // alias of soporte_template_id
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	idx, err := h.loadTeamIndex(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "team settings: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	tmplID, supID := idx.st.templateID, idx.st.soporteID
	if req.MentoriaTemplateID.set {
		tmplID = strings.TrimSpace(req.MentoriaTemplateID.id)
	}
	switch {
	case req.SoporteTemplateID.set:
		supID = strings.TrimSpace(req.SoporteTemplateID.id)
	case req.SoporteSharedID.set:
		supID = strings.TrimSpace(req.SoporteSharedID.id)
	}
	if tmplID != "" && tmplID == supID {
		h.writeError(w, http.StatusBadRequest, "La plantilla de Mentoría y la de Soporte deben ser tipos distintos.")
		return
	}
	// Validate on change: a value that is already saved is not re-judged.
	for _, c := range []struct{ id, stored, area string }{
		{tmplID, idx.st.templateID, areaMentoria}, {supID, idx.st.soporteID, areaSoporte},
	} {
		if c.id == "" || c.id == c.stored {
			continue
		}
		if msg, err := h.teamSettingCandidateError(r.Context(), idx, user, c.id, c.area); err != nil {
			h.logger.ErrorContext(r.Context(), "team settings: validate", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		} else if msg != "" {
			h.writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	if err := h.setForkSettings(r.Context(), map[string]string{
		forkKeyTeamTemplate: tmplID,
		forkKeyTeamSoporte:  supID,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "team settings: save", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	stats := h.reconcileTeamAfter(r.Context(), "")
	out, err := h.teamSettingsFor(r.Context(), user)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "team settings: reload", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	warn := &teamWarnings{CopiesCreated: stats.Created, CopiesDeactivated: stats.Deactivated}
	if tmplID != "" {
		warn.MentoriaTemplateNoWebhook = !h.ownerWebhookReceives(r.Context(), user.ID, tmplID)
	}
	if supID != "" {
		warn.SoporteTemplateNoWebhook = !h.ownerWebhookReceives(r.Context(), user.ID, supID)
	}
	out.Warnings = warn
	h.writeJSON(w, http.StatusOK, out)
}

// teamSettingCandidateError says why id cannot become the template of área ("" = it can).
func (h *Handler) teamSettingCandidateError(ctx context.Context, idx *teamIndex, owner AuthUser, id, area string) (string, error) {
	var name, userID, locType string
	var archived bool
	err := h.db.QueryRowContext(ctx,
		`SELECT name, user_id, location_type, archived_at IS NOT NULL FROM event_types WHERE id = ?`, id).
		Scan(&name, &userID, &locType, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return "El tipo de atención elegido no existe.", nil
	}
	if err != nil {
		return "", err
	}
	switch idx.kindOf(id) {
	case teamKindMentoriaCopy:
		return "«" + name + "» es la copia de un mentor; elige un tipo de atención tuyo.", nil
	case teamKindSoporteCopy:
		return "«" + name + "» es la copia de una persona de soporte; elige un tipo de atención tuyo.", nil
	case teamKindHolder:
		return "«" + name + "» guarda preguntas retiradas y no se puede elegir.", nil
	}
	if userID != owner.ID {
		return "«" + name + "» no es tuyo: los tipos predefinidos deben ser del propietario.", nil
	}
	if archived {
		return "«" + name + "» está archivado; restáuralo antes de elegirlo.", nil
	}
	if locType != "livekit" {
		return "«" + name + "» debe usar la sala de video integrada para ser un tipo predefinido.", nil
	}
	// A template's copies are Mentoría or Soporte by the área of their template (one value
	// per template, teamTemplateArea), so a type that already has copies stays in its área:
	// switching it would relabel every existing session - and offer it to the other área.
	if idx.anyCopies[id] > 0 && idx.templateArea(id) != area {
		if area == areaSoporte {
			return "«" + name + "» ya tiene copias de Mentoría (una por mentor); para Soporte elige otro tipo de atención o duplícalo.", nil
		}
		return "«" + name + "» ya tiene copias de Soporte (una por persona de soporte); para Mentoría elige otro tipo de atención o duplícalo.", nil
	}
	return "", nil
}

// ownerWebhookReceives reports whether some active webhook of the owner would receive the
// bookings of etID: an unfiltered one, or one whose filter lists it. Best effort (false on
// error, which only shows a warning).
func (h *Handler) ownerWebhookReceives(ctx context.Context, ownerID, etID string) bool {
	var n int
	err := h.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM webhooks w
		WHERE w.user_id = ? AND w.is_active = 1
		  AND (NOT EXISTS (SELECT 1 FROM webhook_event_type_filters f WHERE f.webhook_id = w.id)
		       OR EXISTS (SELECT 1 FROM webhook_event_type_filters f WHERE f.webhook_id = w.id AND f.event_type_id = ?))`,
		ownerID, etID).Scan(&n)
	return err == nil && n > 0
}

// teamFamily returns the event type ids of an área: its current template and every copy
// whose template serves that área (old templates included: a copy's bookings keep the
// área it was made for - teamTemplateArea). Nil for an unknown área.
func (h *Handler) teamFamily(ctx context.Context, area string) ([]string, error) {
	if area != areaMentoria && area != areaSoporte {
		return nil, nil
	}
	idx, err := h.loadTeamIndex(ctx)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	if id := idx.st.templateFor(area); id != "" {
		ids = append(ids, id)
	}
	for _, li := range idx.links {
		if li.kind == linkKindCopy && li.area == area {
			ids = append(ids, li.copyID)
		}
	}
	return ids, nil
}

// SetTeamRole handles PUT /v1/users/{id}/team-role: {"tier": "admin"|"member", "area":
// "mentoria"|"soporte"|""}. The matrix:
//   - the owner may set the tier and área of anyone but themselves, and their OWN área
//     (their tier stays owner; tier may be omitted or "owner"/"admin");
//   - an admin may set only the área of a non-admin member (tier must stay "member"),
//     never another admin's, never their own;
//   - nobody but the owner targets the owner; members get 403.
//
// One transaction (users.is_admin, is_support cleared, fork_member_areas), then a
// reconcile for that user. Answers {id, tier, area, upcoming_in_previous_area}: how many
// upcoming sessions the person hosts in the área they just left (the panel warns BEFORE
// with GET /v1/users/{id}/upcoming-bookings; this is the after-the-fact count).
func (h *Handler) SetTeamRole(w http.ResponseWriter, r *http.Request) {
	actor, _ := userFromContext(r.Context())
	if !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "Solo el propietario y los administradores cambian roles.")
		return
	}
	targetID := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		Tier string `json:"tier"`
		Area string `json:"area"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	switch req.Area {
	case "", areaMentoria, areaSoporte:
	default:
		h.writeError(w, http.StatusBadRequest, "El área debe ser 'mentoria', 'soporte' o vacía.")
		return
	}

	var isAdmin, isOwner bool
	var prevArea string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT u.is_admin, u.is_owner, COALESCE(a.area, '')
		FROM users u LEFT JOIN fork_member_areas a ON a.user_id = u.id WHERE u.id = ?`, targetID).
		Scan(&isAdmin, &isOwner, &prevArea)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "No existe esa persona.")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "team role: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	tier := req.Tier
	switch {
	case isOwner:
		if !actor.IsOwner || actor.ID != targetID {
			h.writeError(w, http.StatusForbidden, "Solo el propietario cambia su propia área.")
			return
		}
		if tier != "" && tier != "owner" && tier != "admin" {
			h.writeError(w, http.StatusBadRequest, "El propietario sigue siendo propietario; aquí solo cambia su área.")
			return
		}
		tier = "owner"
		// The templates ARE the owner's links: owning one, they get no copy of it, so its
		// área would do nothing (and read as if they attended through a copy).
		if msg, err := h.ownerAreaRefusal(r.Context(), targetID, req.Area); err != nil {
			h.logger.ErrorContext(r.Context(), "team role: owner área", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		} else if msg != "" {
			h.writeError(w, http.StatusBadRequest, msg)
			return
		}
	case actor.IsOwner:
		if tier != "admin" && tier != "member" {
			h.writeError(w, http.StatusBadRequest, "El nivel debe ser 'admin' o 'member'.")
			return
		}
	default: // an admin who is not the owner
		if actor.ID == targetID {
			h.writeError(w, http.StatusForbidden, "No puedes cambiar tu propio rol; pídeselo al propietario.")
			return
		}
		if isAdmin {
			h.writeError(w, http.StatusForbidden, "Solo el propietario cambia el rol de un administrador.")
			return
		}
		if tier == "" {
			tier = "member"
		}
		if tier != "member" {
			h.writeError(w, http.StatusForbidden, "Solo el propietario nombra administradores.")
			return
		}
	}

	upcoming := 0
	if prevArea != "" && prevArea != req.Area {
		upcoming, err = h.upcomingInArea(r.Context(), targetID, prevArea)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "team role: count upcoming", "error", err)
		}
	}

	err = h.teamTx(r.Context(), func(tx *sql.Tx) error {
		if tier == "admin" || tier == "member" {
			admin := 0
			if tier == "admin" {
				admin = 1
			}
			if _, err := tx.ExecContext(r.Context(),
				`UPDATE users SET is_admin = ?, is_support = 0 WHERE id = ? AND is_owner = 0`, admin, targetID); err != nil {
				return err
			}
		}
		if req.Area == "" {
			_, err := tx.ExecContext(r.Context(), `DELETE FROM fork_member_areas WHERE user_id = ?`, targetID)
			return err
		}
		_, err := tx.ExecContext(r.Context(), `
			INSERT INTO fork_member_areas (user_id, area, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (user_id) DO UPDATE SET area = excluded.area, updated_at = excluded.updated_at`,
			targetID, req.Area, teamNow())
		return err
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "team role: save", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.reconcileTeamAfter(r.Context(), targetID)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"id": targetID, "tier": tier, "area": req.Area, "upcoming_in_previous_area": upcoming,
		// Fork: whether their weekly hours reach what they now attend; false = their
		// personal link shows no times until they set them (fork_team_hours.go).
		"has_availability": h.teamHoursChecker(r.Context())(targetID, req.Area),
	})
}

// ownerAreaRefusal is the Spanish 400 for the owner taking área while they own that área's
// template, or "".
func (h *Handler) ownerAreaRefusal(ctx context.Context, ownerID, area string) (string, error) {
	st, err := loadTeamSettings(ctx, h.db)
	if err != nil {
		return "", err
	}
	t, err := loadTeamType(ctx, h.db, st.templateFor(area))
	if err != nil || t == nil || t.userID != ownerID {
		return "", err
	}
	if area == areaSoporte {
		return "Tu enlace de Soporte es la plantilla «" + t.name + "»: como propietario no recibes una copia, " +
			"así que no necesitas el área Soporte.", nil
	}
	return "Tu enlace de Mentoría es la plantilla «" + t.name + "»: como propietario no recibes una copia, " +
		"así que no necesitas el área Mentoría.", nil
}

// upcomingInArea counts the upcoming, non-cancelled bookings userID hosts (primary or a
// seat) whose type belongs to área.
func (h *Handler) upcomingInArea(ctx context.Context, userID, area string) (int, error) {
	ids, err := h.teamFamily(ctx, area)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	idsJSON, _ := json.Marshal(ids)
	var n int
	err = h.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT b.id) FROM bookings b
		LEFT JOIN booking_hosts bh ON bh.booking_id = b.id
		WHERE (b.host_id = ? OR bh.user_id = ?) AND b.status != 'cancelled'
		  AND b.end_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		  AND b.event_type_id IN (SELECT value FROM json_each(?))`, userID, userID, string(idsJSON)).Scan(&n)
	return n, err
}

// personalLinkJSON is a person's own booking link: their copy of their área's template,
// or T itself for its owner. url is the public booking page.
type personalLinkJSON struct {
	Slug   string `json:"slug"`
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// teamPeople returns every user's área and personal link. Three small queries; best
// effort (empty maps on error, logged): the members list must never fail over this. The
// link is the person's copy of S when their área is soporte, else their copy of T (a
// person with no área keeps showing a T copy they may still have); T's owner always gets
// T itself. S's owner has no copy of S, so without T they have no link.
func (h *Handler) teamPeople(ctx context.Context) (map[string]string, map[string]*personalLinkJSON) {
	areas := map[string]string{}
	links := map[string]*personalLinkJSON{}
	rows, err := h.db.QueryContext(ctx, `SELECT user_id, area FROM fork_member_areas`)
	if err != nil {
		h.logger.ErrorContext(ctx, "team people: areas", "error", err)
		return areas, links
	}
	for rows.Next() {
		var id, area string
		if rows.Scan(&id, &area) == nil {
			areas[id] = area
		}
	}
	rows.Close() // #nosec G104 -- drained
	st, err := loadTeamSettings(ctx, h.db)
	if err != nil || (st.templateID == "" && st.soporteID == "") {
		return areas, links
	}
	// Each row: a copy of T or S (its template id), or T itself (source 'template').
	rows, err = h.db.QueryContext(ctx, `
		SELECT l.user_id, c.slug, c.is_active = 1 AND c.archived_at IS NULL, l.template_id
		FROM fork_event_type_links l JOIN event_types c ON c.id = l.copy_id
		WHERE l.kind = ? AND l.template_id <> '' AND l.template_id IN (?, ?)
		UNION ALL
		SELECT t.user_id, t.slug, t.is_active = 1 AND t.archived_at IS NULL, 'template'
		FROM event_types t WHERE t.id = ?`, linkKindCopy, st.templateID, st.soporteID, st.templateID)
	if err != nil {
		h.logger.ErrorContext(ctx, "team people: links", "error", err)
		return areas, links
	}
	defer rows.Close()
	ownT := map[string]bool{}
	for rows.Next() {
		var id, slug, source string
		var active bool
		if rows.Scan(&id, &slug, &active, &source) != nil {
			continue
		}
		link := &personalLinkJSON{Slug: slug, URL: h.bookURL(slug), Active: active}
		switch {
		case source == "template":
			links[id], ownT[id] = link, true
		case ownT[id]:
		case source == st.templateFor(areas[id]) || (areas[id] != areaSoporte && source == st.templateID):
			links[id] = link
		}
	}
	return areas, links
}

// teamInfoForUser is teamPeople for one person (GET/PATCH /v1/users/me).
func (h *Handler) teamInfoForUser(ctx context.Context, userID string) (string, *personalLinkJSON) {
	areas, links := h.teamPeople(ctx)
	return areas[userID], links[userID]
}

// ---- Invites with a role --------------------------------------------------------------

// Invite roles (fork_invite_roles.role): the área spelling, plus admin.
const inviteRoleAdmin = "admin"

// teamBufferWriter buffers a wrapped handler's response so a wrapper can read or amend it.
type teamBufferWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newTeamBufferWriter() *teamBufferWriter { return &teamBufferWriter{header: http.Header{}} }

func (b *teamBufferWriter) Header() http.Header { return b.header }
func (b *teamBufferWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}
func (b *teamBufferWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}
func (b *teamBufferWriter) code() int {
	if b.status == 0 {
		return http.StatusOK
	}
	return b.status
}
func (b *teamBufferWriter) ok() bool { return b.code() >= 200 && b.code() < 300 }

// flush writes the buffered response, with body (which may be an amended copy).
func (b *teamBufferWriter) flush(w http.ResponseWriter, body []byte) {
	for k, v := range b.header {
		w.Header()[k] = v
	}
	w.Header().Del("Content-Length")
	w.WriteHeader(b.code())
	_, _ = w.Write(body)
}

// teamStatusWriter records the status a wrapped handler wrote, passing everything through.
type teamStatusWriter struct {
	http.ResponseWriter
	status int
}

func (s *teamStatusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}
func (s *teamStatusWriter) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(p)
}
func (s *teamStatusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
func (s *teamStatusWriter) ok() bool {
	c := s.status
	if c == 0 {
		c = http.StatusOK
	}
	return c >= 200 && c < 300
}

// readBody buffers r's body (bounded) and puts an identical copy back for the next handler.
func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// decodeBody decodes a buffered body EXACTLY as the wrapped upstream handlers do (one
// json.Decoder value; trailing data is ignored), so a guard checks the very values the
// handler will act on. A body it cannot decode fails the handler the same way (its 400).
func decodeBody(body []byte, v any) error {
	return json.NewDecoder(bytes.NewReader(body)).Decode(v)
}

// TeamCreateInvite wraps POST /v1/invites: the body may carry "role": "mentoria" |
// "soporte" | "admin" (only the owner may invite an admin). After the upstream handler
// answers 2xx, the role is stored for that email (fork_invite_roles) - or removed when
// none is sent - and echoed as "role" in the answer. A non-owner never overwrites a
// stored admin row (the owner's decision stands).
func (h *Handler) TeamCreateInvite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := userFromContext(r.Context())
		body, err := readBody(w, r, 4<<10)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var req struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		if err := decodeBody(body, &req); err != nil {
			// A mistyped role is the one error upstream would not see (it reads only the
			// email): refuse it here rather than create the invite without its role.
			var te *json.UnmarshalTypeError
			if errors.As(err, &te) && te.Field == "role" {
				h.writeError(w, http.StatusBadRequest, "El rol debe ser 'mentoria', 'soporte' o 'admin'.")
				return
			}
			next(w, r) // upstream answers the malformed body
			return
		}
		switch req.Role {
		case "", areaMentoria, areaSoporte:
		case inviteRoleAdmin:
			if actor.IsAdmin && !actor.IsOwner {
				h.writeError(w, http.StatusForbidden, "Solo el propietario puede invitar administradores.")
				return
			}
		default:
			h.writeError(w, http.StatusBadRequest, "El rol debe ser 'mentoria', 'soporte' o 'admin'.")
			return
		}
		buf := newTeamBufferWriter()
		next(buf, r)
		if !buf.ok() {
			buf.flush(w, buf.body.Bytes())
			return
		}
		email := strings.TrimSpace(strings.ToLower(req.Email))
		role, err := h.storeInviteRole(r.Context(), actor, email, req.Role)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "invite role: save", "error", err)
		}
		buf.flush(w, withJSONField(buf.body.Bytes(), "role", role))
	}
}

// storeInviteRole writes (or clears) the role of email's invite and returns the role that
// is now stored.
func (h *Handler) storeInviteRole(ctx context.Context, actor AuthUser, email, role string) (string, error) {
	var stored string
	err := h.db.QueryRowContext(ctx, `SELECT role FROM fork_invite_roles WHERE email = ?`, email).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if stored == inviteRoleAdmin && !actor.IsOwner {
		return stored, nil
	}
	if role == "" {
		_, err := h.db.ExecContext(ctx, `DELETE FROM fork_invite_roles WHERE email = ?`, email)
		return "", err
	}
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO fork_invite_roles (email, role, created_by, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (email) DO UPDATE SET role = excluded.role, created_by = excluded.created_by,
		                                  created_at = excluded.created_at`,
		email, role, actor.ID, teamNow())
	return role, err
}

// withJSONField adds key=value to a JSON object body; anything else is returned as it is.
func withJSONField(body []byte, key string, value any) []byte {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil || m == nil {
		return body
	}
	m[key] = value
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return append(out, '\n')
}

// TeamRevokeInvite wraps DELETE /v1/invites/{id}: after a 2xx, the email's stored role is
// removed too (a leftover admin row would be a standing grant for the next invite).
func (h *Handler) TeamRevokeInvite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var email string
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT email FROM invite_tokens WHERE id = ? AND used_at IS NULL`, r.PathValue("id")).Scan(&email)
		sw := &teamStatusWriter{ResponseWriter: w}
		next(sw, r)
		if sw.ok() && email != "" {
			if _, err := h.db.ExecContext(context.WithoutCancel(r.Context()),
				`DELETE FROM fork_invite_roles WHERE email = ?`, email); err != nil {
				h.logger.ErrorContext(r.Context(), "invite role: delete", "error", err)
			}
		}
	}
}

// TeamListInvites wraps GET /v1/invites: every item gains "role" ("" = none).
func (h *Handler) TeamListInvites(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		buf := newTeamBufferWriter()
		next(buf, r)
		var items []map[string]any
		if !buf.ok() || json.Unmarshal(buf.body.Bytes(), &items) != nil {
			buf.flush(w, buf.body.Bytes())
			return
		}
		roles := map[string]string{}
		if rows, err := h.db.QueryContext(r.Context(), `SELECT email, role FROM fork_invite_roles`); err == nil {
			for rows.Next() {
				var e, role string
				if rows.Scan(&e, &role) == nil {
					roles[e] = role
				}
			}
			rows.Close() // #nosec G104 -- drained
		} else {
			h.logger.ErrorContext(r.Context(), "invite roles: list", "error", err)
		}
		for _, it := range items {
			email, _ := it["email"].(string)
			it["role"] = roles[strings.ToLower(email)]
		}
		out, err := json.Marshal(items)
		if err != nil {
			buf.flush(w, buf.body.Bytes())
			return
		}
		buf.flush(w, append(out, '\n'))
	}
}

// applyInviteRole is ClaimInvite's hook, run on ITS transaction (the pool is a single
// connection: h.db here would deadlock). It applies the role stored for email to the new
// user and deletes the row: admin only while the invite's creator is still the owner
// (else nothing - logged); an área as an área row. Never fails the claim.
func (h *Handler) applyInviteRole(ctx context.Context, tx *sql.Tx, email, userID string) {
	var role, createdBy string
	err := tx.QueryRowContext(ctx,
		`SELECT role, created_by FROM fork_invite_roles WHERE email = ?`, strings.ToLower(email)).Scan(&role, &createdBy)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "invite role: load on claim", "error", err)
		return
	}
	switch role {
	case inviteRoleAdmin:
		var ownerStill bool
		_ = tx.QueryRowContext(ctx,
			`SELECT COUNT(*) > 0 FROM users WHERE id = ? AND is_owner = 1 AND archived_at IS NULL`, createdBy).Scan(&ownerStill)
		if !ownerStill {
			h.logger.WarnContext(ctx, "invite role: admin grant ignored, its creator is no longer the owner")
			break
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET is_admin = 1 WHERE id = ?`, userID); err != nil {
			h.logger.ErrorContext(ctx, "invite role: grant admin", "error", err)
		}
	case areaMentoria, areaSoporte:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fork_member_areas (user_id, area, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (user_id) DO UPDATE SET area = excluded.area, updated_at = excluded.updated_at`,
			userID, role, teamNow()); err != nil {
			h.logger.ErrorContext(ctx, "invite role: set área", "error", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fork_invite_roles WHERE email = ?`, strings.ToLower(email)); err != nil {
		h.logger.ErrorContext(ctx, "invite role: delete on claim", "error", err)
	}
}
