package handler

// Fork (Agenda Maestros 4x4): attendance of the built-in video room, for supervision. The
// bookings list (GET /v1/bookings) items gain "attendance": did the client and whoever
// attends the session enter the room, and for how long were they in it together.
//
// Two sources, both recorded by one-line hooks in upstream handlers:
//
//  1. The token MINT (LiveKitToken, right after lk.AccessToken succeeds) - synchronous, on
//     our server, so it works even when LiveKit's project webhook is not registered. Each
//     access token has a fresh random identity; recordLiveKitMint classifies it once, when
//     we still know who asked (fork_livekit_mints):
//       - host, verified: the signed-in user is a CURRENT host of the booking (primary or a
//         booking_hosts seat);
//       - staff: any other signed-in workspace user (the owner or an admin supervising, a
//         previous host), or a signed-out holder of a host link that is no longer the
//         booking's current one (the previous host) - staff never counts as client or host;
//       - host, unverified: signed out, holding the booking's current host link;
//       - attendee: everyone else (the client's link).
//  2. The LiveKit webhook (LiveKitWebhook, right after VerifyWebhook, before any branch):
//     participant_joined / participant_left / participant_connection_aborted refine WHEN
//     each identity was in the room (fork_livekit_sessions, one row per participant sid -
//     the SDK reconnects with the same token, so an identity may have several), and
//     room_finished closes that room's open rows. Only identities we minted for that
//     booking and STANDARD participants count (the recording egress is neither).
//
// Status (computeAttendance), over the window [start - 15 min, end + liveKitJoinGrace] of
// the booking's CURRENT times; an open session ends at min(now, window end):
//
//	not_applicable       cancelled, not a LiveKit booking / no room, or it started before
//	                     attendance_since (the records did not exist yet)
//	pending              before the window; also inside it while nobody has entered yet
//	                     and the session has not reached its end
//	in_progress          inside the window and someone (not staff) is in the room now
//	attended             the host and the client were in at the same time (minutes_together)
//	attended_unverified  2+ distinct non-staff participants overlapped (2 min or more) but no
//	                     verified host (a mentor who joined signed out through the client's link)
//	client_absent        the host entered, the client did not
//	host_absent          the client entered, no host of any kind did
//	nobody               nobody entered
//
// When the booking has webhook sessions, they are the truth and a mint alone is not a
// join; when it has none (the webhook is not registered, or failed), a mint inside the
// window counts as "entered" and minutes_together is null - never a false "nadie entró".
// So does a mint up to attendanceMintEarly before the start (someone who entered early
// and stayed: one mint per join), clipped to the window's start.
// Both entered but never together: whoever entered first is the one who waited (the other
// is reported absent). No display names are stored.
//
// Cost per list page: two queries (attendance_since, then one UNION ALL over the page's
// bookings, mints and sessions). Best effort, like the WhatsApp notices.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/booking"
)

// Mint kinds (fork_livekit_mints.kind).
const (
	attendKindHost     = "host"
	attendKindStaff    = "staff"
	attendKindAttendee = "attendee"
)

// Attendance states (attendanceJSON.Status).
const (
	attendNotApplicable = "not_applicable"
	attendPending       = "pending"
	attendInProgress    = "in_progress"
	attendAttended      = "attended"
	attendUnverified    = "attended_unverified"
	attendClientAbsent  = "client_absent"
	attendHostAbsent    = "host_absent"
	attendNobody        = "nobody"
)

// attendanceEarly is how long before the start a join counts.
const attendanceEarly = 15 * time.Minute

// attendanceMintEarly is how long before the start a MINT counts when the booking has no
// webhook sessions. The room asks for a token once per join and the person may then stay
// in, so a mint before the window is someone who entered early and waited (the 1 h reminder
// carries the link); its entry is clipped to the window, like a session. Mints earlier than
// this (a link test from the confirmation, days before) are not a join.
const attendanceMintEarly = 60 * time.Minute

// attendUnverifiedMin is the least overlap of two non-host participants that reads as a
// session: a client who reloads the page gets a new identity while LiveKit still holds the
// old one for a few seconds, which must not look like two people.
const attendUnverifiedMin = 2 * time.Minute

// forkKeyAttendanceSince is the fork_settings key EnsureTeamSchema sets once, at the first
// boot that has the attendance tables.
const forkKeyAttendanceSince = "attendance_since"

