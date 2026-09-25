package handler_test

// Fork (Agenda Maestros 4x4): áreas and the owner's predefined event types - the reconcile
// (fork_team.go), its API (fork_team_api.go), guards and triggers (fork_team_guards.go)
// and the bookings list (fork_team_bookings.go). Handlers are called the way server.go
// wires them (RequireAuth + the fork wrappers).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

type teamFixture struct {
	t                 *testing.T
	h                 *handler.Handler
	db                *sql.DB
	ownerKey, ownerID string
	tSlug, tID        string // "Mentoría privada", livekit
	sSlug, sID        string // "Soporte 1 a 1", livekit
}

func newTeamFixture(t *testing.T) *teamFixture {
	t.Helper()
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	f := &teamFixture{t: t, h: h, db: database, ownerKey: key, ownerID: ownerID}
	f.tSlug, f.tID = seedEventTypeHTTP(t, h, key)
	f.sSlug, f.sID = seedEventTypeHTTP(t, h, key)
	mustExec(t, database, `UPDATE event_types SET name = 'Mentoría privada', location_type = 'livekit', location_value = '' WHERE id = ?`, f.tID)
	mustExec(t, database, `UPDATE event_types SET name = 'Soporte 1 a 1', location_type = 'livekit', location_value = '' WHERE id = ?`, f.sID)
	return f
}

// call runs fn behind RequireAuth with the given path values (name, value, ...).
func (f *teamFixture) call(fn http.HandlerFunc, method, path, body, key string, pathValues ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := authReq(method, path, body, key)
	for i := 0; i+1 < len(pathValues); i += 2 {
		req.SetPathValue(pathValues[i], pathValues[i+1])
	}
	rec := httptest.NewRecorder()
	f.h.RequireAuth(fn)(rec, req)
	return rec
}

func (f *teamFixture) guard(op handler.TeamOp, fn http.HandlerFunc) http.HandlerFunc {
	return f.h.TeamEventTypeGuard(op, fn)
}

func (f *teamFixture) putSettings(key, body string) *httptest.ResponseRecorder {
	return f.call(f.h.PutTeamSettings, http.MethodPut, "/v1/team/settings", body, key)
}

func (f *teamFixture) mustSettings(body string) map[string]any {
	f.t.Helper()
	return mustJSON(f.t, f.putSettings(f.ownerKey, body), http.StatusOK, "PUT team settings "+body)
}

func (f *teamFixture) setRole(key, userID, tier, area string) *httptest.ResponseRecorder {
	return f.call(f.h.SetTeamRole, http.MethodPut, "/v1/users/"+userID+"/team-role",
		fmt.Sprintf(`{"tier":%q,"area":%q}`, tier, area), key, "id", userID)
}

func (f *teamFixture) mustRole(userID, tier, area string) {
	f.t.Helper()
	mustStatus(f.t, f.setRole(f.ownerKey, userID, tier, area), http.StatusOK, "team-role "+userID+" "+area)
}

func (f *teamFixture) patchET(slug, body string) *httptest.ResponseRecorder {
	return f.call(f.guard(handler.TeamOpPatch, f.h.PatchEventType), http.MethodPatch, "/v1/event-types/"+slug, body, f.ownerKey, "slug", slug)
}

// teamCopy is a mentor's copy as stored.
type teamCopy struct {
	id, slug, userID, name, routing, locType, locValue, templateID string
	active                                                         bool
	duration                                                       int
}

// copyOf returns userID's copy of templateID ("" id when there is none).
func (f *teamFixture) copyOf(templateID, userID string) teamCopy {
	f.t.Helper()
	var c teamCopy
	err := f.db.QueryRow(`
		SELECT et.id, et.slug, et.user_id, et.name, et.routing_mode, et.location_type, COALESCE(et.location_value,''),
		       l.template_id, et.is_active, et.duration_minutes
		FROM fork_event_type_links l JOIN event_types et ON et.id = l.copy_id
		WHERE l.kind = 'copy' AND l.template_id = ? AND l.user_id = ?`, templateID, userID).
		Scan(&c.id, &c.slug, &c.userID, &c.name, &c.routing, &c.locType, &c.locValue, &c.templateID, &c.active, &c.duration)
	if err != nil && err != sql.ErrNoRows {
		f.t.Fatalf("copyOf: %v", err)
	}
	return c
}

