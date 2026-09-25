package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The browser claim is how a real install gets its first account; that account must be
// the owner, or nobody can ever change roles or archive an admin.
func TestClaim_firstAccountIsTheOwner(t *testing.T) {
	h, database := newTestHandlerDB(t)

	body := `{"name":"Dueña","email":"duena@example.com","password":"una-clave-larga","timezone":"America/Lima"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Claim(rec, req)
	if rec.Code >= 300 {
		t.Fatalf("claim: got %d — %s", rec.Code, rec.Body.String())
	}

	var isOwner, isAdmin int
	if err := database.QueryRow(`SELECT is_owner, is_admin FROM users WHERE email = 'duena@example.com'`).
		Scan(&isOwner, &isAdmin); err != nil {
		t.Fatal(err)
	}
	if isOwner != 1 || isAdmin != 1 {
		t.Errorf("claimed account: is_owner=%d is_admin=%d, want 1 and 1", isOwner, isAdmin)
	}
}

// An install claimed before that fix has admins but no owner: the earliest active admin
// is promoted once, and the repair never creates a second owner afterwards.
func TestEnsureWorkspaceOwner_promotesEarliestAdminOnce(t *testing.T) {
	h, database := newTestHandlerDB(t)
	for _, q := range []string{
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,created_at) VALUES ('a0','archivado@example.com','Old','UTC',1,'2026-01-01T00:00:00Z')`,
		`UPDATE users SET archived_at = '2026-02-01T00:00:00Z' WHERE id = 'a0'`,
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,created_at) VALUES ('a1','primera@example.com','First','UTC',1,'2026-03-01T00:00:00Z')`,
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,created_at) VALUES ('a2','segunda@example.com','Second','UTC',1,'2026-04-01T00:00:00Z')`,
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,created_at) VALUES ('m1','miembro@example.com','Member','UTC',0,'2025-01-01T00:00:00Z')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	got, err := h.EnsureWorkspaceOwner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "primera@example.com" {
		t.Fatalf("promoted %q, want the earliest active admin primera@example.com", got)
	}

	again, err := h.EnsureWorkspaceOwner(context.Background())
	if err != nil || again != "" {
		t.Fatalf("second run promoted %q (err %v); must be a no-op once an owner exists", again, err)
	}
	var owners int
	database.QueryRow(`SELECT COUNT(*) FROM users WHERE is_owner = 1`).Scan(&owners)
	if owners != 1 {
		t.Errorf("owners = %d, want exactly 1", owners)
	}
}

// A workspace that already has an owner is left exactly as it is.
func TestEnsureWorkspaceOwner_leavesAnExistingOwnerAlone(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t) // Setup creates the owner
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,created_at) VALUES ('early','temprano@example.com','Early','UTC',1,'2000-01-01T00:00:00Z')`)

	got, err := h.EnsureWorkspaceOwner(context.Background())
	if err != nil || got != "" {
		t.Fatalf("promoted %q (err %v) although an owner exists", got, err)
	}
}

// Until the repair runs, the privacy page still names a contact: the earliest admin.
func TestPrivacyPage_fallsBackToTheEarliestAdmin(t *testing.T) {
	h, database := newTestHandlerDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('a1','admin@example.com','Admin','UTC',1)`)

	rec := httptest.NewRecorder()
	h.PrivacyPage(rec, httptest.NewRequest(http.MethodGet, "/privacidad", nil))
	if !strings.Contains(rec.Body.String(), "mailto:admin@example.com") {
		t.Error("privacy page has no contact when the workspace has admins but no owner")
	}
}
