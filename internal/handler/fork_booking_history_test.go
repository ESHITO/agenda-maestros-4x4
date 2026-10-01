package handler_test

// Fork (Agenda Maestros 4x4): GET /v1/bookings?when=history (the Pasadas tab), the tabs'
// counts, the history's sub-filters, and the reschedule history written by every path.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type historyItem struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Rescheduled *struct {
		Count               int    `json:"count"`
		LastPreviousStartAt string `json:"last_previous_start_at"`
	} `json:"rescheduled"`
}

type historyResp struct {
	Items  []historyItem `json:"items"`
	Total  int           `json:"total"`
	Counts struct {
		Upcoming int `json:"upcoming"`
		Past     int `json:"past"`
	} `json:"counts"`
}

func listHistory(t *testing.T, h interface {
	RequireAuth(http.HandlerFunc) http.HandlerFunc
	ListBookings(http.ResponseWriter, *http.Request)
}, key, query string) historyResp {
	t.Helper()
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(rec, authReq(http.MethodGet, "/v1/bookings"+query, "", key))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/bookings%s: %d - %s", query, rec.Code, rec.Body.String())
	}
	var out historyResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func historyIDs(r historyResp) []string {
	out := make([]string, len(r.Items))
	for i, it := range r.Items {
		out[i] = it.ID
	}
	return out
}

func rfc(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }

func setUpdatedAt(t *testing.T, db *sql.DB, id string, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`UPDATE bookings SET updated_at = ? WHERE id = ?`, at.UTC().Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}
}

