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
-- Numbered after upstream, not in a reserved high range. First shipped as 00062; upstream
-- then released its own 00062-00065, goose refused the duplicate version, and this became
-- 00066 (00063_question_type_phone became 00067). A reserved range (00500) was tried and
-- dropped earlier: goose refuses a migration numbered below the highest one applied, so an
-- upstream 00062 landing on a database that ran 00500 would abort the boot, and
-- WithAllowMissing would make SchemaReady report ready with upstream work still pending.
-- Renumbering on each upstream merge is the cheap, visible cost; the duplicate-version
-- panic in every test is what flags it.
ALTER TABLE users ADD COLUMN is_support INTEGER NOT NULL DEFAULT 0;

-- No backfill: DEFAULT 0 keeps every existing user exactly what they were, and
-- keeps the existing SELECT/Scan pairs valid for rows created before this column.

-- +goose Down
-- SQLite does not support DROP COLUMN here; this migration cannot be reversed.