// ---- 1. The mint ----------------------------------------------------------------------

// recordLiveKitMint is LiveKitToken's hook after an access token for identity was minted:
// it records who got it (see the file comment). Rooms that belong to no booking are not
// recorded. Best effort: an error is logged, the token is served regardless.
func (h *Handler) recordLiveKitMint(r *http.Request, room, identity, roomToken string) {
	ctx := r.Context()
	bookingID, err := h.bookingForRoom(ctx, room)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			h.logger.ErrorContext(ctx, "attendance: mint booking", "error", err)
		}
		return
	}
	kind, verified := attendKindAttendee, 0
	var userID sql.NullString
	if uid, _, ok := h.sessionUser(r); ok {
		userID = sql.NullString{String: uid, Valid: true}
		kind = attendKindStaff
		if h.isCurrentBookingHost(ctx, bookingID, uid) {
			kind, verified = attendKindHost, 1
		}
	} else if lk := h.getLiveKit(); lk != nil {
		if _, role, _, err := lk.VerifyRoomToken(roomToken); err == nil && role == "host" {
			kind = attendKindStaff // a host link that is no longer the booking's: the previous host
			if h.teamHostLinkCurrent(ctx, room, roomToken) {
				kind = attendKindHost
			}
		}
	}
	if _, err := h.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO fork_livekit_mints (identity, booking_id, kind, user_id, verified, minted_at)
		VALUES (?, ?, ?, ?, ?, ?)`, identity, bookingID, kind, userID, verified, teamNow()); err != nil {
		h.logger.ErrorContext(ctx, "attendance: record mint", "error", err)
	}
}

// bookingForRoom maps a LiveKit room to its booking. Rooms are named "booking-<id>"
// (booking_handler.go) and the booking must still hold that room in bookings.livekit_room;
// a primary-key lookup, not a scan. sql.ErrNoRows for any other room.
func (h *Handler) bookingForRoom(ctx context.Context, room string) (string, error) {
	id, ok := strings.CutPrefix(room, "booking-")
	if !ok || id == "" {
		return "", sql.ErrNoRows
	}
	var bookingID string
	err := h.db.QueryRowContext(ctx,
		`SELECT id FROM bookings WHERE id = ? AND livekit_room = ?`, id, room).Scan(&bookingID)
	return bookingID, err
}

// isCurrentBookingHost reports whether userID hosts bookingID now: its primary host or a
// booking_hosts seat.
func (h *Handler) isCurrentBookingHost(ctx context.Context, bookingID, userID string) bool {
	return h.userHostsBooking(ctx, userID, bookingID)
}

// ---- 2. The LiveKit webhook ------------------------------------------------------------

// lkInt64 is a LiveKit int64: protojson writes it as a quoted string ("1717000000"); a bare
// number is accepted too. Anything unreadable is 0 (= unknown), never an error that would
// drop the rest of the event.
type lkInt64 int64

func (v *lkInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		n = 0
	}
	*v = lkInt64(n)
	return nil
}

// time returns the Unix seconds as RFC3339 UTC, or "" for 0.
func (v lkInt64) time() string {
	if v <= 0 {
		return ""
	}
	return time.Unix(int64(v), 0).UTC().Format(time.RFC3339)
}

// lkAttendanceEvent is the part of a LiveKit webhook event attendance reads; the upstream
// handler parses its own struct.
type lkAttendanceEvent struct {
	ID        string  `json:"id"`
	Event     string  `json:"event"`
	CreatedAt lkInt64 `json:"createdAt"`
	Room      struct {
		Name string `json:"name"`
	} `json:"room"`
	Participant struct {
		SID      string          `json:"sid"`
		Identity string          `json:"identity"`
		JoinedAt lkInt64         `json:"joinedAt"`
		Kind     json.RawMessage `json:"kind"` // enum: absent (= STANDARD, protojson omits the zero value), a name, or a number
	} `json:"participant"`
}

// standardParticipant reports whether a ParticipantInfo.kind is STANDARD (a browser): the
// zero value is omitted by protojson, so absent counts too. EGRESS, INGRESS, SIP and AGENT
// never count as someone in the room.
func standardParticipant(kind json.RawMessage) bool {
	s := strings.Trim(strings.TrimSpace(string(kind)), `"`)
	return s == "" || s == "null" || s == "0" || strings.EqualFold(s, "STANDARD")
}

