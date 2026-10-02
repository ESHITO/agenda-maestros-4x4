package handler_test

// Fork (Agenda Maestros 4x4): each person's WhatsApp number (fork_member_whatsapp.go) - who
// may set whose, the validation, who sees it in GET /v1/users - plus the webhook guard and
// the WhatsApp texts API for the host moments.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/webhook"
)

func TestMemberWhatsApp_permissionMatrix(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	a1 := addMember(t, database, "a1", "UTC")
	addMember(t, database, "a2", "UTC")
	mustExec(t, database, `UPDATE users SET is_admin = 1 WHERE id IN ('a1','a2')`)
	m1 := addMember(t, database, "m1", "America/Toronto")
	addMember(t, database, "m2", "UTC")
	addMember(t, database, "x1", "UTC")
	mustExec(t, database, `UPDATE users SET archived_at = '2026-09-01T00:00:00Z' WHERE id = 'x1'`)

	const ok = `{"phone":"+51 987 654 321"}`
	for _, c := range []struct {
		name, key, target string
		want              int
	}{
		{"owner → member", ownerKey, "m1", http.StatusOK},
		{"owner → admin", ownerKey, "a2", http.StatusOK},
		{"owner → themselves", ownerKey, ownerID, http.StatusOK},
		{"admin → member", a1, "m2", http.StatusOK},
		{"admin → themselves", a1, "a1", http.StatusOK},
		{"admin → owner", a1, ownerID, http.StatusForbidden},
		{"admin → another admin", a1, "a2", http.StatusForbidden},
		{"member → another member", m1, "m2", http.StatusForbidden},
		{"member → themselves by id (their profile uses /me)", m1, "m1", http.StatusForbidden},
		{"unknown person", ownerKey, "nobody", http.StatusNotFound},
		{"archived person", ownerKey, "x1", http.StatusNotFound},
	} {
		rec := putWhatsApp(t, h, c.key, c.target, ok)
		if rec.Code != c.want {
			t.Errorf("%s: %d; want %d — %s", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	if rec := putWhatsApp(t, h, a1, ownerID, ok); !strings.Contains(rec.Body.String(), "Solo el propietario cambia su propio WhatsApp.") {
		t.Errorf("admin → owner message: %s", rec.Body.String())
	}
	if rec := putWhatsApp(t, h, m1, "m2", ok); !strings.Contains(rec.Body.String(), "el tuyo se cambia en tu perfil") {
		t.Errorf("member message: %s", rec.Body.String())
	}
	if rec := putWhatsApp(t, h, "", "", ok); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", rec.Code)
	}

	got := mustJSON(t, putWhatsApp(t, h, ownerKey, "m2", `{"phone":"0051 (987) 654-321"}`), http.StatusOK, "owner sets m2")
	if got["user_id"] != "m2" || got["phone"] != "+51987654321" || got["whatsapp"] != "51987654321" ||
		got["country"] != "PE" || got["country_name"] != "Perú" || got["updated_at"] == nil {
		t.Errorf("answer = %v", got)
	}
}

func TestMemberWhatsApp_validationAndMe(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	m1 := addMember(t, database, "m1", "America/Toronto")
	for body, want := range map[string]string{
		`{}`:                        "Falta «phone»",
		`{"phone":"987654321"}`:     "Escribe el número con el código de país",
		`{"phone":"+999 123 4567"}`: "Ese código de país no existe.",
		`{"phone":12345678}`:        "Escribe el número con el código de país",
		`{`:                         "JSON inválido",
	} {
		rec := putWhatsApp(t, h, m1, "", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("PUT %s: %d %s; want 400 %q", body, rec.Code, rec.Body.String(), want)
		}
	}
	if n := scalarOf(t, database, `SELECT COUNT(*) FROM fork_member_phones`); n != "0" {
		t.Errorf("a refused number was stored (%s rows)", n)
	}

	// +1 is shared: the person's zone (Toronto) makes it Canada.
	got := mustJSON(t, putWhatsApp(t, h, m1, "", `{"phone":"+1 416 555 1234"}`), http.StatusOK, "m1 /me")
	if got["phone"] != "+14165551234" || got["country"] != "CA" || got["country_name"] != "Canadá" {
		t.Errorf("answer = %v", got)
	}
	req := authReq(http.MethodGet, "/v1/users/me/whatsapp", "", m1)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetMyWhatsApp)(rec, req)
	if got := mustJSON(t, rec, http.StatusOK, "GET /me"); got["phone"] != "+14165551234" {
		t.Errorf("GET /me = %v", got)
	}
	if n := scalarOf(t, database, `SELECT phone FROM fork_member_phones WHERE user_id = 'm1'`); n != "+14165551234" {
		t.Errorf("stored %q; want E.164", n)
	}

	for _, remove := range []string{`{"phone":null}`, `{"phone":"  "}`} {
		mustStatus(t, putWhatsApp(t, h, m1, "", `{"phone":"+51987654321"}`), http.StatusOK, "set again")
		got = mustJSON(t, putWhatsApp(t, h, m1, "", remove), http.StatusOK, "remove "+remove)
		if got["phone"] != nil || got["whatsapp"] != nil || got["country"] != nil || got["user_id"] != "m1" {
			t.Errorf("after %s: %v; want every field null", remove, got)
		}
		if n := scalarOf(t, database, `SELECT COUNT(*) FROM fork_member_phones WHERE user_id = 'm1'`); n != "0" {
			t.Errorf("%s left the row", remove)
		}
	}
}

// GET /v1/users: the number only for whoever may change it (or the person themselves);
// "has_whatsapp" for every admin.
func TestMemberWhatsApp_visibilityInTheMembersList(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	a1 := addMember(t, database, "a1", "UTC")
	addMember(t, database, "a2", "UTC")
	mustExec(t, database, `UPDATE users SET is_admin = 1 WHERE id IN ('a1','a2')`)
	addMember(t, database, "m1", "America/Lima")
	addMember(t, database, "m2", "UTC")
	mustExec(t, database, `INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES
		(?, '+51911111111', 'x'), ('a1', '+51922222222', 'x'), ('a2', '+51933333333', 'x'), ('m1', '+51944444444', 'x')`, ownerID)

	list := func(key string) map[string]map[string]any {
		t.Helper()
		req := authReq(http.MethodGet, "/v1/users", "", key)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.ListUsers)(rec, req)
		mustStatus(t, rec, http.StatusOK, "list users")
		var rows []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]any{}
		for _, r := range rows {
			out[r["id"].(string)] = r
		}
		return out
	}
	asAdmin := list(a1)
	for id, want := range map[string]struct {
		phone   any
		has     bool
		canEdit bool
	}{
		ownerID: {nil, true, false},
		"a1":    {"+51922222222", true, true},
		"a2":    {nil, true, false},
		"m1":    {"+51944444444", true, true},
		"m2":    {nil, false, true},
	} {
		r := asAdmin[id]
		if r["whatsapp_phone"] != want.phone || r["has_whatsapp"] != want.has || r["can_edit_whatsapp"] != want.canEdit {
			t.Errorf("admin sees %s: phone %v has %v can_edit %v; want %v %v %v", id,
				r["whatsapp_phone"], r["has_whatsapp"], r["can_edit_whatsapp"], want.phone, want.has, want.canEdit)
		}
	}
	if asAdmin["m1"]["whatsapp_country"] != "PE" || asAdmin["a2"]["whatsapp_country"] != nil {
		t.Errorf("whatsapp_country: m1 %v, a2 %v", asAdmin["m1"]["whatsapp_country"], asAdmin["a2"]["whatsapp_country"])
	}
	asOwner := list(ownerKey)
	for _, id := range []string{ownerID, "a1", "a2", "m1"} {
		if asOwner[id]["whatsapp_phone"] == nil || asOwner[id]["can_edit_whatsapp"] != true {
			t.Errorf("the owner must see and edit %s's number: %v", id, asOwner[id])
		}
	}
}

