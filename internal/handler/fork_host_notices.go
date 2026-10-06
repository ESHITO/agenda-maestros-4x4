package handler

// Fork (Agenda Maestros 4x4): the handler's side of the WhatsApp notices to the HOST
// (internal/webhook/fork_host.go explains them).
//
//   - enqueueHostNotice: booking.host_created, called on one line from
//     dispatchBookingConfirmation (every creation path) and from ReassignBooking (the new
//     host); booking.host_cancelled, called on one line from cancelSideEffects (every cancel:
//     panel, /manage and its /c short link, MCP - the unpaid-hold releases of
//     stripe_booking.go and worker.Poll never go through it). booking.host_reminder_5m is
//     the "webhook.reminder" job of kind host_5m (webhook_reminders.go).
//   - The host's room link: GET /h/{code} (fork_short_links.go) and, when no code can be
//     made, the long link the webhook package asks hostRoomLink for. Both mint a UNIQUE
//     host room token for the booking's CURRENT end and record its hash, with the host it
//     was minted for, in fork_livekit_host_tokens; teamHostLinkCurrent
//     (fork_team_supervision.go) accepts such a token only while that person is still the
//     booking's host. fork_livekit_host_links - the one hash of the e-mailed host link - is
//     never touched, so that link keeps working.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/webhook"
)

// cancelSideEffects' two budgets: cancelSideEffectsTimeout for the calendar, Zoom, refund
// and e-mails (upstream's 30 s), and a fresh cancelNoticesTimeout for the host notice and
// booking.cancelled after them, so slow SMTP cannot spend the WhatsApp notices' context
// (the same split as dispatchBookingConfirmation). Variables only so a test can shorten
// the first (SetCancelSideEffectsTimeoutForTest).
var (
	cancelSideEffectsTimeout = 30 * time.Second
	cancelNoticesTimeout     = 15 * time.Second
)

// enqueueHostNotice queues event (a host notice) for b's CURRENT host - for a cancellation,
// whoever hosted it when it was cancelled - with its cancellation reason ({motivo} of
// host_cancelled; "" otherwise). No manage link, ever. Best effort: a failure is logged,
// the booking is already committed.
//
// Never for an unpaid Stripe hold (payment_status 'pending'): it is not an appointment yet -
// if the checkout expires, releaseUnpaidHold frees the slot, and the host would have been
// handed the client's data for a session that never happened. Once Stripe confirms,
// confirmPaidBooking runs dispatchBookingConfirmation, which sends the one notice to whoever
// hosts the session then ("Pasar a otra persona" on a hold included). The host_5m job keeps
// the same rule (JobWebhookReminder), and so does host_cancelled: a hold cancelled before it
// was paid never had a host_created, so its host is not told it went away.
func (h *Handler) enqueueHostNotice(ctx context.Context, event string, b *booking.Booking) {
	if h.webhookSvc == nil || b == nil || b.PaymentStatus == "pending" {
		return
	}
	if err := h.webhookSvc.Enqueue(ctx, event, webhook.BookingPayload{
		ID:                 b.ID,
		EventTypeSlug:      h.slugForEventTypeID(ctx, b.EventTypeID),
		HostID:             b.HostID,
		StartAt:            b.StartAt.UTC().Format(time.RFC3339),
		EndAt:              b.EndAt.UTC().Format(time.RFC3339),
		Status:             b.Status,
		CancellationReason: b.CancellationReason,
		LocationValue:      b.LocationValue,
		CreatedAt:          b.CreatedAt.UTC().Format(time.RFC3339),
		PaymentStatus:      paymentStatusForWebhook(b.PaymentStatus),
		AmountPaidCents:    b.AmountPaidCents,
		AmountPaidCurrency: b.AmountPaidCurrency,
	}); err != nil {
		h.logger.ErrorContext(ctx, "enqueue host notice", "error", err, "event", event, "booking_id", b.ID)
	}
}

// errNoHostRoom: the booking has no built-in video room to enter (none made, another
// location, LiveKit switched off).
var errNoHostRoom = errors.New("host link: no video room for this booking")

// mintHostRoomLink mints a unique host room link for room, valid until end +
// liveKitJoinGrace, and records its hash for (bookingID, userID). The caller has checked
// that userID hosts the booking now.
func (h *Handler) mintHostRoomLink(ctx context.Context, bookingID, userID, room string, end time.Time) (string, error) {
	lk := h.getLiveKit()
	if lk == nil || room == "" {
		return "", errNoHostRoom
	}
	link := lk.BookingJoinURLUnique(h.baseURL, room, "host", end.Add(liveKitJoinGrace))
	u, err := url.Parse(link)
	if err != nil {
		return "", err
	}
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO fork_livekit_host_tokens (token_hash, booking_id, user_id, created_at) VALUES (?, ?, ?, ?)`,
		teamRoomTokenHash(u.Query().Get("t")), bookingID, userID, teamNow()); err != nil {
		return "", fmt.Errorf("host link: record token: %w", err)
	}
	return link, nil
}

// hostRoomLink is the webhook service's fallback for {enlace_mentor} (SetHostRoomLinker):
// the long host link of bookingID for hostID, when hostID is its current host and it has a
// room; "" otherwise or on failure (logged, never the link).
func (h *Handler) hostRoomLink(ctx context.Context, bookingID, hostID string) string {
	var host, room, locType, end string
	err := h.db.QueryRowContext(ctx, `
		SELECT host_id, COALESCE(livekit_room, ''), COALESCE(location_type, ''), end_at
		FROM bookings WHERE id = ?`, bookingID).Scan(&host, &room, &locType, &end)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (host != hostID || (locType != "livekit" && locType != ""))) {
		return ""
	}
	endAt, perr := time.Parse(time.RFC3339Nano, end)
	if err == nil && perr != nil {
		err = perr
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "host link: load booking", "error", err, "booking_id", bookingID)
		return ""
	}
	link, err := h.mintHostRoomLink(ctx, bookingID, hostID, room, endAt)
	if err != nil {
		if !errors.Is(err, errNoHostRoom) {
			h.logger.ErrorContext(ctx, "host link: mint", "error", err, "booking_id", bookingID)
		}
		return ""
	}
	return link
}
