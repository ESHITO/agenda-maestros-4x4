package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// Fork: the booking page and the embed widget's /public payload carry the server's clock
// choice (Locale.Uses12h, internal/i18n/fork_clock.go). With FORCE_LOCALE=es and the fork
// default, that is the 12-hour clock: "5:00 p. m." on every slot, label and confirmation.
func TestBookPageAndPublicPayload_carryTheTwelveHourClock(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	h.SetForceLocale("es")

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("book page: %d — %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !regexp.MustCompile(`const HOUR12\s*=\s*true\s*;`).MatchString(body) { // html/template pads it: " true "
		t.Error("book page: HOUR12 is not true")
	}
	if !strings.Contains(body, "BookingLogic.formatTime(iso, TZ, LOCALE, HOUR12)") {
		t.Error("book page: fmtTime does not pass HOUR12 on")
	}

	preq := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/public", nil)
	preq.SetPathValue("slug", slug)
	prec := httptest.NewRecorder()
	h.PublicEventType(prec, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("public: %d — %s", prec.Code, prec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(prec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["hour12"] != true {
		t.Errorf("public payload hour12 = %v; want true (the widget's clock)", resp["hour12"])
	}
}
