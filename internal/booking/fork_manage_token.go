package booking

import (
	"context"
	"fmt"
	"time"
)

// IssueManageTokenUntil is IssueManageToken (additive: no other token of the booking is
// touched) for a token that expires at until instead of after the usual 60 days, and never
// later than those 60 days.
//
// Fork (Agenda Maestros 4x4): the WhatsApp /c short link mints one on every use
// (handler/fork_short_links.go). Bounding it to the code's own validity keeps a /c opened
// near the end of its window from handing out a /manage credential that outlives it by
// weeks, and keeps the rows from repeated taps (link scanners, previews) short-lived: the
// worker's manage-token purge takes them once until has passed.
func (s *Service) IssueManageTokenUntil(ctx context.Context, bookingID string, until time.Time) (string, error) {
	rawHex, hash, expiresAt, err := generateManageToken()
	if err != nil {
		return "", err
	}
	if capped := until.UTC().Format(time.RFC3339); capped < expiresAt {
		expiresAt = capped
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO booking_manage_tokens (token_hash, booking_id, expires_at)
		VALUES (?, ?, ?)`, hash, bookingID, expiresAt); err != nil {
		return "", fmt.Errorf("booking: insert manage token: %w", err)
	}
	return rawHex, nil
}
