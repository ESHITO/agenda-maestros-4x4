package handler

import (
	"context"
	"database/sql"
	"errors"
)

// EnsureWorkspaceOwner gives an owner-less workspace its owner back: the earliest active
// admin. The browser claim flow used to create the first account as admin only, so an
// install claimed that way had no owner, and the owner-only actions (changing roles,
// archiving admins, transferring ownership) were unreachable for everyone.
//
// It runs at boot and is a no-op once any owner exists, so it can never create a second
// owner or override a transfer. This is deliberately code, not a goose migration: the
// fork's migration numbers already collide with upstream's, and a data repair needs no
// schema version. Returns the promoted user's email, or "" when nothing changed.
func (h *Handler) EnsureWorkspaceOwner(ctx context.Context) (string, error) {
	var id, email string
	err := h.db.QueryRowContext(ctx, `
		SELECT id, email FROM users
		WHERE is_admin = 1 AND archived_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM users WHERE is_owner = 1)
		ORDER BY created_at ASC, id ASC
		LIMIT 1`).Scan(&id, &email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// Re-check inside the UPDATE so a concurrent claim or transfer can't yield two owners.
	res, err := h.db.ExecContext(ctx, `
		UPDATE users SET is_owner = 1, is_admin = 1, is_support = 0
		WHERE id = ? AND NOT EXISTS (SELECT 1 FROM users WHERE is_owner = 1)`, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", nil
	}
	return email, nil
}
