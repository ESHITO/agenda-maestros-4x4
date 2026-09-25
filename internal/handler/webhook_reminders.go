package handler

// Fork (Agenda Maestros 4x4): scheduled reminder webhooks.
//
// The owner sends WhatsApp messages through FunnelChat, one FunnelChat flow per webhook.
// FunnelChat cannot branch on the envelope's "event", so each moment is its own event and
// its own webhook: booking.reminder_morning (the day of the meeting at REMINDER_MORNING_HOUR
// in the attendee's zone), booking.reminder_1h and booking.reminder_5m.
//
// Mechanics: one "webhook.reminder" row in the jobs table per moment, payload
// {"booking_id","kind","start_at"} - start_at is the start the reminder was planned for,
// which is what lets a stale job recognise itself after a reschedule. Rows are written
// wherever the e-mail reminders are (dispatchBookingConfirmation, rescheduleSideEffects),
// removed on cancel, and JobWebhookReminder re-checks everything when it runs, so a row
// that escaped cleanup is harmless. No schema change: jobs already has (type, payload)
// uniqueness for live rows, which makes scheduling idempotent (INSERT OR IGNORE).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/uid"
	"github.com/calnode/calnode/internal/webhook"
)

// webhookReminderJobType is jobs.type for a scheduled reminder webhook. Registered with
// the worker in server.go.
const webhookReminderJobType = "webhook.reminder"

// defaultReminderMorning mirrors config.DefaultReminderMorningHour (the handler does not
// import config); used when SetReminderMorningHour was never called, e.g. in tests.
const defaultReminderMorning = "08:00"

// Reminder kinds, as stored in the job payload.
const (
	reminderKindMorning = "morning"
	reminderKind1h      = "1h"
	reminderKind5m      = "5m"
)

// webhookReminderEvents maps a job's kind to the webhook event it fires.
var webhookReminderEvents = map[string]string{
	reminderKindMorning: webhook.EventReminderMorning,
	reminderKind1h:      webhook.EventReminder1h,
	reminderKind5m:      webhook.EventReminder5m,
}

// webhookReminderJob is the job payload. Field order is fixed by the struct, so the same
// reminder always marshals to the same bytes and the unique (type, payload) index can
// de-duplicate it.
type webhookReminderJob struct {
	BookingID string `json:"booking_id"`
	Kind      string `json:"kind"`
	StartAt   string `json:"start_at"` // RFC3339, UTC
}

// plannedWebhookReminder is one reminder due at RunAt.
type plannedWebhookReminder struct {
	Kind  string
	RunAt time.Time
}

// SetReminderMorningHour sets REMINDER_MORNING_HOUR ("HH:MM", 24 h). An empty or
// malformed value keeps the 08:00 default and reports false. config.Validate already
// refuses a malformed env var at boot; this is the second line.
func (h *Handler) SetReminderMorningHour(hhmm string) bool {
	if _, _, ok := parseClock(hhmm); !ok {
		h.reminderMorning = ""
		return false
	}
	h.reminderMorning = hhmm
	return true
}

// reminderMorningHour is the effective morning reminder time, "HH:MM".
func (h *Handler) reminderMorningHour() string {
	if h.reminderMorning == "" {
		return defaultReminderMorning
	}
	return h.reminderMorning
}

// parseClock parses a strict "HH:MM" 24-hour time.
func parseClock(s string) (hour, minute int, ok bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}

// morningReminderAt returns the morning reminder's moment for a meeting starting at start
// - morningHour:morningMinute on the attendee's LOCAL calendar day of the start, built
// with time.Date in that zone (so DST is the zone's business, not ours) - and whether the
// ordering rule allows one at all, whatever the time now.
//
// The rule: the morning reminder must come strictly BEFORE the 1 h one, so the attendee
// always hears morning → 1 h → 5 min, as the owner asked. With 08:00, a meeting at 08:30
// gets none (its 1 h reminder, at 07:30, would arrive first and the morning one would read
// as a correction), nor does one at 09:00 (two messages at the same minute); 09:05 does.
func morningReminderAt(start time.Time, zone *time.Location, morningHour, morningMinute int) (time.Time, bool) {
	if zone == nil {
		zone = time.UTC
	}
	local := start.In(zone)
	morning := time.Date(local.Year(), local.Month(), local.Day(), morningHour, morningMinute, 0, 0, zone)
	return morning, morning.Before(start.Add(-time.Hour))
}

