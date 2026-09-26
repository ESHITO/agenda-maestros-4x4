package handler_test

// Fork (Agenda Maestros 4x4): regressions from the team-feature review - the guards
// decode a body exactly as the wrapped handler does (trailing bytes included), and a
// reassign never leaves a client's answer on a copy that the orphan rule may delete.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// Each guard sees what the handler's json.Decoder sees: a valid object followed by stray
// bytes is still checked (it used to skip the guard, while the handler accepted it).
func TestTeamGuards_trailingBytesAreStillChecked(t *testing.T) {
	rf := newTeamReassignFixture(t)
	f := rf.teamFixture
	h := f.h
	adm := supAddAdmin(t, f, "adm")

	// Webhooks: a member selecting whatsapp_message, the owner listing a copy.
	create := func(key, body string) *httptest.ResponseRecorder {
		return f.call(h.TeamWebhookGuard(h.CreateWebhook), http.MethodPost, "/v1/webhooks", body, key)
	}
	if rec := create("member-key-m1", `{"url":"https://example.com/hook","events":["booking.created"],"fields":["id","whatsapp_message"],"event_type_ids":["`+rf.c1.id+`"]} x`); rec.Code != http.StatusForbidden {
		t.Errorf("member whatsapp_message + trailing byte: %d %s; want 403", rec.Code, rec.Body.String())
	}
	if got := f.scalar(`SELECT COUNT(*) FROM webhooks WHERE user_id = 'm1'`); got != "0" {
		t.Errorf("the member's webhook was stored (%s)", got)
	}
	if rec := create(f.ownerKey, `{"url":"https://example.com/hook","events":["booking.created"],"event_type_ids":["`+rf.c1.id+`"]}`+"\nx"); rec.Code != http.StatusBadRequest ||
		!strings.Contains(errorOf(t, rec), "copia de un mentor") {
		t.Errorf("copy filter + trailing byte: %d %q; want 400 about the copy", rec.Code, errorOf(t, rec))
	}
	mustExec(t, f.db, `INSERT INTO webhooks (id, user_id, url, events, secret_enc, fields) VALUES ('mwh', 'm1', 'https://example.com/m', '["booking.created"]', 'x', '["id"]')`)
	patch := f.call(h.TeamWebhookGuard(h.PatchWebhook), http.MethodPatch, "/v1/webhooks/mwh",
		`{"fields":["id","whatsapp_message"]} x`, "member-key-m1", "id", "mwh")
	if patch.Code != http.StatusForbidden {
		t.Errorf("member PATCH whatsapp_message + trailing byte: %d %s; want 403", patch.Code, patch.Body.String())
	}
	if got := f.scalar(`SELECT fields FROM webhooks WHERE id = 'mwh'`); got != `["id"]` {
		t.Errorf("the member's fields changed to %s", got)
	}
	// A body the decoder cannot read at all still gets the handler's own 400.
	mustStatus(t, create("member-key-m1", `{"url":`), http.StatusBadRequest, "malformed webhook body")

	// T: a location change with trailing bytes is refused, and nothing reaches the copies.
	rec := f.patchET(f.tSlug, `{"location_type":"in_person","location_value":"Calle 1"}`+"\nx")
	if rec.Code != http.StatusConflict || !strings.Contains(errorOf(t, rec), "sala de video") {
		t.Errorf("T location + trailing byte: %d %q; want 409", rec.Code, errorOf(t, rec))
	}
	if rec := f.patchET(f.sSlug, `{"routing_mode":"round_robin"}x`); rec.Code != http.StatusConflict {
		t.Errorf("S routing + trailing byte: %d %s; want 409", rec.Code, rec.Body.String())
	}
	if got := f.scalar(`SELECT location_type FROM event_types WHERE id = ?`, f.tID); got != "livekit" {
		t.Errorf("T location_type = %q; want livekit", got)
	}
	if got := f.copyOf(f.tID, "m1"); got.locType != "livekit" {
		t.Errorf("copy location_type = %q; want livekit", got.locType)
	}

	// Reassign: an S booking to someone outside soporte, by an admin.
	supBooking(t, f, "bS", rf.s5.id, "m5", "2099-03-12T10:00:00Z", "2099-03-12T10:30:00Z")
	re := f.call(h.TeamReassignGuard(h.ReassignBooking), http.MethodPost, "/v1/bookings/bS/reassign",
		`{"host_id":"m1"} x`, adm, "id", "bS")
	if re.Code != http.StatusBadRequest || !strings.Contains(errorOf(t, re), "no tiene un enlace de Soporte activo") {
		t.Errorf("S reassign + trailing byte: %d %q; want 400 (same-área rule)", re.Code, errorOf(t, re))
	}
	if got := f.scalar(`SELECT host_id FROM bookings WHERE id = 'bS'`); got != "m5" {
		t.Errorf("S booking host = %q; want m5", got)
	}
	re = f.call(h.TeamReassignGuard(h.ReassignBooking), http.MethodPost, "/v1/bookings/bS/reassign",
		`{"host_id":`, adm, "id", "bS")
	if re.Code != http.StatusBadRequest || errorOf(t, re) != "Elige a la persona que atenderá la sesión." {
		t.Errorf("malformed reassign body: %d %q; want the handler's 400, in Spanish", re.Code, errorOf(t, re))
	}

	// Invites: an admin inviting an admin; a role that is not a string.
	seedRoleUser(t, f.db, "adm2", "adm2@example.com", 1, 0, "adm2-key")
	invite := func(key, body string) *httptest.ResponseRecorder {
		return f.call(h.TeamCreateInvite(h.CreateInvite), http.MethodPost, "/v1/invites", body, key)
	}
	mustStatus(t, invite("adm2-key", `{"email":"x@example.com","role":"admin"} x`), http.StatusForbidden, "admin invites an admin + trailing byte")
	mustStatus(t, invite(f.ownerKey, `{"email":"y@example.com","role":5}`), http.StatusBadRequest, "role of the wrong type")
	if got := f.scalar(`SELECT COUNT(*) FROM invite_tokens WHERE email IN ('x@example.com', 'y@example.com')`); got != "0" {
		t.Errorf("a refused invite was created (%s)", got)
	}
}

