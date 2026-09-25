package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/uid"
)

// Fork (Agenda Maestros 4x4): event_type_ids on POST/PATCH/GET /v1/webhooks and
// GET /v1/webhooks/event-types (webhook_event_types.go). Storage and delivery are tested
// in internal/webhook/fork_event_types_test.go; this covers the API, who may pick which
// type, and a real booking + reminder going through the filter.

const whMemberKey = "member-webhook-types-key"

// webhookTypesWorld is an owner (the setup user) with two event types - A (member u2
// hosts it) and B - plus member u2 with an API key and a type of their own, and member u3
// with a type u2 neither owns nor hosts.
type webhookTypesWorld struct {
	h                            *handler.Handler
	db                           *sql.DB
	ownerKey, ownerID            string
	slugA, etA, slugB, etB       string
	etMember, etStranger, member string
}

// createEventTypeHTTP is seedEventTypeHTTP without re-seeding availability.
func createEventTypeHTTP(t *testing.T, h *handler.Handler, apiKey, name string) (slug, id string) {
	t.Helper()
	slug = "tipo-" + uid.New()[:8]
	body := fmt.Sprintf(`{"slug":%q,"name":%q,"duration_minutes":40,"max_active_bookings":0,"max_future_days":0}`, slug, name)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, apiKey))
	return slug, mustString(t, mustCreated(t, rec, "create event type "+name), "id", "create event type")
}

func newWebhookTypesWorld(t *testing.T) *webhookTypesWorld {
	t.Helper()
	w := &webhookTypesWorld{}
	w.h, w.db, w.ownerKey, w.ownerID = setupWorkspaceWithDB(t)
	w.slugA, w.etA = seedEventTypeHTTP(t, w.h, w.ownerKey) // also opens the owner's week
	w.slugB, w.etB = createEventTypeHTTP(t, w.h, w.ownerKey, "Mentoría personal")

	w.member = seedMember(t, w.db, "u2", "mentor@example.com")
	mustExec(t, w.db, `INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k-u2','u2','t',?,'2024-01-01')`, sha256HexForTest(whMemberKey))
	mustExec(t, w.db, `INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority) VALUES ('eth-u2', ?, 'u2', 'rotation', 0)`, w.etA)
	w.etMember = "et-u2-own"
	mustExec(t, w.db, `INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES (?, 'u2', 'mentoria-privada-u2', 'Mentoría privada', 40)`, w.etMember)

	seedMember(t, w.db, "u3", "otra@example.com")
	w.etStranger = "et-u3-own"
	mustExec(t, w.db, `INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES (?, 'u3', 'taller-u3', 'Taller', 60)`, w.etStranger)
	return w
}

func createWebhookReq(t *testing.T, h *handler.Handler, apiKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, authReq(http.MethodPost, "/v1/webhooks", body, apiKey))
	return rec
}

func patchWebhookReq(t *testing.T, h *handler.Handler, apiKey, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := authReq(http.MethodPatch, "/v1/webhooks/"+id, body, apiKey)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchWebhook)(rec, req)
	return rec
}

