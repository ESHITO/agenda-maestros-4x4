package handler

// Fork (Agenda Maestros 4x4): the guards and triggers of the team feature, as route
// wrappers (server.go) so the upstream handlers stay untouched, plus the "team" object
// on the event-type API. The engine is fork_team.go, the API fork_team_api.go.
//
// Guards (Spanish 409s) - checked BEFORE the wrapped handler, which resolves {slug}
// itself; a wrapper resolves slug -> id first, because a PATCH may rename the slug:
//   - a copy (a mentor's copy of T, a support person's copy of S): PATCH, DELETE,
//     duplicate, transfer, hosts PUT, questions, WhatsApp PUT/preview - it is edited
//     through its template;
//   - a holder (retired questions): every mutation;
//   - T and S alike: transfer, hosts PUT (their owner, locked by the reconcile), a PATCH
//     that CHANGES routing_mode / rr_strategy or moves the location off the built-in
//     video room (validate on change: the editor PATCHes the whole form), DELETE of a
//     template that has copies;
//   - workspace ownership transfer while either setting is set.
//
// Triggers (after a 2xx, detached from the request's cancellation): template edits
// (PATCH, questions) reconcile; so do role, archive, restore, delete and an invite claim
// (TeamReconcileAfter).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/calnode/calnode/internal/webhook"
)

// TeamOp names the guarded event-type endpoint a TeamEventTypeGuard wraps.
type TeamOp int

const (
	TeamOpPatch TeamOp = iota
	TeamOpDelete
	TeamOpDuplicate
	TeamOpTransfer
	TeamOpHostsPut
	TeamOpQuestions     // create, update and delete of a question
	TeamOpWhatsAppWrite // PUT texts and the preview
)

// teamEventType is what a guard knows about the event type in {slug}.
type teamEventType struct {
	id, userID, name        string
	routingMode, rrStrategy string
	locationType            string
	kind                    string // teamKind*, "" = ordinary
	link                    teamLinkInfo
	copies                  int    // copy links whose template is this type
	copiesArea              string // the área those copies serve (teamTemplateArea)
}