func TestListBookings_historyTab(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	_, etID := seedEventTypeHTTP(t, h, key)
	now := time.Now().UTC()
	at := func(d time.Duration) string { return rfc(now.Add(d)) }

	seedBooking(t, database, "ended-recent", etID, ownerID, at(-3*time.Hour), at(-2*time.Hour), "confirmed")
	seedBooking(t, database, "ended-old", etID, ownerID, at(-50*time.Hour), at(-49*time.Hour), "confirmed")
	seedBooking(t, database, "cancelled-future", etID, ownerID, at(72*time.Hour), at(73*time.Hour), "cancelled")
	setUpdatedAt(t, database, "cancelled-future", now.Add(-1*time.Hour)) // cancelled an hour ago
	seedBooking(t, database, "cancelled-past", etID, ownerID, at(-100*time.Hour), at(-99*time.Hour), "cancelled")
	setUpdatedAt(t, database, "cancelled-past", now.Add(-200*time.Hour))
	seedBooking(t, database, "in-progress", etID, ownerID, at(-30*time.Minute), at(30*time.Minute), "confirmed")
	seedBooking(t, database, "tomorrow", etID, ownerID, at(24*time.Hour), at(25*time.Hour), "confirmed")
	if _, err := database.Exec(`INSERT INTO fork_booking_reschedules (id, booking_id, previous_start_at, new_start_at, rescheduled_at)
		VALUES ('m1', 'ended-recent', ?, ?, ?), ('m2', 'ended-recent', ?, ?, ?)`,
		at(-27*time.Hour), at(-26*time.Hour), rfc(now.Add(-48*time.Hour)),
		at(-26*time.Hour), at(-3*time.Hour), rfc(now.Add(-40*time.Hour))); err != nil {
		t.Fatal(err)
	}

	hist := listHistory(t, h, key, "?when=history&order=desc")
	wantHist := []string{"cancelled-future", "ended-recent", "ended-old", "cancelled-past"}
	if !slices.Equal(historyIDs(hist), wantHist) {
		t.Errorf("history = %v; want %v", historyIDs(hist), wantHist)
	}
	if hist.Counts.Upcoming != 2 || hist.Counts.Past != 4 {
		t.Errorf("history counts = %+v; want upcoming 2, past 4", hist.Counts)
	}

	up := listHistory(t, h, key, "?when=upcoming&order=asc&tabs=1")
	if !slices.Equal(historyIDs(up), []string{"in-progress", "tomorrow"}) {
		t.Errorf("upcoming = %v; want [in-progress tomorrow]", historyIDs(up))
	}
	if up.Counts.Upcoming != 2 || up.Counts.Past != 4 {
		t.Errorf("upcoming counts = %+v; want the same 2/4 labels on both tabs", up.Counts)
	}

	// Without tabs=1 (an API-key integration), when=upcoming keeps upstream's counts and
	// total: non-cancelled only, split by end_at (ended-recent, ended-old on the past side).
	plain := listHistory(t, h, key, "?when=upcoming&order=asc")
	if !slices.Equal(historyIDs(plain), []string{"in-progress", "tomorrow"}) {
		t.Errorf("upcoming (no tabs) = %v; want [in-progress tomorrow]", historyIDs(plain))
	}
	if plain.Counts.Upcoming != 2 || plain.Counts.Past != 2 || plain.Total != 4 {
		t.Errorf("upcoming (no tabs) counts = %+v total %d; want upstream's 2/2, total 4", plain.Counts, plain.Total)
	}
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(rec, authReq(http.MethodGet, "/v1/bookings?when=upcoming&tabs=x", "", key))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("tabs=x: %d; want 400", rec.Code)
	}

	// The history's sub-filters, and the tab in view counts what it lists.
	for q, want := range map[string][]string{
		"?when=history&status=cancelled": {"cancelled-future", "cancelled-past"},
		"?when=history&status=confirmed": {"ended-recent", "ended-old"},
		"?when=history&rescheduled=1":    {"ended-recent"},
	} {
		r := listHistory(t, h, key, q)
		if !slices.Equal(historyIDs(r), want) {
			t.Errorf("%s = %v; want %v", q, historyIDs(r), want)
		}
		if r.Counts.Past != len(want) || r.Counts.Upcoming != 2 {
			t.Errorf("%s counts = %+v; want past %d, upcoming 2", q, r.Counts, len(want))
		}
	}

	// Items carry the reschedule history: count and the start the last move left.
	for _, it := range hist.Items {
		switch it.ID {
		case "ended-recent":
			if it.Rescheduled == nil || it.Rescheduled.Count != 2 || it.Rescheduled.LastPreviousStartAt != at(-26*time.Hour) {
				t.Errorf("ended-recent rescheduled = %+v; want 2 moves, last left %s", it.Rescheduled, at(-26*time.Hour))
			}
		default:
			if it.Rescheduled != nil {
				t.Errorf("%s rescheduled = %+v; want absent (never moved)", it.ID, it.Rescheduled)
			}
		}
	}

	// Upstream's past (no cancelled) and its counts are unchanged.
	past := listHistory(t, h, key, "?when=past&order=desc")
	if !slices.Equal(historyIDs(past), []string{"ended-recent", "ended-old"}) {
		t.Errorf("past = %v; want upstream's [ended-recent ended-old]", historyIDs(past))
	}
	if past.Counts.Upcoming != 2 || past.Counts.Past != 2 {
		t.Errorf("past counts = %+v; want upstream's 2/2", past.Counts)
	}

	// Bad values are 400s.
	for _, q := range []string{"?when=later", "?when=history&rescheduled=maybe"} {
		rec := httptest.NewRecorder()
		h.RequireAuth(h.ListBookings)(rec, authReq(http.MethodGet, "/v1/bookings"+q, "", key))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d; want 400", q, rec.Code)
		}
	}

	// The public GET /v1/bookings/{id} keeps upstream's keys.
	req := httptest.NewRequest(http.MethodGet, "/v1/bookings/ended-recent", nil)
	req.SetPathValue("id", "ended-recent")
	pub := httptest.NewRecorder()
	h.GetBooking(pub, req)
	if pub.Code != http.StatusOK {
		t.Fatalf("public GET: %d - %s", pub.Code, pub.Body.String())
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(pub.Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["rescheduled"]; ok {
		t.Errorf("public booking carries rescheduled: %s", pub.Body.String())
	}
}

// historyActors returns the actors of a booking's history rows, oldest first.
func historyActors(t *testing.T, db *sql.DB, bookingID string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT COALESCE(actor, '') FROM fork_booking_reschedules WHERE booking_id = ? ORDER BY julianday(rescheduled_at), rowid`, bookingID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

// Every reschedule path writes one history row: the panel, the client's /manage page, MCP.
func TestReschedule_everyPathRecordsHistory(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	t.Run("panel", func(t *testing.T) {
		id := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))
		if rec := patchReschedule(t, h, id, futureAt(11, 14, 0).Format(time.RFC3339), key); rec.Code != http.StatusOK {
			t.Fatalf("reschedule: %d - %s", rec.Code, rec.Body.String())
		}
		if got := historyActors(t, database, id); !slices.Equal(got, []string{"panel:" + ownerID}) {
			t.Errorf("actors = %v; want [panel:%s]", got, ownerID)
		}
		var prev, next string
		if err := database.QueryRow(`SELECT previous_start_at, new_start_at FROM fork_booking_reschedules WHERE booking_id = ?`, id).Scan(&prev, &next); err != nil {
			t.Fatal(err)
		}
		if prev != rfc(futureAt(10, 10, 0)) || next != rfc(futureAt(11, 14, 0)) {
			t.Errorf("row = %s -> %s; want %s -> %s", prev, next, rfc(futureAt(10, 10, 0)), rfc(futureAt(11, 14, 0)))
		}
	})

	t.Run("manage", func(t *testing.T) {
		id := createBookingViaHTTP(t, h, slug, futureAt(12, 9, 0).Format(time.RFC3339))
		tok := issueTestToken(t, database, id)
		body := `{"start_at":"` + futureAt(12, 11, 0).Format(time.RFC3339) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/reschedule", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("token", tok)
		rec := httptest.NewRecorder()
		h.RescheduleByToken(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("manage reschedule: %d - %s", rec.Code, rec.Body.String())
		}
		if got := historyActors(t, database, id); !slices.Equal(got, []string{"client"}) {
			t.Errorf("actors = %v; want [client]", got)
		}
	})

	t.Run("mcp", func(t *testing.T) {
		id := createBookingViaHTTP(t, h, slug, futureAt(13, 9, 0).Format(time.RFC3339))
		cs := connectMCP(t, h)
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "reschedule_booking", Arguments: map[string]any{
			"booking_id":     id,
			"new_slot_start": futureAt(13, 15, 0).Format(time.RFC3339),
		}})
		if err != nil || res.IsError {
			t.Fatalf("reschedule_booking: %v %+v", err, res)
		}
		if got := historyActors(t, database, id); !slices.Equal(got, []string{"mcp"}) {
			t.Errorf("actors = %v; want [mcp] (the local operator)", got)
		}
	})

	// And the list shows it on the item.
	r := listHistory(t, h, key, "?when=upcoming")
	moved := 0
	for _, it := range r.Items {
		if it.Rescheduled != nil && it.Rescheduled.Count == 1 {
			moved++
		}
	}
	if moved != 3 {
		got := make([]string, 0, len(r.Items))
		for _, it := range r.Items {
			got = append(got, it.ID)
		}
		sort.Strings(got)
		t.Errorf("items with one move = %d of %v; want 3", moved, got)
	}
}