// planWebhookReminders decides which reminders a booking starting at start gets, given
// the attendee's zone and "now". Pure, so the rules are testable without a clock:
//
//   - morning: see morningReminderAt (attendee's day, before the 1 h reminder), and only
//     when still in the future: a meeting at 07:30, or one booked at 10:00 for 15:00 the
//     same day, gets no morning reminder.
//   - 1h / 5m: start minus 60 / 5 minutes, only when still in the future.
func planWebhookReminders(start time.Time, zone *time.Location, morningHour, morningMinute int, now time.Time) []plannedWebhookReminder {
	var out []plannedWebhookReminder
	if morning, ok := morningReminderAt(start, zone, morningHour, morningMinute); ok && morning.After(now) {
		out = append(out, plannedWebhookReminder{Kind: reminderKindMorning, RunAt: morning.UTC()})
	}
	if t := start.Add(-time.Hour); t.After(now) {
		out = append(out, plannedWebhookReminder{Kind: reminderKind1h, RunAt: t.UTC()})
	}
	if t := start.Add(-5 * time.Minute); t.After(now) {
		out = append(out, plannedWebhookReminder{Kind: reminderKind5m, RunAt: t.UTC()})
	}
	return out
}

// webhookReminderZone is the attendee's zone for bookingID (organizer attendee, else
// host, else UTC - see webhook.AttendeeZone for why a stored "UTC" counts as missing).
func (h *Handler) webhookReminderZone(ctx context.Context, bookingID string) *time.Location {
	var attendeeTZ, hostTZ string
	_ = h.db.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT a.iana_timezone FROM booking_attendees a
		                 WHERE a.booking_id = b.id AND a.is_organizer = 1 LIMIT 1), ''),
		       COALESCE(u.iana_timezone, '')
		FROM bookings b LEFT JOIN users u ON u.id = b.host_id
		WHERE b.id = ?`, bookingID).Scan(&attendeeTZ, &hostTZ)
	return webhook.AttendeeZone(attendeeTZ, hostTZ)
}

// planForBooking reads what planWebhookReminders needs and returns the job rows to write.
// Runs its query BEFORE any transaction is opened: the pool is a single connection.
func (h *Handler) planForBooking(ctx context.Context, bookingID string, start time.Time) []plannedWebhookReminder {
	hour, minute, _ := parseClock(h.reminderMorningHour())
	return planWebhookReminders(start, h.webhookReminderZone(ctx, bookingID), hour, minute, time.Now())
}

// reminderJobPayload is the exact jobs.payload for one reminder (see webhookReminderJob).
func reminderJobPayload(bookingID, kind string, start time.Time) (string, error) {
	b, err := json.Marshal(webhookReminderJob{BookingID: bookingID, Kind: kind, StartAt: start.UTC().Format(time.RFC3339)})
	if err != nil {
		return "", fmt.Errorf("webhook reminder: marshal payload: %w", err)
	}
	return string(b), nil
}

// insertWebhookReminders writes one pending job per planned reminder through exec (the
// pool or an open transaction). INSERT OR IGNORE: the live (type, payload) unique index
// makes a repeat call a no-op. The NOT EXISTS also skips a reminder that already RAN for
// this same start (a 'done' row is outside that index): without it, moving
// REMINDER_MORNING_HOUR later on a morning whose reminder had fired would let the boot
// backfill plan it again, and the attendee would get it twice.
func insertWebhookReminders(ctx context.Context, exec func(ctx context.Context, query string, args ...any) error, bookingID string, start time.Time, plan []plannedWebhookReminder) error {
	for _, p := range plan {
		payload, err := reminderJobPayload(bookingID, p.Kind, start)
		if err != nil {
			return err
		}
		if err := exec(ctx, `
			INSERT OR IGNORE INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
			SELECT ?, ?, ?, ?, 'pending', 0, 3
			WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE type = ? AND payload = ?)`,
			uid.New(), webhookReminderJobType, payload, p.RunAt.UTC().Format(time.RFC3339),
			webhookReminderJobType, payload); err != nil {
			return fmt.Errorf("webhook reminder: insert %s: %w", p.Kind, err)
		}
	}
	return nil
}

// syncMorningReminder brings an already-pending morning job in line with the CURRENT
// REMINDER_MORNING_HOUR and attendee/host zone. INSERT OR IGNORE cannot: run_at is not
// part of the payload (it must not be - every change would then add a duplicate), so a
// job planned for 08:00 would otherwise survive a switch to 09:00 untouched. It moves the
// pending job when the new moment is still ahead, deletes it when the new rule gives this
// meeting no morning reminder at all, and leaves it alone when the new moment has already
// passed today (sending at the old time beats sending none). 1h/5m never depend on the
// setting or the zone, so they need no such pass.
func (h *Handler) syncMorningReminder(ctx context.Context, bookingID string, start time.Time) error {
	hour, minute, _ := parseClock(h.reminderMorningHour())
	morning, allowed := morningReminderAt(start, h.webhookReminderZone(ctx, bookingID), hour, minute)
	payload, err := reminderJobPayload(bookingID, reminderKindMorning, start)
	if err != nil {
		return err
	}
	switch {
	case !allowed:
		_, err = h.db.ExecContext(ctx, `
			DELETE FROM jobs WHERE type = ? AND payload = ? AND status = 'pending'`,
			webhookReminderJobType, payload)
	case morning.After(time.Now()):
		runAt := morning.UTC().Format(time.RFC3339)
		_, err = h.db.ExecContext(ctx, `
			UPDATE jobs SET run_at = ?
			WHERE type = ? AND payload = ? AND status = 'pending' AND run_at != ?`,
			runAt, webhookReminderJobType, payload, runAt)
	}
	if err != nil {
		return fmt.Errorf("webhook reminder: sync morning: %w", err)
	}
	return nil
}

// scheduleWebhookReminders plans and stores the reminder jobs for a newly confirmed
// booking. Called from dispatchBookingConfirmation, which every creation path shares
// (booking page, REST API, embed, MCP and the chat assistant via createBookingForSlug,
// Group bookings, and paid bookings once Stripe confirms payment).
func (h *Handler) scheduleWebhookReminders(ctx context.Context, bookingID string, start time.Time) error {
	plan := h.planForBooking(ctx, bookingID, start)
	return insertWebhookReminders(ctx, func(ctx context.Context, q string, args ...any) error {
		_, err := h.db.ExecContext(ctx, q, args...)
		return err
	}, bookingID, start, plan)
}

// BackfillWebhookReminders plans reminder jobs for every confirmed booking that has not
// started yet, and returns how many bookings it looked at. Called once at boot
// (server.go) so appointments booked BEFORE this feature shipped - or whose scheduling
// write failed - still get their reminders.
//
// Safe to run on every boot: planning never schedules a moment that has passed, and
// insertWebhookReminders skips a reminder already pending or already run for that start.
// It also re-applies REMINDER_MORNING_HOUR and the current zone to morning jobs that are
// still pending (syncMorningReminder), so changing the setting takes effect for bookings
// already on the calendar, not only for new ones.
// Unpaid Stripe holds (payment_status 'pending') are left out, as their confirmation
// side effects are.
func (h *Handler) BackfillWebhookReminders(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := h.db.QueryContext(ctx, `
		SELECT id, start_at FROM bookings
		WHERE status = 'confirmed' AND payment_status != 'pending' AND start_at >= ?`,
		now.Add(-24*time.Hour).Format(time.RFC3339)) // coarse string filter; exact check below
	if err != nil {
		return 0, fmt.Errorf("webhook reminder backfill: list: %w", err)
	}
	type pending struct {
		id    string
		start time.Time
	}
	var todo []pending
	for rows.Next() {
		var id, startStr string
		if err := rows.Scan(&id, &startStr); err != nil {
			continue
		}
		start, err := time.Parse(time.RFC3339Nano, startStr)
		if err != nil || !start.After(now) {
			continue
		}
		todo = append(todo, pending{id, start})
	}
	rows.Close() // #nosec G104 -- drained above; must be closed before the writes below (single-connection pool)
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("webhook reminder backfill: scan: %w", err)
	}
	for _, p := range todo {
		if err := h.scheduleWebhookReminders(ctx, p.id, p.start); err != nil {
			return 0, fmt.Errorf("webhook reminder backfill: %s: %w", p.id, err)
		}
		if err := h.syncMorningReminder(ctx, p.id, p.start); err != nil {
			return 0, fmt.Errorf("webhook reminder backfill: %s: %w", p.id, err)
		}
	}
	return len(todo), nil
}

// replaceWebhookReminders drops the booking's not-yet-running reminder jobs and plans
// fresh ones for newStart, atomically. Called from rescheduleSideEffects, which the
// admin panel, the /manage link and the MCP reschedule all share, and from
// ReassignBooking with the SAME start: the morning moment depends on the zone, and an
// attendee with no stored zone borrows the host's. A job already running when this lands
// finds start_at changed and skips itself (JobWebhookReminder).
func (h *Handler) replaceWebhookReminders(ctx context.Context, bookingID string, newStart time.Time) error {
	plan := h.planForBooking(ctx, bookingID, newStart) // query first: single-connection pool

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("webhook reminder: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM jobs
		WHERE type = ?
		  AND json_extract(payload, '$.booking_id') = ?
		  AND status != 'running'`, webhookReminderJobType, bookingID); err != nil {
		return fmt.Errorf("webhook reminder: delete old: %w", err)
	}
	if err := insertWebhookReminders(ctx, func(ctx context.Context, q string, args ...any) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}, bookingID, newStart, plan); err != nil {
		return err
	}
	return tx.Commit()
}