func (f *teamFixture) hosts(etID string) []string {
	f.t.Helper()
	rows, err := f.db.Query(`SELECT user_id || ':' || role || ':' || priority FROM event_type_hosts WHERE event_type_id = ? ORDER BY priority, user_id`, etID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func (f *teamFixture) scalar(query string, args ...any) string {
	f.t.Helper()
	var v sql.NullString
	if err := f.db.QueryRow(query, args...).Scan(&v); err != nil && err != sql.ErrNoRows {
		f.t.Fatalf("scalar %q: %v", query, err)
	}
	return v.String
}

func TestTeamReconcile_copyLifecycle(t *testing.T) {
	f := newTeamFixture(t)
	h := f.h
	mustCreated(t, f.call(f.guard(handler.TeamOpQuestions, h.CreateQuestion), http.MethodPost,
		"/v1/event-types/"+f.tSlug+"/questions", `{"label":"¿Qué quieres trabajar?","type":"text","required":true}`,
		f.ownerKey, "slug", f.tSlug), "create T question")
	mustStatus(t, f.patchET(f.tSlug, `{"reminders":[24,1],"description":"Plantilla del equipo"}`), http.StatusOK, "patch T")

	addMember(t, f.db, "m1", "America/Lima")
	mustExec(t, f.db, `UPDATE users SET name = 'María José Núñez' WHERE id = 'm1'`)
	f.mustRole("m1", "member", "mentoria")
	if c := f.copyOf(f.tID, "m1"); c.id != "" {
		t.Fatalf("a copy exists before any template is set: %+v", c)
	}

	body := f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	warn, _ := body["warnings"].(map[string]any)
	if warn["copies_created"] != float64(1) || warn["mentoria_template_no_webhook"] != true {
		t.Errorf("warnings = %v; want copies_created 1 and mentoria_template_no_webhook (no owner webhook)", warn)
	}
	c := f.copyOf(f.tID, "m1")
	if c.id == "" {
		t.Fatal("no copy for the mentor after setting the template")
	}
	if c.slug != f.tSlug+"-maria-jose-nunez" || !c.active || c.userID != f.ownerID || c.routing != "fixed" ||
		c.locType != "livekit" || c.locValue != "" || c.name != "Mentoría privada" || c.duration != 30 {
		t.Errorf("copy = %+v", c)
	}
	if got := f.hosts(c.id); !slices.Equal(got, []string{"m1:required:0"}) {
		t.Errorf("copy hosts = %v; want only the mentor, required", got)
	}
	if got := f.hosts(f.tID); !slices.Equal(got, []string{f.ownerID + ":required:0"}) {
		t.Errorf("T hosts = %v; want the owner locked as required", got)
	}
	if got := f.scalar(`SELECT description FROM event_types WHERE id = ?`, c.id); got != "Plantilla del equipo" {
		t.Errorf("copy description = %q", got)
	}
	if got := f.scalar(`SELECT group_concat(hours_before) FROM (SELECT hours_before FROM event_type_reminders WHERE event_type_id = ? ORDER BY hours_before)`, c.id); got != "1,24" {
		t.Errorf("copy reminders = %q; want 1,24", got)
	}
	if got := f.scalar(`SELECT COUNT(*) FROM event_type_questions q JOIN fork_question_links l ON l.copy_question_id = q.id WHERE q.event_type_id = ? AND q.label = '¿Qué quieres trabajar?' AND q.required = 1`, c.id); got != "1" {
		t.Errorf("linked copy questions = %s; want 1", got)
	}

	// A template edit reaches the copy; the slug never changes.
	mustStatus(t, f.patchET(f.tSlug, `{"name":"Mentoría 1 a 1","duration_minutes":45}`), http.StatusOK, "rename T")
	if got := f.copyOf(f.tID, "m1"); got.name != "Mentoría 1 a 1" || got.duration != 45 || got.slug != c.slug {
		t.Errorf("after the template edit: %+v", got)
	}
	// Pausing the template itself does not pause the mentors' links.
	mustStatus(t, f.patchET(f.tSlug, `{"is_active":false}`), http.StatusOK, "pause T")
	if !f.copyOf(f.tID, "m1").active {
		t.Error("pausing the template deactivated the copy")
	}
	mustStatus(t, f.patchET(f.tSlug, `{"is_active":true}`), http.StatusOK, "resume T")

	// Losing the área deactivates the SAME copy; regaining it reactivates it.
	f.mustRole("m1", "member", "")
	if got := f.copyOf(f.tID, "m1"); got.id != c.id || got.active {
		t.Errorf("after losing the área: %+v; want the same copy, inactive", got)
	}
	f.mustRole("m1", "member", "mentoria")
	if got := f.copyOf(f.tID, "m1"); got.id != c.id || !got.active {
		t.Errorf("after regaining the área: %+v; want the same copy, active", got)
	}

	// Archive / restore of the mentor.
	mustStatus(t, f.call(h.TeamReconcileAfter(h.ArchiveUser), http.MethodPost, "/v1/users/m1/archive", "", f.ownerKey, "id", "m1"), http.StatusOK, "archive")
	if f.copyOf(f.tID, "m1").active {
		t.Error("an archived mentor's copy is still active")
	}
	mustStatus(t, f.call(h.TeamReconcileAfter(h.RestoreUser), http.MethodPost, "/v1/users/m1/restore", "", f.ownerKey, "id", "m1"), http.StatusOK, "restore")
	if !f.copyOf(f.tID, "m1").active {
		t.Error("a restored mentor's copy is not active again")
	}

	// Archiving the template deactivates the copies; unarchiving brings them back.
	mustStatus(t, f.patchET(f.tSlug, `{"archived":true}`), http.StatusOK, "archive T")
	if f.copyOf(f.tID, "m1").active {
		t.Error("the copy of an archived template is still active")
	}
	mustStatus(t, f.patchET(f.tSlug, `{"archived":false}`), http.StatusOK, "unarchive T")
	if !f.copyOf(f.tID, "m1").active {
		t.Error("unarchiving the template did not reactivate the copy")
	}
}

func TestTeamReconcile_oldTemplateAndOrphans(t *testing.T) {
	f := newTeamFixture(t)
	t2Slug, t2ID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	mustExec(t, f.db, `UPDATE event_types SET location_type = 'livekit' WHERE id = ?`, t2ID)
	for _, id := range []string{"m1", "m2", "m3"} {
		addMember(t, f.db, id, "UTC")
		f.mustRole(id, "member", "mentoria")
	}
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	c1, c2, c3 := f.copyOf(f.tID, "m1"), f.copyOf(f.tID, "m2"), f.copyOf(f.tID, "m3")
	if c1.id == "" || c2.id == "" || c3.id == "" {
		t.Fatalf("copies: %v %v %v", c1.id, c2.id, c3.id)
	}
	if c1.slug != f.tSlug+"-mentor-m1" {
		t.Errorf("slug = %q", c1.slug)
	}

	// A deleted mentor whose copy has no bookings: the copy goes.
	mustStatus(t, f.call(f.h.TeamReconcileAfter(f.h.DeleteUser), http.MethodDelete, "/v1/users/m2", "", f.ownerKey, "id", "m2"), http.StatusOK, "delete m2")
	if got := f.scalar(`SELECT COUNT(*) FROM event_types WHERE id = ?`, c2.id); got != "0" {
		t.Errorf("orphan copy without bookings still exists")
	}
	// One with bookings stays, inactive (bookings are RESTRICT); its área row is purged.
	mustExec(t, f.db, `INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk3', ?, ?, '2020-01-01T10:00:00Z', '2020-01-01T10:30:00Z', 'confirmed')`, c3.id, f.ownerID)
	mustExec(t, f.db, `DELETE FROM users WHERE id = 'm3'`)
	if _, err := f.h.ReconcileTeam(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got := f.scalar(`SELECT is_active FROM event_types WHERE id = ?`, c3.id); got != "0" {
		t.Errorf("orphan copy with bookings: is_active = %q; want 0 (kept, deactivated)", got)
	}
	if got := f.scalar(`SELECT COUNT(*) FROM fork_member_areas WHERE user_id = 'm3'`); got != "0" {
		t.Error("the deleted user's área row was not purged")
	}

	// Another template: the old copy is deactivated, link kept; a new copy is made.
	f.mustSettings(`{"mentoria_template_id":"` + t2ID + `"}`)
	if got := f.copyOf(f.tID, "m1"); got.id != c1.id || got.active {
		t.Errorf("old template's copy = %+v; want kept and inactive", got)
	}
	n1 := f.copyOf(t2ID, "m1")
	if n1.id == "" || !n1.active || n1.slug != t2Slug+"-mentor-m1" {
		t.Errorf("new template's copy = %+v", n1)
	}
	// Unset: every copy inactive.
	f.mustSettings(`{"mentoria_template_id":null}`)
	if f.copyOf(t2ID, "m1").active || f.copyOf(f.tID, "m1").active {
		t.Error("a copy stayed active with no template set")
	}
	// Back to the first template: the SAME copy comes back.
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	if got := f.copyOf(f.tID, "m1"); got.id != c1.id || !got.active {
		t.Errorf("back to the first template: %+v; want the original copy, active", got)
	}
}

func TestTeamReconcile_slugCollisionsAndAccents(t *testing.T) {
	f := newTeamFixture(t)
	for _, id := range []string{"a1", "a2", "a3"} {
		addMember(t, f.db, id, "UTC")
	}
	mustExec(t, f.db, `UPDATE users SET name = 'José Núñez' WHERE id IN ('a1','a2')`)
	mustExec(t, f.db, `UPDATE users SET name = 'Ana María', created_at = '2000-01-03' WHERE id = 'a3'`)
	mustExec(t, f.db, `UPDATE users SET created_at = '2000-01-01' WHERE id = 'a1'`)
	mustExec(t, f.db, `UPDATE users SET created_at = '2000-01-02' WHERE id = 'a2'`)
	// A slug somebody already took (slugs are unique instance-wide).
	mustExec(t, f.db, `INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('taken', ?, ?, 'Otro', 30)`, f.ownerID, f.tSlug+"-ana-maria")
	for _, id := range []string{"a1", "a2", "a3"} {
		f.mustRole(id, "member", "mentoria")
	}
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	for id, want := range map[string]string{
		"a1": f.tSlug + "-jose-nunez",
		"a2": f.tSlug + "-jose-nunez-2",
		"a3": f.tSlug + "-ana-maria-2",
	} {
		if got := f.copyOf(f.tID, id).slug; got != want {
			t.Errorf("%s slug = %q; want %q", id, got, want)
		}
	}
	// The slug is the shared link: a later rename does not move it.
	mustExec(t, f.db, `UPDATE users SET name = 'Pepe' WHERE id = 'a1'`)
	if _, err := f.h.ReconcileTeam(context.Background(), "a1"); err != nil {
		t.Fatal(err)
	}
	if got := f.copyOf(f.tID, "a1").slug; got != f.tSlug+"-jose-nunez" {
		t.Errorf("slug after a rename = %q; want it unchanged", got)
	}
}

func TestTeamReconcile_questionsInPlaceAndParking(t *testing.T) {
	f := newTeamFixture(t)
	h := f.h
	createQ := func(body string) string {
		t.Helper()
		rec := f.call(f.guard(handler.TeamOpQuestions, h.CreateQuestion), http.MethodPost,
			"/v1/event-types/"+f.tSlug+"/questions", body, f.ownerKey, "slug", f.tSlug)
		return mustString(t, mustCreated(t, rec, "create question"), "id", "create question")
	}
	q1 := createQ(`{"label":"Tema","type":"text"}`)
	q2 := createQ(`{"label":"Nivel","type":"select","options":["a","b"]}`)
	addMember(t, f.db, "m1", "UTC")
	f.mustRole("m1", "member", "mentoria")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	c := f.copyOf(f.tID, "m1")
	cq := func(tq string) string {
		return f.scalar(`SELECT copy_question_id FROM fork_question_links WHERE copy_id = ? AND template_question_id = ?`, c.id, tq)
	}
	cq1, cq2 := cq(q1), cq(q2)
	if cq1 == "" || cq2 == "" {
		t.Fatalf("copy questions not linked: %q %q", cq1, cq2)
	}
	if got := f.scalar(`SELECT options FROM event_type_questions WHERE id = ?`, cq2); got != `["a","b"]` {
		t.Errorf("copied options = %q", got)
	}
	mustExec(t, f.db, `INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bq', ?, 'm1', '2099-01-01T10:00:00Z', '2099-01-01T10:30:00Z', 'confirmed')`, c.id)
	mustExec(t, f.db, `INSERT INTO booking_answers (id, booking_id, question_id, value) VALUES ('ans1', 'bq', ?, 'Mis finanzas')`, cq1)

	// Edited in place: same id, the answer stays attached.
	mustStatus(t, f.call(f.guard(handler.TeamOpQuestions, h.UpdateQuestion), http.MethodPatch,
		"/v1/event-types/"+f.tSlug+"/questions/"+q1, `{"label":"Tema nuevo","position":3}`, f.ownerKey, "slug", f.tSlug, "id", q1), http.StatusOK, "update question")
	if got := f.scalar(`SELECT label || '@' || position FROM event_type_questions WHERE id = ?`, cq1); got != "Tema nuevo@3" {
		t.Errorf("copy question after the edit = %q; want the new label and position on the same row", got)
	}

	// Deleted on the template: the answered copy question is parked on the holder.
	mustStatus(t, f.call(f.guard(handler.TeamOpQuestions, h.DeleteQuestion), http.MethodDelete,
		"/v1/event-types/"+f.tSlug+"/questions/"+q1, "", f.ownerKey, "slug", f.tSlug, "id", q1), http.StatusNoContent, "delete q1")
	holder := f.scalar(`SELECT copy_id FROM fork_event_type_links WHERE template_id = ? AND kind = 'holder'`, f.tID)
	if holder == "" {
		t.Fatal("no holder type after parking")
	}
	if got := f.scalar(`SELECT event_type_id FROM event_type_questions WHERE id = ?`, cq1); got != holder {
		t.Errorf("parked question lives on %q; want the holder %q", got, holder)
	}
	if got := f.scalar(`SELECT name || '|' || is_active || '|' || is_public || '|' || user_id FROM event_types WHERE id = ?`, holder); got != "Preguntas retiradas — Mentoría privada|0|0|"+f.ownerID {
		t.Errorf("holder = %q", got)
	}
	if got := f.scalar(`SELECT value FROM booking_answers WHERE id = 'ans1'`); got != "Mis finanzas" {
		t.Errorf("the client's answer was lost: %q", got)
	}
	if got := f.scalar(`SELECT COUNT(*) FROM fork_question_links WHERE copy_question_id = ?`, cq1); got != "0" {
		t.Error("the parked question is still linked to the copy")
	}
	// Unanswered: simply deleted.
	mustStatus(t, f.call(f.guard(handler.TeamOpQuestions, h.DeleteQuestion), http.MethodDelete,
		"/v1/event-types/"+f.tSlug+"/questions/"+q2, "", f.ownerKey, "slug", f.tSlug, "id", q2), http.StatusNoContent, "delete q2")
	if got := f.scalar(`SELECT COUNT(*) FROM event_type_questions WHERE id = ?`, cq2); got != "0" {
		t.Error("an unanswered retired question survived")
	}
	// A new template question arrives with its exact position.
	q3 := createQ(`{"label":"Nueva","type":"phone","position":7}`)
	if got := f.scalar(`SELECT q.position FROM event_type_questions q JOIN fork_question_links l ON l.copy_question_id = q.id WHERE l.template_question_id = ? AND q.event_type_id = ?`, q3, c.id); got != "7" {
		t.Errorf("new copy question position = %q; want 7", got)
	}

	// Lists: the owner sees T (with its copies) but neither the copy nor the holder; the
	// mentor sees their copy, marked.
	holderSlug := f.scalar(`SELECT slug FROM event_types WHERE id = ?`, holder)
	ownerList := listEventTypesTeam(t, f, f.ownerKey)
	for _, et := range ownerList {
		if et.ID == c.id || et.ID == holder {
			t.Errorf("owner's list shows %s (%s)", et.Slug, et.ID)
		}
		if et.ID == f.tID && (et.Team == nil || et.Team.Kind != "mentoria_template" || et.Team.Copies == nil || *et.Team.Copies != 1 || len(et.Team.CopyLinks) != 1) {
			t.Errorf("T's team info = %+v", et.Team)
		}
	}
	mentorList := listEventTypesTeam(t, f, "member-key-m1")
	found := false
	for _, et := range mentorList {
		if et.ID == c.id {
			found = true
			if et.Team == nil || et.Team.Kind != "mentoria_copy" || et.Team.TemplateName != "Mentoría privada" || et.Team.MentorName != "Mentor m1" || et.Team.TemplateSlug != f.tSlug {
				t.Errorf("copy team info for the mentor = %+v", et.Team)
			}
		}
	}
	if !found {
		t.Error("the mentor's list does not show their copy")
	}
	mustStatus(t, f.call(h.GetEventType, http.MethodGet, "/v1/event-types/"+holderSlug, "", f.ownerKey, "slug", holderSlug), http.StatusNotFound, "GET holder")
	got := mustJSON(t, f.call(h.GetEventType, http.MethodGet, "/v1/event-types/"+c.slug, "", f.ownerKey, "slug", c.slug), http.StatusOK, "GET copy")
	if team, _ := got["team"].(map[string]any); team["kind"] != "mentoria_copy" || team["mentor_name"] != "Mentor m1" {
		t.Errorf("GET copy team = %v", got["team"])
	}
}

type etTeamItem struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Team *struct {
		Kind         string `json:"kind"`
		TemplateSlug string `json:"template_slug"`
		TemplateName string `json:"template_name"`
		MentorName   string `json:"mentor_name"`
		Copies       *int   `json:"copies"`
		CopyLinks    []struct {
			Slug, URL, MentorName string
			Active                bool
		} `json:"copy_links"`
		Hosts []struct{ ID, Name string } `json:"hosts"`
	} `json:"team"`
}

func listEventTypesTeam(t *testing.T, f *teamFixture, key string) []etTeamItem {
	t.Helper()
	rec := f.call(f.h.ListEventTypes, http.MethodGet, "/v1/event-types", "", key)
	mustStatus(t, rec, http.StatusOK, "list event types")
	var out struct {
		Items []etTeamItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Items
}

func TestTeamReconcile_soporteRotationFallbackAndRelease(t *testing.T) {
	f := newTeamFixture(t)
	f.mustSettings(`{"soporte_shared_id":"` + f.sID + `"}`)
	routing := func(id string) string { return f.scalar(`SELECT routing_mode FROM event_types WHERE id = ?`, id) }
	if got := f.hosts(f.sID); !slices.Equal(got, []string{f.ownerID + ":required:0"}) || routing(f.sID) != "fixed" {
		t.Errorf("S with nobody in soporte: hosts %v, routing %s; want the owner, fixed", got, routing(f.sID))
	}
	addMember(t, f.db, "s1", "UTC")
	addMember(t, f.db, "s2", "UTC")
	mustExec(t, f.db, `UPDATE users SET created_at = '2000-01-02' WHERE id = 's1'`)
	mustExec(t, f.db, `UPDATE users SET created_at = '2000-01-01' WHERE id = 's2'`) // s2 joined first
	seedFullAvailabilityDB(t, f.db, "s1") // soporte rotates only with weekly hours
	seedFullAvailabilityDB(t, f.db, "s2")
	f.mustRole("s1", "member", "soporte")
	f.mustRole("s2", "member", "soporte")
	if got := f.hosts(f.sID); !slices.Equal(got, []string{"s2:rotation:0", "s1:rotation:1"}) || routing(f.sID) != "round_robin" {
		t.Errorf("S rotation = %v, routing %s", got, routing(f.sID))
	}
	mustStatus(t, f.call(f.h.TeamReconcileAfter(f.h.ArchiveUser), http.MethodPost, "/v1/users/s2/archive", "", f.ownerKey, "id", "s2"), http.StatusOK, "archive s2")
	if got := f.hosts(f.sID); !slices.Equal(got, []string{"s1:rotation:0"}) {
		t.Errorf("S rotation after archiving s2 = %v", got)
	}

	// A new S: the previous one is released back to its owner.
	s2Slug, s2ID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	_ = s2Slug
	mustExec(t, f.db, `UPDATE event_types SET location_type = 'livekit' WHERE id = ?`, s2ID)
	f.mustSettings(`{"soporte_shared_id":"` + s2ID + `"}`)
	if got := f.hosts(f.sID); !slices.Equal(got, []string{f.ownerID + ":required:0"}) || routing(f.sID) != "fixed" {
		t.Errorf("released S: hosts %v, routing %s", got, routing(f.sID))
	}
	if got := f.hosts(s2ID); !slices.Equal(got, []string{"s1:rotation:0"}) {
		t.Errorf("new S rotation = %v", got)
	}
	if got := f.scalar(`SELECT value FROM fork_settings WHERE key = 'team_soporte_last_managed_id'`); got != s2ID {
		t.Errorf("last managed = %q; want the new S", got)
	}
	f.mustSettings(`{"soporte_shared_id":null}`)
	if got := f.hosts(s2ID); !slices.Equal(got, []string{f.ownerID + ":required:0"}) || routing(s2ID) != "fixed" {
		t.Errorf("unset S not released: hosts %v, routing %s", got, routing(s2ID))
	}
}

func TestTeamGuards(t *testing.T) {
	f := newTeamFixture(t)
	h := f.h
	addMember(t, f.db, "m1", "UTC")
	addMember(t, f.db, "s1", "UTC")
	seedFullAvailabilityDB(t, f.db, "s1") // soporte rotates only with weekly hours
	f.mustRole("m1", "member", "mentoria")
	f.mustRole("s1", "member", "soporte")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_shared_id":"` + f.sID + `"}`)
	c := f.copyOf(f.tID, "m1")

	want409 := func(rec *httptest.ResponseRecorder, what, contains string) {
		t.Helper()
		body := mustJSON(t, rec, http.StatusConflict, what)
		if msg, _ := body["error"].(string); !strings.Contains(msg, contains) {
			t.Errorf("%s: error %q; want it to say %q", what, msg, contains)
		}
	}
	// Every edit of a copy is refused, pointing at the template.
	copyMsg := "Esta es la copia de «Mentoría privada» para Mentor m1"
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"patch":     f.patchET(c.slug, `{"name":"x"}`),
		"delete":    f.call(f.guard(handler.TeamOpDelete, h.DeleteEventType), http.MethodDelete, "/", "", f.ownerKey, "slug", c.slug),
		"duplicate": f.call(f.guard(handler.TeamOpDuplicate, h.DuplicateEventType), http.MethodPost, "/", "", f.ownerKey, "slug", c.slug),
		"transfer":  f.call(f.guard(handler.TeamOpTransfer, h.TransferEventType), http.MethodPost, "/", `{"expected_owner_id":"`+f.ownerID+`","new_owner_id":"m1"}`, f.ownerKey, "slug", c.slug),
		"hosts":     f.call(f.guard(handler.TeamOpHostsPut, h.SetEventTypeHosts), http.MethodPut, "/", `{"hosts":[{"user_id":"m1","role":"required"}]}`, f.ownerKey, "slug", c.slug),
		"question":  f.call(f.guard(handler.TeamOpQuestions, h.CreateQuestion), http.MethodPost, "/", `{"label":"x","type":"text"}`, f.ownerKey, "slug", c.slug),
		"whatsapp":  f.call(f.guard(handler.TeamOpWhatsAppWrite, h.PutWhatsAppMessages), http.MethodPut, "/", `{"created":"x"}`, f.ownerKey, "slug", c.slug),
		"preview":   f.call(f.guard(handler.TeamOpWhatsAppWrite, h.PreviewWhatsAppMessages), http.MethodPost, "/", `{}`, f.ownerKey, "slug", c.slug),
	} {
		want409(rec, "copy "+name, copyMsg)
	}
	if got := f.copyOf(f.tID, "m1"); got.userID != f.ownerID {
		t.Errorf("the copy changed owner: %+v", got)
	}

	// T and S.
	want409(f.call(f.guard(handler.TeamOpTransfer, h.TransferEventType), http.MethodPost, "/", `{"expected_owner_id":"`+f.ownerID+`","new_owner_id":"m1"}`, f.ownerKey, "slug", f.tSlug), "transfer T", "no se transfieren")
	want409(f.call(f.guard(handler.TeamOpHostsPut, h.SetEventTypeHosts), http.MethodPut, "/", `{"hosts":[{"user_id":"m1","role":"required"}]}`, f.ownerKey, "slug", f.tSlug), "hosts T", "Miembros")
	want409(f.patchET(f.tSlug, `{"routing_mode":"round_robin"}`), "routing T", "reparto")
	want409(f.patchET(f.tSlug, `{"location_type":"zoom"}`), "location T", "sala de video")
	want409(f.call(f.guard(handler.TeamOpDelete, h.DeleteEventType), http.MethodDelete, "/", "", f.ownerKey, "slug", f.tSlug), "delete T with copies", "copias")
	want409(f.patchET(f.sSlug, `{"routing_mode":"fixed"}`), "routing S", "reparto")
	want409(f.call(f.guard(handler.TeamOpDelete, h.DeleteEventType), http.MethodDelete, "/", "", f.ownerKey, "slug", f.sSlug), "delete S", "Soporte")

	// Validate on change: the editor's whole-form PATCH of S with the routing it has.
	mustStatus(t, f.patchET(f.sSlug,
		`{"name":"Soporte técnico","routing_mode":"round_robin","rr_strategy":"even","location_type":"livekit","location_value":"","duration_minutes":40}`),
		http.StatusOK, "whole-form PATCH of S")
	if got := f.hosts(f.sID); !slices.Equal(got, []string{"s1:rotation:0"}) {
		t.Errorf("S rotation after its PATCH = %v", got)
	}

	// Holders: every mutation refused.
	mustExec(t, f.db, `INSERT INTO event_types (id, user_id, slug, name, duration_minutes, is_active) VALUES ('hold', ?, 'holder-x', 'Preguntas retiradas', 30, 0)`, f.ownerID)
	mustExec(t, f.db, `INSERT INTO fork_event_type_links (copy_id, template_id, user_id, kind, created_at) VALUES ('hold', ?, ?, 'holder', 'x')`, f.tID, f.ownerID)
	want409(f.patchET("holder-x", `{"name":"y"}`), "patch holder", "preguntas retiradas")

	// Workspace ownership transfer waits until the predefined types are unset.
	transfer := func() *httptest.ResponseRecorder {
		return f.call(h.TeamTransferOwnershipGuard(h.TransferOwnership), http.MethodPost, "/v1/users/m1/transfer-ownership", "", f.ownerKey, "id", "m1")
	}
	want409(transfer(), "transfer ownership", "Primero quita los tipos predefinidos")
	f.mustSettings(`{"mentoria_template_id":null,"soporte_shared_id":null}`)
	mustStatus(t, transfer(), http.StatusOK, "transfer ownership after unsetting")
}

func TestTeamRole_matrix(t *testing.T) {
	f := newTeamFixture(t)
	seedRoleUser(t, f.db, "adm", "adm@example.com", 1, 0, "adm-key")
	seedRoleUser(t, f.db, "adm2", "adm2@example.com", 1, 0, "")
	addMember(t, f.db, "m1", "UTC")
	addMember(t, f.db, "m2", "UTC")
	area := func(id string) string { return f.scalar(`SELECT area FROM fork_member_areas WHERE user_id = ?`, id) }
	isAdmin := func(id string) string { return f.scalar(`SELECT is_admin FROM users WHERE id = ?`, id) }

	// Owner: tier and área of anyone else.
	mustStatus(t, f.setRole(f.ownerKey, "m1", "admin", "soporte"), http.StatusOK, "owner makes m1 admin + soporte")
	if isAdmin("m1") != "1" || area("m1") != "soporte" {
		t.Errorf("m1 = admin %s, área %s", isAdmin("m1"), area("m1"))
	}
	mustStatus(t, f.setRole(f.ownerKey, "m1", "member", "mentoria"), http.StatusOK, "owner makes m1 a mentor")
	if isAdmin("m1") != "0" || area("m1") != "mentoria" {
		t.Errorf("m1 = admin %s, área %s", isAdmin("m1"), area("m1"))
	}
	// Admin: only the área of a non-admin member.
	mustStatus(t, f.setRole("adm-key", "m2", "member", "soporte"), http.StatusOK, "admin sets m2's área")
	for name, c := range map[string]struct {
		key, target, tier, area string
		want                    int
	}{
		"admin promotes":      {"adm-key", "m2", "admin", "soporte", http.StatusForbidden},
		"admin targets admin": {"adm-key", "adm2", "member", "soporte", http.StatusForbidden},
		"admin targets self":  {"adm-key", "adm", "member", "soporte", http.StatusForbidden},
		"admin targets owner": {"adm-key", f.ownerID, "owner", "soporte", http.StatusForbidden},
		"member":              {"member-key-m1", "m2", "member", "", http.StatusForbidden},
		"bad área":            {f.ownerKey, "m2", "member", "ventas", http.StatusBadRequest},
		"owner demotes self":  {f.ownerKey, f.ownerID, "member", "", http.StatusBadRequest},
		"owner bad tier":      {f.ownerKey, "m2", "owner", "", http.StatusBadRequest},
	} {
		if rec := f.setRole(c.key, c.target, c.tier, c.area); rec.Code != c.want {
			t.Errorf("%s: %d; want %d — %s", name, rec.Code, c.want, rec.Body.String())
		}
	}
	// The owner's own área; not Mentoría while their type is the template.
	body := mustJSON(t, f.setRole(f.ownerKey, f.ownerID, "", "soporte"), http.StatusOK, "owner's own área")
	if body["tier"] != "owner" || area(f.ownerID) != "soporte" {
		t.Errorf("owner's own área: %v, stored %q", body, area(f.ownerID))
	}
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_shared_id":"` + f.sID + `"}`)
	mustStatus(t, f.setRole(f.ownerKey, f.ownerID, "", "mentoria"), http.StatusBadRequest, "owner as mentor of their own template")

	// Leaving an área reports the upcoming sessions left behind in it.
	mustExec(t, f.db, `INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('up1', ?, 'm2', '2099-05-01T10:00:00Z', '2099-05-01T10:30:00Z', 'confirmed')`, f.sID)
	body = mustJSON(t, f.setRole("adm-key", "m2", "member", "mentoria"), http.StatusOK, "m2 soporte -> mentoria")
	if body["upcoming_in_previous_area"] != float64(1) || body["area"] != "mentoria" || body["tier"] != "member" {
		t.Errorf("answer = %v; want upcoming_in_previous_area 1", body)
	}
	if f.copyOf(f.tID, "m2").id == "" {
		t.Error("the new mentor got no copy (the role change must reconcile)")
	}
}

func inviteToken(t *testing.T, body map[string]any) string {
	t.Helper()
	u := mustString(t, body, "invite_url", "invite")
	return u[strings.LastIndex(u, "/")+1:]
}

func TestTeamInvites_roleAppliedOnClaim(t *testing.T) {
	f := newTeamFixture(t)
	h := f.h
	seedRoleUser(t, f.db, "adm", "adm@example.com", 1, 0, "adm-key")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	invite := func(key, body string) *httptest.ResponseRecorder {
		return f.call(h.TeamCreateInvite(h.CreateInvite), http.MethodPost, "/v1/invites", body, key)
	}
	claim := func(token, name string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/invites/"+token+"/claim",
			strings.NewReader(`{"name":"`+name+`","password":"una-clave-larga-2026","timezone":"America/Lima"}`))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("token", token)
		rec := httptest.NewRecorder()
		h.TeamReconcileAfter(h.ClaimInvite)(rec, req)
		return mustJSON(t, rec, http.StatusCreated, "claim "+name)
	}
	storedRole := func(email string) string {
		return f.scalar(`SELECT role FROM fork_invite_roles WHERE email = ?`, email)
	}

	// Owner invites an admin; an admin re-inviting that email cannot downgrade it.
	adminInv := mustCreated(t, invite(f.ownerKey, `{"email":"Nuevo@Example.com","role":"admin"}`), "owner invites admin")
	if adminInv["role"] != "admin" || storedRole("nuevo@example.com") != "admin" {
		t.Errorf("admin invite: answer role %v, stored %q", adminInv["role"], storedRole("nuevo@example.com"))
	}
	again := mustCreated(t, invite("adm-key", `{"email":"nuevo@example.com","role":"mentoria"}`), "admin re-invites")
	if again["role"] != "admin" || storedRole("nuevo@example.com") != "admin" {
		t.Errorf("a non-owner overwrote the owner's admin row: answer %v, stored %q", again["role"], storedRole("nuevo@example.com"))
	}
	mustStatus(t, invite("adm-key", `{"email":"x@example.com","role":"admin"}`), http.StatusForbidden, "admin invites an admin")
	mustStatus(t, invite(f.ownerKey, `{"email":"x@example.com","role":"jefe"}`), http.StatusBadRequest, "unknown role")

	mentorInv := mustCreated(t, invite("adm-key", `{"email":"mentor@example.com","role":"mentoria"}`), "admin invites a mentor")
	revoked := mustCreated(t, invite("adm-key", `{"email":"gone@example.com","role":"soporte"}`), "invite to revoke")

	// The list carries each invite's role.
	rec := f.call(h.TeamListInvites(h.ListInvites), http.MethodGet, "/v1/invites", "", f.ownerKey)
	mustStatus(t, rec, http.StatusOK, "list invites")
	var items []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &items)
	roles := map[string]any{}
	for _, it := range items {
		roles[it["email"].(string)] = it["role"]
	}
	if roles["nuevo@example.com"] != "admin" || roles["mentor@example.com"] != "mentoria" || roles["gone@example.com"] != "soporte" {
		t.Errorf("invite roles = %v", roles)
	}
	// Revoking removes the role too.
	revokedID := mustString(t, revoked, "id", "invite")
	mustStatus(t, f.call(h.TeamRevokeInvite(h.RevokeInvite), http.MethodDelete, "/v1/invites/"+revokedID, "", f.ownerKey, "id", revokedID), http.StatusOK, "revoke")
	if storedRole("gone@example.com") != "" {
		t.Error("a revoked invite kept its role")
	}

	// Claims: admin (its creator is still the owner), and a mentor who gets a copy at once.
	adminID := mustString(t, claim(inviteToken(t, again), "Nueva Admin"), "user_id", "claim")
	if f.scalar(`SELECT is_admin FROM users WHERE id = ?`, adminID) != "1" || storedRole("nuevo@example.com") != "" {
		t.Error("the admin invite was not applied (or its row not deleted)")
	}
	mentorID := mustString(t, claim(inviteToken(t, mentorInv), "Mentora Nueva"), "user_id", "claim")
	if f.scalar(`SELECT area FROM fork_member_areas WHERE user_id = ?`, mentorID) != "mentoria" {
		t.Error("the mentor invite did not set the área")
	}
	if c := f.copyOf(f.tID, mentorID); c.id == "" || !c.active {
		t.Errorf("the claimed mentor has no active copy: %+v", c)
	}

	// An admin row whose creator is no longer the owner grants nothing.
	stale := mustCreated(t, invite(f.ownerKey, `{"email":"stale@example.com","role":"admin"}`), "stale admin invite")
	mustExec(t, f.db, `UPDATE fork_invite_roles SET created_by = 'adm' WHERE email = 'stale@example.com'`)
	staleID := mustString(t, claim(inviteToken(t, stale), "Sin Admin"), "user_id", "claim")
	if f.scalar(`SELECT is_admin FROM users WHERE id = ?`, staleID) != "0" {
		t.Error("an admin grant from a non-owner was applied")
	}
}

