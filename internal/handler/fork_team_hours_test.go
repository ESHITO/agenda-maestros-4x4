package handler_test

// Fork (Agenda Maestros 4x4): only área-soporte people with weekly hours rotate on the
// Soporte shared type (fork_team_hours.go). The production incident this pins: the owner
// put an admin with NO availability rules in área soporte, S's rotation became [that
// admin], and the public Soporte page went from 319 slots to 0.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// soporteSlots counts S's public slots over the next week (the owner has full hours).
func (f *teamFixture) soporteSlots() int {
	f.t.Helper()
	from := time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")
	to := time.Now().UTC().AddDate(0, 0, 8).Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+f.sSlug+"/slots?from="+from+"&to="+to+"&tz=UTC", nil)
	req.SetPathValue("slug", f.sSlug)
	rec := httptest.NewRecorder()
	f.h.GetSlots(rec, req)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("slots: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Slots []any `json:"slots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		f.t.Fatal(err)
	}
	return len(resp.Slots)
}

// soporteWaiting is GET /v1/team/settings' soporte_shared.waiting, as ids.
func (f *teamFixture) soporteWaiting() []string {
	f.t.Helper()
	body := mustJSON(f.t, f.call(f.h.GetTeamSettings, http.MethodGet, "/v1/team/settings", "", f.ownerKey), http.StatusOK, "GET team settings")
	sup, _ := body["soporte_shared"].(map[string]any)
	if sup == nil {
		f.t.Fatalf("no soporte_shared in %v", body)
	}
	raw, ok := sup["waiting"].([]any)
	if !ok {
		f.t.Fatalf("soporte_shared.waiting missing or not a list: %v", sup)
	}
	out := []string{}
	for _, p := range raw {
		out = append(out, p.(map[string]any)["id"].(string))
	}
	return out
}

// hasAvailability is GET /v1/users' has_availability for userID.
func (f *teamFixture) hasAvailability(userID string) bool {
	f.t.Helper()
	rec := f.call(f.h.ListUsers, http.MethodGet, "/v1/users", "", f.ownerKey)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("list users: %d %s", rec.Code, rec.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		f.t.Fatal(err)
	}
	for _, r := range rows {
		if r["id"] == userID {
			v, ok := r["has_availability"].(bool)
			if !ok {
				f.t.Fatalf("has_availability missing for %s: %v", userID, r)
			}
			return v
		}
	}
	f.t.Fatalf("user %s not listed", userID)
	return false
}

// addRule posts a weekly rule as the user behind key, the way server.go wires the route.
func (f *teamFixture) addRule(key, body string) string {
	f.t.Helper()
	rec := f.call(f.h.TeamReconcileAfterCaller(f.h.CreateAvailabilityRule), http.MethodPost, "/v1/availability-rules", body, key)
	got := mustJSON(f.t, rec, http.StatusCreated, "POST availability rule "+body)
	return got["id"].(string)
}

func (f *teamFixture) deleteRule(key, id string) {
	f.t.Helper()
	mustStatus(f.t, f.call(f.h.TeamReconcileAfterCaller(f.h.DeleteAvailabilityRule), http.MethodDelete,
		"/v1/availability-rules/"+id, "", key, "id", id), http.StatusNoContent, "DELETE availability rule")
}

