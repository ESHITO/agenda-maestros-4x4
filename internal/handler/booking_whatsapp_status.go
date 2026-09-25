package handler

// Fork (Agenda Maestros 4x4): the bookings list (GET /v1/bookings) tells, for each booking
// on the page, what happened to its four WhatsApp notices - 1 Confirmación
// (booking.created), 2 Mañana (booking.reminder_morning), 3 1 hora, 4 5 min - and names
// the event type and who attends, so the panel can show them without another request.
//
// Visibility is the list's own: this only decorates the page parseBookingListFilter
// already allowed (the owner/admins with ?scope=all: everyone's; anyone else: the
// bookings they host). It adds no endpoint to authorise separately.
//
// Cost is fixed per PAGE, never per row: one query for the names, one aggregated query
// over webhook_deliveries + the reminder jobs of every booking on the page (UNION ALL),
// and one for the active webhooks and their event-type filters. Best effort: if any of it
// fails, the list is still served, just without "whatsapp" on its items.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/webhook"
)

// Notice kinds, in the order the panel numbers them (1-4). The reminder kinds are the job
// payload's "kind" (webhook_reminders.go).
const noticeKindCreated = "created"

var noticeKinds = []string{noticeKindCreated, reminderKindMorning, reminderKind1h, reminderKind5m}

// noticeEvents maps a notice kind to the webhook event that carries it.
var noticeEvents = map[string]string{
	noticeKindCreated:   "booking.created",
	reminderKindMorning: webhook.EventReminderMorning,
	reminderKind1h:      webhook.EventReminder1h,
	reminderKind5m:      webhook.EventReminder5m,
}

// Notice states.
const (
	// a delivery to a webhook succeeded
	noticeSent = "sent"
	// a delivery is in flight (retrying), or the job is running
	noticeSending = "sending"
	// planned: a reminder job waits for its moment ("at")
	noticePending = "pending"
	// every delivery ran out of attempts, or the job failed
	noticeFailed = "failed"
	// the booking was cancelled before it went out
	noticeCancelled = "cancelled"
	// the job ran too late and dropped it (reminderSuperseded): the worker was down or the
	// instance asleep at that moment
	noticeMissed = "missed"
	// no record left: the worker purges deliveries and jobs after noticeRecordRetention, so
	// an old notice can no longer be told apart from one never sent
	noticeUnknown = "unknown"
	// never planned: morning left out by the ordering rule, booked too late, a webhook-less
	// type, or the job ran and found nothing to send
	noticeNotApplicable = "not_applicable"
)

// noticeRecordRetention mirrors the worker's purge (worker.go: webhookDeliveryRetention for
// finished deliveries, and the same 30 days for finished jobs). Past it, "no record" no
// longer means "never sent", so the notice reads unknown instead of not_applicable.
const noticeRecordRetention = 30 * 24 * time.Hour

// reminderLeadMax bounds how long before the start a reminder can go out: the morning one
// is on the meeting's own day (in the attendee's or the fixed zone), so within 24 h of it.
const reminderLeadMax = 24 * time.Hour

// whatsAppNoticeJSON is one of the four notices of a booking.
type whatsAppNoticeJSON struct {
	Kind   string `json:"kind"`         // created | morning | 1h | 5m
	Status string `json:"status"`       // see the notice states above
	At     string `json:"at,omitempty"` // RFC3339 UTC: last attempt (sent/failed), planned moment (pending), or when the late job dropped it (missed)
}

// bookingListItem is one GET /v1/bookings item: the upstream booking JSON plus the fork's
// fields. Embedded, so the upstream keys stay exactly where they were.
type bookingListItem struct {
	bookingJSON
	EventTypeName string               `json:"event_type_name,omitempty"`
	WhatsApp      []whatsAppNoticeJSON `json:"whatsapp,omitempty"`
	// Area is the team área of the booking's type: "mentoria" | "soporte" | "" (fork_team_bookings.go).
	Area string `json:"area"`
	// Attendance is what the video room saw (fork_attendance.go); absent if it could not be read.
	Attendance *attendanceJSON `json:"attendance,omitempty"`
}