// The review's scenario: T stops being the template, its question is deleted, a session on
// a mentor's T copy passes to the owner (the answer cannot follow), then the mentor is
// deleted. The answer must survive: parked on T's holder by the reassign, and the orphan
// rule never deletes a copy whose questions still carry answers.
func TestTeamReassign_orphanCopyKeepsMovedAnswers(t *testing.T) {
	rf := newTeamReassignFixture(t)
	f := rf.teamFixture
	h := f.h
	_, t2ID := seedEventTypeHTTP(t, h, f.ownerKey)
	mustExec(t, f.db, `UPDATE event_types SET location_type = 'livekit', location_value = '' WHERE id = ?`, t2ID)
	f.mustSettings(`{"mentoria_template_id":"` + t2ID + `","soporte_template_id":"` + f.sID + `"}`)
	cq := rf.copyQuestion(rf.c1.id)
	mustStatus(t, f.call(f.guard(handler.TeamOpQuestions, h.DeleteQuestion), http.MethodDelete,
		"/v1/event-types/"+f.tSlug+"/questions/"+rf.tq, "", f.ownerKey, "slug", f.tSlug, "id", rf.tq), http.StatusNoContent, "delete T's question")

	mustStatus(t, rf.reassign(f.ownerKey, "bC", f.ownerID), http.StatusOK, "reassign to the owner")
	holder := f.scalar(`SELECT copy_id FROM fork_event_type_links WHERE template_id = ? AND kind = 'holder'`, f.tID)
	if holder == "" {
		t.Fatal("the reassign parked nothing: T has no holder")
	}
	if got := f.scalar(`SELECT event_type_id FROM event_type_questions WHERE id = ?`, cq); got != holder {
		t.Errorf("the unmapped copy question lives on %q; want T's holder %q", got, holder)
	}
	if got := f.scalar(`SELECT COUNT(*) FROM fork_question_links WHERE copy_question_id = ?`, cq); got != "0" {
		t.Error("the parked question is still linked to the copy")
	}

	mustStatus(t, f.call(h.TeamReconcileAfter(h.DeleteUser), http.MethodDelete, "/v1/users/m1", "", f.ownerKey, "id", "m1"), http.StatusOK, "delete m1")
	if got := f.scalar(`SELECT value FROM booking_answers WHERE id = 'ans-bC'`); got != "Ventas" {
		t.Errorf("the client's answer was lost after deleting the mentor: %q", got)
	}

	// Without the parking (an answer on a copy question, its booking elsewhere), the orphan
	// copy is kept inactive instead of deleted.
	cq2 := rf.copyQuestion(rf.c2.id)
	_, oID := seedEventTypeHTTP(t, h, f.ownerKey)
	supBooking(t, f, "bX", oID, f.ownerID, "2099-04-10T10:00:00Z", "2099-04-10T10:30:00Z")
	mustExec(t, f.db, `INSERT INTO booking_answers (id, booking_id, question_id, value) VALUES ('ans-bX', 'bX', ?, 'Marketing')`, cq2)
	mustExec(t, f.db, `DELETE FROM users WHERE id = 'm2'`)
	if _, err := h.ReconcileTeam(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got := f.scalar(`SELECT is_active FROM event_types WHERE id = ?`, rf.c2.id); got != "0" {
		t.Errorf("orphan copy with an answered question: is_active = %q; want it kept, inactive", got)
	}
	if got := f.scalar(`SELECT value FROM booking_answers WHERE id = 'ans-bX'`); got != "Marketing" {
		t.Errorf("the answer on the orphan copy's question was lost: %q", got)
	}
}
