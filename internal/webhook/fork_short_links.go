package webhook

// Fork (Agenda Maestros 4x4): short links for the WhatsApp text.
//
// The clients read the agenda's messages on WhatsApp, mostly on a phone, where {enlace} (a
// LiveKit join link: /room/booking-<uuid>?t=<~150-character token>) and {cancelar}
// (/manage/<64 hex>) wrapped over several lines and looked like spam. So whatsapp_message
// carries instead
//
//	{PUBLIC_BASE_URL}/e/{code}   enter the session       (kind "room")
//	{PUBLIC_BASE_URL}/c/{code}   cancel or change the date (kind "manage")
//
// with an 8-character code, e.g. https://citas.clubmaestros4x4.com/e/k3pq9abx. The handler
// (handler/fork_short_links.go) resolves the code when the client taps it: /e builds the
// attendee's join link right then, /c issues a fresh manage token. Only whatsapp_message
// uses them; location_value and manage_url in the payload are unchanged.
//
// A code is a credential (it opens the room or the manage page), so the database keeps a
// KEYED hash of it and never the code: HMAC-SHA256 under a key derived from the instance
// key (the keyvault DEK the Service already holds). Not a plain SHA-256 as for manage
// tokens: those carry 256 random bits, a code carries ~40, and 31^8 SHA-256s take minutes
// on a GPU - a leaked database or Litestream backup would give every code back. Without
// the instance key (unwrapped only with CALNODE_ENCRYPTION_KEY, which is not in the
// database) the stored hashes reveal nothing. With no key configured (dev) the key is
// ephemeral and codes stop resolving after a restart, like the webhook secrets.
//
// Guessing: 31^8 ≈ 8.5e11 codes. The resolver is behind a rate limit keyed per IPv4
// address and per IPv6 /64 (server.shortLinkClientKey: a single host usually holds a whole
// /64), a code only resolves while its booking is still ahead (see ShortLinkValidUntil),
// and /e and /c are separate namespaces (a room code never opens the manage page). If that
// ever needs to be harder, raise ShortCodeLen and keep MinShortCodeLen where it is: each
// extra character multiplies the work by 31, and codes already sent keep resolving because
// ValidShortCode accepts every length from MinShortCodeLen to ShortCodeLen.
//
// A reschedule revokes the manage codes (DeleteShortLinks, next to RotateManageToken in
// handler.rescheduleSideEffects): a /c from an older message must die with the manage
// links it stands for. The rescheduled message and the re-planned reminders are rendered
// afterwards, so they carry new codes. Room codes are kept and follow the booking's new time.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

// Short link kinds: what a code opens. Stored in short_links.kind (CHECK) and bound to the
// route: /e resolves only "room" codes, /c only "manage" ones.
const (
	ShortLinkRoom   = "room"
	ShortLinkManage = "manage"
)

// ShortCodeAlphabet: lower case, no look-alikes (no 0/o, 1/l/i), so a code read aloud or
// copied by hand survives. 31 symbols.
const ShortCodeAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"

// ShortCodeLen is the number of characters in a NEW code (~39.6 bits with this alphabet).
const ShortCodeLen = 8

// MinShortCodeLen is the shortest code ValidShortCode accepts: the length codes were first
// issued with. Never raise it while codes of that length may still be live (60 days).
const MinShortCodeLen = 8

// ShortLinkMaxTTL is the hard cap of a code, from its creation - the same 60 days as a
// manage token. The real limit is the booking's own time (ShortLinkValidUntil), checked
// against the booking as it is WHEN the code is used, so a reschedule moves a room code's
// with no write here (manage codes are revoked instead, see DeleteShortLinks); this cap is
// what bounds the table (PurgeExpiredShortLinks).
const ShortLinkMaxTTL = 60 * 24 * time.Hour

// ShortLinkGrace is how long after the booking's moment a code still resolves: a room code
// until the meeting's END + 12 h, a manage code until its START + 12 h (after that there is
// nothing left to cancel).
const ShortLinkGrace = 12 * time.Hour

// ErrShortLinkNotFound: no such code for that kind, or past its hard cap. The resolver
// answers the same friendly page either way (no oracle for a guesser).
var ErrShortLinkNotFound = errors.New("webhook: short link not found or expired")

// shortCodeSymbol[b] reports whether byte b belongs to ShortCodeAlphabet.
var shortCodeSymbol = func() (t [256]bool) {
	for i := 0; i < len(ShortCodeAlphabet); i++ {
		t[ShortCodeAlphabet[i]] = true
	}
	return t
}()

