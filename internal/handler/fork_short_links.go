package handler

// Fork (Agenda Maestros 4x4): the WhatsApp short links, resolved.
//
//	GET /e/{code}   302 to the attendee's join link, built at this moment
//	GET /c/{code}   302 to /manage/{a NEW manage token}
//
// The codes are made by the webhook package while it renders whatsapp_message
// (internal/webhook/fork_short_links.go, which explains the format, why the database
// holds only a keyed hash, and the guessing budget). Both routes are public and behind the
// per-IP RateLimit (server.go); the request log redacts their paths (redactTokenPaths).
//
// A code resolves while its booking is still relevant, judged on the booking AS IT IS NOW
// (webhook.ShortLinkValidUntil): a room code until the meeting's end + 12 h (a LiveKit one
// only until end + liveKitJoinGrace, when the room token it would sign is dead), a manage
// code until its start + 12 h. A reschedule moves a room code's window with no write, and
// REVOKES the manage codes (rescheduleSideEffects, like RotateManageToken for the e-mail's
// link): the rescheduled message brings new ones. A cancelled booking closes the room code,
// while its manage code still opens /manage, which shows the cancellation and offers a new
// date. Anything else - unknown, revoked, past its time, past the 60-day hard cap, a room
// code on /c - gets the same friendly page, status 404.
//
// The manage token a /c mints expires with the code's window (IssueManageTokenUntil), not
// after the usual 60 days.

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/webhook"
)

// liveKitJoinGrace is how long after a LiveKit meeting's end its join link still works
// (late joins, overruns). One constant for the link minted at booking time
// (mintMeetingLink) and the one a /e code builds, so both are the same link.
const liveKitJoinGrace = 2 * time.Hour

// wireWebhookSvc hands the webhook service what its short links need from the handler:
// the booker-facing base URL (read at render time, so PUBLIC_BASE_URL/BASE_URL set later
// still apply) and the logger. Called from New and SetWebhookSvc.
func (h *Handler) wireWebhookSvc(svc *webhook.Service) {
	if svc == nil {
		return
	}
	svc.SetShortLinkBaseURL(h.publicURL)
	svc.SetLogger(h.logger)
}

// ShortRoomLink handles GET /e/{code}: the "enter the session" link of a WhatsApp text.
func (h *Handler) ShortRoomLink(w http.ResponseWriter, r *http.Request) {
	h.serveShortLink(w, r, webhook.ShortLinkRoom)
}

// ShortManageLink handles GET /c/{code}: the "cancel or change the date" link.
func (h *Handler) ShortManageLink(w http.ResponseWriter, r *http.Request) {
	h.serveShortLink(w, r, webhook.ShortLinkManage)
}

// shortLinkBooking is what a code's resolution reads from its booking, in one query.
type shortLinkBooking struct {
	status             string
	start, end         time.Time
	locationType       string
	locationValue      string
	liveKitRoom        string
	parsedTimesInvalid bool
}

func (h *Handler) serveShortLink(w http.ResponseWriter, r *http.Request, kind string) {
	// Every answer, the redirect included: the Location carries a live credential.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	// Codes are lower case; a hand-typed upper-case one is the same code. A malformed one
	// is answered without touching the database (not even for the branded page).
	code := strings.ToLower(r.PathValue("code"))
	if !webhook.ValidShortCode(code) {
		http.Error(w, "Este enlace no es válido.", http.StatusNotFound)
		return
	}
	ctx := r.Context()
	if h.webhookSvc == nil {
		h.logger.WarnContext(ctx, "short link: webhook service unavailable", "kind", kind)
		h.renderShortLinkInvalid(w, r)
		return
	}
	now := time.Now()
	bookingID, err := h.webhookSvc.ResolveShortLink(ctx, code, kind, now)
	if errors.Is(err, webhook.ErrShortLinkNotFound) {
		h.renderShortLinkInvalid(w, r)
		return
	}
	if err != nil {
		// Never the code or the path: the code is the credential.
		h.logger.ErrorContext(ctx, "short link: lookup", "error", err, "kind", kind)
		http.Error(w, "Error interno. Inténtalo de nuevo en unos minutos.", http.StatusInternalServerError)
		return
	}
	b, err := h.loadShortLinkBooking(r, bookingID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && b.parsedTimesInvalid) {
		h.renderShortLinkInvalid(w, r)
		return
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "short link: load booking", "error", err, "booking_id", bookingID, "kind", kind)
		http.Error(w, "Error interno. Inténtalo de nuevo en unos minutos.", http.StatusInternalServerError)
		return
	}
	validUntil := webhook.ShortLinkValidUntil(kind, b.start, b.end)
	if !now.Before(validUntil) {
		h.renderShortLinkInvalid(w, r)
		return
	}

	var target string
	switch kind {
	case webhook.ShortLinkRoom:
		if b.status == "cancelled" {
			h.renderShortLinkInvalid(w, r)
			return
		}
		if target = h.shortRoomTarget(b, now); target == "" {
			h.renderShortLinkInvalid(w, r)
			return
		}
	case webhook.ShortLinkManage:
		// A NEW token on every use (additive, never Rotate): the code cannot be turned back
		// into a token that was minted before, only its hash is stored. It lasts as long as
		// the code itself would still resolve, not 60 days.
		tok, err := h.bookingSvc.IssueManageTokenUntil(ctx, bookingID, validUntil)
		if err != nil {
			h.logger.ErrorContext(ctx, "short link: issue manage token", "error", err, "booking_id", bookingID)
			http.Error(w, "Error interno. Inténtalo de nuevo en unos minutos.", http.StatusInternalServerError)
			return
		}
		target = h.publicURL() + "/manage/" + tok
	}
	// Location only: http.Redirect would also echo the link in an HTML body.
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusFound)
}

