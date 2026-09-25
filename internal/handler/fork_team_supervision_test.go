package handler_test

// Fork (Agenda Maestros 4x4): supervision of the team's sessions - answers, passing a
// session to another person of the same área, the host link after that, and the video
// room's attendance (fork_team_supervision.go, fork_attendance.go).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/livekit"
	"github.com/calnode/calnode/internal/mailer"
)

const (
	supLKKey    = "devkey"
	supLKSecret = "devsecret"
	supSite     = "https://agenda.example.com"
)

// supLiveKit turns LiveKit on for h, against a local server that answers every server API
// call with 404 (the room does not exist yet), so nothing leaves the machine.
func supLiveKit(t *testing.T, h *handler.Handler) *livekit.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	lk := livekit.New(srv.URL, supLKKey, supLKSecret, [32]byte{9})
	h.SetLiveKit(lk)
	return lk
}

// supMailer records every message (the confirmation side effects run on a goroutine).
type supMailer struct {
	mu   sync.Mutex
	msgs []mailer.Message
}

func (m *supMailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
	return nil
}

var supRoomTokenRE = regexp.MustCompile(`/room/booking-[A-Za-z0-9-]+\?t=([A-Za-z0-9_.%-]+)`)

// waitHostLinkTo waits for a message to "to" carrying a room link and returns its token.
func (m *supMailer) waitHostLinkTo(t *testing.T, to string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		for _, msg := range m.msgs {
			if len(msg.To) == 0 || msg.To[0] != to {
				continue
			}
			if mm := supRoomTokenRE.FindStringSubmatch(msg.Text + msg.HTML); mm != nil {
				m.mu.Unlock()
				tok, err := url.QueryUnescape(mm[1])
				if err != nil {
					t.Fatal(err)
				}
				return tok
			}
		}
		m.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("no email with a room link to %s", to)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// supSession signs userID in (a calnode_session cookie value).
func supSession(t *testing.T, f *teamFixture, userID string) string {
	t.Helper()
	id := "sess-" + userID
	mustExec(t, f.db, `INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, '2099-01-01T00:00:00Z')`, id, userID)
	return id
}

// supToken calls POST /v1/livekit/token with a room token and, optionally, a session.
func supToken(t *testing.T, f *teamFixture, roomToken, session string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"t": roomToken, "name": "Alguien"})
	req := httptest.NewRequest(http.MethodPost, "/v1/livekit/token", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "calnode_session", Value: session})
	}
	rec := httptest.NewRecorder()
	f.h.LiveKitToken(rec, req)
	return mustJSON(t, rec, http.StatusOK, "livekit token")
}

// supMint is a mint row by identity: "kind/verified/user".
func supMint(t *testing.T, f *teamFixture, identity string) string {
	t.Helper()
	return f.scalar(`SELECT kind || '/' || verified || '/' || COALESCE(user_id, '') FROM fork_livekit_mints WHERE identity = ?`, identity)
}

func supHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func supAddAdmin(t *testing.T, f *teamFixture, id string) string {
	t.Helper()
	key := addMember(t, f.db, id, "UTC")
	mustExec(t, f.db, `UPDATE users SET is_admin = 1 WHERE id = ?`, id)
	return key
}

// supBooking seeds a confirmed booking with its primary seat.
func supBooking(t *testing.T, f *teamFixture, id, etID, hostID, start, end string) {
	t.Helper()
	mustExec(t, f.db, `INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, location_type, location_value)
		VALUES (?, ?, ?, ?, ?, 'confirmed', 'livekit', '')`, id, etID, hostID, start, end)
	mustExec(t, f.db, `INSERT INTO booking_hosts (id, booking_id, user_id, is_primary) VALUES (?, ?, ?, 1)`, "bh-"+id, id, hostID)
}

func (f *teamFixture) reassign(key, bookingID, hostID string) *httptest.ResponseRecorder {
	return f.call(f.h.TeamReassignGuard(f.h.ReassignBooking), http.MethodPost, "/v1/bookings/"+bookingID+"/reassign",
		`{"host_id":"`+hostID+`"}`, key, "id", bookingID)
}