// NewShortCode returns a random code of ShortCodeLen characters from ShortCodeAlphabet,
// from crypto/rand. Bytes at or above the largest multiple of 31 that fits in a byte (248)
// are thrown away, so every symbol is exactly as likely as the others.
func NewShortCode() (string, error) {
	const n = len(ShortCodeAlphabet)
	const limit = 256 - 256%n
	out := make([]byte, 0, ShortCodeLen)
	buf := make([]byte, 2*ShortCodeLen)
	for len(out) < ShortCodeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("webhook: short code: %w", err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, ShortCodeAlphabet[int(b)%n])
			if len(out) == ShortCodeLen {
				break
			}
		}
	}
	return string(out), nil
}

// ValidShortCode reports whether code has the shape of a code: MinShortCodeLen to
// ShortCodeLen characters of ShortCodeAlphabet. The resolver checks it before touching the
// database: anything else is a 404 at once.
func ValidShortCode(code string) bool {
	if len(code) < MinShortCodeLen || len(code) > ShortCodeLen {
		return false
	}
	for i := 0; i < len(code); i++ {
		if !shortCodeSymbol[code[i]] {
			return false
		}
	}
	return true
}

// ShortLinkPath is the route prefix of kind: "/e/" (room) or "/c/" (manage).
func ShortLinkPath(kind string) string {
	if kind == ShortLinkManage {
		return "/c/"
	}
	return "/e/"
}

// ShortLinkValidUntil is the moment a code of kind stops resolving for a booking that
// starts at start and ends at end (its CURRENT times; see ShortLinkMaxTTL).
func ShortLinkValidUntil(kind string, start, end time.Time) time.Time {
	if kind == ShortLinkManage {
		return start.Add(ShortLinkGrace)
	}
	return end.Add(ShortLinkGrace)
}

// forkShortLinkSchema is created by EnsureForkSchema with the rest of the fork's schema, in
// code and not by goose (see forkSchema). A plain table and two indexes, no trigger.
//
//   - code_hash: hex HMAC-SHA256 of the code (shortCodeHash); the code itself is never stored.
//   - booking_id: CASCADE, a deleted booking takes its codes with it (the index serves that).
//   - expires_at: the hard cap (ShortLinkMaxTTL); indexed for the worker's purge.
var forkShortLinkSchema = []string{
	`CREATE TABLE IF NOT EXISTS short_links (
		code_hash  TEXT PRIMARY KEY,
		booking_id TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
		kind       TEXT NOT NULL CHECK (kind IN ('room', 'manage')),
		expires_at TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`,
	`CREATE INDEX IF NOT EXISTS idx_short_links_booking ON short_links (booking_id)`,
	`CREATE INDEX IF NOT EXISTS idx_short_links_expires ON short_links (expires_at)`,
}

// shortLinkKeyLabel separates the short-link key from every other use of the instance key.
const shortLinkKeyLabel = "agenda-maestros-4x4/short-links/v1"

