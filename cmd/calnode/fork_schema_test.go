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
		} {
			var n int
			if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, obj.typ, obj.name).Scan(&n); err != nil || n != 1 {
				t.Errorf("boot %d: %s %s count = %d, err = %v; want 1", boot, obj.typ, obj.name, n, err)
			}
		}
	}
}
