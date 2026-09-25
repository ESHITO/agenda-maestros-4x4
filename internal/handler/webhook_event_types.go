package handler

// Fork (Agenda Maestros 4x4): the event-type filter of a webhook ("¿Para qué tipos de
// cita?"). Storage and delivery live in internal/webhook/fork_event_types.go; this file
// holds who may pick which event type, and the list the panel draws its checkboxes from.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// maxWebhookEventTypes bounds event_type_ids; a workspace has a handful of types, so a
// longer list is a client bug, not a configuration.
const maxWebhookEventTypes = 100

// webhookEventTypeScope is the visibility rule shared by validation and the panel list,
// and it follows the DELIVERY scope (webhook.matchingWebhooks): the owner may limit a
// webhook to any event type of the workspace, because the owner's webhooks receive every
// member's bookings; anyone else - admins included, whose webhooks get only the bookings
// they host - to the types they own or host, the same set GET /v1/event-types shows them.
// Offering an admin another member's type would save a filter that can never fire.
// Args: all (1/0), user, user.
const webhookEventTypeScope = `(? = 1
	OR et.user_id = ?
	OR et.id IN (SELECT event_type_id FROM event_type_hosts WHERE user_id = ?))`

func webhookEventTypeScopeArgs(user AuthUser) []any {
	all := 0
	if user.IsOwner { // not IsAdmin: see webhookEventTypeScope
		all = 1
	}
	return []any{all, user.ID, user.ID}
}

// validateWebhookEventTypes checks a requested event_type_ids list: every id must exist and
// be in the caller's scope (webhookEventTypeScope). It returns the ids without blanks or
// repeats, or a message for a 400. An empty list is valid: every event type.
func (h *Handler) validateWebhookEventTypes(ctx context.Context, user AuthUser, ids []string) ([]string, string, error) {
	clean := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, "event_type_ids: hay un tipo de atención vacío", nil
		}
		if !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	if len(clean) > maxWebhookEventTypes {
		return nil, fmt.Sprintf("event_type_ids: como máximo %d tipos de atención", maxWebhookEventTypes), nil
	}
	if len(clean) == 0 {
		return clean, "", nil
	}

	// One query for the whole list: the ids travel as a JSON array (json_each), so the SQL
	// text never depends on the input.
	idsJSON, _ := json.Marshal(clean)
	args := append([]any{string(idsJSON)}, webhookEventTypeScopeArgs(user)...)
	rows, err := h.db.QueryContext(ctx, `
		SELECT et.id FROM event_types et
		WHERE et.id IN (SELECT value FROM json_each(?))
		  AND `+webhookEventTypeScope, args...) // #nosec G202 -- webhookEventTypeScope is a constant; every value is bound
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	found := make(map[string]bool, len(clean))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, "", err
		}
		found[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	for _, id := range clean {
		if found[id] {
			continue
		}
		if user.IsOwner {
			return nil, "event_type_ids: el tipo de atención " + id + " no existe", nil
		}
		return nil, "event_type_ids: el tipo de atención " + id + " no existe o no es tuyo (solo puedes elegir tus tipos de atención o los que atiendes)", nil
	}
	return clean, "", nil
}

// webhookEventTypeJSON is one entry of GET /v1/webhooks/event-types.
type webhookEventTypeJSON struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	IsActive  bool   `json:"is_active"`
	Archived  bool   `json:"archived"`
	Owned     bool   `json:"owned"`
	OwnerName string `json:"owner_name"`
}

// ListWebhookEventTypes handles GET /v1/webhooks/event-types (fork): the event types this
// user may limit a webhook to (webhookEventTypeScope), active and inactive, so the panel
// can both offer checkboxes (active ones) and name every id an existing filter holds. For
// the owner that is more than GET /v1/event-types, which lists only what they own or host.
func (h *Handler) ListWebhookEventTypes(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	args := append([]any{user.ID}, webhookEventTypeScopeArgs(user)...)
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT et.id, et.slug, et.name, et.is_active, (et.archived_at IS NOT NULL),
		       (et.user_id = ?), COALESCE(u.name, '')
		FROM event_types et
		LEFT JOIN users u ON u.id = et.user_id
		WHERE `+webhookEventTypeScope+`
		ORDER BY et.name COLLATE NOCASE, et.slug`, args...) // #nosec G202 -- webhookEventTypeScope is a constant; every value is bound
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list webhook event types", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	items := make([]webhookEventTypeJSON, 0)
	for rows.Next() {
		var it webhookEventTypeJSON
		if err := rows.Scan(&it.ID, &it.Slug, &it.Name, &it.IsActive, &it.Archived, &it.Owned, &it.OwnerName); err != nil {
			h.logger.ErrorContext(r.Context(), "scan webhook event type", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list webhook event types rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// eventTypeIDsJSON renders a filter for a response: never null, [] = every event type.
func eventTypeIDsJSON(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