// teamEventTypeBySlug loads the event type named slug (any owner: slugs are unique) with
// its team classification. nil when there is none.
func (h *Handler) teamEventTypeBySlug(ctx context.Context, slug string) (*teamEventType, *teamIndex, error) {
	var et teamEventType
	err := h.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, routing_mode, rr_strategy, location_type
		FROM event_types WHERE slug = ?`, slug).
		Scan(&et.id, &et.userID, &et.name, &et.routingMode, &et.rrStrategy, &et.locationType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	idx, err := h.loadTeamIndex(ctx)
	if err != nil {
		return nil, nil, err
	}
	et.kind = idx.kindOf(et.id)
	et.link = idx.links[et.id]
	et.copies = idx.anyCopies[et.id]
	et.copiesArea = idx.templateArea(et.id)
	return &et, idx, nil
}

// copyGuardMessage is the 409 for any edit of a copy (of either template).
func copyGuardMessage(li teamLinkInfo) string {
	tmpl := li.templateName
	if tmpl == "" {
		tmpl = "la plantilla"
	}
	person := li.mentorName
	if person == "" {
		person = "un mentor"
		if li.area == areaSoporte {
			person = "una persona de soporte"
		}
	}
	return "Esta es la copia de «" + tmpl + "» para " + person + "; se edita en la plantilla."
}

const holderGuardMessage = "Este tipo guarda las preguntas retiradas de una plantilla y no se edita."

// TeamEventTypeGuard wraps an event-type endpoint of kind op with the team guards and, on
// success, the reconcile it triggers (see the file comment).
func (h *Handler) TeamEventTypeGuard(op TeamOp, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		et, _, err := h.teamEventTypeBySlug(r.Context(), r.PathValue("slug"))
		if err != nil {
			h.logger.ErrorContext(r.Context(), "team guard: load event type", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if et == nil {
			next(w, r) // the handler answers its own 404
			return
		}
		switch {
		case isTeamCopyKind(et.kind):
			h.writeError(w, http.StatusConflict, copyGuardMessage(et.link))
			return
		case et.kind == teamKindHolder:
			h.writeError(w, http.StatusConflict, holderGuardMessage)
			return
		case isTeamTemplateKind(et.kind):
			if msg, err := h.predefinedGuard(w, r, op, et); err != nil {
				h.writeError(w, http.StatusBadRequest, "invalid JSON")
				return
			} else if msg != "" {
				h.writeError(w, http.StatusConflict, msg)
				return
			}
		}
		if op == TeamOpDelete && et.copies > 0 {
			whose := "de los mentores"
			if et.copiesArea == areaSoporte {
				whose = "del personal de soporte"
			}
			h.writeError(w, http.StatusConflict,
				"«"+et.name+"» tiene copias "+whose+" y no se puede borrar; archívala si ya no la usas.")
			return
		}

		sw := &teamStatusWriter{ResponseWriter: w}
		next(sw, r)
		if !sw.ok() {
			return
		}
		// A template edit reaches every copy: a whole pass (the workspace is small).
		if isTeamTemplateKind(et.kind) && (op == TeamOpPatch || op == TeamOpQuestions) {
			h.reconcileTeamAfter(r.Context(), "")
		}
	}
}

// predefinedGuard is the T/S part of TeamEventTypeGuard (the same rules for both): the
// refusal message, or "".
func (h *Handler) predefinedGuard(w http.ResponseWriter, r *http.Request, op TeamOp, et *teamEventType) (string, error) {
	switch op {
	case TeamOpTransfer:
		return "Los tipos predefinidos no se transfieren.", nil
	case TeamOpHostsPut:
		return "Los tipos predefinidos los atiende el propietario; cada persona atiende su copia, que se asigna desde Miembros.", nil
	case TeamOpPatch:
		body, err := readBody(w, r, 32<<10)
		if err != nil {
			return "", err
		}
		var req struct {
			RoutingMode  *string `json:"routing_mode"`
			RRStrategy   *string `json:"rr_strategy"`
			LocationType *string `json:"location_type"`
		}
		if decodeBody(body, &req) != nil {
			return "", nil // the handler fails the same decode: it answers the malformed body
		}
		if (req.RoutingMode != nil && *req.RoutingMode != et.routingMode) ||
			(req.RRStrategy != nil && *req.RRStrategy != et.rrStrategy) {
			return "El reparto de los tipos predefinidos es fijo: cada persona atiende su propia copia.", nil
		}
		if req.LocationType != nil && *req.LocationType != et.locationType && *req.LocationType != "livekit" {
			return "Los tipos predefinidos usan la sala de video integrada.", nil
		}
	}
	return "", nil
}

// TeamTransferOwnershipGuard wraps POST /v1/users/{id}/transfer-ownership: refused while
// T or S is set (the templates and all their copies belong to the owner; the owner unsets
// them first). Non-owners fall through to the handler's 403.
func (h *Handler) TeamTransferOwnershipGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if actor, _ := userFromContext(r.Context()); actor.IsOwner {
			st, err := loadTeamSettings(r.Context(), h.db)
			if err != nil {
				h.logger.ErrorContext(r.Context(), "transfer ownership: team settings", "error", err)
				h.writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			if st.templateID != "" || st.soporteID != "" {
				h.writeError(w, http.StatusConflict, "Primero quita los tipos predefinidos en Miembros.")
				return
			}
		}
		next(w, r)
	}
}

// TeamReconcileAfter wraps a user mutation (role, archive, restore, delete - {id} is the
// user) or the invite claim (no {id}: a whole pass) with a reconcile after a 2xx.
func (h *Handler) TeamReconcileAfter(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sw := &teamStatusWriter{ResponseWriter: w}
		next(sw, r)
		if sw.ok() {
			h.reconcileTeamAfter(r.Context(), r.PathValue("id"))
		}
	}
}

// TeamReconcileAfterCaller wraps a mutation of the signed-in user's own data - POST, PATCH
// and DELETE /v1/availability-rules ({id} there is the rule, and a rule always belongs to
// its caller) - with a reconcile for the caller after a 2xx. Nothing the reconcile writes
// depends on hours any more (nobody rotates; has_availability is computed on read, in
// fork_team_hours.go), so this pass is a cheap idempotent no-op today; it stays so that
// hours-dependent team state, should any return, is refreshed on the change that causes it.
func (h *Handler) TeamReconcileAfterCaller(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sw := &teamStatusWriter{ResponseWriter: w}
		next(sw, r)
		if !sw.ok() {
			return
		}
		if user, ok := userFromContext(r.Context()); ok && user.ID != "" {
			h.reconcileTeamAfter(r.Context(), user.ID)
		}
	}
}

// TeamWebhookGuard wraps POST /v1/webhooks and PATCH /v1/webhooks/{id}, on CHANGE only
// (a PATCH re-sends what is stored):
//   - only the owner may select the whatsapp_message field: a member's webhook would send
//     the client a second copy of the owner's message (403);
//   - a filter may not newly list a copy of either template (the template already
//     includes every copy) or a holder (400).
func (h *Handler) TeamWebhookGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		body, err := readBody(w, r, 32<<10)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var req struct {
			Fields       *[]string `json:"fields"`
			EventTypeIDs *[]string `json:"event_type_ids"`
		}
		if decodeBody(body, &req) != nil {
			next(w, r) // the handler fails the same decode: its 400
			return
		}
		webhookID := r.PathValue("id") // "" on create
		var storedFields, storedIDs []string
		if webhookID != "" {
			var fieldsJSON sql.NullString
			err := h.db.QueryRowContext(r.Context(),
				`SELECT fields FROM webhooks WHERE id = ? AND user_id = ?`, webhookID, user.ID).Scan(&fieldsJSON)
			if errors.Is(err, sql.ErrNoRows) {
				next(w, r) // not theirs: the handler answers 404
				return
			}
			if err != nil {
				h.logger.ErrorContext(r.Context(), "webhook guard: load", "error", err)
				h.writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			if fieldsJSON.Valid {
				_ = json.Unmarshal([]byte(fieldsJSON.String), &storedFields)
			}
			rows, err := h.db.QueryContext(r.Context(),
				`SELECT event_type_id FROM webhook_event_type_filters WHERE webhook_id = ?`, webhookID)
			if err == nil {
				for rows.Next() {
					var id string
					if rows.Scan(&id) == nil {
						storedIDs = append(storedIDs, id)
					}
				}
				rows.Close() // #nosec G104 -- drained
			}
		}
		if !user.IsOwner && req.Fields != nil &&
			slices.Contains(*req.Fields, webhook.FieldWhatsAppMessage) && !slices.Contains(storedFields, webhook.FieldWhatsAppMessage) {
			h.writeError(w, http.StatusForbidden, "Los mensajes de WhatsApp los envía el propietario.")
			return
		}
		if req.EventTypeIDs != nil {
			var added []string
			for _, id := range *req.EventTypeIDs {
				if id != "" && !slices.Contains(storedIDs, id) {
					added = append(added, id)
				}
			}
			if len(added) > 0 {
				idx, err := h.loadTeamIndex(r.Context())
				if err != nil {
					h.logger.ErrorContext(r.Context(), "webhook guard: team index", "error", err)
					h.writeError(w, http.StatusInternalServerError, "internal error")
					return
				}
				for _, id := range added {
					li, ok := idx.links[id]
					if !ok {
						continue
					}
					var name string
					_ = h.db.QueryRowContext(r.Context(), `SELECT name FROM event_types WHERE id = ?`, id).Scan(&name)
					if li.kind == linkKindHolder {
						h.writeError(w, http.StatusBadRequest, "«"+name+"» guarda preguntas retiradas y no se puede elegir.")
						return
					}
					tmpl := li.templateName
					if tmpl == "" {
						tmpl = name
					}
					msg := "«" + name + "» es la copia de un mentor; elige la plantilla «" + tmpl + "» (incluye a todos los mentores)."
					if li.area == areaSoporte {
						msg = "«" + name + "» es la copia de una persona de soporte; elige la plantilla «" + tmpl +
							"» (incluye a todo el personal de soporte)."
					}
					h.writeError(w, http.StatusBadRequest, msg)
					return
				}
			}
		}
		next(w, r)
	}
}

// ---- The "team" object on GET /v1/event-types and GET /v1/event-types/{slug} ----------

// eventTypeTeamJSON says what a predefined event type is. Absent on ordinary types.
type eventTypeTeamJSON struct {
	// mentoria_template | mentoria_copy | soporte_template | soporte_copy
	Kind         string             `json:"kind"`
	TemplateSlug string             `json:"template_slug,omitempty"` // copy: its template
	TemplateName string             `json:"template_name,omitempty"` // copy: its template
	MentorName   string             `json:"mentor_name,omitempty"`   // copy: who attends it (mentor or support person)
	Copies       *int               `json:"copies,omitempty"`        // template: active copies
	CopyLinks    []teamCopyLinkJSON `json:"copy_links,omitempty"`    // template, owner only: every copy
}

// teamJSONFor builds et's team object for viewer (nil for an ordinary type).
func (h *Handler) teamJSONFor(idx *teamIndex, viewer AuthUser, etID string) *eventTypeTeamJSON {
	kind := idx.kindOf(etID)
	switch {
	case isTeamCopyKind(kind):
		li := idx.links[etID]
		return &eventTypeTeamJSON{Kind: kind, TemplateSlug: li.templateSlug, TemplateName: li.templateName, MentorName: li.mentorName}
	case isTeamTemplateKind(kind):
		n := idx.activeCopies[etID]
		t := &eventTypeTeamJSON{Kind: kind, Copies: &n}
		if viewer.IsOwner {
			t.CopyLinks = h.copyLinksJSON(idx.copiesOf(etID))
		}
		return t
	}
	return nil
}

// withTeamInfo is ListEventTypes' hook: holders are dropped for everyone, copies from
// their owner's list (the owner sees them through their template; the person who hosts
// one keeps it), and predefined types gain "team". Best effort: on error the list is
// returned as it is.
func (h *Handler) withTeamInfo(ctx context.Context, viewer AuthUser, items []eventTypeJSON) []eventTypeJSON {
	idx, err := h.loadTeamIndex(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "list event types: team info", "error", err)
		return items
	}
	out := items[:0]
	for _, et := range items {
		kind := idx.kindOf(et.ID)
		if kind == teamKindHolder || (isTeamCopyKind(kind) && et.Owned) {
			continue
		}
		et.Team = h.teamJSONFor(idx, viewer, et.ID)
		out = append(out, et)
	}
	return out
}

// decorateTeamEventType is GetEventType's hook: it sets et.Team and reports whether the
// type must be hidden (a holder answers 404, like any type the caller cannot see).
func (h *Handler) decorateTeamEventType(ctx context.Context, viewer AuthUser, et *eventTypeJSON) (hide bool) {
	idx, err := h.loadTeamIndex(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "get event type: team info", "error", err)
		return false
	}
	if idx.kindOf(et.ID) == teamKindHolder {
		return true
	}
	et.Team = h.teamJSONFor(idx, viewer, et.ID)
	return false
}

// ---- WhatsApp texts of copies, and the preview names of T and S ------------------------

// writeInheritedWhatsApp answers GET /v1/event-types/{slug}/whatsapp-messages for a copy
// (of either template), for its owner or the person hosting it: the TEMPLATE's texts
// (what the copy really sends), plus inherited_from {slug, name} and read_only: true.
// Returns false when slug is not a copy the viewer may see, so the normal handler runs.
func (h *Handler) writeInheritedWhatsApp(w http.ResponseWriter, r *http.Request, viewer AuthUser) bool {
	et, _, err := h.teamEventTypeBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || et == nil || !isTeamCopyKind(et.kind) || h.webhookSvc == nil {
		return false
	}
	if et.userID != viewer.ID {
		var hosts int
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM event_type_hosts WHERE event_type_id = ? AND user_id = ?`, et.id, viewer.ID).Scan(&hosts)
		if hosts == 0 {
			return false
		}
	}
	saved, err := h.webhookSvc.WhatsAppMessages(r.Context(), et.link.templateID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "whatsapp messages: inherited", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return true
	}
	out := make(map[string]any, len(saved)+3)
	defaults := make(map[string]string, len(webhook.WhatsAppMoments))
	for _, m := range webhook.WhatsAppMoments {
		out[m] = saved[m]
		defaults[m] = webhook.DefaultWhatsAppMessage(m)
	}
	out["defaults"] = defaults
	out["read_only"] = true
	out["inherited_from"] = map[string]string{"slug": et.link.templateSlug, "name": et.link.templateName}
	h.writeJSON(w, http.StatusOK, out)
	return true
}

// Preview names of {mentor} for the templates: the real deliveries name the person who
// attends each copy (a mentor on T's, a support person on S's), never the owner.
const (
	previewMentorTemplate = "Nombre del mentor"
	previewMentorSoporte  = "Nombre de quien atiende"
)

// teamPreviewMentor is the preview's {mentor} for etID, or "" to keep the signed-in name.
func (h *Handler) teamPreviewMentor(ctx context.Context, etID string) string {
	st, err := loadTeamSettings(ctx, h.db)
	if err != nil {
		return ""
	}
	switch etID {
	case st.templateID:
		return previewMentorTemplate
	case st.soporteID:
		return previewMentorSoporte
	}
	return ""
}