func TestTeamAnswers_accessMatrix(t *testing.T) {
	f := newTeamFixture(t)
	oSlug, oID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	_ = oSlug
	m1 := addMember(t, f.db, "m1", "UTC")
	m2 := addMember(t, f.db, "m2", "UTC")
	m3 := addMember(t, f.db, "m3", "UTC")
	a1 := supAddAdmin(t, f, "a1")
	supBooking(t, f, "b1", oID, "m1", "2099-03-10T10:00:00Z", "2099-03-10T10:30:00Z")
	mustExec(t, f.db, `INSERT INTO booking_hosts (id, booking_id, user_id, is_primary) VALUES ('bh-b1-m3', 'b1', 'm3', 0)`)
	mustExec(t, f.db, `INSERT INTO event_type_questions (id, event_type_id, label, type, required, position) VALUES ('q1', ?, 'Tema', 'text', 0, 0)`, oID)
	mustExec(t, f.db, `INSERT INTO booking_answers (id, booking_id, question_id, value) VALUES ('a-1', 'b1', 'q1', 'Ventas')`)

	for _, c := range []struct {
		who, key string
		want     int
	}{
		{"primary host", m1, http.StatusOK},
		{"booking_hosts seat", m3, http.StatusOK},
		{"owner", f.ownerKey, http.StatusOK},
		{"admin", a1, http.StatusOK},
		{"unrelated member", m2, http.StatusNotFound},
	} {
		rec := f.call(f.h.GetBookingAnswers, http.MethodGet, "/v1/bookings/b1/answers", "", c.key, "id", "b1")
		if rec.Code != c.want {
			t.Errorf("%s: %d; want %d — %s", c.who, rec.Code, c.want, rec.Body.String())
			continue
		}
		if c.want == http.StatusOK && !strings.Contains(rec.Body.String(), `"Ventas"`) {
			t.Errorf("%s: body %s", c.who, rec.Body.String())
		}
	}
}

// teamReassignFixture: T (with one text question) and S set; mentors m1 and m2 (active
// copies), m4 with no área, soporte staff m5 and m6. bC is on m1's copy, answered.
type teamReassignFixture struct {
	*teamFixture
	c1, c2 teamCopy
	tq     string // T's question
}

func newTeamReassignFixture(t *testing.T) *teamReassignFixture {
	f := newTeamFixture(t)
	mustCreated(t, f.call(f.guard(handler.TeamOpQuestions, f.h.CreateQuestion), http.MethodPost,
		"/v1/event-types/"+f.tSlug+"/questions", `{"label":"¿Qué quieres trabajar?","type":"text","required":true}`,
		f.ownerKey, "slug", f.tSlug), "create T question")
	for _, id := range []string{"m1", "m2", "m4", "m5", "m6"} {
		addMember(t, f.db, id, "UTC")
	}
	seedFullAvailabilityDB(t, f.db, "m5") // soporte rotates only with weekly hours
	seedFullAvailabilityDB(t, f.db, "m6")
	f.mustRole("m1", "member", "mentoria")
	f.mustRole("m2", "member", "mentoria")
	f.mustRole("m5", "member", "soporte")
	f.mustRole("m6", "member", "soporte")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `","soporte_shared_id":"` + f.sID + `"}`)
	rf := &teamReassignFixture{teamFixture: f, c1: f.copyOf(f.tID, "m1"), c2: f.copyOf(f.tID, "m2")}
	rf.tq = f.scalar(`SELECT id FROM event_type_questions WHERE event_type_id = ?`, f.tID)
	supBooking(t, f, "bC", rf.c1.id, "m1", "2099-03-10T10:00:00Z", "2099-03-10T10:30:00Z")
	mustExec(t, f.db, `INSERT INTO booking_answers (id, booking_id, question_id, value) VALUES ('ans-bC', 'bC', ?, 'Ventas')`,
		rf.copyQuestion(rf.c1.id))
	return rf
}

// copyQuestion is copyID's question linked to T's question.
func (rf *teamReassignFixture) copyQuestion(copyID string) string {
	return rf.scalar(`SELECT copy_question_id FROM fork_question_links WHERE copy_id = ? AND template_question_id = ?`, copyID, rf.tq)
}

func (rf *teamReassignFixture) state(bookingID string) string {
	return rf.scalar(`
		SELECT b.host_id || '|' || b.event_type_id || '|' ||
		       COALESCE((SELECT group_concat(user_id || ':' || is_primary) FROM booking_hosts WHERE booking_id = b.id), '') || '|' ||
		       COALESCE((SELECT group_concat(question_id) FROM booking_answers WHERE booking_id = b.id), '')
		FROM bookings b WHERE b.id = ?`, bookingID)
}