// listedWebhookTypes returns event_type_ids per webhook id from GET /v1/webhooks.
func listedWebhookTypes(t *testing.T, h *handler.Handler, apiKey string) map[string][]string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListWebhooks)(rec, authReq(http.MethodGet, "/v1/webhooks", "", apiKey))
	mustStatus(t, rec, http.StatusOK, "list webhooks")
	var body struct {
		Items []struct {
			ID           string    `json:"id"`
			EventTypeIDs *[]string `json:"event_type_ids"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("list webhooks: %v", err)
	}
	out := map[string][]string{}
	for _, it := range body.Items {
		if it.EventTypeIDs == nil {
			t.Fatalf("list webhooks: %s has no event_type_ids (or null); want a list — %s", it.ID, rec.Body.String())
		}
		ids := append([]string{}, (*it.EventTypeIDs)...)
		sort.Strings(ids)
		out[it.ID] = ids
	}
	return out
}

func sorted(ids ...string) []string {
	out := append([]string{}, ids...)
	sort.Strings(out)
	return out
}

func TestWebhookEventTypes_createListPatch(t *testing.T) {
	w := newWebhookTypesWorld(t)

	body := mustCreated(t, createWebhookReq(t, w.h, w.ownerKey, fmt.Sprintf(
		`{"url":"https://example.com/hook","events":["booking.created"],"event_type_ids":[%q,%q]}`, w.etA, w.etA)),
		"create filtered webhook")
	filtered := mustString(t, body, "id", "create filtered webhook")
	if got, _ := body["event_type_ids"].([]any); len(got) != 1 || got[0] != w.etA {
		t.Errorf("create response event_type_ids = %v; want [%s] (repeat dropped)", body["event_type_ids"], w.etA)
	}
	body = mustCreated(t, createWebhookReq(t, w.h, w.ownerKey,
		`{"url":"https://example.com/hook2","events":["booking.created"]}`), "create unfiltered webhook")
	unfiltered := mustString(t, body, "id", "create unfiltered webhook")
	if got, ok := body["event_type_ids"].([]any); !ok || len(got) != 0 {
		t.Errorf("unfiltered create response event_type_ids = %v; want [] (every type)", body["event_type_ids"])
	}

	list := listedWebhookTypes(t, w.h, w.ownerKey)
	if !slices.Equal(list[filtered], []string{w.etA}) || len(list[unfiltered]) != 0 {
		t.Fatalf("list = %v; want %s:[%s] and %s:[]", list, filtered, w.etA, unfiltered)
	}

	steps := []struct {
		what, body string
		status     int
		want       []string
	}{
		{"replace", fmt.Sprintf(`{"event_type_ids":[%q,%q]}`, w.etA, w.etB), http.StatusNoContent, sorted(w.etA, w.etB)},
		{"omitted = unchanged", `{"events":["booking.cancelled"]}`, http.StatusNoContent, sorted(w.etA, w.etB)},
		{"null = unchanged", `{"event_type_ids":null}`, http.StatusNoContent, sorted(w.etA, w.etB)},
		{"unknown id = 400, unchanged", `{"event_type_ids":["no-such-type"]}`, http.StatusBadRequest, sorted(w.etA, w.etB)},
		{"empty id = 400, unchanged", `{"event_type_ids":[""]}`, http.StatusBadRequest, sorted(w.etA, w.etB)},
		{"[] = every type", `{"event_type_ids":[]}`, http.StatusNoContent, []string{}},
	}
	for _, s := range steps {
		mustStatus(t, patchWebhookReq(t, w.h, w.ownerKey, filtered, s.body), s.status, s.what)
		if got := listedWebhookTypes(t, w.h, w.ownerKey)[filtered]; !slices.Equal(got, s.want) {
			t.Errorf("%s: event_type_ids = %v; want %v", s.what, got, s.want)
		}
	}

	// Someone else's webhook: 404, even with a valid list.
	mustStatus(t, patchWebhookReq(t, w.h, whMemberKey, filtered, fmt.Sprintf(`{"event_type_ids":[%q]}`, w.etA)),
		http.StatusNotFound, "patch another user's webhook")
}

func TestWebhookEventTypes_whoMayPickWhich(t *testing.T) {
	w := newWebhookTypesWorld(t)
	create := func(key, what string, ids ...string) *httptest.ResponseRecorder {
		t.Helper()
		idsJSON, _ := json.Marshal(ids)
		return createWebhookReq(t, w.h, key, fmt.Sprintf(
			`{"url":"https://example.com/hook","events":["booking.created"],"event_type_ids":%s}`, idsJSON))
	}

	// A mentor: their own types and the ones they host, nothing else.
	mustCreated(t, create(whMemberKey, "own", w.etMember), "mentor, own type")
	mustCreated(t, create(whMemberKey, "hosted", w.etA), "mentor, hosted type")
	for what, id := range map[string]string{
		"another member's type":   w.etStranger,
		"owner's type not hosted": w.etB,
		"nonexistent":             "no-such-type",
	} {
		rec := create(whMemberKey, what, w.etMember, id)
		mustStatus(t, rec, http.StatusBadRequest, "mentor, "+what)
		if !strings.Contains(rec.Body.String(), id) {
			t.Errorf("mentor, %s: message does not name the id: %s", what, rec.Body.String())
		}
	}
	// The refused requests created nothing.
	if n := len(listedWebhookTypes(t, w.h, whMemberKey)); n != 2 {
		t.Errorf("mentor has %d webhooks; want 2 (refused creates must not leave one behind)", n)
	}

	// The owner may limit to any type of the workspace (their webhooks get every booking).
	mustCreated(t, create(w.ownerKey, "any", w.etStranger, w.etMember, w.etB), "owner, other members' types")
}

func TestListWebhookEventTypes_scopeByRole(t *testing.T) {
	w := newWebhookTypesWorld(t)
	ids := func(key string) []string {
		t.Helper()
		rec := httptest.NewRecorder()
		w.h.RequireAuth(w.h.ListWebhookEventTypes)(rec, authReq(http.MethodGet, "/v1/webhooks/event-types", "", key))
		mustStatus(t, rec, http.StatusOK, "list webhook event types")
		var body struct {
			Items []struct {
				ID, Name string
				Owned    bool `json:"owned"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range body.Items {
			if it.Name == "" {
				t.Errorf("event type %s has no name", it.ID)
			}
			out = append(out, it.ID)
		}
		sort.Strings(out)
		return out
	}
	if got, want := ids(w.ownerKey), sorted(w.etA, w.etB, w.etMember, w.etStranger); !slices.Equal(got, want) {
		t.Errorf("owner sees %v; want every type %v", got, want)
	}
	if got, want := ids(whMemberKey), sorted(w.etA, w.etMember); !slices.Equal(got, want) {
		t.Errorf("mentor sees %v; want own + hosted %v", got, want)
	}
}

