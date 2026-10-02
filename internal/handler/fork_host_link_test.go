package handler_test

// Fork (Agenda Maestros 4x4): the host's way into the room from a host notice - GET
// /h/{code} (fork_short_links.go), its long fallback (fork_host_notices.go) and the extra
// host tokens teamHostLinkCurrent accepts only while their person still hosts the booking.

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/livekit"
	"github.com/calnode/calnode/internal/webhook"
)

// hostLinkFixture: the team fixture with LiveKit on, served at supSite, a webhook service the
// test holds, the owner's WhatsApp number, a Mentoría template whose host text carries
// {enlace_mentor}, and the owner's webhook for booking.host_created.
func hostLinkFixture(t *testing.T) (*teamFixture, *livekit.Client, *webhook.Service, string) {
	t.Helper()
	f := newTeamFixture(t)
	lk := supLiveKit(t, f.h)
	f.h.SetMailer(&supMailer{}, supSite)
	f.h.SetPublicBaseURL(supSite)
	f.h.SetForceLocale("es")
	whs, err := webhook.New(f.db, "")
	if err != nil {
		t.Fatal(err)
	}
	f.h.SetWebhookSvc(whs)
	addMember(t, f.db, "m1", "America/Lima")
	f.mustRole("m1", "member", "mentoria")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	mustExec(t, f.db, `INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES (?, '+51911111111', 'x'), ('m1', '+51922222222', 'x')`, f.ownerID)
	if err := whs.SetWhatsAppMessages(context.Background(), f.tID, map[string]string{
		webhook.WhatsAppHostCreated: "Nueva sesión\nEntra: {enlace_mentor}",
	}); err != nil {
		t.Fatal(err)
	}
	hook := seedReminderWebhook(t, f.db, f.ownerID, webhook.EventHostCreated, []string{"host_whatsapp", "host_whatsapp_message"})
	return f, lk, whs, hook
}

var hostLinkRE = regexp.MustCompile(`https://agenda\.example\.com/h/([a-z0-9]{8})\b`)

func TestShortHostLink_hostTokenForTheCurrentHostOnly(t *testing.T) {
	f, lk, whs, hook := hostLinkFixture(t)
	ctx := context.Background()
	id := bookInZone(t, f.h, f.tSlug, futureAt(10, 15, 0), "America/Lima", "")
	room := "booking-" + id

	data, _ := waitDeliveries(t, f.db, hook, 1)[0]["data"].(map[string]any)
	msg, _ := data["host_whatsapp_message"].(string)
	m := hostLinkRE.FindStringSubmatch(msg)
	if m == nil || data["host_whatsapp"] != "51911111111" {
		t.Fatalf("host notice = %v; want the owner's number and a /h link", data)
	}
	end, err := time.Parse(time.RFC3339, f.scalar(`SELECT end_at FROM bookings WHERE id = ?`, id))
	if err != nil {
		t.Fatal(err)
	}
	emailed := lk.SignRoomToken(room, "host", end.Add(2*time.Hour)) // the creation e-mail's host link
	if f.scalar(`SELECT token_hash FROM fork_livekit_host_links WHERE booking_id = ?`, id) != supHash(emailed) {
		t.Fatal("the e-mailed host link was not recorded")
	}

	hostToken := func(code string) string {
		t.Helper()
		loc := followShortLink(t, f.h, f.h.ShortHostLink, code)
		if !strings.HasPrefix(loc, supSite+"/room/"+room+"?t=") {
			t.Fatalf("/h → %q; want this booking's room", loc)
		}
		u, _ := url.Parse(loc)
		tok := u.Query().Get("t")
		r, role, exp, err := lk.VerifyRoomToken(tok)
		if err != nil || r != room || role != "host" || !exp.Equal(end.Add(2*time.Hour)) {
			t.Fatalf("room token: %q %q %v %v; want a host token until the end + 2 h", r, role, exp, err)
		}
		return tok
	}
	role := func(tok string) string {
		t.Helper()
		s, _ := supToken(t, f, tok, "")["role"].(string)
		return s
	}

	ownerTok := hostToken(m[1])
	if got := f.scalar(`SELECT user_id FROM fork_livekit_host_tokens WHERE token_hash = ?`, supHash(ownerTok)); got != f.ownerID {
		t.Errorf("host token recorded for %q; want the owner", got)
	}
	if role(ownerTok) != "host" {
		t.Error("the /h host token is not accepted as host")
	}
	if role(emailed) != "host" {
		t.Error("the e-mailed host link stopped working after a /h click")
	}
	if again := hostToken(m[1]); again == ownerTok || role(again) != "host" || role(ownerTok) != "host" {
		t.Error("a second click must mint another valid token without demoting the first")
	}

	// Passed to m1: the owner's code and token stop opening the room as host; m1's work.
	mustStatus(t, f.reassign(f.ownerKey, id, "m1"), http.StatusOK, "reassign to m1")
	expectFriendlyPage(t, shortLinkGet(f.h.ShortHostLink, "/h/", m[1]), "the previous host's code", m[1])
	if role(ownerTok) == "host" {
		t.Error("the previous host's /h token is still host")
	}
	m1Code, err := whs.CreateHostShortLink(ctx, id, "m1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if role(hostToken(m1Code)) != "host" {
		t.Error("the new host's /h token is not host")
	}

	// Archived host, cancelled booking, over: the friendly page.
	mustExec(t, f.db, `UPDATE users SET archived_at = '2026-09-01T00:00:00Z' WHERE id = 'm1'`)
	expectFriendlyPage(t, shortLinkGet(f.h.ShortHostLink, "/h/", m1Code), "archived host", m1Code)
	mustExec(t, f.db, `UPDATE users SET archived_at = NULL WHERE id = 'm1'`)
	mustExec(t, f.db, `UPDATE bookings SET status = 'cancelled' WHERE id = ?`, id)
	expectFriendlyPage(t, shortLinkGet(f.h.ShortHostLink, "/h/", m1Code), "cancelled", m1Code)
	mustExec(t, f.db, `UPDATE bookings SET status = 'confirmed' WHERE id = ?`, id)
	bookingTimes(t, f.db, id, time.Now().Add(-3*time.Hour)) // ended 2.5 h ago: past the 2 h join grace
	expectFriendlyPage(t, shortLinkGet(f.h.ShortHostLink, "/h/", m1Code), "past the join grace", m1Code)

	// A room code is not a host code; unknown and malformed codes.
	roomCode, err := whs.CreateShortLink(ctx, id, webhook.ShortLinkRoom, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	expectFriendlyPage(t, shortLinkGet(f.h.ShortHostLink, "/h/", roomCode), "a room code on /h", roomCode)
	expectFriendlyPage(t, shortLinkGet(f.h.ShortHostLink, "/h/", "zzzzzzzz"), "unknown", "zzzzzzzz")
	if rec := shortLinkGet(f.h.ShortHostLink, "/h/", "abc"); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("malformed: %d %q; want a plain 404", rec.Code, rec.Body.String())
	}
}

