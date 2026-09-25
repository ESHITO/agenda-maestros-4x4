package handler

// Fork (Agenda Maestros 4x4): settings the owner changes from the panel, kept in the fork's
// key/value table fork_settings (created in code by webhook.EnsureForkSchema, not by
// goose). A key with no row falls back to its env var, so an install that never saved
// anything behaves exactly as before.
//
// Today: the morning reminder (booking.reminder_morning, webhook_reminders.go).
//   - reminder_morning_hour     "HH:MM" (24 h)           env REMINDER_MORNING_HOUR (08:00)
//   - reminder_morning_timezone IANA name, "" = the     env REMINDER_MORNING_TIMEZONE ("")
//     attendee's own zone (the original behaviour)
//
// With a fixed zone the reminder goes out on the meeting's day AS SEEN IN THAT ZONE, at
// that hour in that zone - e.g. 07:00 America/Lima for every client, wherever they are.
// The ordering rules do not change (strictly before the 1 h reminder, still in the future,
// morning → 1 h → 5 min), so a client whose meeting is early in THEIR day may get no
// morning reminder at all: 07:00 Lima is 14:00 in Madrid in summer, after a 10:00 meeting.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// fork_settings keys.
const (
	forkKeyMorningHour = "reminder_morning_hour"
	forkKeyMorningTZ   = "reminder_morning_timezone"
)

// morningSetting is the effective morning-reminder rule: the saved setting, else the env.
type morningSetting struct {
	hhmm   string         // "08:00"
	hour   int            // 8
	minute int            // 0
	tzName string         // "America/Lima", or "" = each attendee's zone
	zone   *time.Location // tzName loaded; nil = each attendee's zone
}

// validFixedZone reports whether name is a loadable IANA zone the owner may pin the
// morning reminder to. "" is not a zone (it means "each attendee's"), and "Local" is the
// server's own clock, which is no answer to "what time is it in Lima".
func validFixedZone(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// SetReminderMorningTimezone sets REMINDER_MORNING_TIMEZONE, the default fixed zone of the
// morning reminder when none is saved from the panel. "" (each attendee's zone) is valid;
// an unknown zone clears it and reports false. config.Validate already refuses a bad value
// at boot; this is the second line.
func (h *Handler) SetReminderMorningTimezone(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		h.reminderMorningTZ = ""
		return true
	}
	if !validFixedZone(name) {
		h.reminderMorningTZ = ""
		return false
	}
	h.reminderMorningTZ = name
	return true
}

// forkSettings reads the given keys from fork_settings in one query. A missing table or a
// failed read yields an empty map: every caller then falls back to the env value.
func (h *Handler) forkSettings(ctx context.Context, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return out
	}
	keysJSON, _ := json.Marshal(keys)
	rows, err := h.db.QueryContext(ctx,
		`SELECT key, value FROM fork_settings WHERE key IN (SELECT value FROM json_each(?))`, string(keysJSON))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	return out
}

// setForkSettings upserts every key in one transaction.
func (h *Handler) setForkSettings(ctx context.Context, kv map[string]string) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fork_settings (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// loadMorningSetting returns the effective morning-reminder rule: each saved value when it
// is valid, else the env one. One small query; callers run it before opening a cursor or a
// transaction (single-connection pool). A saved value that stopped being valid (a zone
// renamed in a tzdata update) is ignored rather than trusted.
func (h *Handler) loadMorningSetting(ctx context.Context) morningSetting {
	hhmm, tzName := h.reminderMorningHour(), h.reminderMorningTZ
	saved := h.forkSettings(ctx, forkKeyMorningHour, forkKeyMorningTZ)
	if v, ok := saved[forkKeyMorningHour]; ok {
		if _, _, valid := parseClock(v); valid {
			hhmm = v
		}
	}
	if v, ok := saved[forkKeyMorningTZ]; ok && (v == "" || validFixedZone(v)) {
		tzName = v
	}
	ms := morningSetting{hhmm: hhmm, tzName: tzName}
	ms.hour, ms.minute, _ = parseClock(hhmm)
	if tzName != "" {
		if loc, err := time.LoadLocation(tzName); err == nil {
			ms.zone = loc
		} else {
			ms.tzName = ""
		}
	}
	return ms
}

// webhookSettingsJSON is the body of GET (and the PUT's answer) /v1/webhooks/settings.
func (h *Handler) webhookSettingsJSON(ctx context.Context, user AuthUser) map[string]any {
	ms := h.loadMorningSetting(ctx)
	return map[string]any{
		"reminder_morning_hour":     ms.hhmm,
		"reminder_morning_timezone": ms.tzName,
		"team_scope":                user.IsOwner,
		"can_edit":                  user.IsOwner,
	}
}

// PutWebhookSettings handles PUT /v1/webhooks/settings - the workspace OWNER only, since
// the morning reminder is one rule for the whole team (the owner's webhooks send every
// mentor's reminders). Body: {"reminder_morning_hour":"07:00",
// "reminder_morning_timezone":"America/Lima"}; an omitted key is left as it is, and a
// timezone of "" goes back to each client's own zone.
//
// Saving re-syncs the reminders already planned, right away and not at the next boot:
// BackfillWebhookReminders moves every pending morning job to the new moment, drops it
// when the new rule gives the meeting none, and plans it when the new rule newly allows
// one (never twice for a reminder that already ran). The answer is the GET body plus
// "resynced_bookings" (how many upcoming bookings were checked) and "resync_ok".
func (h *Handler) PutWebhookSettings(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if !user.IsOwner {
		h.writeError(w, http.StatusForbidden, "Solo el dueño del equipo puede cambiar el recordatorio de la mañana.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Hour     *string `json:"reminder_morning_hour"`
		Timezone *string `json:"reminder_morning_timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Hour == nil && req.Timezone == nil {
		h.writeError(w, http.StatusBadRequest, "Indica reminder_morning_hour y/o reminder_morning_timezone.")
		return
	}
	updates := map[string]string{}
	if req.Hour != nil {
		v := strings.TrimSpace(*req.Hour)
		if _, _, ok := parseClock(v); !ok {
			h.writeError(w, http.StatusBadRequest, "La hora debe tener el formato HH:MM de 24 horas, por ejemplo 07:00.")
			return
		}
		updates[forkKeyMorningHour] = v
	}
	if req.Timezone != nil {
		v := strings.TrimSpace(*req.Timezone)
		if v != "" && !validFixedZone(v) {
			h.writeError(w, http.StatusBadRequest, "Zona horaria desconocida: «"+v+"». Usa un nombre IANA, por ejemplo America/Lima, o déjala vacía para usar la hora de cada cliente.")
			return
		}
		updates[forkKeyMorningTZ] = v
	}
	if err := h.setForkSettings(r.Context(), updates); err != nil {
		h.logger.ErrorContext(r.Context(), "save webhook settings", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Detached from the request: a client that closes the tab must not leave half the
	// bookings re-planned. Bounded, like the boot backfill.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	n, err := h.BackfillWebhookReminders(ctx)
	resyncOK := true
	if err != nil {
		// The settings are saved; the next boot's backfill finishes the job.
		resyncOK = false
		h.logger.ErrorContext(r.Context(), "webhook settings: resync morning reminders", "error", err)
	}
	out := h.webhookSettingsJSON(r.Context(), user)
	out["resynced_bookings"] = n
	out["resync_ok"] = resyncOK
	h.writeJSON(w, http.StatusOK, out)
}
