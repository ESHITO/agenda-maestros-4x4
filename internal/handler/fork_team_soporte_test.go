package handler_test

// Fork (Agenda Maestros 4x4): the Soporte template works exactly like the Mentoría one -
// a support person's copy is booked like a mentor's, reaches the owner's webhooks that
// list S (the template-aware filter), sends S's WhatsApp texts, and the bookings list's
// notice status agrees with that delivery. Also the "team" object of S and its copies.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTeamSoporte_copyBookingReachesOwnerWebhooksWithSTexts(t *testing.T) {
	f := newTeamFixture(t)
	h := f.h
	addMember(t, f.db, "s1", "America/Lima")
	mustExec(t, f.db, `UPDATE users SET name = 'Luis Soporte' WHERE id = 's1'`)
	seedFullAvailabilityDB(t, f.db, "s1")
	f.mustRole("s1", "member", "soporte")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_template_id":"` + f.sID + `"}`)
	sc := f.copyOf(f.sID, "s1")
	mustStatus(t, f.call(h.PutWhatsAppMessages, http.MethodPut, "/", `{"created":"Soporte para {nombre} con {mentor}"}`,
		f.ownerKey, "slug", f.sSlug), http.StatusOK, "save S texts")

	// The owner's four FunnelChat webhooks, each limited to S; and one limited to T.
	for _, ev := range []string{"booking.created", "booking.reminder_morning", "booking.reminder_1h", "booking.reminder_5m"} {
		id := seedReminderWebhook(t, f.db, f.ownerID, ev, []string{"id", "event_type_slug", "whatsapp_message"})
		mustExec(t, f.db, `INSERT INTO webhook_event_type_filters (webhook_id, event_type_id) VALUES (?, ?)`, id, f.sID)
	}
	mustExec(t, f.db, `INSERT INTO webhooks (id, user_id, url, events, secret_enc, fields)
		VALUES ('wh-only-t', ?, 'https://hooks.example.com/t', '["booking.created"]', 'unused', '["id"]')`, f.ownerID)
	mustExec(t, f.db, `INSERT INTO webhook_event_type_filters (webhook_id, event_type_id) VALUES ('wh-only-t', ?)`, f.tID)

	id := bookInZone(t, h, sc.slug, futureAt(3, 15, 0), "America/Lima", "")
	if got := f.scalar(`SELECT host_id || '|' || event_type_id FROM bookings WHERE id = ?`, id); got != "s1|"+sc.id {
		t.Fatalf("booking on s1's copy = %s; want s1 hosting it on the copy", got)
	}
	data, _ := waitDeliveries(t, f.db, "wh-booking-created", 1)[0]["data"].(map[string]any)
	if msg, _ := data["whatsapp_message"].(string); msg != "Soporte para Ana con Luis Soporte" {
		t.Errorf("whatsapp_message = %q; want S's text, naming the support person", msg)
	}
	if data["event_type_slug"] != sc.slug {
		t.Errorf("event_type_slug = %v; want the copy's %s", data["event_type_slug"], sc.slug)
	}
	if got := f.scalar(`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = 'wh-only-t'`); got != "0" {
		t.Errorf("the webhook limited to T got %s deliveries of a Soporte booking", got)
	}

	// A booking of another type, for the contrast in the list.
	other := bookInZone(t, h, f.tSlug, futureAt(3, 18, 0), "America/Lima", "")

	// The list's notices agree: every moment of the copy's booking has a webhook (the
	// owner's, through S), while the T booking's reminders have none.
	rec := f.call(h.ListBookings, http.MethodGet, "/v1/bookings?scope=all", "", f.ownerKey)
	mustStatus(t, rec, http.StatusOK, "list bookings")
	var out struct {
		Items []struct {
			ID       string `json:"id"`
			Area     string `json:"area"`
			WhatsApp []struct {
				Kind   string `json:"kind"`
				Status string `json:"status"`
			} `json:"whatsapp"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, it := range out.Items {
		status := map[string]string{}
		for _, n := range it.WhatsApp {
			status[n.Kind] = n.Status
		}
		switch it.ID {
		case id:
			seen++
			if it.Area != "soporte" {
				t.Errorf("S copy booking área = %q; want soporte", it.Area)
			}
			for _, k := range []string{"created", "morning", "1h", "5m"} {
				if s := status[k]; s == "" || s == "not_applicable" {
					t.Errorf("S copy booking %s notice = %q; want one a webhook receives", k, s)
				}
			}
		case other:
			seen++
			if it.Area != "mentoria" {
				t.Errorf("T booking área = %q; want mentoria", it.Area)
			}
			if s := status["1h"]; s != "not_applicable" {
				t.Errorf("T booking 1h notice = %q; want not_applicable (no webhook takes T's reminders)", s)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("the list shows %d of the 2 bookings", seen)
	}
}

// The "team" object: S reads as soporte_template (copies, copy_links for the owner), a
// support person sees their copy as soporte_copy naming the template and themselves, the
// owner's list hides the copies.
func TestTeamEventTypes_soporteKinds(t *testing.T) {
	f := newTeamFixture(t)
	addMember(t, f.db, "s1", "UTC")
	addMember(t, f.db, "m1", "UTC")
	f.mustRole("s1", "member", "soporte")
	f.mustRole("m1", "member", "mentoria")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_template_id":"` + f.sID + `"}`)
	sc := f.copyOf(f.sID, "s1")
	c := f.copyOf(f.tID, "m1")

	for _, et := range listEventTypesTeam(t, f, f.ownerKey) {
		switch et.ID {
		case sc.id, c.id:
			t.Errorf("the owner's list shows the copy %s", et.Slug)
		case f.sID:
			if et.Team == nil || et.Team.Kind != "soporte_template" || et.Team.Copies == nil || *et.Team.Copies != 1 ||
				len(et.Team.CopyLinks) != 1 || et.Team.CopyLinks[0].Slug != sc.slug || et.Team.CopyLinks[0].MentorName != "Mentor s1" {
				t.Errorf("S team = %+v", et.Team)
			}
		case f.tID:
			if et.Team == nil || et.Team.Kind != "mentoria_template" || et.Team.Copies == nil || *et.Team.Copies != 1 {
				t.Errorf("T team = %+v", et.Team)
			}
		}
	}
	found := false
	for _, et := range listEventTypesTeam(t, f, "member-key-s1") {
		if et.ID == sc.id {
			found = true
			if et.Team == nil || et.Team.Kind != "soporte_copy" || et.Team.TemplateSlug != f.sSlug ||
				et.Team.TemplateName != "Soporte 1 a 1" || et.Team.MentorName != "Mentor s1" || et.Team.CopyLinks != nil {
				t.Errorf("the support person's copy team = %+v", et.Team)
			}
		}
		if et.ID == c.id {
			t.Error("the support person sees a mentor's copy")
		}
	}
	if !found {
		t.Error("the support person's list does not show their copy")
	}
	got := mustJSON(t, f.call(f.h.GetEventType, http.MethodGet, "/", "", "member-key-s1", "slug", sc.slug), http.StatusOK, "GET S copy")
	team, _ := got["team"].(map[string]any)
	if team["kind"] != "soporte_copy" || team["template_slug"] != f.sSlug {
		t.Errorf("GET S copy team = %v", got["team"])
	}
	adm := mustJSON(t, f.call(f.h.GetEventType, http.MethodGet, "/", "", f.ownerKey, "slug", f.sSlug), http.StatusOK, "GET S")
	if team, _ := adm["team"].(map[string]any); team["kind"] != "soporte_template" || strings.Contains(adm["routing_mode"].(string), "round") {
		t.Errorf("GET S = team %v, routing %v", adm["team"], adm["routing_mode"])
	}
}

