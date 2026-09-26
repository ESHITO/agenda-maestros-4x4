package handler_test

// Fork (Agenda Maestros 4x4): has_availability per personal link (fork_team_hours.go).
// Every mentor and every support person attends their OWN copy of a template, hosted by
// them alone, so their link shows times only where their weekly hours can hold a slot of
// it: a global rule or one for that copy. The Soporte template itself stays hosted by the
// owner, so someone without hours never empties it (the retired rotation once took the
// public Soporte page from 319 slots to 0 that way).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// slotsOf counts slug's public slots over the next week.
func (f *teamFixture) slotsOf(slug string) int {
	f.t.Helper()
	from := time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")
	to := time.Now().UTC().AddDate(0, 0, 8).Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/slots?from="+from+"&to="+to+"&tz=UTC", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	f.h.GetSlots(rec, req)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("slots of %s: %d %s", slug, rec.Code, rec.Body.String())
	}
	var resp struct {
		Slots []any `json:"slots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		f.t.Fatal(err)
	}
	return len(resp.Slots)
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

// The production incident, in the new model: an admin with NO availability rules put in
// área soporte gets their own copy of S, which shows nothing until they set hours - and S
// itself, hosted by the owner, keeps every slot. has_availability follows their copy.
func TestTeamHours_soporteCopyHasAvailability(t *testing.T) {
	f := newTeamFixture(t)
	f.mustSettings(`{"soporte_template_id":"` + f.sID + `"}`)
	owner := []string{f.ownerID + ":required:0"}
	before := f.slotsOf(f.sSlug)
	if before == 0 {
		t.Fatal("S has no slots with the owner hosting it; the fixture is wrong")
	}

	admKey := supAddAdmin(t, f, "adm")
	body := mustJSON(t, f.setRole(f.ownerKey, "adm", "admin", "soporte"), http.StatusOK, "adm -> admin + soporte")
	if v, ok := body["has_availability"].(bool); !ok || v {
		t.Errorf("team-role answer has_availability = %v (%v); want false", body["has_availability"], ok)
	}
	c := f.copyOf(f.sID, "adm")
	if c.id == "" || !c.active {
		t.Fatalf("adm has no active copy of S: %+v", c)
	}
	if got := f.hosts(f.sID); !slices.Equal(got, owner) {
		t.Errorf("S hosts = %v; want the owner alone", got)
	}
	if got := f.slotsOf(f.sSlug); got != before {
		t.Errorf("S slots = %d; want %d (the owner still hosts it)", got, before)
	}
	if got := f.slotsOf(c.slug); got != 0 {
		t.Errorf("adm's copy offers %d slots with no rules", got)
	}
	if f.hasAvailability("adm") {
		t.Error("adm has_availability = true with no rules")
	}

	// Rules that open nothing on the copy do not count: another type's rule, one for S
	// itself (S is the owner's; the copy reads global rules and its own), a backwards one.
	_, otherID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	f.addRule(admKey, `{"event_type_id":"`+otherID+`","day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	f.addRule(admKey, `{"event_type_id":"`+f.sID+`","day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	f.addRule(admKey, `{"day_of_week":2,"start_time":"17:00","end_time":"09:00"}`)
	if f.hasAvailability("adm") {
		t.Error("adm has_availability = true with rules that open nothing on their copy")
	}

	// A global rule: their link shows times.
	ruleID := f.addRule(admKey, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	if !f.hasAvailability("adm") {
		t.Error("adm has_availability = false with a global rule")
	}
	if f.slotsOf(c.slug) == 0 {
		t.Error("adm's copy has no slots with Mondays 09-17")
	}
	f.deleteRule(admKey, ruleID)
	if f.hasAvailability("adm") {
		t.Error("adm has_availability = true after deleting their only usable rule")
	}

	// A rule for their own copy counts.
	f.addRule(admKey, `{"event_type_id":"`+c.id+`","day_of_week":3,"start_time":"10:00","end_time":"12:00"}`)
	if !f.hasAvailability("adm") {
		t.Error("adm has_availability = false with a rule for their copy")
	}
	// S never gained a host through any of this.
	if got := f.hosts(f.sID); !slices.Equal(got, owner) {
		t.Errorf("S hosts after adm's rules = %v; want the owner alone", got)
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
	// Too short for one 30-minute slot of their copy.
	short := f.addRule(key, `{"day_of_week":1,"start_time":"09:00","end_time":"09:05"}`)
	if f.hasAvailability("m1") {
		t.Error("mentor with only a 09:00-09:05 rule: has_availability = true")
	}
	f.deleteRule(key, short)
	f.addRule(key, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	if !f.hasAvailability("m1") {
		t.Error("mentor with a global rule: has_availability = false")
	}
	if !f.hasAvailability(f.ownerID) {
		t.Error("owner (full hours, no área): has_availability = false")
	}
}

// Review follow-up, kept per copy: a rule the slot engine cannot parse ("24:00": POST
// accepts it, and resolveDay fails the whole slot request) and a rule too short or too
// misaligned to hold one slot of the copy's duration never count as hours.
func TestTeamHours_rulesThatCannotHoldASlotDoNotCount(t *testing.T) {
	f := newTeamFixture(t)
	f.mustSettings(`{"soporte_template_id":"` + f.sID + `"}`)
	admKey := supAddAdmin(t, f, "adm")
	mustJSON(t, f.setRole(f.ownerKey, "adm", "admin", "soporte"), http.StatusOK, "adm -> admin + soporte")
	c := f.copyOf(f.sID, "adm")
	before := f.slotsOf(f.sSlug)

	// A good global rule plus an unparseable one: the bad one poisons their copy's slots.
	f.addRule(admKey, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	bad := f.addRule(admKey, `{"day_of_week":3,"start_time":"09:00","end_time":"24:00"}`)
	if f.hasAvailability("adm") {
		t.Error("good rule + 09:00-24:00 rule: has_availability = true")
	}
	if got := f.slotsOf(f.sSlug); got != before { // slotsOf fails the test on a 500
		t.Errorf("S slots = %d; want %d (adm's bad rule never reaches S)", got, before)
	}
	f.deleteRule(admKey, bad)
	if !f.hasAvailability("adm") || f.slotsOf(c.slug) == 0 {
		t.Error("after deleting the bad rule: no availability on adm's copy")
	}

	// Start over: short and misaligned windows on a 30-minute, 30-minute interval copy.
	mustExec(t, f.db, `DELETE FROM availability_rules WHERE user_id = 'adm'`)
	f.addRule(admKey, `{"day_of_week":1,"start_time":"09:00","end_time":"09:05"}`)
	f.addRule(admKey, `{"day_of_week":2,"start_time":"09:05","end_time":"09:35"}`)
	if f.hasAvailability("adm") {
		t.Error("09:00-09:05 and 09:05-09:35 rules: has_availability = true")
	}
	// An exact fit (09:00-09:30) holds one.
	f.addRule(admKey, `{"day_of_week":4,"start_time":"09:00","end_time":"09:30"}`)
	if !f.hasAvailability("adm") {
		t.Error("adm has_availability = false with an exact-fit rule")
	}
	if f.slotsOf(c.slug) == 0 {
		t.Error("adm's copy has no slots with Thursdays 09:00-09:30")
	}
}