// deleteWebhookReminders removes a cancelled booking's not-yet-running reminder jobs.
// Housekeeping only: JobWebhookReminder skips cancelled bookings on its own.
func (h *Handler) deleteWebhookReminders(ctx context.Context, bookingID string) error {
	_, err := h.db.ExecContext(ctx, `
		DELETE FROM jobs
		WHERE type = ?
		  AND json_extract(payload, '$.booking_id') = ?
		  AND status != 'running'`, webhookReminderJobType, bookingID)
	return err
}

// webhookManageURL returns the attendee's reschedule/cancel link for a webhook payload.
// existing (a link already minted for this moment, e.g. the one in the confirmation or
// reschedule e-mail) is reused as is. Otherwise a NEW token is issued - additive
// IssueManageToken, never Rotate, so links already sent by e-mail or WhatsApp keep
// working - and only when some webhook receiving event selected manage_url, since each
// call writes a token row.
func (h *Handler) webhookManageURL(ctx context.Context, event, hostID, bookingID, existing string) string {
	if existing != "" {
		return existing
	}
	if h.webhookSvc == nil {
		return ""
	}
	want, err := h.webhookSvc.WantsField(ctx, event, hostID, webhook.FieldManageURL)
	if err != nil {
		h.logger.ErrorContext(ctx, "webhook manage url: check subscribers", "error", err, "booking_id", bookingID)
		return ""
	}
	if !want {
		return ""
	}
	tok, err := h.bookingSvc.IssueManageToken(ctx, bookingID)
	if err != nil {
		h.logger.ErrorContext(ctx, "webhook manage url: issue token", "error", err, "booking_id", bookingID)
		return ""
	}
	return h.publicURL() + "/manage/" + tok
}

