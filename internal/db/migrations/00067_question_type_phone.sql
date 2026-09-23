-- +goose NO TRANSACTION
-- Intake questions gain a fourth type: 'phone'.
--
-- A 'phone' question renders as a country selector (dial code) plus a number field
-- on the public booking surfaces, and stores ONE combined string ("+51 987654321"),
-- so the webhook payload keeps its shape (answers[].answer stays plain text).
--
-- NOT the same thing as event_types.location_type = 'phone' (widened in 00042) nor
-- as event_types.allow_phone_call (00061): those are the meeting's phone location and
-- the attendee's call-back number, stored on the booking. This one is an admission
-- question stored in booking_answers. Do not merge the two concepts.
--
-- SQLite can't ALTER a CHECK, so this is the standard table rebuild, copying every
-- existing row. It runs with NO TRANSACTION + foreign_keys=OFF because
-- booking_answers.question_id references event_type_questions(id) ON DELETE CASCADE
-- (00014): a DROP TABLE with FKs enabled would cascade-delete every stored answer.
-- PRAGMA foreign_keys is a no-op inside a transaction, hence NO TRANSACTION. The
-- closing foreign_keys=ON is not cosmetic either — db.go pins the pool to a single
-- connection that is never recycled and sets the PRAGMA once at open, so leaving it
-- off here would leave the whole process without referential integrity.
--
-- The table has no indexes or triggers of its own, so nothing to recreate after the
-- rename. Numbered 00067 (first shipped as 00063; see 00066 on the renumbering and on why gaps and
-- reserved ranges break goose and SchemaReady).

-- +goose Up
PRAGMA foreign_keys=OFF;

CREATE TABLE event_type_questions_new (
    id            TEXT PRIMARY KEY,
    event_type_id TEXT NOT NULL REFERENCES event_types(id) ON DELETE CASCADE,
    label         TEXT NOT NULL,
    type          TEXT NOT NULL CHECK (type IN ('text', 'select', 'checkbox', 'phone')),
    options       TEXT,           -- JSON array; used for type='select'
    required      INTEGER NOT NULL DEFAULT 0,
    position      INTEGER NOT NULL DEFAULT 0
);

INSERT INTO event_type_questions_new SELECT * FROM event_type_questions;
DROP TABLE event_type_questions;
ALTER TABLE event_type_questions_new RENAME TO event_type_questions;

PRAGMA foreign_keys=ON;

-- +goose Down
-- Irreversible widening of a CHECK. No-op down.