// loadShortLinkBooking reads the booking a code points at. sql.ErrNoRows = gone.
func (h *Handler) loadShortLinkBooking(r *http.Request, bookingID string) (shortLinkBooking, error) {
	var b shortLinkBooking
	var start, end string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT status, start_at, end_at, COALESCE(location_type, ''), COALESCE(location_value, ''),
		       COALESCE(livekit_room, '')
		FROM bookings WHERE id = ?`, bookingID).
		Scan(&b.status, &start, &end, &b.locationType, &b.locationValue, &b.liveKitRoom)
	if err != nil {
		return b, err
	}
	var perr1, perr2 error
	b.start, perr1 = time.Parse(time.RFC3339Nano, start)
	b.end, perr2 = time.Parse(time.RFC3339Nano, end)
	b.parsedTimesInvalid = perr1 != nil || perr2 != nil
	return b, nil
}

// shortRoomTarget is where a room code sends the client at now: for a LiveKit booking, the
// attendee's join link signed right now (role "", valid until the CURRENT end +
// liveKitJoinGrace - the same link mintMeetingLink stores in location_value, but for the
// booking's time today, so a rescheduled meeting gets a link that still works); for any
// other booking, its location_value when that is a web link. "" = nothing to open (LiveKit
// switched off since, a LiveKit meeting past its join grace, an in-person or telephone
// booking).
func (h *Handler) shortRoomTarget(b shortLinkBooking, now time.Time) string {
	// livekit_room is set only when mintMeetingLink made a room; a booking from before
	// bookings.location_type existed (migration 00061) has that column "".
	if b.liveKitRoom != "" && (b.locationType == "livekit" || b.locationType == "") {
		lk := h.getLiveKit()
		if lk == nil {
			return "" // the room page would 404
		}
		joinUntil := b.end.Add(liveKitJoinGrace)
		if !now.Before(joinUntil) {
			// The room would ask for camera and name, then refuse the expired token: say it
			// here, on the friendly page, instead.
			return ""
		}
		return lk.BookingJoinURL(h.baseURL, b.liveKitRoom, "", joinUntil)
	}
	if webhook.IsWebLink(b.locationValue) {
		return strings.TrimSpace(b.locationValue)
	}
	return ""
}

// renderShortLinkInvalid answers the friendly "Este enlace ya no es válido" page: the
// manage page's own invalid-link view (same branding, CSS and headers) with the short
// link's wording, status 404.
func (h *Handler) renderShortLinkInvalid(w http.ResponseWriter, r *http.Request) {
	h.renderManage(&statusOnFirstWrite{ResponseWriter: w, status: http.StatusNotFound}, r,
		managePageData{TokenInvalid: true, ShortLinkInvalid: true}, h.resolveLocale(r))
}

// statusOnFirstWrite sends status instead of the implicit 200 of the first Write, so a
// renderer that sets its own headers (renderManage) can still answer another status.
type statusOnFirstWrite struct {
	http.ResponseWriter
	status int
	sent   bool
}

func (s *statusOnFirstWrite) WriteHeader(int) {
	if !s.sent {
		s.sent = true
		s.ResponseWriter.WriteHeader(s.status)
	}
}

func (s *statusOnFirstWrite) Write(p []byte) (int, error) {
	s.WriteHeader(s.status)
	return s.ResponseWriter.Write(p)
}
