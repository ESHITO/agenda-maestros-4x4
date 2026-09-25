package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/calnode/calnode/internal/netutil"
	"github.com/calnode/calnode/internal/webhook"
)

var validWebhookEvents = []string{
	"booking.created", "booking.cancelled", "booking.rescheduled",
	"recording.completed", "transcript.ready", "notes.ready",
	// Fork: scheduled reminders (webhook_reminders.go), one event per moment because
	// FunnelChat cannot branch on "event".
	webhook.EventReminderMorning, webhook.EventReminder1h, webhook.EventReminder5m,
}

func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Fields []string `json:"fields"` // optional payload field selection; nil = default set
		// Fork: limit to these event types (webhook_event_types.go); omitted/[] = every type.
		EventTypeIDs []string `json:"event_type_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.URL == "" {
		h.writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	u, err := url.ParseRequestURI(req.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		h.writeError(w, http.StatusBadRequest, "url must be a valid http or https URL")
		return
	}
	if err := validateWebhookURL(r.Context(), u); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Events) == 0 {
		h.writeError(w, http.StatusBadRequest, "events must not be empty")
		return
	}
	for _, e := range req.Events {
		if !slices.Contains(validWebhookEvents, e) {
			h.writeError(w, http.StatusBadRequest, "unknown event: "+e)
			return
		}
	}

	eventTypeIDs, msg, err := h.validateWebhookEventTypes(r.Context(), user, req.EventTypeIDs)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create webhook: check event types", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}

	// Fork: CreateWithEventTypes writes the filter in the webhook's own transaction.
	wh, secret, err := h.webhookSvc.CreateWithEventTypes(r.Context(), user.ID, req.URL, req.Events, eventTypeIDs)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create webhook", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Apply the field selection (if any) as a follow-up update so Create keeps its
	// stable signature; unknown keys are filtered out.
	if req.Fields != nil {
		if err := h.webhookSvc.Update(r.Context(), user.ID, wh.ID, nil, &req.Fields); err != nil {
			h.logger.ErrorContext(r.Context(), "create webhook: set fields", "error", err)
		} else {
			wh.Fields = webhook.ValidFields(req.Fields)
		}
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"id":         wh.ID,
		"url":        wh.URL,
		"events":     wh.Events,
		"fields":     wh.Fields,
		"secret":     secret,
		"is_active":  wh.IsActive,
		"created_at": wh.CreatedAt.UTC().Format(time.RFC3339),
		// Fork: [] = every event type.
		"event_type_ids": eventTypeIDsJSON(wh.EventTypeIDs),
	})
}

// PatchWebhook handles PATCH /v1/webhooks/{id} — update events and/or the payload
// field selection of an existing webhook. Fork: and/or its event-type filter
// ("event_type_ids": null/omitted = unchanged, [] = every type).
func (h *Handler) PatchWebhook(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		Events       *[]string `json:"events"`
		Fields       *[]string `json:"fields"`
		EventTypeIDs *[]string `json:"event_type_ids"` // fork
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Events != nil {
		if len(*req.Events) == 0 {
			h.writeError(w, http.StatusBadRequest, "events must not be empty")
			return
		}
		for _, e := range *req.Events {
			if !slices.Contains(validWebhookEvents, e) {
				h.writeError(w, http.StatusBadRequest, "unknown event: "+e)
				return
			}
		}
	}
	// Fork: validate the whole request before writing anything, then replace the filter
	// (one transaction of its own) before the upstream update.
	if req.EventTypeIDs != nil {
		ids, msg, err := h.validateWebhookEventTypes(r.Context(), user, *req.EventTypeIDs)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "update webhook: check event types", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if msg != "" {
			h.writeError(w, http.StatusBadRequest, msg)
			return
		}
		if err := h.webhookSvc.SetEventTypes(r.Context(), user.ID, id, ids); err != nil {
			if errors.Is(err, webhook.ErrNotFound) {
				h.writeError(w, http.StatusNotFound, "webhook not found")
				return
			}
			h.logger.ErrorContext(r.Context(), "update webhook: event types", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if err := h.webhookSvc.Update(r.Context(), user.ID, id, req.Events, req.Fields); err != nil {
		if errors.Is(err, webhook.ErrNotFound) {
			h.writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "update webhook", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	webhooks, err := h.webhookSvc.List(r.Context(), user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list webhooks", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	items := make([]map[string]any, len(webhooks))
	for i, wh := range webhooks {
		items[i] = map[string]any{
			"id":         wh.ID,
			"url":        wh.URL,
			"events":     wh.Events,
			"fields":     wh.Fields,
			"is_active":  wh.IsActive,
			"created_at": wh.CreatedAt.UTC().Format(time.RFC3339),
			// Fork: [] = every event type.
			"event_type_ids": eventTypeIDsJSON(wh.EventTypeIDs),
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")

	if err := h.webhookSvc.Delete(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, webhook.ErrNotFound) {
			h.writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "delete webhook", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	webhookID := r.PathValue("id")

	deliveries, err := h.webhookSvc.ListDeliveries(r.Context(), user.ID, webhookID)
	if err != nil {
		if errors.Is(err, webhook.ErrNotFound) {
			h.writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "list deliveries", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	items := make([]map[string]any, len(deliveries))
	for i, d := range deliveries {
		item := map[string]any{
			"id":            d.ID,
			"webhook_id":    d.WebhookID,
			"event":         d.Event,
			"status":        d.Status,
			"attempt_count": d.AttemptCount,
		}
		if d.BookingID != "" {
			item["booking_id"] = d.BookingID
		}
		if d.ResponseStatus != nil {
			item["response_status"] = *d.ResponseStatus
		}
		if d.LastAttemptedAt != nil {
			item["last_attempted_at"] = *d.LastAttemptedAt
		}
		items[i] = item
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// validateWebhookURL resolves the URL host and rejects any address in a
// loopback, link-local, or private range to prevent SSRF.
// validateWebhookURL rejects a webhook URL whose host resolves (now) to a private/
// loopback address — the same SSRF check the worker re-applies at actual delivery
// time (netutil.ResolveSafe), since DNS can change between saving a URL and
// delivering to it.
func validateWebhookURL(ctx context.Context, u *url.URL) error {
	if _, err := netutil.ResolveSafe(ctx, u.Hostname()); err != nil {
		return fmt.Errorf("webhook URL must not resolve to a private or loopback address: %w", err)
	}
	return nil
}