// reminderSuperseded reports whether a reminder of kind, for a meeting starting at start,
// is too late to send at now: once the NEXT reminder's moment has arrived (the 1 h one for
// the morning reminder, the 5 min one for the 1 h reminder, the start itself for the 5 min
// one) its text is no longer true. Without this, a worker that comes back after hours down
// sends every overdue reminder in one burst - "your meeting is in 1 hour" three minutes
// before it starts, next to the morning and 5-minute ones.
func reminderSuperseded(kind string, start, now time.Time) bool {
	next := start
	switch kind {
	case reminderKindMorning:
		next = start.Add(-time.Hour)
	case reminderKind1h:
		next = start.Add(-5 * time.Minute)
	}
	return !now.Before(next)
}

// JobWebhookReminder processes one "webhook.reminder" job (registered in server.go).
//
// It fires only if the booking still exists, is confirmed, still starts at the payload's
// start_at - so a job orphaned by a reschedule or cancel that cleanup missed does nothing
// - and the reminder is not late enough to be false (reminderSuperseded). Bad or stale
// data returns nil (logged): retrying would not change the answer. Only a failing
// database read/write returns an error, which the worker retries up to the job's
// max_attempts (3).
func (h *Handler) JobWebhookReminder(ctx context.Context, payload string) error {
	var p webhookReminderJob
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		h.logger.WarnContext(ctx, "webhook reminder: bad payload", "error", err)
		return nil
	}
	event, ok := webhookReminderEvents[p.Kind]
	if !ok || p.BookingID == "" {
		h.logger.WarnContext(ctx, "webhook reminder: unknown kind or no booking", "kind", p.Kind, "booking_id", p.BookingID)
		return nil
	}
	planned, err := time.Parse(time.RFC3339, p.StartAt)
	if err != nil {
		h.logger.WarnContext(ctx, "webhook reminder: bad start_at", "start_at", p.StartAt, "booking_id", p.BookingID)
		return nil
	}
	if h.webhookSvc == nil {
		return nil
	}

	b, err := h.bookingSvc.Get(ctx, p.BookingID)
	if errors.Is(err, booking.ErrNotFound) {
		return nil // deleted
	}
	if err != nil {
		return fmt.Errorf("webhook reminder: load booking: %w", err)
	}
	switch {
	case b.Status != "confirmed":
		return nil // cancelled
	case b.PaymentStatus == "pending":
		return nil // unpaid checkout hold, not a real appointment yet
	case !b.StartAt.Truncate(time.Second).Equal(planned.Truncate(time.Second)):
		return nil // rescheduled since this job was planned; its replacement will fire
	case reminderSuperseded(p.Kind, b.StartAt, time.Now()):
		// The worker was down (restart, failed deploy, an idle instance put to sleep) and
		// this reminder is now late enough that its text is false.
		h.logger.WarnContext(ctx, "webhook reminder: skipped, too late (next reminder already due)", "kind", p.Kind, "booking_id", b.ID)
		return nil
	}

	wp := webhook.BookingPayload{
		ID:                 b.ID,
		EventTypeSlug:      h.slugForEventTypeID(ctx, b.EventTypeID),
		HostID:             b.HostID,
		StartAt:            b.StartAt.UTC().Format(time.RFC3339),
		EndAt:              b.EndAt.UTC().Format(time.RFC3339),
		Status:             b.Status,
		LocationValue:      b.LocationValue,
		CreatedAt:          b.CreatedAt.UTC().Format(time.RFC3339),
		PaymentStatus:      paymentStatusForWebhook(b.PaymentStatus),
		AmountPaidCents:    b.AmountPaidCents,
		AmountPaidCurrency: b.AmountPaidCurrency,
	}
	wp.ManageURL = h.webhookManageURL(ctx, event, b.HostID, b.ID, "")
	if err := h.webhookSvc.Enqueue(ctx, event, wp); err != nil {
		return fmt.Errorf("webhook reminder: enqueue %s: %w", event, err)
	}
	return nil
}

// GetWebhookSettings handles GET /v1/webhooks/settings (any signed-in user): read-only
// facts the webhooks page shows next to the fork's reminder events.
//   - reminder_morning_hour: REMINDER_MORNING_HOUR as applied ("08:00").
//   - team_scope: whether this user's webhooks also receive the whole team's bookings
//     (true for the workspace owner; see webhook.Service matchingWebhooks).
func (h *Handler) GetWebhookSettings(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	h.writeJSON(w, http.StatusOK, map[string]any{
		"reminder_morning_hour": h.reminderMorningHour(),
		"team_scope":            user.IsOwner,
	})
}
