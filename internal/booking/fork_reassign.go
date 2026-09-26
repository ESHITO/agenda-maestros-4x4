package booking

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/calnode/calnode/internal/db"
)

// ReassignHostWith is ReassignHost (Agenda Maestros 4x4 fork) with the caller's own writes
// in the SAME transaction: after the busy check and the host_id update, inTx runs on the
// open tx with the booking as it was loaded (HostID already the new host) and may change
// more rows, and fields of b the caller moved (EventTypeID) - an error rolls everything
// back. The team feature uses it to move the booking_hosts primary seat, the event type
// (the new host's copy of the template) and the answers together with the host, so a
// half-done reassign can never leave the old host with a seat or the new host on another
// person's copy.
//
// It mirrors ReassignHost statement for statement (same errors, same no-op for the
// current host, which skips inTx); keep the two in step. inTx must use only tx - the pool
// is a single connection.
func (s *Service) ReassignHostWith(ctx context.Context, bookingID, newHostID string,
	inTx func(ctx context.Context, tx *sql.Tx, b *Booking) error) (*Booking, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("booking: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b, err := scanBooking(tx.QueryRowContext(ctx, `SELECT `+bookingColumns+` FROM bookings WHERE id = ?`, bookingID))
	if err != nil {
		return nil, err
	}
	if b.Status == "cancelled" {
		return nil, ErrAlreadyCancelled
	}
	if b.HostID == newHostID {
		return b, nil // already this host — nothing to do
	}

	startStr := b.StartAt.UTC().Format(time.RFC3339Nano)
	endStr := b.EndAt.UTC().Format(time.RFC3339Nano)

	// The new host must be free at this time across everything they attend.
	busy, err := hostBusy(ctx, tx, newHostID, startStr, endStr, bookingID)
	if err != nil {
		return nil, fmt.Errorf("booking: reassign overlap: %w", err)
	}
	if busy {
		return nil, ErrDoubleBooked
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		UPDATE bookings SET host_id = ?, updated_at = ? WHERE id = ?`,
		newHostID, now, bookingID); err != nil {
		if db.IsUniqueViolation(err) {
			return nil, ErrDoubleBooked
		}
		return nil, fmt.Errorf("booking: reassign update: %w", err)
	}
	b.HostID = newHostID
	if inTx != nil {
		if err := inTx(ctx, tx, b); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("booking: reassign commit: %w", err)
	}
	if t, err := time.Parse(time.RFC3339Nano, now); err == nil {
		b.UpdatedAt = t
	}
	return b, nil
}
