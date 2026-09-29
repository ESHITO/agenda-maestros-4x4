package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Fork (Agenda Maestros 4x4): the invite claim stored the browser's zone without checking
// it. An unloadable name (some ICU setups report "Etc/Unknown") was read as UTC by the
// slot engine and a privacy browser's "UTC" was taken literally: a Lima mentor's
// 09:00-17:00 became 04:00-12:00 with no warning. Now an unknown zone falls back to the
// inviter's.
func TestClaimInvite_unknownZoneFallsBackToTheInviters(t *testing.T) {
	for _, c := range []struct{ browser, want string }{
		{"Mars/Olympus", "America/Lima"},
		{"Etc/Unknown", "America/Lima"},
		{"UTC", "America/Lima"},
		{"", "America/Lima"},
		{"Local", "America/Lima"},
		{"Europe/Madrid", "Europe/Madrid"}, // a real zone is kept as sent
		{" America/Bogota ", "America/Bogota"},
	} {
		t.Run(c.browser, func(t *testing.T) {
			h, database, key, ownerID := setupWorkspaceWithDB(t)
			if _, err := database.Exec(`UPDATE users SET iana_timezone = 'America/Lima' WHERE id = ?`, ownerID); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			h.RequireAuth(h.CreateInvite)(rec, createInviteReq("bob@example.com", key))
			created := mustCreated(t, rec, "create invite")
			parts := strings.Split(mustString(t, created, "invite_url", "create invite"), "/")
			token := parts[len(parts)-1]

			body := `{"name":"Bob","password":"bobspassword1","timezone":"` + c.browser + `"}`
			req := httptest.NewRequest(http.MethodPost, "/v1/invites/"+token+"/claim", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.SetPathValue("token", token)
			rec2 := httptest.NewRecorder()
			h.ClaimInvite(rec2, req)
			if rec2.Code != http.StatusCreated {
				t.Fatalf("claim: %d %s", rec2.Code, rec2.Body)
			}
			var got string
			if err := database.QueryRow(`SELECT iana_timezone FROM users WHERE email = 'bob@example.com'`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("browser zone %q stored as %q; want %q", c.browser, got, c.want)
			}
		})
	}
}
