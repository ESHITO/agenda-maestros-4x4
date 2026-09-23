-- +goose Up
-- Support is a third workspace tier, between Member and Admin: it may see every
-- booking in the workspace, cancel and reschedule any of them, and list users to
-- find a member. It may NOT touch settings, secrets, API keys or webhooks, nor
-- invite, archive, delete, re-role or reassign anyone.
--
-- is_support is DELIBERATELY independent of is_admin, and support does NOT imply
-- admin. That is the whole wall: requireAdmin() (internal/handler/handler.go)
-- checks only IsAdmin, and every configuration handler goes through it, so a user
-- with is_support = 1 and is_admin = 0 gets 403 on branding, email, Google, Zoom,
-- Stripe, LiveKit, storage, AI, tracking and notetaker without a single rule being
-- written here. Never make this column satisfy requireAdmin().
--
-- Role precedence when reading the flags is Owner > Admin > Support > Member.
--
-- Numbered 00062 — the next number in sequence, NOT a reserved high range. A gap
-- was tried (00500) and reverted: goose refuses any migration whose version is
-- below the highest already applied, so an upstream 00062 landing on a database
-- that had run 00500 aborts the boot. WithAllowMissing silences that but makes
-- SchemaReady compare MAX(version_id) against the highest embedded migration and
-- report ready while an upstream migration is still pending. If upstream ever
-- ships its own 00062, git flags the filename collision at merge time and this
-- one is renumbered — a visible, one-line conflict, which is the cheaper failure.
ALTER TABLE users ADD COLUMN is_support INTEGER NOT NULL DEFAULT 0;

-- No backfill: DEFAULT 0 keeps every existing user exactly what they were, and
-- keeps the existing SELECT/Scan pairs valid for rows created before this column.

-- +goose Down
-- SQLite does not support DROP COLUMN here; this migration cannot be reversed.
