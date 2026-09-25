package handler_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/livekit"
	"github.com/calnode/calnode/internal/webhook"
)

// Fork (Agenda Maestros 4x4): the WhatsApp short links /e/{code} and /c/{code}
// (fork_short_links.go here and in internal/webhook).

const shortSite = "https://citas.example.com"

// shortLinkGet calls a short-link handler for code as the mux would.
func shortLinkGet(fn http.HandlerFunc, prefix, code string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, prefix+url.PathEscape(code), nil)
	req.SetPathValue("code", code)
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

// followShortLink expects a 302 for code and returns its Location. Every answer carries
// no-store and no-referrer.
func followShortLink(t *testing.T, h *handler.Handler, fn http.HandlerFunc, code string) string {
	t.Helper()
	rec := shortLinkGet(fn, "/x/", code)
	if rec.Code != http.StatusFound {
		t.Fatalf("short link %q: status %d; want 302 — %s", code, rec.Code, rec.Body.String())
	}
	assertShortLinkHeaders(t, rec)
	if rec.Body.Len() != 0 {
		t.Errorf("redirect body = %q; want none (it would echo the link)", rec.Body.String())
	}
	return rec.Header().Get("Location")
}

func assertShortLinkHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q; want no-store", cc)
	}
	if rp := rec.Header().Get("Referrer-Policy"); rp != "no-referrer" {
		t.Errorf("Referrer-Policy = %q; want no-referrer", rp)
	}
}

// expectFriendlyPage expects the branded "Este enlace ya no es válido" page, status 404.
func expectFriendlyPage(t *testing.T, rec *httptest.ResponseRecorder, what, code string) {
	t.Helper()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("%s: status %d; want 404 — %s", what, rec.Code, rec.Body.String())
	}
	assertShortLinkHeaders(t, rec)
	body := rec.Body.String()
	for _, want := range []string{"<title>Este enlace ya no es válido</title>", "<h2>Este enlace ya no es válido</h2>", "contacta al club", `id="token-invalid-view"`} {
		if !strings.Contains(body, want) {
			t.Errorf("%s: friendly page lacks %q", what, want)
		}
	}
	if code != "" && strings.Contains(body, code) {
		t.Errorf("%s: the page echoes the code", what)
	}
}

var shortInMessageRE = regexp.MustCompile(`https://citas\.example\.com/([ec])/([a-z0-9]{8})\b`)