// recordAttendanceEvent is LiveKitWebhook's hook, right after the signature is verified and
// before any of its branches. Idempotent under LiveKit's retries and out-of-order delivery:
// sessions are keyed by participant sid, joined_at keeps the earliest value and left_at
// the latest. It never changes the webhook's answer (always 200); DB errors are logged.
func (h *Handler) recordAttendanceEvent(ctx context.Context, body []byte) {
	var ev lkAttendanceEvent
	if json.Unmarshal(body, &ev) != nil {
		return
	}
	left := false
	switch ev.Event {
	case "participant_joined":
	case "participant_left", "participant_connection_aborted":
		left = true
	case "room_finished":
	default:
		return
	}
	if ev.Room.Name == "" {
		return
	}
	bookingID, err := h.bookingForRoom(ctx, ev.Room.Name)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			h.logger.ErrorContext(ctx, "attendance: webhook booking", "error", err)
		}
		return
	}
	at := ev.CreatedAt.time()
	if at == "" {
		at = teamNow()
	}
	if ev.Event == "room_finished" {
		if _, err := h.db.ExecContext(ctx,
			`UPDATE fork_livekit_sessions SET left_at = ? WHERE booking_id = ? AND left_at IS NULL`, at, bookingID); err != nil {
			h.logger.ErrorContext(ctx, "attendance: close room", "error", err)
		}
		return
	}
	p := ev.Participant
	if p.SID == "" || p.Identity == "" || !standardParticipant(p.Kind) {
		return
	}
	var mintBooking string
	err = h.db.QueryRowContext(ctx,
		`SELECT booking_id FROM fork_livekit_mints WHERE identity = ?`, p.Identity).Scan(&mintBooking)
	if err != nil || mintBooking != bookingID {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			h.logger.ErrorContext(ctx, "attendance: webhook identity", "error", err)
		}
		return // not a participant we minted for this booking
	}
	joined := sql.NullString{String: p.JoinedAt.time(), Valid: p.JoinedAt > 0}
	if !joined.Valid && !left {
		joined = sql.NullString{String: at, Valid: true}
	}
	leftAt := sql.NullString{String: at, Valid: left}
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO fork_livekit_sessions (participant_sid, identity, booking_id, joined_at, left_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (participant_sid) DO UPDATE SET
			joined_at = MIN(COALESCE(fork_livekit_sessions.joined_at, excluded.joined_at),
			                COALESCE(excluded.joined_at, fork_livekit_sessions.joined_at)),
			left_at   = MAX(COALESCE(fork_livekit_sessions.left_at, excluded.left_at),
			                COALESCE(excluded.left_at, fork_livekit_sessions.left_at))`,
		p.SID, p.Identity, bookingID, joined, leftAt); err != nil {
		h.logger.ErrorContext(ctx, "attendance: record session", "error", err)
	}
}

// ---- 3. The status ---------------------------------------------------------------------

// attendanceJSON is a list item's "attendance". The times are RFC3339 UTC (first entry in
// the window), null when unknown; minutes_together is null when it cannot be measured.
type attendanceJSON struct {
	Status          string  `json:"status"`
	HostJoinedAt    *string `json:"host_joined_at"`
	ClientJoinedAt  *string `json:"client_joined_at"`
	MinutesTogether *int    `json:"minutes_together"`
}

// attendMint is one fork_livekit_mints row.
type attendMint struct {
	kind     string
	verified bool
	mintedAt time.Time
}

// attendSession is one fork_livekit_sessions row (zero time = NULL).
type attendSession struct {
	identity         string
	joinedAt, leftAt time.Time
}

// attendanceInput is everything computeAttendance needs about one booking.
type attendanceInput struct {
	status, locationType, room string
	start, end, since          time.Time
	mints                      map[string]attendMint // by identity
	sessions                   []attendSession
}

// attendIv is a half-open time interval [from, to).
type attendIv struct{ from, to time.Time }

// attendEntry is one non-staff presence in the window.
type attendEntry struct {
	identity, kind string
	verified, open bool
	iv             attendIv
}

func parseAttendTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// computeAttendance is the pure status rule (see the file comment).
func computeAttendance(in attendanceInput, now time.Time) attendanceJSON {
	if in.status == "cancelled" || in.locationType != "livekit" || in.room == "" || in.start.Before(in.since) {
		return attendanceJSON{Status: attendNotApplicable}
	}
	ws, we := in.start.Add(-attendanceEarly), in.end.Add(liveKitJoinGrace)
	if now.Before(ws) {
		return attendanceJSON{Status: attendPending}
	}
	fromWebhook := len(in.sessions) > 0
	var entries []attendEntry
	if fromWebhook {
		for _, s := range in.sessions {
			m, ok := in.mints[s.identity]
			if !ok || m.kind == attendKindStaff {
				continue
			}
			from := s.joinedAt
			if from.IsZero() {
				from = m.mintedAt
			}
			to, open := s.leftAt, s.leftAt.IsZero()
			if open {
				to = minTime(now, we)
			}
			from, to = maxTime(from, ws), minTime(to, we)
			if to.Before(from) {
				continue // entirely outside the window (a link test days before, say)
			}
			entries = append(entries, attendEntry{identity: s.identity, kind: m.kind, verified: m.verified,
				open: open && now.Before(we), iv: attendIv{from, to}})
		}
	} else {
		earliest := in.start.Add(-max(attendanceMintEarly, attendanceEarly))
		for id, m := range in.mints {
			if m.kind == attendKindStaff || m.mintedAt.Before(earliest) || m.mintedAt.After(we) {
				continue
			}
			at := maxTime(m.mintedAt, ws) // entered early and waited: clipped like a session
			entries = append(entries, attendEntry{identity: id, kind: m.kind, verified: m.verified,
				iv: attendIv{at, at}})
		}
	}

	out := attendanceJSON{}
	var hostIvs, clientIvs []attendIv
	var hostFirst, clientFirst time.Time
	verifiedHost, someoneIn := false, false
	perIdentity := map[string][]attendIv{}
	for _, e := range entries {
		switch e.kind {
		case attendKindHost:
			hostIvs = append(hostIvs, e.iv)
			if hostFirst.IsZero() || e.iv.from.Before(hostFirst) {
				hostFirst = e.iv.from
			}
			verifiedHost = verifiedHost || e.verified
		case attendKindAttendee:
			clientIvs = append(clientIvs, e.iv)
			if clientFirst.IsZero() || e.iv.from.Before(clientFirst) {
				clientFirst = e.iv.from
			}
		default:
			continue
		}
		perIdentity[e.identity] = append(perIdentity[e.identity], e.iv)
		if e.open {
			someoneIn = true
		}
	}
	if !hostFirst.IsZero() {
		s := hostFirst.Format(time.RFC3339)
		out.HostJoinedAt = &s
	}
	if !clientFirst.IsZero() {
		s := clientFirst.Format(time.RFC3339)
		out.ClientJoinedAt = &s
	}
	// Without webhook data nobody's leaving is known: during the session itself, a mint
	// means someone is (or was just) in.
	if !fromWebhook && len(entries) > 0 && !now.After(in.end) {
		someoneIn = true
	}
	if someoneIn && now.Before(we) {
		out.Status = attendInProgress
		return out
	}

	minutes := func(d time.Duration) *int {
		m := int(math.Round(d.Minutes()))
		return &m
	}
	hasHost, hasClient := len(hostIvs) > 0, len(clientIvs) > 0
	if fromWebhook {
		if together := attendOverlap(hostIvs, clientIvs); hasHost && hasClient && together > 0 {
			out.Status, out.MinutesTogether = attendAttended, minutes(together)
			return out
		}
		if !verifiedHost {
			if together := attendAtLeastTwo(perIdentity); together >= attendUnverifiedMin {
				out.Status, out.MinutesTogether = attendUnverified, minutes(together)
				return out
			}
		}
	} else if hasHost && hasClient {
		out.Status = attendAttended // both got in; how long together is unknown
		return out
	}
	switch {
	case hasHost && hasClient: // both entered, never at the same time: the first one waited
		if !clientFirst.Before(hostFirst) {
			out.Status = attendClientAbsent
		} else {
			out.Status = attendHostAbsent
		}
	case hasHost:
		out.Status = attendClientAbsent
	case hasClient:
		out.Status = attendHostAbsent
	case now.Before(in.end):
		out.Status = attendPending // nobody yet, and the session is not over
	default:
		out.Status = attendNobody
	}
	return out
}

// attendUnion merges intervals into disjoint, sorted ones (zero-length ones drop out).
func attendUnion(ivs []attendIv) []attendIv {
	var in []attendIv
	for _, iv := range ivs {
		if iv.to.After(iv.from) {
			in = append(in, iv)
		}
	}
	sort.Slice(in, func(i, j int) bool { return in[i].from.Before(in[j].from) })
	var out []attendIv
	for _, iv := range in {
		if n := len(out); n > 0 && !iv.from.After(out[n-1].to) {
			out[n-1].to = maxTime(out[n-1].to, iv.to)
			continue
		}
		out = append(out, iv)
	}
	return out
}

// attendOverlap is how long a and b were both present.
func attendOverlap(a, b []attendIv) time.Duration {
	ua, ub := attendUnion(a), attendUnion(b)
	var total time.Duration
	for _, x := range ua {
		for _, y := range ub {
			from, to := maxTime(x.from, y.from), minTime(x.to, y.to)
			if to.After(from) {
				total += to.Sub(from)
			}
		}
	}
	return total
}

// attendAtLeastTwo is how long at least two distinct identities were present at once.
func attendAtLeastTwo(perIdentity map[string][]attendIv) time.Duration {
	type edge struct {
		at    time.Time
		delta int
	}
	var edges []edge
	for _, ivs := range perIdentity {
		for _, iv := range attendUnion(ivs) {
			edges = append(edges, edge{iv.from, +1}, edge{iv.to, -1})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at.Equal(edges[j].at) {
			return edges[i].delta < edges[j].delta // leave before enter: touching is not overlapping
		}
		return edges[i].at.Before(edges[j].at)
	})
	var total time.Duration
	count := 0
	var since time.Time
	for _, e := range edges {
		if count >= 2 {
			total += e.at.Sub(since)
		}
		count += e.delta
		since = e.at
	}
	return total
}

// fillBookingAttendance sets each list item's attendance: two queries for the page, then
// computeAttendance per booking. Best effort (withWhatsAppNotices logs an error and serves
// the list without it).
func (h *Handler) fillBookingAttendance(ctx context.Context, bookings []booking.Booking, idsJSON string,
	idx map[string]int, out []bookingListItem, now time.Time) error {
	var sinceStr string
	err := h.db.QueryRowContext(ctx,
		`SELECT value FROM fork_settings WHERE key = ?`, forkKeyAttendanceSince).Scan(&sinceStr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	since := parseAttendTime(sinceStr)

	rows, err := h.db.QueryContext(ctx, `
		SELECT 'r', b.id, b.livekit_room, '', 0, ''
		FROM bookings b WHERE b.id IN (SELECT value FROM json_each(?))
		UNION ALL
		SELECT 'm', m.booking_id, m.identity, m.kind, m.verified, m.minted_at
		FROM fork_livekit_mints m WHERE m.booking_id IN (SELECT value FROM json_each(?))
		UNION ALL
		SELECT 's', s.booking_id, s.identity, COALESCE(s.joined_at, ''), 0, COALESCE(s.left_at, '')
		FROM fork_livekit_sessions s WHERE s.booking_id IN (SELECT value FROM json_each(?))`,
		idsJSON, idsJSON, idsJSON)
	if err != nil {
		return err
	}
	defer rows.Close()
	rooms := map[string]string{}
	mints := map[string]map[string]attendMint{}
	sessions := map[string][]attendSession{}
	for rows.Next() {
		var src, bookingID, a, b, c string
		var verified int
		if err := rows.Scan(&src, &bookingID, &a, &b, &verified, &c); err != nil {
			return err
		}
		switch src {
		case "r":
			rooms[bookingID] = a
		case "m":
			if mints[bookingID] == nil {
				mints[bookingID] = map[string]attendMint{}
			}
			mints[bookingID][a] = attendMint{kind: b, verified: verified == 1, mintedAt: parseAttendTime(c)}
		case "s":
			sessions[bookingID] = append(sessions[bookingID],
				attendSession{identity: a, joinedAt: parseAttendTime(b), leftAt: parseAttendTime(c)})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, bk := range bookings {
		i, ok := idx[bk.ID]
		if !ok {
			continue
		}
		a := computeAttendance(attendanceInput{
			status: bk.Status, locationType: bk.LocationType, room: rooms[bk.ID],
			start: bk.StartAt.UTC(), end: bk.EndAt.UTC(), since: since,
			mints: mints[bk.ID], sessions: sessions[bk.ID],
		}, now)
		out[i].Attendance = &a
	}
	return nil
}