// An admin who is not the owner gets the same choice as a mentor: their webhooks receive
// only the bookings they host (webhook.matchingWebhooks widens to is_owner alone), so
// another member's type would be a filter that can never fire.
func TestWebhookEventTypes_adminWhoIsNotOwner(t *testing.T) {
	w := newWebhookTypesWorld(t)
	mustExec(t, w.db, `UPDATE users SET is_admin = 1, is_owner = 0 WHERE id = 'u2'`)

	for what, id := range map[string]string{
		"another member's type":   w.etStranger,
		"owner's type not hosted": w.etB,
	} {
		rec := createWebhookReq(t, w.h, whMemberKey, fmt.Sprintf(
			`{"url":"https://example.com/hook","events":["booking.created"],"event_type_ids":[%q]}`, id))
		mustStatus(t, rec, http.StatusBadRequest, "admin, "+what)
	}
	mustCreated(t, createWebhookReq(t, w.h, whMemberKey, fmt.Sprintf(
		`{"url":"https://example.com/hook","events":["booking.created"],"event_type_ids":[%q,%q]}`, w.etMember, w.etA)),
		"admin, own + hosted types")

	rec := httptest.NewRecorder()
	w.h.RequireAuth(w.h.ListWebhookEventTypes)(rec, authReq(http.MethodGet, "/v1/webhooks/event-types", "", whMemberKey))
	mustStatus(t, rec, http.StatusOK, "admin: list webhook event types")
	var body struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range body.Items {
		got = append(got, it.ID)
	}
	sort.Strings(got)
	if want := sorted(w.etA, w.etMember); !slices.Equal(got, want) {
		t.Errorf("admin (not owner) sees %v; want own + hosted %v", got, want)
	}
}

// A real booking and a real reminder job, through the HTTP booking path: the owner's
// webhook limited to type A fires for A and stays silent for B.
func TestWebhookEventTypes_bookingAndReminderDeliveries(t *testing.T) {
	w := newWebhookTypesWorld(t)
	body := mustCreated(t, createWebhookReq(t, w.h, w.ownerKey, fmt.Sprintf(
		`{"url":"https://example.com/hook","events":["booking.created","booking.reminder_1h"],"event_type_ids":[%q]}`, w.etA)),
		"create filtered webhook")
	whID := mustString(t, body, "id", "create filtered webhook")
	body = mustCreated(t, createWebhookReq(t, w.h, w.ownerKey,
		`{"url":"https://example.com/all","events":["booking.created","booking.reminder_1h"]}`), "create unfiltered webhook")
	allID := mustString(t, body, "id", "create unfiltered webhook")

	events := func(webhookID, bookingID string) []string {
		t.Helper()
		var out []string
		for _, p := range deliveryPayloads(t, w.db, webhookID) {
			if data, _ := p["data"].(map[string]any); data["id"] == bookingID {
				out = append(out, p["event"].(string))
			}
		}
		sort.Strings(out)
		return out
	}

	start := futureAt(10, 15, 0)
	bkA := bookInZone(t, w.h, w.slugA, start, "America/Lima", "")
	bkB := bookInZone(t, w.h, w.slugB, start.Add(2*time.Hour), "America/Lima", "")
	// booking.created is enqueued before the reminders are planned, so once both bookings
	// have their three reminder jobs, both confirmations have been through Enqueue.
	jobsA := waitReminderJobs(t, w.db, bkA, "booking A", func(j map[string]reminderJob) bool { return len(j) == 3 })
	jobsB := waitReminderJobs(t, w.db, bkB, "booking B", func(j map[string]reminderJob) bool { return len(j) == 3 })

	for _, job := range []string{jobsA["1h"].Payload, jobsB["1h"].Payload} {
		if err := w.h.JobWebhookReminder(context.Background(), job); err != nil {
			t.Fatalf("JobWebhookReminder: %v", err)
		}
	}

	both := []string{"booking.created", "booking.reminder_1h"}
	if got := events(whID, bkA); !slices.Equal(got, both) {
		t.Errorf("filtered webhook, type A: events = %v; want %v", got, both)
	}
	if got := events(whID, bkB); len(got) != 0 {
		t.Errorf("filtered webhook, type B: events = %v; want none", got)
	}
	for _, bk := range []string{bkA, bkB} {
		if got := events(allID, bk); !slices.Equal(got, both) {
			t.Errorf("unfiltered webhook, booking %s: events = %v; want %v", bk, got, both)
		}
	}
}
