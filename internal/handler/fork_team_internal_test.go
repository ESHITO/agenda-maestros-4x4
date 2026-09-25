package handler

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/webhook"
)

// Fork (Agenda Maestros 4x4): the team reconcile's pure parts and the invariants that tie
// it to upstream's schema (fork_team.go).

func newTeamInternalHandler(t *testing.T) *Handler {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database, slog.Default())
}

// teamSyncedPinned is every event_types column the field sync copies from the template to
// the mentors' copies. With teamSyncExcluded it must cover the whole table: an upstream
// migration that adds a column fails this test until someone decides which side it goes
// on (a host-specific value - a link, a phone, an address - must be EXCLUDED, or it
// reaches every mentor's clients).
var teamSyncedPinned = []string{
	"name", "description", "duration_minutes", "slot_interval_minutes", "location_type",
	"buffer_before_minutes", "buffer_after_minutes", "min_notice_minutes", "max_future_days",
	"seat_limit", "is_public", "msg_confirmation", "msg_cancellation", "msg_reschedule",
	"msg_reminder", "max_active_bookings", "subj_confirmation", "subj_cancellation",
	"subj_reschedule", "subj_reminder", "price_cents", "currency", "msg_greeting",
	"show_taken_slots", "allow_phone_call",
}

func TestTeamSyncColumns_classified(t *testing.T) {
	h := newTeamInternalHandler(t)
	ctx := context.Background()
	rows, err := h.db.QueryContext(ctx, `SELECT name FROM pragma_table_info('event_types')`)
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		all = append(all, n)
	}
	rows.Close()
	for _, c := range all {
		if !teamSyncExcluded[c] && !slices.Contains(teamSyncedPinned, c) {
			t.Errorf("event_types.%s is not classified: add it to teamSyncedPinned (the copies get the template's value) or to teamSyncExcluded (fork_team.go)", c)
		}
	}
	for c := range teamSyncExcluded {
		if !slices.Contains(all, c) {
			t.Errorf("teamSyncExcluded names %q, which event_types no longer has", c)
		}
	}
	got, err := teamSyncColumns(ctx, h.db)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(teamSyncedPinned)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("synced columns = %v\nwant %v", got, want)
	}
}

func TestTeamCopySlugBase(t *testing.T) {
	long := strings.Repeat("Bartolomé ", 8) // 80 chars
	for _, c := range []struct {
		name, email, want string
	}{
		{"María José Núñez", "mj@example.com", "mentoria-privada-maria-jose-nunez"},
		{"  Ñandú  Peña ", "x@example.com", "mentoria-privada-nandu-pena"},
		{"Łukasz Ærøskøbing", "l@example.com", "mentoria-privada-lukasz-aeroskobing"},
		{"李小龙", "bruce.lee+m@example.com", "mentoria-privada-bruce-lee-m"},
		{"", "", "mentoria-privada-abcdef12"},
		{long, "b@example.com", "mentoria-privada-bartolome-bartolome-bartolome-bartolome"},
	} {
		got := teamCopySlugBase("mentoria-privada", teamUser{id: "ABCDEF12-3456", name: c.name, email: c.email})
		if got != c.want {
			t.Errorf("slug for %q = %q; want %q", c.name, got, c.want)
		}
		if part := strings.TrimPrefix(got, "mentoria-privada-"); len(part) > maxTeamSlugName {
			t.Errorf("name part %q is %d chars; cap is %d", part, len(part), maxTeamSlugName)
		}
	}
}

// The bookings list's notice status (scopedWebhook.receives) must agree with delivery
// (matchingWebhooks) for copy bookings too: a template-filtered webhook receives them.
func TestScopedWebhooks_mirrorTemplateAwareMatching(t *testing.T) {
	h := newTeamInternalHandler(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO users (id, email, name, is_owner, is_admin) VALUES ('own', 'o@example.com', 'Owner', 1, 1)`,
		`INSERT INTO users (id, email, name) VALUES ('men', 'm@example.com', 'Mentor')`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-t', 'own', 't', 'T', 60)`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-c', 'own', 't-men', 'T', 60)`,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-o', 'own', 'o', 'O', 60)`,
		`INSERT INTO fork_event_type_links (copy_id, template_id, user_id, kind, created_at) VALUES ('et-c', 'et-t', 'men', 'copy', 'x')`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk-t', 'et-t', 'own', '2026-10-01T14:00:00Z', '2026-10-01T15:00:00Z', 'confirmed')`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk-c', 'et-c', 'men', '2026-10-01T14:00:00Z', '2026-10-01T15:00:00Z', 'confirmed')`,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk-o', 'et-o', 'men', '2026-10-02T14:00:00Z', '2026-10-02T15:00:00Z', 'confirmed')`,
	} {
		if _, err := h.db.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	events := []string{"booking.created", webhook.EventReminder1h}
	for _, filter := range [][]string{{"et-t"}, {"et-o"}, nil} {
		if _, err := h.db.Exec(`DELETE FROM webhooks`); err != nil {
			t.Fatal(err)
		}
		if _, _, err := h.webhookSvc.CreateWithEventTypes(ctx, "own", "https://owner.example.com/hook", events, filter); err != nil {
			t.Fatal(err)
		}
		hooks, err := h.scopedWebhooks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range []booking.Booking{
			{ID: "bk-t", EventTypeID: "et-t", HostID: "own"},
			{ID: "bk-c", EventTypeID: "et-c", HostID: "men"},
			{ID: "bk-o", EventTypeID: "et-o", HostID: "men"},
		} {
			for _, ev := range events {
				mirror := false
				for _, wh := range hooks {
					mirror = mirror || wh.receives(ev, b)
				}
				delivery, err := h.webhookSvc.WantsField(ctx, ev, b.HostID, b.ID, webhook.FieldEventTypeSlug)
				if err != nil {
					t.Fatal(err)
				}
				if mirror != delivery {
					t.Errorf("filter %v, %s, %s: list says %v, delivery says %v", filter, b.ID, ev, mirror, delivery)
				}
			}
		}
	}
}
