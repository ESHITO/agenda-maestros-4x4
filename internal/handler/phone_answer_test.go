package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An OPTIONAL phone answer reaches the webhook the WhatsApp automations dial, and
// POST /v1/bookings is public: the forms only ever send "" or "+<code> <digits>", but a
// direct caller (or an MCP model) can send anything. A non-empty value that is not a phone
// number is dropped like no answer; a usable number and the forms' "" are stored as sent.
func TestCreateBooking_optionalPhoneAnswerKeepsOnlyNumbers(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	qID := createQuestion(t, h, slug, key, `{"label":"WhatsApp","type":"phone","required":false}`)

	for i, tc := range []struct {
		value  string
		stored bool
		want   string
	}{
		{"+51 987654321", true, "+51 987654321"},
		{"  +34 612345678 ", true, "+34 612345678"},
		{"", true, ""}, // book.html sends "" for every unanswered question
		{"+51", false, ""},
		{"https://evil.example/?to=1234567", false, ""},
		{"+51 987654321&text=hola", false, ""},
		{"<script>alert(1)</script> 123456", false, ""},
	} {
		start := fmt.Sprintf("2026-06-%02dT10:00:00Z", 10+i)
		body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":"Ana","email":"ana@example.com","answers":[{"question_id":%q,"value":%q}]}`,
			slug, start, qID, tc.value)
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("%q: status %d - %s; an optional phone answer must never fail the booking", tc.value, rec.Code, rec.Body.String())
		}
		var resp struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.ID == "" {
			t.Fatalf("%q: no booking id in %s", tc.value, rec.Body.String())
		}

		var got []string
		rows, err := database.QueryContext(context.Background(),
			`SELECT value FROM booking_answers WHERE booking_id = ? AND question_id = ?`, resp.ID, qID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		rows.Close()

		switch {
		case !tc.stored && len(got) != 0:
			t.Errorf("%q: stored %q; a value that is not a phone number must be dropped", tc.value, got)
		case tc.stored && (len(got) != 1 || got[0] != tc.want):
			t.Errorf("%q: stored %q; want [%q]", tc.value, got, tc.want)
		}
	}
}