// noticeRow is one row of the aggregated state query.
type noticeRow struct {
	bookingID, src, what, status, at, startAt string
	finishedAt                                string // jobs only: when it reached done/failed
}

// scopedWebhook is an active webhook, as the delivery scope sees it (matchingWebhooks).
type scopedWebhook struct {
	userID       string
	ownerHook    bool // its user is a workspace owner, not archived: it gets every booking
	events       map[string]bool
	eventTypeIDs map[string]bool // empty = every event type
}

// receives reports whether wh would get event for booking b - the same three tests as
// webhook.Service.matchingWebhooks: subscribed, in scope (the host's own or an owner's),
// and the event-type filter, if any, holds the booking's type. A filter listing a team
// template also holds that template's copies: scopedWebhooks adds their ids to the set,
// mirroring the template-aware clause (internal/webhook/fork_team.go).
func (wh scopedWebhook) receives(event string, b booking.Booking) bool {
	if !wh.events[event] || (wh.userID != b.HostID && !wh.ownerHook) {
		return false
	}
	return len(wh.eventTypeIDs) == 0 || wh.eventTypeIDs[b.EventTypeID]
}

// withWhatsAppNotices turns one page of list items into bookingListItems carrying the
// event type's name, the primary host's name (in every view, not only the workspace one:
// each row says who attends) and the four notices.
func (h *Handler) withWhatsAppNotices(ctx context.Context, bookings []booking.Booking, items []bookingJSON) []bookingListItem {
	out := make([]bookingListItem, len(items))
	for i := range items {
		out[i] = bookingListItem{bookingJSON: items[i]}
	}
	if len(items) == 0 || len(items) != len(bookings) {
		return out
	}
	ids := make([]string, len(bookings))
	idx := make(map[string]int, len(bookings))
	for i, b := range bookings {
		ids[i] = b.ID
		idx[b.ID] = i
	}
	idsJSON, _ := json.Marshal(ids)

	if err := h.fillBookingNames(ctx, string(idsJSON), idx, out); err != nil {
		h.logger.ErrorContext(ctx, "list bookings: names", "error", err)
	}
	if err := h.fillBookingAreas(ctx, string(idsJSON), idx, out); err != nil {
		h.logger.ErrorContext(ctx, "list bookings: areas", "error", err)
	}
	if err := h.fillBookingAttendance(ctx, bookings, string(idsJSON), idx, out, time.Now().UTC()); err != nil {
		h.logger.ErrorContext(ctx, "list bookings: attendance", "error", err)
	}
	rows, err := h.noticeRows(ctx, string(idsJSON))
	if err != nil {
		h.logger.ErrorContext(ctx, "list bookings: whatsapp notices", "error", err)
		return out
	}
	hooks, err := h.scopedWebhooks(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "list bookings: webhooks in scope", "error", err)
		return out
	}
	byBooking := make(map[string][]noticeRow, len(bookings))
	for _, r := range rows {
		byBooking[r.bookingID] = append(byBooking[r.bookingID], r)
	}
	now := time.Now().UTC()
	for i, b := range bookings {
		out[i].WhatsApp = bookingNotices(b, byBooking[b.ID], hooks, now)
	}
	return out
}

// fillBookingNames sets the event type's name and, where the list left it empty, the
// primary host's name. One query for the page.
func (h *Handler) fillBookingNames(ctx context.Context, idsJSON string, idx map[string]int, out []bookingListItem) error {
	rows, err := h.db.QueryContext(ctx, `
		SELECT b.id, COALESCE(et.name, ''), COALESCE(u.name, '')
		FROM bookings b
		LEFT JOIN event_types et ON et.id = b.event_type_id
		LEFT JOIN users u ON u.id = b.host_id
		WHERE b.id IN (SELECT value FROM json_each(?))`, idsJSON)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, etName, hostName string
		if err := rows.Scan(&id, &etName, &hostName); err != nil {
			return err
		}
		if i, ok := idx[id]; ok {
			out[i].EventTypeName = etName
			if out[i].HostName == "" {
				out[i].HostName = hostName
			}
		}
	}
	return rows.Err()
}