// shortCodesInMessage returns the codes of the short links in msg by route letter.
func shortCodesInMessage(msg string) map[string]string {
	out := map[string]string{}
	for _, m := range shortInMessageRE.FindAllStringSubmatch(msg, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// shortLinkWorkspace is a workspace on https://citas.example.com, Spanish pages, with a
// webhook service the test holds (to make codes directly) and LiveKit on.
func shortLinkWorkspace(t *testing.T) (h *handler.Handler, database *sql.DB, key, userID string, whs *webhook.Service, lk *livekit.Client) {
	t.Helper()
	h, database, key, userID = setupWorkspaceWithDB(t)
	h.SetBaseURL(shortSite)
	h.SetPublicBaseURL(shortSite)
	h.SetForceLocale("es")
	whs, err := webhook.New(database, "")
	if err != nil {
		t.Fatal(err)
	}
	h.SetWebhookSvc(whs)
	lk = livekit.New("wss://livekit.example.test", "devkey", "devsecret", [32]byte{7})
	h.SetLiveKit(lk)
	return h, database, key, userID, whs, lk
}

func newCode(t *testing.T, whs *webhook.Service, bookingID, kind string) string {
	t.Helper()
	code, err := whs.CreateShortLink(context.Background(), bookingID, kind, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// bookingTimes moves a booking to start at start (30 minutes long), straight in the table.
func bookingTimes(t *testing.T, database *sql.DB, id string, start time.Time) {
	t.Helper()
	mustExec(t, database, `UPDATE bookings SET start_at = ?, end_at = ? WHERE id = ?`,
		start.UTC().Format(time.RFC3339Nano), start.Add(30*time.Minute).UTC().Format(time.RFC3339Nano), id)
}

// A LiveKit booking: the confirmation's whatsapp_message carries /e and /c short links,
// the payload's location_value keeps the long join link, /e answers exactly that link
// (built at click time) and /c a fresh manage token for the booking.
func TestShortLinks_liveKitConfirmationEndToEnd(t *testing.T) {
	h, database, key, userID, _, lk := shortLinkWorkspace(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	mustExec(t, database, `UPDATE event_types SET location_type = 'livekit' WHERE id = ?`, etID)
	whID := seedReminderWebhook(t, database, userID, "booking.created",
		[]string{"id", "location_value", "manage_url", "whatsapp_message"})
	start := futureAt(10, 15, 0)
	id := bookInZone(t, h, slug, start, "America/Lima", `,"language":"es"`)

	data, _ := waitDeliveries(t, database, whID, 1)[0]["data"].(map[string]any)
	msg, _ := data["whatsapp_message"].(string)
	codes := shortCodesInMessage(msg)
	if codes["e"] == "" || codes["c"] == "" || strings.Contains(msg, "/room/") || strings.Contains(msg, "/manage/") {
		t.Fatalf("whatsapp_message = %q; want only /e and /c short links", msg)
	}
	locValue, _ := data["location_value"].(string)
	if !strings.HasPrefix(locValue, shortSite+"/room/booking-"+id+"?t=") {
		t.Fatalf("location_value = %q; want the long LiveKit link, unchanged", locValue)
	}
	if mu, _ := data["manage_url"].(string); !strings.HasPrefix(mu, shortSite+"/manage/") {
		t.Errorf("manage_url = %q; want the long link, unchanged", mu)
	}

	loc := followShortLink(t, h, h.ShortRoomLink, codes["e"])
	if loc != locValue {
		t.Errorf("/e → %q; want the attendee link in location_value %q", loc, locValue)
	}
	u, _ := url.Parse(loc)
	room, role, exp, err := lk.VerifyRoomToken(u.Query().Get("t"))
	if err != nil || room != "booking-"+id || role != "" || !exp.Equal(start.Add(30*time.Minute+2*time.Hour)) {
		t.Errorf("room token: room %q role %q exp %v err %v; want the attendee's, until end + 2 h", room, role, exp, err)
	}

	loc = followShortLink(t, h, h.ShortManageLink, codes["c"])
	b, err := booking.New(database).ValidateManageToken(context.Background(), strings.TrimPrefix(loc, shortSite+"/manage/"))
	if !strings.HasPrefix(loc, shortSite+"/manage/") || err != nil || b.ID != id {
		t.Errorf("/c → %q (%v); want /manage/{a token for this booking}", loc, err)
	}
	// Upper case is the same code (typed by hand).
	if got := followShortLink(t, h, h.ShortManageLink, strings.ToUpper(codes["c"])); !strings.HasPrefix(got, shortSite+"/manage/") {
		t.Errorf("upper-case code → %q", got)
	}
}

// Every /c use issues a NEW token (additive), each of which opens the booking.
func TestShortManageLink_issuesAFreshTokenEachTime(t *testing.T) {
	h, database, key, _, whs, _ := shortLinkWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := bookAndSettle(t, h, database, slug)
	code := newCode(t, whs, id, webhook.ShortLinkManage)
	var before, after int
	database.QueryRow(`SELECT COUNT(*) FROM booking_manage_tokens WHERE booking_id = ?`, id).Scan(&before)
	first := followShortLink(t, h, h.ShortManageLink, code)
	second := followShortLink(t, h, h.ShortManageLink, code)
	database.QueryRow(`SELECT COUNT(*) FROM booking_manage_tokens WHERE booking_id = ?`, id).Scan(&after)
	if first == second || after != before+2 {
		t.Errorf("tokens %d → %d, locations %q / %q; want two different new tokens", before, after, first, second)
	}
	svc := booking.New(database)
	for _, loc := range []string{first, second} {
		if b, err := svc.ValidateManageToken(context.Background(), strings.TrimPrefix(loc, shortSite+"/manage/")); err != nil || b.ID != id {
			t.Errorf("%q does not open the booking: %v", loc, err)
		}
	}
}

// A non-LiveKit booking: /e sends to its web link; a phone call-back, an address, or a
// LiveKit room with LiveKit switched off since, open nothing (friendly page).
func TestShortRoomLink_otherLocations(t *testing.T) {
	h, database, key, _, whs, _ := shortLinkWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := bookAndSettle(t, h, database, slug)
	code := newCode(t, whs, id, webhook.ShortLinkRoom)

	const zoom = "https://us02web.zoom.us/j/81234567890?pwd=QWxhZGRpbjpvcGVuIHNlc2FtZQ"
	mustExec(t, database, `UPDATE bookings SET location_type = 'zoom', location_value = ? WHERE id = ?`, zoom, id)
	if loc := followShortLink(t, h, h.ShortRoomLink, code); loc != zoom {
		t.Errorf("zoom booking: /e → %q; want %q", loc, zoom)
	}
	for _, c := range []struct{ typ, value string }{
		{"phone", "tel:+51 987 654 321"},
		{"in_person", "Av. Larco 123, Miraflores"},
		{"link", "javascript:alert(1)"},
	} {
		mustExec(t, database, `UPDATE bookings SET location_type = ?, location_value = ? WHERE id = ?`, c.typ, c.value, id)
		expectFriendlyPage(t, shortLinkGet(h.ShortRoomLink, "/e/", code), c.typ, code)
	}
	mustExec(t, database, `UPDATE bookings SET location_type = 'livekit', livekit_room = ?, location_value = '' WHERE id = ?`, "booking-"+id, id)
	if loc := followShortLink(t, h, h.ShortRoomLink, code); !strings.HasPrefix(loc, shortSite+"/room/booking-"+id+"?t=") {
		t.Errorf("livekit booking: /e → %q", loc)
	}
	h.SetLiveKit(nil)
	expectFriendlyPage(t, shortLinkGet(h.ShortRoomLink, "/e/", code), "LiveKit switched off", code)
}

// Unknown code, a room code on /c (and the reverse), past the hard cap, and past the
// booking's own time: all the same friendly page. Within the 12 h grace it still works.
func TestShortLinks_unknownWrongKindAndExpired(t *testing.T) {
	h, database, key, _, whs, _ := shortLinkWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := bookAndSettle(t, h, database, slug)
	room := newCode(t, whs, id, webhook.ShortLinkRoom)
	manage := newCode(t, whs, id, webhook.ShortLinkManage)
	mustExec(t, database, `UPDATE bookings SET location_type = 'zoom', location_value = 'https://us02web.zoom.us/j/81234567890?pwd=abcdefghijklmnop' WHERE id = ?`, id)

	expectFriendlyPage(t, shortLinkGet(h.ShortRoomLink, "/e/", "zzzzzzzz"), "unknown code", "zzzzzzzz")
	expectFriendlyPage(t, shortLinkGet(h.ShortManageLink, "/c/", room), "room code on /c", room)
	expectFriendlyPage(t, shortLinkGet(h.ShortRoomLink, "/e/", manage), "manage code on /e", manage)

	now := time.Now()
	// Started 11 h ago (30 min long): both still open.
	bookingTimes(t, database, id, now.Add(-11*time.Hour))
	followShortLink(t, h, h.ShortRoomLink, room)
	followShortLink(t, h, h.ShortManageLink, manage)
	// Started 12 h 10 min ago: nothing left to cancel; the room lasts until end + 12 h.
	bookingTimes(t, database, id, now.Add(-12*time.Hour-10*time.Minute))
	expectFriendlyPage(t, shortLinkGet(h.ShortManageLink, "/c/", manage), "manage code after start + 12 h", manage)
	followShortLink(t, h, h.ShortRoomLink, room)
	// Ended 12 h 5 min ago: the room code is done too.
	bookingTimes(t, database, id, now.Add(-12*time.Hour-35*time.Minute))
	expectFriendlyPage(t, shortLinkGet(h.ShortRoomLink, "/e/", room), "room code after end + 12 h", room)

	// Back in the future, but past the 60-day hard cap.
	bookingTimes(t, database, id, futureAt(10, 15, 0))
	followShortLink(t, h, h.ShortRoomLink, room)
	mustExec(t, database, `UPDATE short_links SET expires_at = ?`, now.Add(-time.Minute).UTC().Format(time.RFC3339))
	expectFriendlyPage(t, shortLinkGet(h.ShortRoomLink, "/e/", room), "past the hard cap", room)
}

// A malformed code is a plain 404 before any database access (the database is closed here).
func TestShortLinks_malformedCodesNeverReachTheDatabase(t *testing.T) {
	h, database, _, _, _, _ := shortLinkWorkspace(t)
	database.Close()
	for _, code := range []string{"abc", "k3pq9ab0", "k3pq9abxy", "k3pq9ab.", "ejemplo1", "../../x", ""} {
		for _, fn := range []http.HandlerFunc{h.ShortRoomLink, h.ShortManageLink} {
			rec := shortLinkGet(fn, "/e/", code)
			if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != "Este enlace no es válido." {
				t.Errorf("%q: %d %q; want the plain 404", code, rec.Code, rec.Body.String())
			}
			assertShortLinkHeaders(t, rec)
		}
	}
}

// Rescheduling revokes the /c codes already sent, as RotateManageToken does for the e-mail's
// manage link (a forwarded confirmation must not keep control of the new date), while the
// booking.rescheduled message brings a new /c that works. Room codes are kept and follow the
// booking with no write: a meeting that had passed and is moved to next week opens again,
// and /e signs a join link valid for the NEW time (location_value holds the old one).
func TestShortLinks_rescheduleRevokesManageCodesAndMovesRoomExpiry(t *testing.T) {
	h, database, key, userID, whs, lk := shortLinkWorkspace(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	mustExec(t, database, `UPDATE event_types SET location_type = 'livekit' WHERE id = ?`, etID)
	id := bookAndSettle(t, h, database, slug)
	whID := seedReminderWebhook(t, database, userID, "booking.rescheduled", []string{"id", "whatsapp_message"})
	room := newCode(t, whs, id, webhook.ShortLinkRoom)
	manage := newCode(t, whs, id, webhook.ShortLinkManage)
	followShortLink(t, h, h.ShortManageLink, manage) // live before the reschedule

	bookingTimes(t, database, id, time.Now().Add(-2*24*time.Hour)) // as if it had passed
	expectFriendlyPage(t, shortLinkGet(h.ShortRoomLink, "/e/", room), "passed booking", room)
	expectFriendlyPage(t, shortLinkGet(h.ShortManageLink, "/c/", manage), "passed booking", manage)

	newStart := futureAt(12, 18, 0)
	mustStatus(t, patchReschedule(t, h, id, newStart.Format(time.RFC3339), key), http.StatusOK, "reschedule")
	// The side effects run on their own goroutine; the revocation comes before this delivery.
	data, _ := waitDeliveries(t, database, whID, 1)[0]["data"].(map[string]any)
	msg, _ := data["whatsapp_message"].(string)
	fresh := shortCodesInMessage(msg)["c"]
	if fresh == "" {
		t.Fatalf("rescheduled whatsapp_message = %q; want a /c short link", msg)
	}

	expectFriendlyPage(t, shortLinkGet(h.ShortManageLink, "/c/", manage), "manage code sent before the reschedule", manage)
	loc := followShortLink(t, h, h.ShortManageLink, fresh)
	if b, err := booking.New(database).ValidateManageToken(context.Background(), strings.TrimPrefix(loc, shortSite+"/manage/")); err != nil || b.ID != id {
		t.Errorf("the rescheduled message's /c → %q (%v); want /manage/{a token for this booking}", loc, err)
	}

	loc = followShortLink(t, h, h.ShortRoomLink, room)
	u, _ := url.Parse(loc)
	_, _, exp, err := lk.VerifyRoomToken(u.Query().Get("t"))
	if err != nil || !exp.Equal(newStart.Add(30*time.Minute+2*time.Hour)) {
		t.Errorf("after reschedule: token exp %v (err %v); want the new end + 2 h", exp, err)
	}
}

// A LiveKit room code stops at end + liveKitJoinGrace (2 h), when the room token it would
// sign is already dead: the friendly page, not a room that refuses the client after asking
// for camera and name. A non-LiveKit link keeps the full end + 12 h (see the test above).
func TestShortRoomLink_liveKitPastItsJoinGrace(t *testing.T) {
	h, database, key, _, whs, _ := shortLinkWorkspace(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	mustExec(t, database, `UPDATE event_types SET location_type = 'livekit' WHERE id = ?`, etID)
	id := bookAndSettle(t, h, database, slug)
	room := newCode(t, whs, id, webhook.ShortLinkRoom)

	bookingTimes(t, database, id, time.Now().Add(-90*time.Minute)) // ended 1 h ago
	if loc := followShortLink(t, h, h.ShortRoomLink, room); !strings.HasPrefix(loc, shortSite+"/room/booking-"+id+"?t=") {
		t.Errorf("ended 1 h ago: /e → %q; want the room", loc)
	}
	bookingTimes(t, database, id, time.Now().Add(-3*time.Hour-30*time.Minute)) // ended 3 h ago
	expectFriendlyPage(t, shortLinkGet(h.ShortRoomLink, "/e/", room), "LiveKit meeting ended 3 h ago", room)
}

// manageTokenExpiry is the stored expires_at of the manage token a /c redirect carries.
func manageTokenExpiry(t *testing.T, database *sql.DB, loc string) time.Time {
	t.Helper()
	sum := sha256.Sum256([]byte(strings.TrimPrefix(loc, shortSite+"/manage/")))
	var s string
	if err := database.QueryRow(`SELECT expires_at FROM booking_manage_tokens WHERE token_hash = ?`, hex.EncodeToString(sum[:])).Scan(&s); err != nil {
		t.Fatalf("token of %q: %v", loc, err)
	}
	exp, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return exp
}

// The token a /c mints lasts as long as the code would still resolve (start + 12 h), not
// the usual 60 days: opened near the end of the window, it is good for about an hour.
func TestShortManageLink_tokenExpiresWithTheCode(t *testing.T) {
	h, database, key, _, whs, _ := shortLinkWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := bookAndSettle(t, h, database, slug)
	manage := newCode(t, whs, id, webhook.ShortLinkManage)

	for _, start := range []time.Time{futureAt(10, 15, 0), time.Now().Add(-11 * time.Hour)} {
		bookingTimes(t, database, id, start)
		exp := manageTokenExpiry(t, database, followShortLink(t, h, h.ShortManageLink, manage))
		if want := start.Add(12 * time.Hour).UTC().Truncate(time.Second); !exp.Equal(want) {
			t.Errorf("start %v: token expires %v; want start + 12 h = %v", start, exp, want)
		}
	}
}

// bookAndSettle books 10 days ahead for a Lima client and waits for the confirmation side
// effects (room, e-mails, webhooks, reminders) to finish, so the test's own writes to the
// booking come after them.
func bookAndSettle(t *testing.T, h *handler.Handler, database *sql.DB, slug string) string {
	t.Helper()
	id := bookInZone(t, h, slug, futureAt(10, 15, 0), "America/Lima", "")
	waitReminderJobs(t, database, id, "confirmation side effects", func(j map[string]reminderJob) bool { return len(j) == 3 })
	return id
}

// Cancelled: /c still opens /manage, which shows the cancellation (and a new date); /e
// opens nothing.
func TestShortLinks_cancelledBooking(t *testing.T) {
	h, database, key, _, whs, _ := shortLinkWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := bookAndSettle(t, h, database, slug)
	room := newCode(t, whs, id, webhook.ShortLinkRoom)
	manage := newCode(t, whs, id, webhook.ShortLinkManage)
	mustExec(t, database, `UPDATE bookings SET location_type = 'zoom', location_value = 'https://us02web.zoom.us/j/81234567890?pwd=abcdefghijklmnop' WHERE id = ?`, id)

	req := authReq(http.MethodPost, "/v1/bookings/"+id+"/cancel", `{"reason":"No puedo"}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CancelBooking)(rec, req)
	mustStatus(t, rec, http.StatusOK, "panel cancel")

	loc := followShortLink(t, h, h.ShortManageLink, manage)
	tok := strings.TrimPrefix(loc, shortSite+"/manage/")
	page := httptest.NewRequest(http.MethodGet, "/manage/"+tok, nil)
	page.SetPathValue("token", tok)
	prec := httptest.NewRecorder()
	h.ManagePage(prec, page)
	mustStatus(t, prec, http.StatusOK, "manage page")
	if body := prec.Body.String(); !strings.Contains(body, "Reserva ya cancelada") || !strings.Contains(body, "Elegir otra fecha") {
		t.Errorf("the manage page does not show the cancellation")
	}
	expectFriendlyPage(t, shortLinkGet(h.ShortRoomLink, "/e/", room), "cancelled booking's room", room)
}

// The editor's preview shows short links with sample codes where a delivery would shorten.
func TestWhatsAppPreview_showsShortLinks(t *testing.T) {
	h, database, key, _, _, _ := shortLinkWorkspace(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	path := "/v1/event-types/" + slug + "/whatsapp-messages/preview"
	body := `{"created":"Entra: {enlace}\nCancela: {cancelar}"}`

	mustExec(t, database, `UPDATE event_types SET location_type = 'livekit' WHERE id = ?`, etID)
	p := mustJSON(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path, slug, body, key), http.StatusOK, "livekit preview")
	if p["created"] != "Entra: https://citas.example.com/e/ejemplo1\nCancela: https://citas.example.com/c/ejemplo2" {
		t.Errorf("livekit preview = %q", p["created"])
	}
	// A Meet link is already shorter than a short link: it stays.
	mustExec(t, database, `UPDATE event_types SET location_type = 'google_meet', location_value = NULL WHERE id = ?`, etID)
	p = mustJSON(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path, slug, body, key), http.StatusOK, "meet preview")
	if p["created"] != "Entra: https://meet.google.com/abc-defg-hij\nCancela: https://citas.example.com/c/ejemplo2" {
		t.Errorf("meet preview = %q", p["created"])
	}
	// Zoom connected, no manual link: the meeting made at booking time has a join_url with
	// ?pwd= (~80 characters), which a delivery shortens - so the preview does too.
	mustExec(t, database, `UPDATE event_types SET location_type = 'zoom', location_value = NULL WHERE id = ?`, etID)
	p = mustJSON(t, slugCall(h, h.PreviewWhatsAppMessages, http.MethodPost, path, slug, body, key), http.StatusOK, "zoom preview")
	if p["created"] != "Entra: https://citas.example.com/e/ejemplo1\nCancela: https://citas.example.com/c/ejemplo2" {
		t.Errorf("zoom preview = %q", p["created"])
	}
}
