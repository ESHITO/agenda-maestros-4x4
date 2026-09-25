package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Google's OAuth review reads this page: it must be public, carry the Limited Use
// statement verbatim with a link to the policy, and name a way to reach the operator.
func TestPrivacyPage_isPublicAndCarriesGoogleLimitedUse(t *testing.T) {
	h, _, _ := setupWorkspace(t)

	req := httptest.NewRequest(http.MethodGet, "/privacidad", nil)
	rec := httptest.NewRecorder()
	h.PrivacyPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("privacy page: got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP = %q, want a locked-down policy (the page runs no script)", csp)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<html lang="es">`,
		"requisitos de Uso Limitado",
		"https://developers.google.com/terms/api-services-user-data-policy",
		"https://myaccount.google.com/permissions",
		"mailto:test@example.com", // the owner created by setupWorkspace is the contact
		"Club Maestros 4x4",       // fallback name while no business name is set
	} {
		if !strings.Contains(body, want) {
			t.Errorf("privacy page is missing %q", want)
		}
	}
}

// Without an owner row the page still renders; it just leaves out the contact lines
// instead of printing an empty mailto link.
func TestPrivacyPage_rendersWithoutAnOwner(t *testing.T) {
	h := newTestHandler(t)

	rec := httptest.NewRecorder()
	h.PrivacyPage(rec, httptest.NewRequest(http.MethodGet, "/privacidad", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("privacy page: got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `href="mailto:"`) {
		t.Error("an empty mailto link was rendered when no owner exists")
	}
}