type areaListResp struct {
	Items []struct {
		ID   string `json:"id"`
		Area string `json:"area"`
	} `json:"items"`
	Total int `json:"total"`
}

func TestTeamBookings_areaFilterAndPublicShape(t *testing.T) {
	f := newTeamFixture(t)
	oSlug, oID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	_ = oSlug
	addMember(t, f.db, "m1", "UTC")
	f.mustRole("m1", "member", "mentoria")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_shared_id":"` + f.sID + `"}`)
	c := f.copyOf(f.tID, "m1")
	for _, b := range []struct{ id, et, host, day string }{
		{"bT", f.tID, f.ownerID, "10"}, {"bC", c.id, "m1", "11"}, {"bS", f.sID, f.ownerID, "12"}, {"bO", oID, f.ownerID, "13"},
	} {
		mustExec(t, f.db, `INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, location_type, location_value)
			VALUES (?, ?, ?, ?, ?, 'confirmed', 'livekit', 'https://example.com/room')`,
			b.id, b.et, b.host, "2099-03-"+b.day+"T10:00:00Z", "2099-03-"+b.day+"T10:30:00Z")
	}
	list := func(key, query string) areaListResp {
		t.Helper()
		rec := f.call(f.h.ListBookings, http.MethodGet, "/v1/bookings"+query, "", key)
		mustStatus(t, rec, http.StatusOK, "list "+query)
		var out areaListResp
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	ids := func(r areaListResp) []string {
		var out []string
		for _, it := range r.Items {
			out = append(out, it.ID)
		}
		slices.Sort(out)
		return out
	}
	for query, want := range map[string][]string{
		"?scope=all&area=mentoria":                           {"bC", "bT"},
		"?scope=all&area=soporte":                            {"bS"},
		"?scope=all&event_type=" + f.tSlug:                   {"bC", "bT"}, // the template's whole family
		"?scope=all&event_type=" + f.tSlug + "&area=soporte": nil,
		"?scope=all&event_type=" + c.slug:                    {"bC"},
	} {
		if got := ids(list(f.ownerKey, query)); !slices.Equal(got, want) {
			t.Errorf("%s: %v; want %v", query, got, want)
		}
	}
	all := list(f.ownerKey, "?scope=all")
	areas := map[string]string{}
	for _, it := range all.Items {
		areas[it.ID] = it.Area
	}
	if areas["bT"] != "mentoria" || areas["bC"] != "mentoria" || areas["bS"] != "soporte" || areas["bO"] != "" {
		t.Errorf("item areas = %v", areas)
	}
	// A mentor still sees only their own, whatever the filter.
	if got := ids(list("member-key-m1", "?scope=all&area=mentoria")); !slices.Equal(got, []string{"bC"}) {
		t.Errorf("mentor's area=mentoria: %v; want only their own bC", got)
	}
	mustStatus(t, f.call(f.h.ListBookings, http.MethodGet, "/v1/bookings?area=ventas", "", f.ownerKey), http.StatusBadRequest, "bad area")

	// The public booking endpoint keeps exactly the upstream keys.
	req := httptest.NewRequest(http.MethodGet, "/v1/bookings/bC", nil)
	req.SetPathValue("id", "bC")
	rec := httptest.NewRecorder()
	f.h.GetBooking(rec, req)
	pub := mustJSON(t, rec, http.StatusOK, "public GET booking")
	var keys []string
	for k := range pub {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"created_at", "end_at", "event_type_id", "host_id", "id", "location_type", "location_value", "start_at", "status", "updated_at"}
	if !slices.Equal(keys, want) {
		t.Errorf("public booking keys = %v; want %v", keys, want)
	}
}

func TestTeamSettings_validationAndRead(t *testing.T) {
	f := newTeamFixture(t)
	seedRoleUser(t, f.db, "adm", "adm@example.com", 1, 0, "adm-key")
	addMember(t, f.db, "m1", "UTC")
	f.mustRole("m1", "member", "mentoria")
	plainSlug, plainID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	_ = plainSlug
	mustExec(t, f.db, `UPDATE event_types SET location_type = 'zoom' WHERE id = ?`, plainID)
	mustExec(t, f.db, `INSERT INTO event_types (id, user_id, slug, name, duration_minutes, location_type) VALUES ('mine', 'm1', 'de-m1', 'De m1', 30, 'livekit')`)
	mustExec(t, f.db, `INSERT INTO event_types (id, user_id, slug, name, duration_minutes, location_type, archived_at) VALUES ('arch', ?, 'archivado', 'Archivado', 30, 'livekit', '2026-01-01')`, f.ownerID)

	mustStatus(t, f.putSettings("adm-key", `{"mentoria_template_id":"`+f.tID+`"}`), http.StatusForbidden, "admin PUT")
	for name, c := range map[string]struct{ body, contains string }{
		"same id":     {`{"mentoria_template_id":"` + f.tID + `","soporte_shared_id":"` + f.tID + `"}`, "distintos"},
		"not livekit": {`{"mentoria_template_id":"` + plainID + `"}`, "sala de video"},
		"not owner's": {`{"soporte_shared_id":"mine"}`, "no es tuyo"},
		"archived":    {`{"soporte_shared_id":"arch"}`, "archivado"},
		"missing":     {`{"soporte_shared_id":"nope"}`, "no existe"},
	} {
		body := mustJSON(t, f.putSettings(f.ownerKey, c.body), http.StatusBadRequest, name)
		if msg, _ := body["error"].(string); !strings.Contains(msg, c.contains) {
			t.Errorf("%s: %q; want %q", name, msg, c.contains)
		}
	}
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	c := f.copyOf(f.tID, "m1")
	body := mustJSON(t, f.putSettings(f.ownerKey, `{"soporte_shared_id":"`+c.id+`"}`), http.StatusBadRequest, "copy as S")
	if msg, _ := body["error"].(string); !strings.Contains(msg, "copia de un mentor") {
		t.Errorf("copy as S: %q", msg)
	}
	// Validate on change: T's stored location no longer livekit, re-sent unchanged = fine.
	mustExec(t, f.db, `UPDATE event_types SET location_type = 'zoom' WHERE id = ?`, f.tID)
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_shared_id":"` + f.sID + `"}`)
	mustExec(t, f.db, `UPDATE event_types SET location_type = 'livekit' WHERE id = ?`, f.tID)

	adminView := mustJSON(t, f.call(f.h.GetTeamSettings, http.MethodGet, "/v1/team/settings", "", "adm-key"), http.StatusOK, "admin GET")
	tmpl, _ := adminView["mentoria_template"].(map[string]any)
	if adminView["can_edit"] != false || tmpl["copies"] != float64(1) || tmpl["copy_links"] != nil || tmpl["slug"] != f.tSlug {
		t.Errorf("admin view = %v", adminView)
	}
	if sup, _ := adminView["soporte_shared"].(map[string]any); sup["id"] != f.sID {
		t.Errorf("soporte_shared = %v", adminView["soporte_shared"])
	}
	ownerView := mustJSON(t, f.call(f.h.GetTeamSettings, http.MethodGet, "/v1/team/settings", "", f.ownerKey), http.StatusOK, "owner GET")
	links, _ := ownerView["mentoria_template"].(map[string]any)["copy_links"].([]any)
	if len(links) != 1 {
		t.Fatalf("owner copy_links = %v", links)
	}
	if l := links[0].(map[string]any); l["slug"] != c.slug || l["mentor_name"] != "Mentor m1" || l["active"] != true || !strings.HasSuffix(l["url"].(string), "/book/"+c.slug) {
		t.Errorf("copy link = %v", l)
	}
	mustStatus(t, f.call(f.h.GetTeamSettings, http.MethodGet, "/v1/team/settings", "", "member-key-m1"), http.StatusForbidden, "member GET")

	// Users list and /me carry área and the personal link.
	rec := f.call(f.h.ListUsers, http.MethodGet, "/v1/users", "", f.ownerKey)
	mustStatus(t, rec, http.StatusOK, "list users")
	var users []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &users)
	for _, u := range users {
		switch u["id"] {
		case "m1":
			pl, _ := u["personal_link"].(map[string]any)
			if u["area"] != "mentoria" || pl["slug"] != c.slug || pl["active"] != true {
				t.Errorf("m1 row = area %v, personal_link %v", u["area"], u["personal_link"])
			}
		case f.ownerID:
			if pl, _ := u["personal_link"].(map[string]any); pl["slug"] != f.tSlug {
				t.Errorf("owner's personal link = %v; want the template", u["personal_link"])
			}
		case "adm":
			if u["personal_link"] != nil || u["area"] != "" {
				t.Errorf("adm row = %v", u)
			}
		}
	}
	me := mustJSON(t, f.call(f.h.GetMe, http.MethodGet, "/v1/users/me", "", "member-key-m1"), http.StatusOK, "me")
	if pl, _ := me["personal_link"].(map[string]any); me["area"] != "mentoria" || pl["slug"] != c.slug {
		t.Errorf("/me = area %v, personal_link %v", me["area"], me["personal_link"])
	}
}

