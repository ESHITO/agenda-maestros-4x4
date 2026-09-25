package main

import (
	"context"
	"testing"

	"github.com/calnode/calnode/internal/db"
)

// Fork: the boot path leaves the goose schema AND the fork's code-made schema in place,
// on a fresh database and on every later boot (nothing pending: the trigger is kept).
func TestMigrateWithForkSchema_freshAndRepeated(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	for boot := 1; boot <= 2; boot++ {
		if err := migrateWithForkSchema(context.Background(), database); err != nil {
			t.Fatalf("boot %d: %v", boot, err)
		}
		if ready, err := db.SchemaReady(context.Background(), database); err != nil || !ready {
			t.Fatalf("boot %d: schema ready = %v, err = %v; want every migration applied", boot, ready, err)
		}
		for _, obj := range []struct{ typ, name string }{
			{"table", "webhook_event_type_filters"},
			{"trigger", "webhook_event_type_filters_never_widen"},
			// The team feature (internal/webhook/fork_team.go): links in EnsureForkSchema,
			// the rest in EnsureTeamSchema, both on this path.
			{"table", "fork_event_type_links"},
			{"table", "fork_question_links"},
			{"table", "fork_member_areas"},
			{"table", "fork_invite_roles"},
			{"table", "fork_livekit_mints"},
			{"table", "fork_livekit_sessions"},
			{"table", "fork_livekit_host_links"},
		} {
			var n int
			if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, obj.typ, obj.name).Scan(&n); err != nil || n != 1 {
				t.Errorf("boot %d: %s %s count = %d, err = %v; want 1", boot, obj.typ, obj.name, n, err)
			}
		}
		// attendance_since is written once and kept by later boots (INSERT OR IGNORE).
		var since int
		if err := database.QueryRow(`SELECT COUNT(*) FROM fork_settings WHERE key = 'attendance_since' AND value <> ''`).Scan(&since); err != nil || since != 1 {
			t.Errorf("boot %d: attendance_since rows = %d, err = %v; want 1", boot, since, err)
		}
	}
}
