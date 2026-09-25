package handler_test

// The fork's is_support "desk" tier is RETIRED. It used to see, cancel and reschedule
// every booking and read the directory - the opposite of what the owner's support staff
// is (they attend the Soporte type and see only their own sessions; that is an área now,
// fork_team.go). The column and the positional scans stay, so these tests pin that the
// flag grants NOTHING: a user still carrying is_support = 1 is a plain member everywhere,
// SetUserRole refuses to write it, and the boot repair clears it into the área soporte.
//
// RequireAuth still loads the flags positionally (a wide SELECT scanned into a wide
// pointer list), so a column inserted on one side and not the other would land is_support
// in IsAdmin and hand that user the whole workspace. Asserting is_admin/is_owner/role
// through GetMe catches that the moment it happens.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSupportTier_grantsNothing(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	supportKey := "support-tier-key"
	mustExec(t, database,
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner,is_support) VALUES ('sup','s@example.com','Sup','UTC',0,0,1)`)
	mustExec(t, database,
		`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('ksup','sup','t',?,'2024-01-01')`,
		sha256HexForTest(supportKey))

	// The flags survive the SELECT/Scan in RequireAuth, and the role is plain member.
	me := mustJSON(t, func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.RequireAuth(h.GetMe)(rec, authReq(http.MethodGet, "/v1/users/me", "", supportKey))
		return rec
	}(), http.StatusOK, "GetMe")
	if me["is_admin"] != false || me["is_owner"] != false || me["role"] != "member" {
		t.Fatalf("support user must read as a plain member — is_admin=%v is_owner=%v role=%v",
			me["is_admin"], me["is_owner"], me["role"])
	}
	if _, ok := me["is_support"]; ok {
		t.Errorf("/users/me still reports is_support: %v", me)
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

	// The directory is admin-only again.
	r := httptest.NewRecorder()
	h.RequireAuth(h.ListUsers)(r, authReq(http.MethodGet, "/v1/users", "", supportKey))
	if r.Code != http.StatusForbidden {
		t.Errorf("list users as support: got %d, want 403 — %s", r.Code, r.Body.String())
	}

	// ?scope=all no longer widens: the owner's booking is not in support's list.
	slug, _ := seedEventTypeHTTP(t, h, ownerKey)
	bookingID := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))
	if got := listBookings(t, h, supportKey, "?scope=all"); len(got.Items) != 0 {
		t.Errorf("bookings scope=all as support: %d items, want 0 (only their own)", len(got.Items))
	}

	// Neither cancel nor reschedule reaches someone else's booking.
	rec := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/v1/bookings/"+bookingID+"/cancel", `{"reason":"x"}`, supportKey)
	req.SetPathValue("id", bookingID)
	h.RequireAuth(h.CancelBooking)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("support cancelling the owner's booking: got %d, want 404 — %s", rec.Code, rec.Body.String())
	}
	if rec := patchReschedule(t, h, bookingID, futureAt(11, 14, 0).Format(time.RFC3339), supportKey); rec.Code != http.StatusNotFound {
		t.Errorf("support rescheduling the owner's booking: got %d, want 404 — %s", rec.Code, rec.Body.String())
	}
	var status string
	database.QueryRow(`SELECT status FROM bookings WHERE id = ?`, bookingID).Scan(&status)
	if status != "confirmed" {
		t.Errorf("booking status = %q; support must not have touched it", status)
	}
}

func TestSetUserRole_refusesSupportAndNeverWritesIt(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_admin,is_support) VALUES ('m1','m1@example.com','M1','UTC',0,1)`)

	rec := httptest.NewRecorder()
	req := authReq(http.MethodPatch, "/v1/users/m1/role", `{"role":"support"}`, ownerKey)
	req.SetPathValue("id", "m1")
	h.RequireAuth(h.SetUserRole)(rec, req)
	body := mustJSON(t, rec, http.StatusBadRequest, "role support")
	if msg, _ := body["error"].(string); !strings.Contains(msg, "área Soporte") {
		t.Errorf("error = %q; want the Spanish hint to use the área", msg)
	}

	// admin -> member clears the retired flag too.
	for _, role := range []string{"admin", "member"} {
		rec := httptest.NewRecorder()
		req := authReq(http.MethodPatch, "/v1/users/m1/role", `{"role":"`+role+`"}`, ownerKey)
		req.SetPathValue("id", "m1")
		h.RequireAuth(h.SetUserRole)(rec, req)
		mustStatus(t, rec, http.StatusOK, "role "+role)
	}
	var isSupport int
	database.QueryRow(`SELECT is_support FROM users WHERE id = 'm1'`).Scan(&isSupport)
	if isSupport != 0 {
		t.Errorf("is_support = %d after role changes; want 0", isSupport)
	}
}

func TestRetireSupportTier_movesFlagToAreaIdempotently(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_support) VALUES ('s1','s1@example.com','S1','UTC',1)`)
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_support) VALUES ('s2','s2@example.com','S2','UTC',1)`)
	// s2 already attends Mentoría: the repair keeps an existing área.
	mustExec(t, database, `INSERT INTO fork_member_areas (user_id, area, updated_at) VALUES ('s2','mentoria','x')`)

	for pass := 1; pass <= 2; pass++ {
		n, err := h.RetireSupportTier(context.Background())
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if want := map[int]int{1: 2, 2: 0}[pass]; n != want {
			t.Errorf("pass %d: changed %d users, want %d", pass, n, want)
		}
	}
	var flagged int
	database.QueryRow(`SELECT COUNT(*) FROM users WHERE is_support = 1`).Scan(&flagged)
	if flagged != 0 {
		t.Errorf("%d users still flagged", flagged)
	}
	for id, want := range map[string]string{"s1": "soporte", "s2": "mentoria"} {
		var area string
		database.QueryRow(`SELECT area FROM fork_member_areas WHERE user_id = ?`, id).Scan(&area)
		if area != want {
			t.Errorf("%s área = %q, want %q", id, area, want)
		}
	}
}