// noticeRows is the aggregated state of every notice of the page's bookings, in ONE query:
//   - deliveries of the four events, only each webhook's LATEST one per booking and event
//     (a retry or a re-send does not count twice; idx_fork_webhook_deliveries_booking);
//   - the booking's webhook.reminder jobs, with the start they were planned for.
func (h *Handler) noticeRows(ctx context.Context, idsJSON string) ([]noticeRow, error) {
	events := make([]string, 0, len(noticeKinds))
	for _, k := range noticeKinds {
		events = append(events, noticeEvents[k])
	}
	eventsJSON, _ := json.Marshal(events)
	rows, err := h.db.QueryContext(ctx, `
		SELECT d.booking_id, 'delivery', d.event, d.status, COALESCE(d.last_attempted_at, ''), '', ''
		FROM webhook_deliveries d
		WHERE d.booking_id IN (SELECT value FROM json_each(?))
		  AND d.event IN (SELECT value FROM json_each(?))
		  AND d.rowid = (SELECT MAX(d2.rowid) FROM webhook_deliveries d2
		                 WHERE d2.booking_id = d.booking_id AND d2.event = d.event
		                   AND d2.webhook_id = d.webhook_id)
		UNION ALL
		SELECT json_extract(j.payload, '$.booking_id'), 'job', COALESCE(json_extract(j.payload, '$.kind'), ''),
		       j.status, COALESCE(j.run_at, ''), COALESCE(json_extract(j.payload, '$.start_at'), ''),
		       COALESCE(j.finished_at, '')
		FROM jobs j
		WHERE j.type = ? AND json_extract(j.payload, '$.booking_id') IN (SELECT value FROM json_each(?))`,
		idsJSON, string(eventsJSON), webhookReminderJobType, idsJSON)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []noticeRow
	for rows.Next() {
		var r noticeRow
		if err := rows.Scan(&r.bookingID, &r.src, &r.what, &r.status, &r.at, &r.startAt, &r.finishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scopedWebhooks loads every active webhook with its event-type filter, in one query. The
// table is small (a handful per workspace), so matching is done in Go per booking.
func (h *Handler) scopedWebhooks(ctx context.Context) ([]scopedWebhook, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT w.user_id, w.events,
		       COALESCE(u.is_owner = 1 AND u.archived_at IS NULL, 0),
		       (SELECT json_group_array(f.event_type_id) FROM webhook_event_type_filters f WHERE f.webhook_id = w.id),
		       (SELECT json_group_array(l.copy_id) FROM webhook_event_type_filters f
		          JOIN fork_event_type_links l ON l.template_id = f.event_type_id AND l.kind = 'copy'
		         WHERE f.webhook_id = w.id)
		FROM webhooks w
		LEFT JOIN users u ON u.id = w.user_id
		WHERE w.is_active = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scopedWebhook
	for rows.Next() {
		var userID, eventsJSON, filterJSON, copiesJSON string
		var owner bool
		if err := rows.Scan(&userID, &eventsJSON, &owner, &filterJSON, &copiesJSON); err != nil {
			return nil, err
		}
		wh := scopedWebhook{userID: userID, ownerHook: owner, events: map[string]bool{}, eventTypeIDs: map[string]bool{}}
		var events, etIDs []string
		_ = json.Unmarshal([]byte(eventsJSON), &events)
		_ = json.Unmarshal([]byte(filterJSON), &etIDs)
		if len(etIDs) > 0 { // copies widen only a filtered webhook; empty = every type already
			var copyIDs []string
			_ = json.Unmarshal([]byte(copiesJSON), &copyIDs)
			etIDs = append(etIDs, copyIDs...)
		}
		for _, e := range events {
			wh.events[e] = true
		}
		for _, id := range etIDs {
			wh.eventTypeIDs[id] = true
		}
		out = append(out, wh)
	}
	return out, rows.Err()
}

// bookingNotices decides the four notices of one booking from its state rows. Pure.
//
// Confirmation: its deliveries - any success = sent, else any still retrying = sending,
// else failed; no delivery at all = not_applicable (no webhook received it), or unknown
// once the booking is older than noticeRecordRetention (its delivery may have been purged).
//
// A reminder, first match wins:
//  1. a job planned for the booking's CURRENT start is pending → pending at its run_at, or
//     not_applicable when no active webhook would receive it (the job would fire into
//     nothing); running → sending. Jobs planned for an earlier start are ignored, and so are
//     a cancelled booking's (JobWebhookReminder drops them; cancelSideEffects deletes them
//     right after CancelBooking answers, so the panel's reload can still see them).
//  2. its deliveries, as for the confirmation;
//  3. its job failed → failed;
//  4. the booking is cancelled → cancelled (it will not go out now);
//  5. its job finished only once the reminder was superseded, with no delivery and a webhook
//     that would receive it → missed (dropped as too late: the worker was down or asleep);
//  6. no record may be left (the start is older than the retention plus the longest lead)
//     → unknown;
//  7. otherwise not_applicable: never planned (morning left out by the ordering rule, a
//     booking made too late), or the job ran and found nothing to send.
func bookingNotices(b booking.Booking, rows []noticeRow, hooks []scopedWebhook, now time.Time) []whatsAppNoticeJSON {
	start := b.StartAt.UTC().Format(time.RFC3339)
	cancelled := b.Status == "cancelled"
	purgeCutoff := now.Add(-noticeRecordRetention)
	out := make([]whatsAppNoticeJSON, 0, len(noticeKinds))
	for _, kind := range noticeKinds {
		event := noticeEvents[kind]
		var sentAt, failedAt, pendingAt, missedAt string
		var sending, jobRunning, jobFailed bool
		for _, r := range rows {
			switch {
			case r.src == "delivery" && r.what == event:
				switch r.status {
				case "success":
					sentAt = maxString(sentAt, r.at)
				case "pending":
					sending = true
				case "failed":
					failedAt = maxString(failedAt, r.at)
				}
			case r.src == "job" && r.what == kind && r.startAt == start:
				switch r.status {
				case "pending":
					if !cancelled && (pendingAt == "" || r.at < pendingAt) {
						pendingAt = r.at
					}
				case "running":
					jobRunning = jobRunning || !cancelled
				case "failed":
					jobFailed = true
				case "done":
					if fin, err := time.Parse(time.RFC3339Nano, r.finishedAt); err == nil && reminderSuperseded(kind, b.StartAt, fin) {
						missedAt = maxString(missedAt, fin.UTC().Format(time.RFC3339))
					}
				}
			}
		}
		recordsMayBeGone := !b.CreatedAt.IsZero() && b.CreatedAt.Before(purgeCutoff)
		if kind != noticeKindCreated {
			recordsMayBeGone = b.StartAt.Add(-reminderLeadMax).Before(purgeCutoff)
		}
		n := whatsAppNoticeJSON{Kind: kind}
		switch {
		case pendingAt != "":
			n.Status, n.At = noticePending, pendingAt
			if !anyReceives(hooks, event, b) {
				n.Status, n.At = noticeNotApplicable, ""
			}
		case jobRunning:
			n.Status = noticeSending
		case sentAt != "":
			n.Status, n.At = noticeSent, sentAt
		case sending:
			n.Status = noticeSending
		case failedAt != "" || jobFailed:
			n.Status, n.At = noticeFailed, failedAt
		case kind != noticeKindCreated && cancelled:
			n.Status = noticeCancelled
		case missedAt != "" && anyReceives(hooks, event, b):
			n.Status, n.At = noticeMissed, missedAt
		case recordsMayBeGone:
			n.Status = noticeUnknown
		default:
			n.Status = noticeNotApplicable
		}
		out = append(out, n)
	}
	return out
}

func anyReceives(hooks []scopedWebhook, event string, b booking.Booking) bool {
	for _, wh := range hooks {
		if wh.receives(event, b) {
			return true
		}
	}
	return false
}

// maxString keeps the later of two RFC3339 UTC timestamps (they sort as strings).
func maxString(a, b string) string {
	if b > a {
		return b
	}
	return a
}