func TestTeamSoporte_staffWithoutHoursWaitOutsideTheRotation(t *testing.T) {
	f := newTeamFixture(t)
	f.mustSettings(`{"soporte_shared_id":"` + f.sID + `"}`)
	owner := []string{f.ownerID + ":required:0"}
	routing := func() string { return f.scalar(`SELECT routing_mode FROM event_types WHERE id = ?`, f.sID) }
	before := f.soporteSlots()
	if before == 0 {
		t.Fatal("S has no slots with the owner hosting it; the fixture is wrong")
	}

	// The incident: an admin with no availability rules is put in área soporte.
	admKey := supAddAdmin(t, f, "adm")
	body := mustJSON(t, f.setRole(f.ownerKey, "adm", "admin", "soporte"), http.StatusOK, "adm -> admin + soporte")
	if v, ok := body["has_availability"].(bool); !ok || v {
		t.Errorf("team-role answer has_availability = %v (%v); want false", body["has_availability"], ok)
	}
	if got := f.hosts(f.sID); !slices.Equal(got, owner) || routing() != "fixed" {
		t.Errorf("S after an admin without hours joined soporte: hosts %v, routing %s; want the owner, fixed", got, routing())
	}
	if got := f.soporteSlots(); got != before {
		t.Errorf("S slots = %d; want %d (still bookable)", got, before)
	}
	if got := f.soporteWaiting(); !slices.Equal(got, []string{"adm"}) {
		t.Errorf("waiting = %v; want [adm]", got)
	}
	if f.hasAvailability("adm") {
		t.Error("adm has_availability = true with no rules")
	}

	// Rules that open nothing on S do not count: another type's rule, and a backwards one.
	_, otherID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	f.addRule(admKey, `{"event_type_id":"`+otherID+`","day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	f.addRule(admKey, `{"day_of_week":2,"start_time":"17:00","end_time":"09:00"}`)
	if got := f.hosts(f.sID); !slices.Equal(got, owner) {
		t.Errorf("S after rules that open nothing on it: hosts %v; want the owner", got)
	}

	// A global rule, posted as adm: they join and the owner fallback is released.
	ruleID := f.addRule(admKey, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	if got := f.hosts(f.sID); !slices.Equal(got, []string{"adm:rotation:0"}) || routing() != "round_robin" {
		t.Errorf("S after adm set hours: hosts %v, routing %s; want [adm rotation], round_robin", got, routing())
	}
	if got := f.soporteWaiting(); len(got) != 0 {
		t.Errorf("waiting = %v; want nobody", got)
	}
	if !f.hasAvailability("adm") {
		t.Error("adm has_availability = false with a global rule")
	}
	if f.soporteSlots() == 0 {
		t.Error("S has no slots with adm (Mondays 09-17) in the rotation")
	}

	// Deleting their last usable rule takes them out again.
	f.deleteRule(admKey, ruleID)
	if got := f.hosts(f.sID); !slices.Equal(got, owner) || routing() != "fixed" {
		t.Errorf("S after adm deleted their hours: hosts %v, routing %s; want the owner, fixed", got, routing())
	}
	if got := f.soporteWaiting(); !slices.Equal(got, []string{"adm"}) {
		t.Errorf("waiting after delete = %v; want [adm]", got)
	}

	// A rule for S itself counts; a PATCH that keeps hours keeps them in.
	sRule := f.addRule(admKey, `{"event_type_id":"`+f.sID+`","day_of_week":3,"start_time":"10:00","end_time":"12:00"}`)
	if got := f.hosts(f.sID); !slices.Equal(got, []string{"adm:rotation:0"}) {
		t.Errorf("S after a rule for S: hosts %v; want [adm rotation]", got)
	}
	mustStatus(t, f.call(f.h.TeamReconcileAfterCaller(f.h.UpdateAvailabilityRule), http.MethodPatch,
		"/v1/availability-rules/"+sRule, `{"day_of_week":4}`, admKey, "id", sRule), http.StatusOK, "PATCH rule")
	if got := f.hosts(f.sID); !slices.Equal(got, []string{"adm:rotation:0"}) {
		t.Errorf("S after PATCH: hosts %v; want [adm rotation]", got)
	}

	// A second staff member with hours joins; the stable order is kept.
	addMember(t, f.db, "s1", "UTC")
	mustExec(t, f.db, `UPDATE users SET created_at = '2000-01-01' WHERE id = 's1'`) // s1 joined first
	seedFullAvailabilityDB(t, f.db, "s1")
	f.mustRole("s1", "member", "soporte")
	if got := f.hosts(f.sID); !slices.Equal(got, []string{"s1:rotation:0", "adm:rotation:1"}) {
		t.Errorf("S with s1 and adm: hosts %v", got)
	}
}

func TestTeamHours_mentorHasAvailability(t *testing.T) {
	f := newTeamFixture(t)
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	key := addMember(t, f.db, "m1", "UTC")
	f.mustRole("m1", "member", "mentoria")
	if f.hasAvailability("m1") {
		t.Error("mentor without rules: has_availability = true")
	}
	f.addRule(key, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	if !f.hasAvailability("m1") {
		t.Error("mentor with a global rule: has_availability = false")
	}
	if !f.hasAvailability(f.ownerID) {
		t.Error("owner (full hours, no área): has_availability = false")
	}
}

// Review follow-up: two more ways a lone área-soporte person used to take S to zero.
//   - A rule the slot engine cannot parse ("24:00"): POST accepts it, and resolveDay fails
//     the whole slot request, so S answered 500 even with a good rule beside it.
//   - A rule too short (or too misaligned) to hold one slot of S's duration.
//
// Either way they must wait outside the rotation and S must keep the owner's slots.
func TestTeamSoporte_rulesThatCannotHoldASlotDoNotRotate(t *testing.T) {
	f := newTeamFixture(t)
	f.mustSettings(`{"soporte_shared_id":"` + f.sID + `"}`)
	owner := []string{f.ownerID + ":required:0"}
	before := f.soporteSlots()
	if before == 0 {
		t.Fatal("S has no slots with the owner hosting it; the fixture is wrong")
	}
	admKey := supAddAdmin(t, f, "adm")
	mustJSON(t, f.setRole(f.ownerKey, "adm", "admin", "soporte"), http.StatusOK, "adm -> admin + soporte")
	stillOwner := func(when string) {
		t.Helper()
		if got := f.hosts(f.sID); !slices.Equal(got, owner) {
			t.Errorf("%s: S hosts %v; want the owner", when, got)
		}
		if got := f.soporteSlots(); got != before { // soporteSlots fails the test on a 500
			t.Errorf("%s: S slots = %d; want %d", when, got, before)
		}
		if got := f.soporteWaiting(); !slices.Equal(got, []string{"adm"}) {
			t.Errorf("%s: waiting = %v; want [adm]", when, got)
		}
		if f.hasAvailability("adm") {
			t.Errorf("%s: adm has_availability = true", when)
		}
	}

	// A good global rule plus an unparseable one: the bad one poisons every slot request.
	f.addRule(admKey, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	bad := f.addRule(admKey, `{"day_of_week":3,"start_time":"09:00","end_time":"24:00"}`)
	stillOwner("good rule + 09:00-24:00 rule")
	f.deleteRule(admKey, bad)
	if got := f.hosts(f.sID); !slices.Equal(got, []string{"adm:rotation:0"}) {
		t.Fatalf("after deleting the bad rule: S hosts %v; want [adm rotation]", got)
	}
	if f.soporteSlots() == 0 {
		t.Error("S has no slots with adm (Mondays 09-17) in the rotation")
	}

	// Start over with no rules: short and misaligned windows on a 30-minute, 30-minute
	// interval S hold no slot.
	mustExec(t, f.db, `DELETE FROM availability_rules WHERE user_id = 'adm'`)
	f.addRule(admKey, `{"day_of_week":1,"start_time":"09:00","end_time":"09:05"}`)
	f.addRule(admKey, `{"day_of_week":2,"start_time":"09:05","end_time":"09:35"}`)
	stillOwner("09:00-09:05 and 09:05-09:35 rules")

	// An exact fit (09:00-09:30) does hold one: adm joins and S keeps slots.
	f.addRule(admKey, `{"day_of_week":4,"start_time":"09:00","end_time":"09:30"}`)
	if got := f.hosts(f.sID); !slices.Equal(got, []string{"adm:rotation:0"}) {
		t.Errorf("after an exact-fit rule: S hosts %v; want [adm rotation]", got)
	}
	if f.soporteSlots() == 0 {
		t.Error("S has no slots with adm (Thursdays 09:00-09:30) in the rotation")
	}
	if !f.hasAvailability("adm") {
		t.Error("adm has_availability = false with an exact-fit rule")
	}
}