// No /h code can be stored: the notice carries the long host link instead, already a valid
// host token for that host (and only while they host the booking).
func TestHostNotice_fallbackLongHostLink(t *testing.T) {
	f, lk, _, hook := hostLinkFixture(t)
	mustExec(t, f.db, `DROP TABLE fork_host_short_links`)
	id := bookInZone(t, f.h, f.tSlug, futureAt(10, 15, 0), "America/Lima", "")
	data, _ := waitDeliveries(t, f.db, hook, 1)[0]["data"].(map[string]any)
	msg, _ := data["host_whatsapp_message"].(string)
	mm := regexp.MustCompile(`Entra: (https://agenda\.example\.com/room/booking-[^?\s]+\?t=\S+)`).FindStringSubmatch(msg)
	if mm == nil {
		t.Fatalf("host notice = %q; want the long host link", msg)
	}
	u, _ := url.Parse(mm[1])
	tok := u.Query().Get("t")
	if _, role, _, err := lk.VerifyRoomToken(tok); err != nil || role != "host" {
		t.Fatalf("fallback token role %q, %v", role, err)
	}
	if s, _ := supToken(t, f, tok, "")["role"].(string); s != "host" {
		t.Errorf("fallback link role = %q; want host", s)
	}
	if got := f.scalar(`SELECT user_id FROM fork_livekit_host_tokens WHERE booking_id = ?`, id); got != f.ownerID {
		t.Errorf("fallback token recorded for %q; want the owner", got)
	}
}

// The bookings list keeps exactly the client's four notices: the host's host_5m job and the
// booking.host_* deliveries change none of them (booking_whatsapp_status.go is untouched).
func TestListBookings_hostNoticesStayOutOfTheFourDots(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	mustExec(t, database, `INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES (?, '+51911111111', 'x')`, userID)
	createdHook := seedReminderWebhook(t, database, userID, "booking.created", []string{"id"})
	seedReminderWebhook(t, database, userID, "booking.reminder_morning", []string{"id"})
	seedReminderWebhook(t, database, userID, "booking.reminder_1h", []string{"id"})
	hostHook := seedReminderWebhook(t, database, userID, webhook.EventHostCreated, []string{"id", "host_whatsapp"})
	seedReminderWebhook(t, database, userID, webhook.EventHostReminder5m, []string{"id", "host_whatsapp"})

	start := futureAt(10, 15, 0)
	id := bookInZone(t, h, slug, start, "America/Lima", "")
	jobs := waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 4 })
	if _, ok := jobs["host_5m"]; !ok {
		t.Fatalf("no host_5m job: %v", jobs)
	}
	waitDeliveries(t, database, createdHook, 1)
	waitDeliveries(t, database, hostHook, 1)
	mustExec(t, database, `UPDATE webhook_deliveries SET status = 'success', last_attempted_at = '2026-01-02T03:04:05Z' WHERE booking_id = ? AND event = 'booking.created'`, id)
	mustExec(t, database, `UPDATE webhook_deliveries SET status = 'failed', attempt_count = 5 WHERE booking_id = ? AND event = ?`, id, webhook.EventHostCreated)

	b := listBookingsWA(t, h, key, "when=upcoming")[id]
	if len(b.WhatsApp) != 4 {
		t.Fatalf("whatsapp = %+v; want the 4 client notices", b.WhatsApp)
	}
	day := start.Format("2006-01-02")
	for kind, want := range map[string]listedNotice{
		"created": {"created", "sent", "2026-01-02T03:04:05Z"}, // the host's failed notice does not count
		"morning": {"morning", "pending", day + "T13:00:00Z"},
		"1h":      {"1h", "pending", day + "T14:00:00Z"},
		"5m":      {"5m", "not_applicable", ""}, // host_5m is pending with a webhook, yet this is the client's
	} {
		if got := noticeMap(b)[kind]; got != want {
			t.Errorf("%s = %+v; want %+v", kind, got, want)
		}
	}
}
