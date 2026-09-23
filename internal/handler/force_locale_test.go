package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FORCE_LOCALE exists for an audience that shares one language but not one browser
// setting: Spanish-speaking members abroad, some of them older and on borrowed computers
// set to English. Every signal a visitor's browser sends must lose to it.
func TestBookPage_forceLocaleBeatsEveryVisitorSignal(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	if !h.SetForceLocale("es") {
		t.Fatal("SetForceLocale(\"es\") refused a supported locale")
	}

	cases := map[string]func(*http.Request){
		"english browser": func(r *http.Request) { r.Header.Set("Accept-Language", "en-US,en;q=0.9") },
		"?lang= switch":   func(r *http.Request) { r.URL.RawQuery = "lang=pt" },
		"language cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "calnode_lang", Value: "fr"}) },
	}
	for name, signal := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
			req.SetPathValue("slug", slug)
			signal(req)
			rec := httptest.NewRecorder()
			h.BookPage(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("book page: %d — %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, `<html lang="es">`) {
				t.Errorf("page is not in Spanish despite FORCE_LOCALE=es")
			}
			// A switcher that changes nothing would only confuse; it must be gone.
			if strings.Contains(body, `id="lang-select"`) {
				t.Errorf("language switcher still rendered with FORCE_LOCALE on")
			}
		})
	}
}

// Without FORCE_LOCALE nothing changes: the visitor's browser still decides and the
// switcher is still offered — the setting is opt-in, not a new default.
func TestBookPage_noForceLocaleKeepsPerVisitorResolution(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	req.Header.Set("Accept-Language", "pt")
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `<html lang="pt">`) {
		t.Errorf("without FORCE_LOCALE a Portuguese browser should get Portuguese")
	}
	if !strings.Contains(body, `id="lang-select"`) {
		t.Errorf("without FORCE_LOCALE the language switcher should still be offered")
	}
}

// A typo in the env var must not blank the pages: an unsupported code is refused and the
// stock per-visitor behaviour stays on.
func TestSetForceLocale_refusesUnsupportedCode(t *testing.T) {
	h, _, _ := setupWorkspace(t)
	for _, code := range []string{"", "xx", "espanol", "ES "} {
		if h.SetForceLocale(code) {
			t.Errorf("SetForceLocale(%q) accepted an unsupported code", code)
		}
	}
}
