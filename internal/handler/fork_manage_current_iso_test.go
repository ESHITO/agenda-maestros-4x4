package handler_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Fork (Agenda Maestros 4x4): CURRENT_ISO on the manage page is written the way /slots
// writes slot starts (RFC3339 in the attendee's zone), so the booking's own slot can be
// recognised. It used to be the UTC "...Z" form while slots carry an offset, and the page
// compared the two as strings, so the booking's own time was never excluded (it showed as
// a struck "taken" time on show_taken_slots types). The page now compares instants; this
// guards the server half: same instant, same wire form as a slot start in that zone.
func TestManagePage_currentStartUsesTheSlotWireForm(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	if _, err := database.Exec(`UPDATE booking_attendees SET iana_timezone = 'Europe/Madrid' WHERE booking_id = ?`, bookingID); err != nil {
		t.Fatal(err)
	}
	tok := issueTestToken(t, database, bookingID)

	req := httptest.NewRequest(http.MethodGet, "/manage/"+tok, nil)
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.ManagePage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	m := regexp.MustCompile(`const CURRENT_ISO = "([^"]*)"`).FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("CURRENT_ISO not found in the page")
	}
	got := strings.ReplaceAll(m[1], `+`, "+") // html/template's JS-string escape of "+"
	if got != "2026-06-20T11:00:00+02:00" {
		t.Errorf("CURRENT_ISO = %q; want the slot form in Europe/Madrid, 2026-06-20T11:00:00+02:00", got)
	}
	ts, err := time.Parse(time.RFC3339, got)
	if err != nil || !ts.Equal(time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("CURRENT_ISO %q is not the booking's instant (%v)", got, err)
	}
}