func TestTeamReassign_sameAreaMoveAndRemap(t *testing.T) {
	rf := newTeamReassignFixture(t)
	f := rf.teamFixture
	oSlug, oID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	_ = oSlug
	whID := seedReminderWebhook(t, f.db, f.ownerID, "booking.rescheduled", []string{"id", "event_type_slug", "whatsapp_message"})
	mustExec(t, f.db, `INSERT INTO event_type_whatsapp_messages (event_type_id, moment, body) VALUES (?, 'rescheduled', 'Tema: {tema}')`, f.tID)

	// Refusals, in Spanish, before any write.
	before := rf.state("bC")
	for _, c := range []struct {
		host, want string
		code       int
	}{
		{"m4", "no tiene un enlace de Mentoría activo", http.StatusBadRequest},
		{"m5", "no tiene un enlace de Mentoría activo", http.StatusBadRequest},
		{"nadie", "Esa persona no existe o está archivada.", http.StatusBadRequest},
	} {
		rec := rf.reassign(f.ownerKey, "bC", c.host)
		if rec.Code != c.code || !strings.Contains(errorOf(t, rec), c.want) {
			t.Errorf("reassign to %s: %d %q; want %d %q", c.host, rec.Code, errorOf(t, rec), c.code, c.want)
		}
	}
	if got := rf.state("bC"); got != before {
		t.Fatalf("a refused reassign wrote: %s -> %s", before, got)
	}
	if rec := rf.reassign("member-key-m1", "bC", "m2"); rec.Code != http.StatusForbidden ||
		errorOf(t, rec) != "Solo el propietario y los administradores pasan una sesión a otra persona." {
		t.Errorf("member: %d %q", rec.Code, errorOf(t, rec))
	}

	// To another mentor: host, seat, event type (their copy) and answers move together.
	mustStatus(t, rf.reassign(f.ownerKey, "bC", "m2"), http.StatusOK, "reassign to m2")
	want := "m2|" + rf.c2.id + "|m2:1|" + rf.copyQuestion(rf.c2.id)
	if got := rf.state("bC"); got != want {
		t.Errorf("after the reassign to m2: %s; want %s", got, want)
	}
	// {tema} still resolves on the new copy, and the rescheduled notice carries its slug.
	data, _ := waitDeliveries(t, f.db, whID, 1)[0]["data"].(map[string]any)
	if msg, _ := data["whatsapp_message"].(string); !strings.Contains(msg, "Tema: Ventas") {
		t.Errorf("rescheduled whatsapp_message = %q; want the {tema} answer", msg)
	}
	if data["event_type_slug"] != rf.c2.slug {
		t.Errorf("rescheduled event_type_slug = %v; want %s", data["event_type_slug"], rf.c2.slug)
	}

	// To the template's owner: back onto T itself, answers onto T's question.
	mustStatus(t, rf.reassign(f.ownerKey, "bC", f.ownerID), http.StatusOK, "reassign to the owner")
	if got, want := rf.state("bC"), f.ownerID+"|"+f.tID+"|"+f.ownerID+":1|"+rf.tq; got != want {
		t.Errorf("after the reassign to the owner: %s; want %s", got, want)
	}
	// And from T to a mentor again.
	mustStatus(t, rf.reassign(f.ownerKey, "bC", "m1"), http.StatusOK, "reassign T booking to m1")
	if got, want := rf.state("bC"), "m1|"+rf.c1.id+"|m1:1|"+rf.copyQuestion(rf.c1.id); got != want {
		t.Errorf("after T -> m1: %s; want %s", got, want)
	}

	// Busy: the upstream English error, answered in Spanish.
	supBooking(t, f, "bBusy", oID, "m2", "2099-03-10T10:00:00Z", "2099-03-10T11:00:00Z")
	if rec := rf.reassign(f.ownerKey, "bC", "m2"); rec.Code != http.StatusConflict ||
		errorOf(t, rec) != "Esa persona ya tiene otra sesión a esa hora." {
		t.Errorf("busy: %d %q", rec.Code, errorOf(t, rec))
	}

	// Soporte: only soporte staff; the type does not move.
	supBooking(t, f, "bS", f.sID, "m5", "2099-03-12T10:00:00Z", "2099-03-12T10:30:00Z")
	if rec := rf.reassign(f.ownerKey, "bS", "m1"); rec.Code != http.StatusBadRequest ||
		!strings.Contains(errorOf(t, rec), "no es del personal de soporte") {
		t.Errorf("S booking to a mentor: %d %q", rec.Code, errorOf(t, rec))
	}
	mustStatus(t, rf.reassign(f.ownerKey, "bS", "m6"), http.StatusOK, "S booking to m6")
	if got := rf.state("bS"); got != "m6|"+f.sID+"|m6:1|" {
		t.Errorf("S booking after the reassign: %s", got)
	}

	// Any other type: the upstream rule (anyone active).
	supBooking(t, f, "bO", oID, f.ownerID, "2099-03-13T10:00:00Z", "2099-03-13T10:30:00Z")
	mustStatus(t, rf.reassign(f.ownerKey, "bO", "m4"), http.StatusOK, "other type to m4")
	if got := rf.state("bO"); got != "m4|"+oID+"|m4:1|" {
		t.Errorf("other booking after the reassign: %s", got)
	}

	// A cancelled booking.
	mustExec(t, f.db, `UPDATE bookings SET status = 'cancelled' WHERE id = 'bO'`)
	if rec := rf.reassign(f.ownerKey, "bO", f.ownerID); rec.Code != http.StatusConflict || errorOf(t, rec) != "Esta reserva está cancelada." {
		t.Errorf("cancelled: %d %q", rec.Code, errorOf(t, rec))
	}
}