// shortCodeHash is what short_links.code_hash holds for code: HMAC-SHA256 under a key
// derived from the instance key (see the file comment for why it is keyed).
func (s *Service) shortCodeHash(code string) string {
	derive := hmac.New(sha256.New, s.key[:])
	derive.Write([]byte(shortLinkKeyLabel))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// CreateShortLink stores a new code of kind for bookingID and returns it. Only its keyed
// hash is written; the code exists nowhere else but in the returned string. A hash that is
// already taken (a collision, ~1 in 10^11 per live code) draws a new code.
func (s *Service) CreateShortLink(ctx context.Context, bookingID, kind string, now time.Time) (string, error) {
	if kind != ShortLinkRoom && kind != ShortLinkManage {
		return "", fmt.Errorf("webhook: short link: unknown kind %q", kind)
	}
	expires := now.UTC().Add(ShortLinkMaxTTL).Format(time.RFC3339)
	for attempt := 0; attempt < 3; attempt++ {
		code, err := NewShortCode()
		if err != nil {
			return "", err
		}
		res, err := s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO short_links (code_hash, booking_id, kind, expires_at)
			VALUES (?, ?, ?, ?)`, s.shortCodeHash(code), bookingID, kind, expires)
		if err != nil {
			return "", fmt.Errorf("webhook: short link: insert: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return code, nil
		}
	}
	return "", errors.New("webhook: short link: no free code after 3 draws")
}

// ResolveShortLink returns the booking a code of kind was made for, or ErrShortLinkNotFound
// when there is none or it is past its hard cap. It does NOT check the booking's own time
// or status; the caller does, with ShortLinkValidUntil, against the booking as it is now.
// The caller has already checked ValidShortCode.
func (s *Service) ResolveShortLink(ctx context.Context, code, kind string, now time.Time) (string, error) {
	var bookingID string
	err := s.db.QueryRowContext(ctx, `
		SELECT booking_id FROM short_links
		WHERE code_hash = ? AND kind = ? AND expires_at > ?`,
		s.shortCodeHash(code), kind, now.UTC().Format(time.RFC3339)).Scan(&bookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrShortLinkNotFound
	}
	if err != nil {
		return "", fmt.Errorf("webhook: short link: lookup: %w", err)
	}
	return bookingID, nil
}

// DeleteShortLinks revokes every code of kind made for bookingID, in one indexed statement.
// A reschedule calls it for ShortLinkManage next to RotateManageToken, BEFORE the
// booking.rescheduled message is rendered, so only codes sent from then on open /manage.
func DeleteShortLinks(ctx context.Context, db *sql.DB, bookingID, kind string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM short_links WHERE booking_id = ? AND kind = ?`, bookingID, kind); err != nil {
		return fmt.Errorf("webhook: short link: revoke: %w", err)
	}
	return nil
}

// PurgeExpiredShortLinks deletes the codes past their hard cap, in one indexed statement.
// Called on every worker poll, next to the manage-token purge.
func PurgeExpiredShortLinks(ctx context.Context, db *sql.DB, now time.Time) error {
	_, err := db.ExecContext(ctx, `DELETE FROM short_links WHERE expires_at < ?`, now.UTC().Format(time.RFC3339))
	return err
}

// SetShortLinkBaseURL sets where the short links point: base returns the booker-facing
// base URL (the handler passes its publicURL, i.e. PUBLIC_BASE_URL else BASE_URL), read at
// render time so it always matches the handler's. nil or "" = no short links: the long
// ones are sent, as before.
func (s *Service) SetShortLinkBaseURL(base func() string) { s.shortLinkBase = base }

// SetLogger sets the logger for the fork's best-effort paths (a short link that could not
// be made). nil = slog.Default().
func (s *Service) SetLogger(l *slog.Logger) { s.logger = l }

func (s *Service) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

// shortLinkBaseURL is the base for short links without a trailing slash, or "".
func (s *Service) shortLinkBaseURL() string {
	if s.shortLinkBase == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(s.shortLinkBase()), "/")
}

// ShortLinkURL is the public short link for code: base + "/e/" or "/c/" + code.
func ShortLinkURL(base, kind, code string) string {
	return strings.TrimRight(base, "/") + ShortLinkPath(kind) + code
}

// worthShortening reports whether long gets a short link of kind on base: it is a web
// link and the short one is really shorter. A Meet link (36 characters) stays as it is; a
// LiveKit join link or a manage link is always replaced.
func worthShortening(base, kind, long string) bool {
	return base != "" && IsWebLink(long) &&
		len(ShortLinkURL(base, kind, strings.Repeat("x", ShortCodeLen))) < len(long)
}

// ShortLinkForPreview is the editor's "Ver ejemplo" version of a short link: the same rule
// as a delivery (worthShortening), with sampleCode instead of a stored code.
func ShortLinkForPreview(base, kind, long, sampleCode string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if !worthShortening(base, kind, long) {
		return long
	}
	return ShortLinkURL(base, kind, sampleCode)
}

// IsWebLink reports whether v is an absolute http(s) URL, the only kind of {enlace} a /e
// code can redirect to. A call-back number ("tel:+51...") or an address stays as written.
func IsWebLink(v string) bool {
	u, err := url.Parse(strings.TrimSpace(v))
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// whatsAppShortLink is the value of {enlace} (kind room) or {cancelar} (kind manage) in a
// rendered whatsapp_message: a NEW short link for this message (a code cannot be reused -
// only its hash is kept), or long unchanged when there is nothing to shorten (no link, no
// base URL, not a web link, not shorter). If the code cannot be stored, the long link is
// sent instead - a message without its link is worse than a long one - and that is logged,
// without the link.
func (s *Service) whatsAppShortLink(ctx context.Context, bookingID, kind, long string) string {
	base := s.shortLinkBaseURL()
	if bookingID == "" || !worthShortening(base, kind, long) {
		return long
	}
	code, err := s.CreateShortLink(ctx, bookingID, kind, time.Now())
	if err != nil {
		s.log().ErrorContext(ctx, "whatsapp: short link not created; sending the long link",
			"error", err, "booking_id", bookingID, "kind", kind)
		return long
	}
	return ShortLinkURL(base, kind, code)
}
