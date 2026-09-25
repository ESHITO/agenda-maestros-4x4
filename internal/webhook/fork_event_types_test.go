package webhook_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/calnode/calnode/internal/webhook"
)

// Fork (Agenda Maestros 4x4): webhooks limited to some event types
// (fork_event_types.go). No filter = every type; a filter = only those types, for every
// event; the table is created in code (EnsureForkSchema), not by goose.

// seedTypesAndBookings adds two event types owned by testUserID - "Soporte 1 a 1" (et-a)
// and "Mentoría personal" (et-b) - and one confirmed booking of each (bk-a, bk-b).
func seedTypesAndBookings(t *testing.T, e *env) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-a', '` + testUserID + `', 'soporte-1-a-1', 'Soporte 1 a 1', 40)`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-b', '` + testUserID + `', 'mentoria-personal', 'Mentoría personal', 40)`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		 VALUES ('bk-a', 'et-a', '` + testUserID + `', '2026-10-01T14:00:00.000000000Z', '2026-10-01T14:40:00.000000000Z', 'confirmed')`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		 VALUES ('bk-b', 'et-b', '` + testUserID + `', '2026-10-01T16:00:00.000000000Z', '2026-10-01T16:40:00.000000000Z', 'confirmed')`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
}

func filterRows(t *testing.T, e *env, where string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM webhook_event_type_filters WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func isActive(t *testing.T, e *env, webhookID string) bool {
	t.Helper()
	var a int
	if err := e.db.QueryRow(`SELECT is_active FROM webhooks WHERE id = ?`, webhookID).Scan(&a); err != nil {
		t.Fatal(err)
	}
	return a == 1
}

func listedTypes(t *testing.T, e *env, userID, webhookID string) []string {
	t.Helper()
	whs, err := e.svc.List(context.Background(), userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, wh := range whs {
		if wh.ID == webhookID {
			if wh.EventTypeIDs == nil {
				t.Errorf("List: EventTypeIDs is nil for %s; want a non-nil slice", webhookID)
			}
			return wh.EventTypeIDs
		}
	}
	t.Fatalf("List: webhook %s not found", webhookID)
	return nil
}

func TestEnsureForkSchema_idempotent(t *testing.T) {
	e := newEnv(t) // webhook.New already ran it once
	for i := 0; i < 2; i++ {
		if err := webhook.EnsureForkSchema(e.db); err != nil {
			t.Fatalf("EnsureForkSchema call %d: %v", i+1, err)
		}
	}
	for _, obj := range []struct{ typ, name string }{
		{"table", "webhook_event_type_filters"},
		{"index", "idx_webhook_event_type_filters_event_type"},
		{"trigger", "webhook_event_type_filters_never_widen"},
	} {
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, obj.typ, obj.name).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s %s: count = %d, err = %v; want exactly 1", obj.typ, obj.name, n, err)
		}
	}
}

// The CASCADEs depend on it; db.Open sets it on the pool's only connection.
func TestForeignKeysOnForTheCascade(t *testing.T) {
	e := newEnv(t)
	var on int
	if err := e.db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, err = %v; want 1 (db.Open must keep setting it)", on, err)
	}
}

func TestEnqueue_eventTypeFilter(t *testing.T) {
	e := newEnv(t)
	seedTypesAndBookings(t, e)
	ctx := context.Background()
	events := []string{"booking.created", webhook.EventReminder1h}

	all, _, err := e.svc.Create(ctx, testUserID, "https://all.example.com/hook", events)
	if err != nil {
		t.Fatal(err)
	}
	onlyA, _, err := e.svc.CreateWithEventTypes(ctx, testUserID, "https://a.example.com/hook", events, []string{"et-a"})
	if err != nil {
		t.Fatalf("CreateWithEventTypes: %v", err)
	}
	if got := listedTypes(t, e, testUserID, all.ID); len(got) != 0 {
		t.Errorf("unfiltered webhook lists %v; want [] (every type)", got)
	}
	if got := listedTypes(t, e, testUserID, onlyA.ID); !slices.Equal(got, []string{"et-a"}) {
		t.Errorf("filtered webhook lists %v; want [et-a]", got)
	}

	// Both the confirmation and a reminder: the filter applies to every event.
	for i, ev := range events {
		want := i + 1
		if err := e.svc.Enqueue(ctx, ev, webhook.BookingPayload{ID: "bk-a", HostID: testUserID, Status: "confirmed"}); err != nil {
			t.Fatalf("Enqueue %s bk-a: %v", ev, err)
		}
		if err := e.svc.Enqueue(ctx, ev, webhook.BookingPayload{ID: "bk-b", HostID: testUserID, Status: "confirmed"}); err != nil {
			t.Fatalf("Enqueue %s bk-b: %v", ev, err)
		}
		if got := deliveriesFor(t, e, all.ID); got != 2*want {
			t.Errorf("after %s: unfiltered webhook deliveries = %d; want %d (every type)", ev, got, 2*want)
		}
		if got := deliveriesFor(t, e, onlyA.ID); got != want {
			t.Errorf("after %s: filtered webhook deliveries = %d; want %d (its type only, never et-b)", ev, got, want)
		}
	}
	var wrongType int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND booking_id = 'bk-b'`, onlyA.ID).Scan(&wrongType); err != nil || wrongType != 0 {
		t.Errorf("filtered webhook got %d deliveries for the other type (err %v); want 0", wrongType, err)
	}

	// No booking to resolve: the unfiltered webhook still fires, the filtered one does not.
	before := deliveriesFor(t, e, onlyA.ID)
	if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{HostID: testUserID, Status: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	if got := deliveriesFor(t, e, onlyA.ID); got != before {
		t.Errorf("filtered webhook fired for a booking of unknown type: %d deliveries; want %d", got, before)
	}
	if got := deliveriesFor(t, e, all.ID); got != 5 {
		t.Errorf("unfiltered webhook deliveries = %d; want 5", got)
	}
}