func TestTeamWebhooks_guardsAndEventTypeList(t *testing.T) {
	f := newTeamFixture(t)
	h := f.h
	addMember(t, f.db, "m1", "UTC")
	f.mustRole("m1", "member", "mentoria")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	c := f.copyOf(f.tID, "m1")
	create := func(key, body string) *httptest.ResponseRecorder {
		return f.call(h.TeamWebhookGuard(h.CreateWebhook), http.MethodPost, "/v1/webhooks", body, key)
	}

	body := mustJSON(t, create("member-key-m1", `{"url":"https://example.com/hook","events":["booking.created"],"fields":["id","whatsapp_message"]}`), http.StatusForbidden, "member selects whatsapp_message")
	if msg, _ := body["error"].(string); !strings.Contains(msg, "propietario") {
		t.Errorf("member whatsapp_message: %q", msg)
	}
	body = mustJSON(t, create(f.ownerKey, `{"url":"https://example.com/hook","events":["booking.created"],"event_type_ids":["`+c.id+`"]}`), http.StatusBadRequest, "filter lists a copy")
	if msg, _ := body["error"].(string); !strings.Contains(msg, "copia de un mentor") || !strings.Contains(msg, "«Mentoría privada»") {
		t.Errorf("copy in filter: %q", msg)
	}
	wh := mustCreated(t, create(f.ownerKey, `{"url":"https://example.com/hook","events":["booking.created"],"fields":["id","whatsapp_message"],"event_type_ids":["`+f.tID+`"]}`), "owner webhook on T")
	whID := mustString(t, wh, "id", "create webhook")

	// On change only: a copy already stored in the filter may be re-sent.
	mustExec(t, f.db, `INSERT INTO webhook_event_type_filters (webhook_id, event_type_id) VALUES (?, ?)`, whID, c.id)
	patch := func(key, id, body string) *httptest.ResponseRecorder {
		return f.call(h.TeamWebhookGuard(h.PatchWebhook), http.MethodPatch, "/v1/webhooks/"+id, body, key, "id", id)
	}
	mustStatus(t, patch(f.ownerKey, whID, `{"event_type_ids":["`+f.tID+`","`+c.id+`"]}`), http.StatusNoContent, "re-send stored copy")
	// A member's webhook that already carries whatsapp_message keeps saving.
	mustExec(t, f.db, `INSERT INTO webhooks (id, user_id, url, events, secret_enc, fields) VALUES ('mwh', 'm1', 'https://example.com/m', '["booking.created"]', 'x', '["id","whatsapp_message"]')`)
	mustStatus(t, patch("member-key-m1", "mwh", `{"fields":["id","whatsapp_message"]}`), http.StatusNoContent, "member re-sends stored field")

	// The panel's list names templates with their copies and never offers a copy.
	rec := f.call(h.ListWebhookEventTypes, http.MethodGet, "/v1/webhooks/event-types", "", f.ownerKey)
	mustStatus(t, rec, http.StatusOK, "webhook event types")
	var out struct {
		Items []struct {
			ID     string `json:"id"`
			Copies int    `json:"copies"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	sawT := false
	for _, it := range out.Items {
		if it.ID == c.id {
			t.Error("the webhook list offers a mentor's copy")
		}
		if it.ID == f.tID {
			sawT = true
			if it.Copies != 1 {
				t.Errorf("T copies = %d; want 1", it.Copies)
			}
		}
	}
	if !sawT {
		t.Error("the template is missing from the webhook list")
	}
}

func TestTeamWhatsApp_copyInheritsAndPreviewNames(t *testing.T) {
	f := newTeamFixture(t)
	h := f.h
	addMember(t, f.db, "m1", "UTC")
	f.mustRole("m1", "member", "mentoria")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_shared_id":"` + f.sID + `"}`)
	c := f.copyOf(f.tID, "m1")
	mustStatus(t, f.call(h.PutWhatsAppMessages, http.MethodPut, "/", `{"created":"Hola {nombre}, soy {mentor}"}`, f.ownerKey, "slug", f.tSlug), http.StatusOK, "save T texts")

	for _, key := range []string{f.ownerKey, "member-key-m1"} {
		got := mustJSON(t, f.call(h.GetWhatsAppMessages, http.MethodGet, "/", "", key, "slug", c.slug), http.StatusOK, "GET copy texts")
		from, _ := got["inherited_from"].(map[string]any)
		if got["created"] != "Hola {nombre}, soy {mentor}" || got["read_only"] != true || from["slug"] != f.tSlug || from["name"] != "Mentoría privada" {
			t.Errorf("copy texts = %v", got)
		}
	}
	for slug, want := range map[string]string{f.tSlug: "Nombre del mentor", f.sSlug: "Nombre de quien atiende"} {
		got := mustJSON(t, f.call(h.PreviewWhatsAppMessages, http.MethodPost, "/", `{"created":"Soy {mentor}"}`, f.ownerKey, "slug", slug), http.StatusOK, "preview "+slug)
		if got["created"] != "Soy "+want {
			t.Errorf("preview of %s = %q; want %q", slug, got["created"], "Soy "+want)
		}
	}
}