// The reconcile drops the numbers of deleted users; an archived person keeps theirs.
func TestMemberWhatsApp_reconcileDropsDeletedUsers(t *testing.T) {
	f := newTeamFixture(t)
	addMember(t, f.db, "m1", "UTC")
	mustExec(t, f.db, `UPDATE users SET archived_at = '2026-09-01T00:00:00Z' WHERE id = 'm1'`)
	mustExec(t, f.db, `INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES ('ghost', '+51900000000', 'x'), ('m1', '+51911111111', 'x')`)
	if _, err := f.h.ReconcileTeam(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if got := f.scalar(`SELECT group_concat(user_id) FROM fork_member_phones`); got != "m1" {
		t.Errorf("numbers after the reconcile: %q; want only m1's", got)
	}
}

// The guard: host notices in a webhook of their own, and only the owner selects the host's
// text (the same rule as the client's).
func TestTeamWebhookGuard_hostNotices(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	member := addMember(t, database, "m1", "UTC")
	passed := 0
	next := func(w http.ResponseWriter, _ *http.Request) { passed++; w.WriteHeader(http.StatusNoContent) }
	call := func(method, key, id, body string) *httptest.ResponseRecorder {
		t.Helper()
		path := "/v1/webhooks"
		if id != "" {
			path += "/" + id
		}
		req := authReq(method, path, body, key)
		if id != "" {
			req.SetPathValue("id", id)
		}
		rec := httptest.NewRecorder()
		h.RequireAuth(h.TeamWebhookGuard(next))(rec, req)
		return rec
	}
	rec := call(http.MethodPost, ownerKey, "", `{"url":"https://x.example.com","events":["booking.created","booking.host_created"]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Los avisos al anfitrión van en su propio webhook") {
		t.Errorf("mixed events: %d %s", rec.Code, rec.Body.String())
	}
	mustStatus(t, call(http.MethodPost, ownerKey, "", `{"url":"https://x.example.com","events":["booking.host_created"],"fields":["host_whatsapp","host_whatsapp_message"]}`),
		http.StatusNoContent, "owner, host notice alone")
	mustStatus(t, call(http.MethodPost, ownerKey, "", `{"url":"https://x.example.com","events":["booking.host_created","booking.host_reminder_5m"]}`),
		http.StatusNoContent, "both host notices together (one recipient)")
	rec = call(http.MethodPost, member, "", `{"url":"https://x.example.com","events":["booking.host_created"],"fields":["host_whatsapp_message"]}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("member selecting host_whatsapp_message: %d", rec.Code)
	}
	mustStatus(t, call(http.MethodPost, member, "", `{"url":"https://x.example.com","events":["booking.host_created"],"fields":["host_whatsapp"]}`),
		http.StatusNoContent, "member, host number only")

	// PATCH: mixing refused; no "events" = nothing to check.
	whID := seedReminderWebhook(t, database, ownerID, webhook.EventHostCreated, []string{"host_whatsapp"})
	rec = call(http.MethodPatch, ownerKey, whID, `{"events":["booking.host_reminder_5m","booking.reminder_5m"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PATCH mixing: %d", rec.Code)
	}
	mustStatus(t, call(http.MethodPatch, ownerKey, whID, `{"fields":["host_whatsapp","host_whatsapp_message"]}`), http.StatusNoContent, "PATCH fields only")
	if passed != 4 {
		t.Errorf("the handler ran %d times; want 4", passed)
	}
}

// The host moments in the texts API: GET/PUT carry them, a marker of the other audience is
// refused - only in a text that CHANGES - and the preview renders them in Go.
func TestWhatsAppMessagesAPI_hostMoments(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	h.SetPublicBaseURL("https://citas.example.com")
	slug, etID := seedEventTypeHTTP(t, h, key)
	mustExec(t, database, `UPDATE event_types SET location_type = 'livekit', location_value = '' WHERE id = ?`, etID)
	path := "/v1/event-types/" + slug + "/whatsapp-messages"
	put := func(body string) *httptest.ResponseRecorder {
		return slugCall(h, h.PutWhatsAppMessages, http.MethodPut, path, slug, body, key)
	}

	got := mustJSON(t, slugCall(h, h.GetWhatsAppMessages, http.MethodGet, path, slug, "", key), http.StatusOK, "get")
	defaults, _ := got["defaults"].(map[string]any)
	if got["host_created"] != "" || got["host_reminder_5m"] != "" {
		t.Errorf("fresh host moments = %v / %v", got["host_created"], got["host_reminder_5m"])
	}
	if d, _ := defaults["host_created"].(string); !strings.Contains(d, "{fecha_mentor}") || !strings.Contains(d, "{pais_mentor}") {
		t.Errorf("defaults.host_created = %q", d)
	}
	if d, _ := defaults["host_reminder_5m"].(string); !strings.Contains(d, "{enlace_mentor}") || !strings.Contains(d, "{nombre_corto}") {
		t.Errorf("defaults.host_reminder_5m = %q", d)
	}

	rec := put(`{"created":"Entra: {enlace_mentor}"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "«Confirmación»: {enlace_mentor} solo sirve en los avisos al anfitrión.") {
		t.Errorf("client text with the host link: %d %s", rec.Code, rec.Body.String())
	}
	rec = put(`{"host_created":"Hola\nEntra: {enlace}"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "En los avisos al anfitrión usa {enlace_mentor}") {
		t.Errorf("host text with the client link: %d %s", rec.Code, rec.Body.String())
	}
	// A stored text is not re-validated when the editor sends it back unchanged.
	mustExec(t, database, `INSERT INTO event_type_whatsapp_messages (event_type_id, moment, body) VALUES (?, 'reminder_1h', 'Viejo {enlace_mentor}')`, etID)
	got = mustJSON(t, put(`{"reminder_1h":"Viejo {enlace_mentor}","host_created":"*Nueva Mentoría agendada*\n{cliente}"}`), http.StatusOK, "unchanged + valid")
	if got["host_created"] != "*Nueva Mentoría agendada*\n{cliente}" {
		t.Errorf("host_created = %v", got["host_created"])
	}

	p := mustJSON(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path+"/preview", slug, `{"host_created":""}`, key), http.StatusOK, "preview")
	created, _ := p["host_created"].(string)
	for _, want := range []string{"*Nombre:* María Pérez (Perú 🇵🇪)", "maria@ejemplo.com", "+51987654321", ", 10:00 a. m.\nHora de Perú 🇵🇪"} {
		if !strings.Contains(created, want) {
			t.Errorf("preview host_created lacks %q:\n%s", want, created)
		}
	}
	five, _ := p["host_reminder_5m"].(string)
	if !strings.Contains(five, "*ENTRA AHORA:* https://citas.example.com/h/ejemplo3") || !strings.Contains(five, "*Nombre:* María Pérez") {
		t.Errorf("preview host_reminder_5m = %q", five)
	}
	for _, m := range []string{"created", "reminder_1h", "reminder_5m"} {
		if s, _ := p[m].(string); strings.Contains(s, "/h/") {
			t.Errorf("client preview %s carries the host link: %q", m, s)
		}
	}
}

// «Ver ejemplo» follows the delivery's host-zone rule (webhook HostZone): a profile that says
// UTC with a +34 number reads Spain's time - what the profile card and the real notice say -
// never Lima's; and {dia}/{hora} in a host text are that same host clock.
func TestWhatsAppPreview_hostZoneRule(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	mustExec(t, database, `UPDATE users SET iana_timezone = 'UTC' WHERE id = ?`, userID)
	slug, _ := seedEventTypeHTTP(t, h, key)
	path := "/v1/event-types/" + slug + "/whatsapp-messages/preview"
	body := `{"host_created":"","host_reminder_5m":"Empieza el {dia} a las {hora}."}`

	// No zone and no number: the Lima sample, as before.
	p := mustJSON(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path, slug, body, key), http.StatusOK, "preview, nothing known")
	if created, _ := p["host_created"].(string); !strings.Contains(created, ", 10:00 a. m.\nHora de Perú 🇵🇪") || p["timezone"] != "America/Lima" {
		t.Errorf("nothing known: timezone %v, host_created:\n%s", p["timezone"], created)
	}

	mustStatus(t, putWhatsApp(t, h, key, "", `{"phone":"+34 612 345 678"}`), http.StatusOK, "owner's number")
	p = mustJSON(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path, slug, body, key), http.StatusOK, "preview, UTC + Spain")
	if p["timezone"] != "Europe/Madrid" {
		t.Errorf("timezone = %v; want the +34 number's Europe/Madrid", p["timezone"])
	}
	if created, _ := p["host_created"].(string); !strings.Contains(created, ", 10:00 a. m.\nHora de España 🇪🇸 (Madrid)") || strings.Contains(created, "Hora de Perú") {
		t.Errorf("host_created:\n%s", created)
	}
	if five, _ := p["host_reminder_5m"].(string); !strings.HasPrefix(five, "Empieza el ") || !strings.HasSuffix(five, " a las 10:00 a. m.") {
		t.Errorf("host_reminder_5m = %q", five)
	}

	// A real profile zone wins over the number.
	mustExec(t, database, `UPDATE users SET iana_timezone = 'America/Bogota' WHERE id = ?`, userID)
	p = mustJSON(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path, slug, body, key), http.StatusOK, "preview, Bogotá")
	if created, _ := p["host_created"].(string); !strings.Contains(created, "Hora de Colombia 🇨🇴") || p["timezone"] != "America/Bogota" {
		t.Errorf("profile zone: timezone %v, host_created:\n%s", p["timezone"], created)
	}
}