// Everything a reassign writes is one transaction: when the move of the event type fails
// (here, the handler called without its guard, towards someone with no copy), host_id and
// the seat are rolled back too.
func TestTeamReassign_oneTransaction(t *testing.T) {
	rf := newTeamReassignFixture(t)
	f := rf.teamFixture
	before := rf.state("bC")
	rec := f.call(f.h.ReassignBooking, http.MethodPost, "/v1/bookings/bC/reassign", `{"host_id":"m4"}`, f.ownerKey, "id", "bC")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unguarded reassign to a person with no copy: %d %s", rec.Code, rec.Body.String())
	}
	if got := rf.state("bC"); got != before {
		t.Errorf("a failed reassign left writes behind: %s -> %s", before, got)
	}
}

func TestTeamReassignCandidates(t *testing.T) {
	rf := newTeamReassignFixture(t)
	f := rf.teamFixture
	_, oID := seedEventTypeHTTP(t, f.h, f.ownerKey)
	addMember(t, f.db, "m3", "UTC")
	f.mustRole("m3", "member", "mentoria")
	mustStatus(t, f.call(f.h.TeamReconcileAfter(f.h.ArchiveUser), http.MethodPost, "/v1/users/m3/archive", "", f.ownerKey, "id", "m3"), http.StatusOK, "archive m3")
	a1 := supAddAdmin(t, f, "a1")
	supBooking(t, f, "bS", f.sID, "m5", "2099-03-12T10:00:00Z", "2099-03-12T10:30:00Z")
	supBooking(t, f, "bO", oID, f.ownerID, "2099-03-13T10:00:00Z", "2099-03-13T10:30:00Z")

	cands := func(key, bookingID string) []string {
		t.Helper()
		rec := f.call(f.h.GetReassignCandidates, http.MethodGet, "/v1/bookings/"+bookingID+"/reassign-candidates", "", key, "id", bookingID)
		mustStatus(t, rec, http.StatusOK, "candidates "+bookingID)
		var items []struct{ ID, Name, Area string }
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatalf("candidates %s: not a bare array: %s", bookingID, rec.Body.String())
		}
		var out []string
		for _, it := range items {
			out = append(out, it.ID+":"+it.Area)
		}
		return out
	}
	// Mentoría: the template's owner and the mentors with an active copy (not the current
	// host, not an archived mentor, not people of another área).
	if got, want := cands(f.ownerKey, "bC"), []string{"m2:mentoria", f.ownerID + ":"}; !slices.Equal(got, want) {
		t.Errorf("bC candidates = %v; want %v", got, want)
	}
	// Soporte: the other soporte staff.
	if got, want := cands(a1, "bS"), []string{"m6:soporte"}; !slices.Equal(got, want) {
		t.Errorf("bS candidates = %v; want %v", got, want)
	}
	// Any other type: every active person but the host.
	got := cands(f.ownerKey, "bO")
	slices.Sort(got)
	if want := []string{"a1:", "m1:mentoria", "m2:mentoria", "m4:", "m5:soporte", "m6:soporte"}; !slices.Equal(got, want) {
		t.Errorf("bO candidates = %v; want %v", got, want)
	}
	if rec := f.call(f.h.GetReassignCandidates, http.MethodGet, "/v1/bookings/bC/reassign-candidates", "", "member-key-m1", "id", "bC"); rec.Code != http.StatusForbidden {
		t.Errorf("member: %d", rec.Code)
	}
	if rec := f.call(f.h.GetReassignCandidates, http.MethodGet, "/v1/bookings/nope/reassign-candidates", "", f.ownerKey, "id", "nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown booking: %d", rec.Code)
	}
}