// A copy's área is its template's (one value per template), so a type that already has
// copies can never become the template of the other área: swapping T and S, or making an
// old Mentoría template the Soporte one, would turn every existing mentor session into a
// Soporte one (and offer it to support people in "Pasar a otra persona"). A type with no
// copies may still move between áreas.
func TestTeamSettings_typeWithCopiesKeepsItsArea(t *testing.T) {
	f := newTeamFixture(t)
	both := func(tmpl, sup string) string {
		return `{"mentoria_template_id":"` + tmpl + `","soporte_template_id":"` + sup + `"}`
	}
	saved := func() string {
		return f.scalar(`SELECT group_concat(key || '=' || value, ',') FROM
			(SELECT key, value FROM fork_settings WHERE key IN ('team_mentoria_template_id', 'team_soporte_shared_id') ORDER BY key)`)
	}

	// Nobody attends yet: no copies, so the two types may swap freely.
	f.mustSettings(both(f.tID, f.sID))
	f.mustSettings(both(f.sID, f.tID))
	f.mustSettings(both(f.tID, f.sID))

	addMember(t, f.db, "m1", "UTC")
	addMember(t, f.db, "s1", "UTC")
	f.mustRole("m1", "member", "mentoria")
	f.mustRole("s1", "member", "soporte")
	mc, sc := f.copyOf(f.tID, "m1"), f.copyOf(f.sID, "s1")
	if !mc.active || !sc.active {
		t.Fatalf("copies: mentor %+v, soporte %+v", mc, sc)
	}
	before := saved()

	// The swap is refused, naming the reason, and nothing changes.
	rec := f.putSettings(f.ownerKey, both(f.sID, f.tID))
	mustStatus(t, rec, http.StatusBadRequest, "swap T and S with copies")
	if body := rec.Body.String(); !strings.Contains(body, "ya tiene copias de") {
		t.Errorf("swap refusal = %s; want it to say the type already has copies", body)
	}
	if got := saved(); got != before {
		t.Errorf("settings after the refused swap = %s; want %s", got, before)
	}
	if !f.copyOf(f.tID, "m1").active || f.copyOf(f.tID, "s1").id != "" || f.copyOf(f.sID, "m1").id != "" {
		t.Error("the refused swap touched the copies")
	}

	// An old Mentoría template (no longer a setting, its copy kept inactive) cannot become
	// the Soporte template either.
	_, t2ID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	mustExec(t, f.db, `UPDATE event_types SET location_type = 'livekit', location_value = '' WHERE id = ?`, t2ID)
	f.mustSettings(both(t2ID, f.sID))
	if f.copyOf(f.tID, "m1").active {
		t.Fatal("the old template's copy stayed active")
	}
	rec = f.putSettings(f.ownerKey, `{"soporte_template_id":"`+f.tID+`"}`)
	mustStatus(t, rec, http.StatusBadRequest, "old Mentoría template as S")
	if body := rec.Body.String(); !strings.Contains(body, "copias de Mentoría") {
		t.Errorf("refusal = %s; want it to name the Mentoría copies", body)
	}
	// ...nor may the Soporte template become the Mentoría one (alias key too).
	rec = f.putSettings(f.ownerKey, `{"mentoria_template_id":"`+f.sID+`","soporte_shared_id":"`+f.tID+`"}`)
	mustStatus(t, rec, http.StatusBadRequest, "S as T")
	if got := f.scalar(`SELECT area FROM fork_template_areas WHERE template_id = ?`, f.tID); got != "mentoria" {
		t.Errorf("the old template's recorded área = %q; want mentoria", got)
	}
}
