package handler_test

// The support tier (is_support = 1, is_admin = 0) sits between member and admin:
// it sees every booking, cancels and reschedules any of them, and can look up
// members — and it must never reach a settings screen, a secret, or a role.
//
// The first test is deliberately paranoid about the flags themselves. RequireAuth
// loads them positionally (a wide SELECT scanned into a wide pointer list), so a
// single column inserted on one side and not the other would land is_support in
// IsAdmin and hand every support user the whole workspace. Asserting the three
// flags AND the derived role catches that the moment it happens, which counting
// columns by eye does not.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSupportTier_flagsSurviveAuthAndWallHolds(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	supportKey := "support-tier-key"
	if _, err := database.Exec(
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner,is_support) VALUES ('sup','s@example.com','Sup','UTC',0,0,1)`); err != nil {
		t.Fatalf("insert support user: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('ksup','sup','t',?,'2024-01-01')`,
		sha256HexForTest(supportKey)); err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	// The flags survive the SELECT/Scan in RequireAuth: support, and NOT admin.
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetMe)(rec, authReq(http.MethodGet, "/v1/users/me", "", supportKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("GetMe: got %d — %s", rec.Code, rec.Body.String())
	}
	var me map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode /users/me: %v", err)
	}
	if me["is_support"] != true || me["is_admin"] != false || me["is_owner"] != false || me["role"] != "support" {
		t.Fatalf("support user mis-scanned — is_support=%v is_admin=%v is_owner=%v role=%v",
			me["is_support"], me["is_admin"], me["is_owner"], me["role"])
	}

	// requireAdmin is the whole wall: every settings surface must 403.
	for name, fn := range map[string]http.HandlerFunc{
		"email":     h.GetEmailSettings,
		"google":    h.GetGoogleSettings,
		"zoom":      h.GetZoomSettings,
		"stripe":    h.GetStripeSettings,
		"ai":        h.GetLLMSettings,
		"livekit":   h.GetLiveKitSettings,
		"storage":   h.GetStorageSettings,
		"tracking":  h.GetTrackingSettings,
		"notetaker": h.GetNotetakerSettings,
	} {
		r := httptest.NewRecorder()
		h.RequireAuth(fn)(r, authReq(http.MethodGet, "/v1/settings/"+name, "", supportKey))
		if r.Code != http.StatusForbidden {
			t.Errorf("settings/%s as support: got %d, want 403 — %s", name, r.Code, r.Body.String())
		}
	}

	// Roles and ownership stay owner-only.
	for name, fn := range map[string]http.HandlerFunc{"role": h.SetUserRole, "transfer-ownership": h.TransferOwnership} {
		r := httptest.NewRecorder()
		req := authReq(http.MethodPatch, "/v1/users/x/"+name, `{"role":"admin"}`, supportKey)
		req.SetPathValue("id", "x")
		h.RequireAuth(fn)(r, req)
		if r.Code != http.StatusForbidden {
			t.Errorf("%s as support: got %d, want 403 — %s", name, r.Code, r.Body.String())
		}
	}

	// The member lookup support needs, reporting the tier truthfully.
	r := httptest.NewRecorder()
	h.RequireAuth(h.ListUsers)(r, authReq(http.MethodGet, "/v1/users", "", supportKey))
	if r.Code != http.StatusOK {
		t.Fatalf("list users as support: got %d — %s", r.Code, r.Body.String())
	}
	var users []map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode /users: %v", err)
	}
	found := false
	for _, u := range users {
		if u["id"] == "sup" {
			found = true
			if u["role"] != "support" || u["is_support"] != true || u["is_admin"] != false {
				t.Errorf("support row misreported in the members list: %v", u)
			}
		}
	}
	if !found {
		t.Error("support user missing from the members list")
	}

	// Workspace-wide booking scope: opt-in, and granted.
	r = httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(r, authReq(http.MethodGet, "/v1/bookings?scope=all", "", supportKey))
	if r.Code != http.StatusOK {
		t.Errorf("bookings scope=all as support: got %d — %s", r.Code, r.Body.String())
	}
}

func TestSupportTier_reschedulesAnyBookingButMemberCannot(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	for _, u := range []struct {
		id, key string
		support int
	}{{"sup", "support-resched-key", 1}, {"mem", "member-resched-key", 0}} {
		if _, err := database.Exec(
			`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner,is_support) VALUES (?,?,?,'UTC',0,0,?)`,
			u.id, u.id+"@example.com", u.id, u.support); err != nil {
			t.Fatalf("insert %s: %v", u.id, err)
		}
		if _, err := database.Exec(
			`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES (?,?,'t',?,'2024-01-01')`,
			"k"+u.id, u.id, sha256HexForTest(u.key)); err != nil {
			t.Fatalf("insert key for %s: %v", u.id, err)
		}
	}

	slug, _ := seedEventTypeHTTP(t, h, ownerKey)
	bookingID := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))

	// A plain member may not move a booking they do not host.
	if rec := patchReschedule(t, h, bookingID, futureAt(11, 9, 0).Format(time.RFC3339), "member-resched-key"); rec.Code != http.StatusNotFound {
		t.Errorf("member rescheduling another host's booking: got %d, want 404 — %s", rec.Code, rec.Body.String())
	}

	// Support may — that is the job.
	newStart := futureAt(11, 14, 0)
	if rec := patchReschedule(t, h, bookingID, newStart.Format(time.RFC3339), "support-resched-key"); rec.Code != http.StatusOK {
		t.Fatalf("support rescheduling a member's booking: got %d, want 200 — %s", rec.Code, rec.Body.String())
	}
	var dbStart, hostID string
	database.QueryRow(`SELECT start_at, host_id FROM bookings WHERE id = ?`, bookingID).Scan(&dbStart, &hostID)
	if !strings.Contains(dbStart, newStart.Format("2006-01-02T15:04:05")) {
		t.Errorf("booking not moved: start_at = %q", dbStart)
	}
	// Helping must not become taking: the host is unchanged.
	if hostID == "sup" {
		t.Error("support reschedule reassigned the booking's host")
	}
}