// The host link after a reassign: the link minted when the booking was made is recorded;
// after the booking passes to a mentor, the previous host's link opens the room as an
// attendee, the new host's fresh link (emailed to them) as the host, and a signed-in
// current host is still promoted. The mint records who each token went to.
func TestTeamHostLink_downgradeAfterReassignAndMintKinds(t *testing.T) {
	f := newTeamFixture(t)
	lk := supLiveKit(t, f.h)
	mail := &supMailer{}
	f.h.SetMailer(mail, supSite)
	addMember(t, f.db, "m1", "UTC")
	f.mustRole("m1", "member", "mentoria")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	c1 := f.copyOf(f.tID, "m1")

	id := bookInZone(t, f.h, f.tSlug, futureAt(10, 15, 0), "America/Lima", "")
	room := "booking-" + id
	var stored string
	deadline := time.Now().Add(5 * time.Second)
	for stored == "" && time.Now().Before(deadline) {
		stored = f.scalar(`SELECT token_hash FROM fork_livekit_host_links WHERE booking_id = ?`, id)
		time.Sleep(20 * time.Millisecond)
	}
	end, err := time.Parse(time.RFC3339, f.scalar(`SELECT end_at FROM bookings WHERE id = ?`, id))
	if err != nil {
		t.Fatal(err)
	}
	oldHost := lk.SignRoomToken(room, "host", end.Add(2*time.Hour)) // the link the booking emailed its host
	attendee := lk.SignRoomToken(room, "", end.Add(2*time.Hour))
	if stored != supHash(oldHost) {
		t.Fatalf("stored host link hash = %q; want the hash of the minted host link", stored)
	}
	ownerSess := supSession(t, f, f.ownerID)
	m1Sess := supSession(t, f, "m1")

	role := func(r map[string]any) string { s, _ := r["role"].(string); return s }
	identity := func(r map[string]any) string { s, _ := r["identity"].(string); return s }
	check := func(what, tok, sess, wantRole, wantMint string) {
		t.Helper()
		r := supToken(t, f, tok, sess)
		if role(r) != wantRole {
			t.Errorf("%s: role %q; want %q", what, role(r), wantRole)
		}
		if got := supMint(t, f, identity(r)); got != wantMint {
			t.Errorf("%s: mint %q; want %q", what, got, wantMint)
		}
	}
	check("host link, signed out", oldHost, "", "host", "host/0/")
	check("attendee link, signed out", attendee, "", "", "attendee/0/")
	check("attendee link, the owner hosting it", attendee, ownerSess, "host", "host/1/"+f.ownerID)
	check("attendee link, a mentor who does not host it", attendee, m1Sess, "", "staff/0/m1")

	mustStatus(t, f.reassign(f.ownerKey, id, "m1"), http.StatusOK, "reassign to m1")
	if got := f.scalar(`SELECT event_type_id FROM bookings WHERE id = ?`, id); got != c1.id {
		t.Errorf("event type after the reassign = %s; want m1's copy %s", got, c1.id)
	}
	fresh := mail.waitHostLinkTo(t, "m1@example.com")
	if fresh == oldHost {
		t.Fatal("the new host was emailed the previous host's link")
	}
	if _, r, _, err := lk.VerifyRoomToken(fresh); err != nil || r != "host" {
		t.Fatalf("fresh link: role %q, %v", r, err)
	}
	check("previous host's link, signed out", oldHost, "", "", "staff/0/")
	check("fresh host link, signed out", fresh, "", "host", "host/0/")
	check("previous host's link, the new host signed in", oldHost, m1Sess, "host", "host/1/m1")
	check("attendee link, the owner (no longer hosting)", attendee, ownerSess, "", "staff/0/"+f.ownerID)

	// Host actions: the previous host's link no longer authorises them.
	end1 := func(tok string) int {
		body, _ := json.Marshal(map[string]string{"t": tok})
		req := httptest.NewRequest(http.MethodPost, "/v1/livekit/room/end", strings.NewReader(string(body)))
		rec := httptest.NewRecorder()
		f.h.EndRoom(rec, req)
		return rec.Code
	}
	if code := end1(oldHost); code != http.StatusForbidden {
		t.Errorf("end room with the previous host's link: %d; want 403", code)
	}
	if code := end1(fresh); code == http.StatusForbidden {
		t.Errorf("end room with the fresh host link: 403; want it authorised")
	}
}

