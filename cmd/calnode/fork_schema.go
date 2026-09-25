package main

import (
	"context"
	"database/sql"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/webhook"
)

// migrateWithForkSchema is db.Migrate for the fork (Agenda Maestros 4x4): the goose
// migrations plus the one table the fork creates in code, not with goose
// (webhook_event_type_filters, internal/webhook/fork_event_types.go).
//
// Its trigger lives on event_types but names `webhooks`, and since SQLite 3.26 ALTER
// TABLE ... RENAME re-checks every trigger in the schema: an upstream migration that
// rebuilds `webhooks` (CREATE webhooks_new / DROP webhooks / RENAME) would fail on it and
// the server would never boot. So when migrations are pending the trigger is dropped
// first, and EnsureForkSchema puts it back right after - a failure there stops the boot,
// like a failed migration, because without the trigger a filter could widen to every
// event type. With nothing pending the trigger is left alone, so a `calnode mcp` started
// next to a running server never removes it from under that server.
func migrateWithForkSchema(ctx context.Context, database *sql.DB) error {
	if ready, err := db.SchemaReady(ctx, database); err != nil || !ready {
		if err := webhook.DropForkTrigger(database); err != nil {
			return err
		}
	}
	if err := db.Migrate(database); err != nil {
		return err
	}
	return webhook.EnsureForkSchema(database)
}