// The owner's webhook sees the team's bookings (scope) AND honours its filter; a member's
// webhook still sees only its own bookings.
func TestEnqueue_ownerScopeWithEventTypeFilter(t *testing.T) {
	e := newEnv(t)
	withOwner(t, e)
	seedTypesAndBookings(t, e) // both types and bookings belong to the member testUserID
	ctx := context.Background()

	ownerA, _, err := e.svc.CreateWithEventTypes(ctx, ownerUser, "https://owner.example.com/hook", []string{"booking.created"}, []string{"et-a"})
	if err != nil {
		t.Fatal(err)
	}
	otherA, _, err := e.svc.CreateWithEventTypes(ctx, otherUser, "https://other.example.com/hook", []string{"booking.created"}, []string{"et-a"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bk := range []string{"bk-a", "bk-b"} {
		if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{ID: bk, HostID: testUserID, Status: "confirmed"}); err != nil {
			t.Fatalf("Enqueue %s: %v", bk, err)
		}
	}
	if got := deliveriesFor(t, e, ownerA.ID); got != 1 {
		t.Errorf("owner's filtered webhook deliveries = %d; want 1 (the member's et-a booking only)", got)
	}
	if got := deliveriesFor(t, e, otherA.ID); got != 0 {
		t.Errorf("another member's webhook deliveries = %d; want 0 (scope still applies)", got)
	}

	// WantsField counts the filter too: no manage token is minted for a booking whose
	// type no receiving webhook wants.
	fields := []string{webhook.FieldManageURL}
	if err := e.svc.Update(ctx, ownerUser, ownerA.ID, nil, &fields); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.svc.WantsField(ctx, "booking.created", testUserID, "bk-a", webhook.FieldManageURL); !ok {
		t.Error("WantsField(bk-a) = false; want true")
	}
	if ok, _ := e.svc.WantsField(ctx, "booking.created", testUserID, "bk-b", webhook.FieldManageURL); ok {
		t.Error("WantsField(bk-b) = true; want false (the only webhook asking is limited to et-a)")
	}
}

func TestSetEventTypes_replaceClearAndAtomic(t *testing.T) {
	e := newEnv(t)
	seedTypesAndBookings(t, e)
	ctx := context.Background()
	wh, _, err := e.svc.Create(ctx, testUserID, "https://example.com/hook", []string{"booking.created"})
	if err != nil {
		t.Fatal(err)
	}

	if err := e.svc.SetEventTypes(ctx, testUserID, wh.ID, []string{"et-a", "et-b", "et-a", ""}); err != nil {
		t.Fatalf("SetEventTypes: %v", err)
	}
	if got := listedTypes(t, e, testUserID, wh.ID); len(got) != 2 {
		t.Errorf("types = %v; want et-a and et-b once each", got)
	}
	if err := e.svc.SetEventTypes(ctx, testUserID, wh.ID, []string{"et-b"}); err != nil {
		t.Fatal(err)
	}
	if got := listedTypes(t, e, testUserID, wh.ID); !slices.Equal(got, []string{"et-b"}) {
		t.Errorf("types = %v; want [et-b] (replaced, not added)", got)
	}

	// A bad id fails the foreign key and leaves the previous filter intact.
	if err := e.svc.SetEventTypes(ctx, testUserID, wh.ID, []string{"et-a", "no-such-type"}); err == nil {
		t.Fatal("SetEventTypes with an unknown event type: err = nil; want an error")
	}
	if got := listedTypes(t, e, testUserID, wh.ID); !slices.Equal(got, []string{"et-b"}) {
		t.Errorf("after a failed replace, types = %v; want [et-b] unchanged", got)
	}

	// Someone else's webhook is not found.
	if err := e.svc.SetEventTypes(ctx, otherUser, wh.ID, nil); !errors.Is(err, webhook.ErrNotFound) {
		t.Errorf("SetEventTypes by another user: err = %v; want ErrNotFound", err)
	}

	// Empty = every type again.
	if err := e.svc.SetEventTypes(ctx, testUserID, wh.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	if got := listedTypes(t, e, testUserID, wh.ID); len(got) != 0 {
		t.Errorf("types = %v; want [] after clearing", got)
	}
}

func TestCreateWithEventTypes_unknownTypeCreatesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, _, err := e.svc.CreateWithEventTypes(ctx, testUserID, "https://example.com/hook", []string{"booking.created"}, []string{"no-such-type"}); err == nil {
		t.Fatal("err = nil; want the foreign key error")
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM webhooks`).Scan(&n); err != nil || n != 0 {
		t.Errorf("webhooks = %d (err %v); want 0: the webhook must not exist without its filter", n, err)
	}
}

func TestEventTypeFilter_cascadeOnDelete(t *testing.T) {
	e := newEnv(t)
	seedTypesAndBookings(t, e)
	ctx := context.Background()
	if _, err := e.db.Exec(`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES
		('et-c', ?, 'taller', 'Taller', 60), ('et-d', ?, 'charla', 'Charla', 30)`, testUserID, testUserID); err != nil {
		t.Fatal(err)
	}

	// Deleting a webhook removes its rows.
	gone, _, err := e.svc.CreateWithEventTypes(ctx, testUserID, "https://gone.example.com/hook", []string{"booking.created"}, []string{"et-a", "et-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(ctx, testUserID, gone.ID); err != nil {
		t.Fatal(err)
	}
	if n := filterRows(t, e, "webhook_id = ?", gone.ID); n != 0 {
		t.Errorf("rows left for a deleted webhook = %d; want 0", n)
	}

	// Deleting an event type (one with no bookings) removes its rows. A webhook that keeps
	// another type stays active with it...
	cd, _, err := e.svc.CreateWithEventTypes(ctx, testUserID, "https://cd.example.com/hook", []string{"booking.created"}, []string{"et-c", "et-d"})
	if err != nil {
		t.Fatal(err)
	}
	// ...and one limited to that type alone is switched off rather than left with no rows,
	// which would mean "every type".
	onlyC, _, err := e.svc.CreateWithEventTypes(ctx, testUserID, "https://c.example.com/hook", []string{"booking.created"}, []string{"et-c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`DELETE FROM event_types WHERE id = 'et-c'`); err != nil {
		t.Fatalf("delete event type: %v", err)
	}
	if n := filterRows(t, e, "event_type_id = 'et-c'"); n != 0 {
		t.Errorf("rows left for a deleted event type = %d; want 0", n)
	}
	if got := listedTypes(t, e, testUserID, cd.ID); !slices.Equal(got, []string{"et-d"}) || !isActive(t, e, cd.ID) {
		t.Errorf("webhook with another type left: types = %v, active = %v; want [et-d], active", got, isActive(t, e, cd.ID))
	}
	if isActive(t, e, onlyC.ID) {
		t.Error("webhook limited only to the deleted type is still active; it would now receive EVERY type")
	}
	if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{ID: "bk-b", HostID: testUserID, Status: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	if got := deliveriesFor(t, e, onlyC.ID); got != 0 {
		t.Errorf("switched-off webhook deliveries = %d; want 0", got)
	}

	// An event type with bookings cannot be deleted (RESTRICT): the refused delete also
	// backs out the trigger, so its webhook stays active and keeps its filter.
	onlyA, _, err := e.svc.CreateWithEventTypes(ctx, testUserID, "https://a.example.com/hook", []string{"booking.created"}, []string{"et-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`DELETE FROM event_types WHERE id = 'et-a'`); err == nil {
		t.Fatal("deleting an event type with bookings succeeded; want the RESTRICT error")
	}
	if !isActive(t, e, onlyA.ID) || filterRows(t, e, "webhook_id = ?", onlyA.ID) != 1 {
		t.Errorf("after a refused delete: active = %v, rows = %d; want active with its 1 row",
			isActive(t, e, onlyA.ID), filterRows(t, e, "webhook_id = ?", onlyA.ID))
	}
}

// An upstream migration that rebuilds `webhooks` the standard SQLite way (new table, copy,
// DROP, RENAME) fails while the trigger - on event_types, but naming webhooks - exists;
// with DropForkTrigger first it goes through, the filter rows survive, and
// EnsureForkSchema puts the trigger back (the boot bracket, cmd/calnode/fork_schema.go).
func TestDropForkTrigger_letsAnUpstreamRebuildOfWebhooksRun(t *testing.T) {
	e := newEnv(t)
	seedTypesAndBookings(t, e)
	ctx := context.Background()
	wh, _, err := e.svc.CreateWithEventTypes(ctx, testUserID, "https://a.example.com/hook", []string{"booking.created"}, []string{"et-a"})
	if err != nil {
		t.Fatal(err)
	}
	rebuild := func() error {
		t.Helper()
		if _, err := e.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := e.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
				t.Fatal(err)
			}
		}()
		tx, err := e.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback() //nolint:errcheck
		for _, q := range []string{
			`CREATE TABLE webhooks_new AS SELECT * FROM webhooks`,
			`DROP TABLE webhooks`,
			`ALTER TABLE webhooks_new RENAME TO webhooks`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return tx.Commit()
	}

	if err := rebuild(); err == nil {
		t.Fatal("rebuild of webhooks with the trigger in place succeeded; this test no longer reproduces the failure DropForkTrigger exists for")
	}
	if err := webhook.DropForkTrigger(e.db); err != nil {
		t.Fatalf("DropForkTrigger: %v", err)
	}
	if err := webhook.DropForkTrigger(e.db); err != nil {
		t.Fatalf("DropForkTrigger twice: %v", err)
	}
	if err := rebuild(); err != nil {
		t.Fatalf("rebuild of webhooks without the trigger: %v", err)
	}
	if err := webhook.EnsureForkSchema(e.db); err != nil {
		t.Fatalf("EnsureForkSchema after the rebuild: %v", err)
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'webhook_event_type_filters_never_widen'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("trigger after EnsureForkSchema: count = %d, err = %v; want 1", n, err)
	}
	if got := listedTypes(t, e, testUserID, wh.ID); !slices.Equal(got, []string{"et-a"}) {
		t.Errorf("filter after the rebuild = %v; want [et-a]", got)
	}
}