// supWebhook posts a signed LiveKit webhook event.
func supWebhook(t *testing.T, f *teamFixture, event map[string]any) {
	t.Helper()
	body, _ := json.Marshal(event)
	sum := sha256.Sum256(body)
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{"iss": supLKKey, "sha256": base64.StdEncoding.EncodeToString(sum[:]), "iat": now, "exp": now + 300})
	signing := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(supLKSecret))
	mac.Write([]byte(signing))
	req := httptest.NewRequest(http.MethodPost, "/v1/livekit/webhook", strings.NewReader(string(body)))
	req.Header.Set("Authorization", signing+"."+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	f.h.LiveKitWebhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook %v: %d %s", event["event"], rec.Code, rec.Body.String())
	}
}

func lkEvent(event, room string, created time.Time, sid, identity string, joined time.Time, kind string) map[string]any {
	ev := map[string]any{"id": "EV_" + event + sid, "event": event, "createdAt": fmt.Sprint(created.Unix()), "room": map[string]any{"name": room}}
	if sid != "" {
		p := map[string]any{"sid": sid, "identity": identity, "joinedAt": fmt.Sprint(joined.Unix())}
		if kind != "" {
			p["kind"] = kind
		}
		ev["participant"] = p
	}
	return ev
}

// The LiveKit webhook refines the mints: sessions keyed by participant sid, tolerant of
// replays and out-of-order delivery; staff never counts; the recording egress and unknown
// identities are ignored; room_finished closes what is open. The list then reads the
// status - and a booking with only mints (webhook off) is never "nobody".
func TestAttendance_webhookSessionsAndListStatus(t *testing.T) {
	f := newTeamFixture(t)
	supLiveKit(t, f.h)
	mustExec(t, f.db, `UPDATE fork_settings SET value = '2020-01-01T00:00:00Z' WHERE key = 'attendance_since'`)
	start := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Minute)
	end := start.Add(30 * time.Minute)
	seed := func(id string, s, e time.Time) {
		mustExec(t, f.db, `INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, location_type, location_value, livekit_room)
			VALUES (?, ?, ?, ?, ?, 'confirmed', 'livekit', '', ?)`, id, f.tID, f.ownerID, s.Format(time.RFC3339), e.Format(time.RFC3339), "booking-"+id)
	}
	mint := func(identity, bookingID, kind string, verified int, at time.Time) {
		mustExec(t, f.db, `INSERT INTO fork_livekit_mints (identity, booking_id, kind, user_id, verified, minted_at) VALUES (?, ?, ?, NULL, ?, ?)`,
			identity, bookingID, kind, verified, at.Format(time.RFC3339))
	}
	seed("bW", start, end)
	mint("id-host", "bW", "host", 1, start)
	mint("id-client", "bW", "attendee", 0, start.Add(2*time.Minute))
	mint("id-staff", "bW", "staff", 0, start)
	mint("id-egress", "bW", "attendee", 0, start)
	room := "booking-bW"

	t1, t2 := start, start.Add(25*time.Minute)
	// Out of order: the host's "left" arrives before their "joined"; then both are replayed.
	left := lkEvent("participant_left", room, t2, "PA_host", "id-host", t1, "")
	joined := lkEvent("participant_joined", room, t1, "PA_host", "id-host", t1, "")
	supWebhook(t, f, left)
	supWebhook(t, f, joined)
	supWebhook(t, f, left)
	supWebhook(t, f, joined)
	supWebhook(t, f, lkEvent("participant_joined", room, t1.Add(2*time.Minute), "PA_client", "id-client", t1.Add(2*time.Minute), "STANDARD"))
	supWebhook(t, f, lkEvent("participant_joined", room, t1, "PA_staff", "id-staff", t1, ""))
	supWebhook(t, f, lkEvent("participant_joined", room, t1, "EG_rec", "id-egress", t1, "EGRESS"))
	supWebhook(t, f, lkEvent("participant_joined", room, t1, "PA_ghost", "id-unknown", t1, ""))
	supWebhook(t, f, lkEvent("track_published", room, t1, "PA_client", "id-client", t1, ""))
	supWebhook(t, f, lkEvent("room_finished", room, start.Add(40*time.Minute), "", "", time.Time{}, ""))

	sessions := f.scalar(`SELECT group_concat(participant_sid || '=' || COALESCE(joined_at,'') || '/' || COALESCE(left_at,''), ' ')
		FROM (SELECT * FROM fork_livekit_sessions WHERE booking_id = 'bW' ORDER BY participant_sid)`)
	want := strings.Join([]string{
		"PA_client=" + t1.Add(2*time.Minute).Format(time.RFC3339) + "/" + start.Add(40*time.Minute).Format(time.RFC3339),
		"PA_host=" + t1.Format(time.RFC3339) + "/" + t2.Format(time.RFC3339),
		"PA_staff=" + t1.Format(time.RFC3339) + "/" + start.Add(40*time.Minute).Format(time.RFC3339),
	}, " ")
	if sessions != want {
		t.Errorf("sessions:\n got %s\nwant %s", sessions, want)
	}

	// Only mints (the webhook never reached us): both entered, minutes unknown.
	seed("bM", start.Add(-24*time.Hour), end.Add(-24*time.Hour))
	mint("m-host", "bM", "host", 1, start.Add(-24*time.Hour))
	mint("m-client", "bM", "attendee", 0, start.Add(-24*time.Hour+time.Minute))
	// Nobody at all.
	seed("bN", start.Add(-48*time.Hour), end.Add(-48*time.Hour))
	// Started before attendance_since.
	seed("bOld", time.Date(2019, 5, 1, 10, 0, 0, 0, time.UTC), time.Date(2019, 5, 1, 10, 30, 0, 0, time.UTC))
	mint("o-host", "bOld", "host", 1, time.Date(2019, 5, 1, 10, 0, 0, 0, time.UTC))

	rec := f.call(f.h.ListBookings, http.MethodGet, "/v1/bookings?scope=all", "", f.ownerKey)
	mustStatus(t, rec, http.StatusOK, "list")
	var list struct {
		Items []struct {
			ID         string `json:"id"`
			Attendance *struct {
				Status          string  `json:"status"`
				HostJoinedAt    *string `json:"host_joined_at"`
				ClientJoinedAt  *string `json:"client_joined_at"`
				MinutesTogether *int    `json:"minutes_together"`
			} `json:"attendance"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range list.Items {
		if it.Attendance == nil {
			t.Fatalf("%s: no attendance in %s", it.ID, rec.Body.String())
		}
		a := it.Attendance
		s := a.Status
		if a.MinutesTogether != nil {
			s += fmt.Sprintf(" %dmin", *a.MinutesTogether)
		}
		got[it.ID] = s
	}
	for id, want := range map[string]string{
		"bW":   "attended 23min", // host 0-25, client 2-40: staff and egress not counted
		"bM":   "attended",
		"bN":   "nobody",
		"bOld": "not_applicable",
	} {
		if got[id] != want {
			t.Errorf("%s attendance = %q; want %q", id, got[id], want)
		}
	}

	// The public booking keys still do not carry it.
	req := httptest.NewRequest(http.MethodGet, "/v1/bookings/bW", nil)
	req.SetPathValue("id", "bW")
	pub := httptest.NewRecorder()
	f.h.GetBooking(pub, req)
	if strings.Contains(pub.Body.String(), "attendance") {
		t.Errorf("public booking carries attendance: %s", pub.Body.String())
	}
}
