package webhook_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/webhook"
)

// Fork (Agenda Maestros 4x4): the WhatsApp short links (fork_short_links.go).

const shortBase = "https://citas.example.com"

// A LiveKit attendee link as mintMeetingLink stores it: ~200 characters.
const liveKitLong = shortBase + "/room/booking-bk-wa?t=eyJyIjoiYm9va2luZy1iay13YSIsImUiOjE3OTA3MDAwMDB9." +
	"c2lnbmF0dXJlLXNpZ25hdHVyZS1zaWduYXR1cmUtc2lnbmF0dXJlLXNpZ25hdHVyZQ"

const manageLong = shortBase + "/manage/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNewShortCode_formatAndAlphabet(t *testing.T) {
	seen := map[string]bool{}
	symbols := map[rune]int{}
	for i := 0; i < 3000; i++ {
		code, err := webhook.NewShortCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != webhook.ShortCodeLen || !webhook.ValidShortCode(code) {
			t.Fatalf("code %q: len %d, valid %v", code, len(code), webhook.ValidShortCode(code))
		}
		if strings.ContainsAny(code, "01ilo") || strings.ToLower(code) != code {
			t.Fatalf("code %q has an ambiguous or upper-case character", code)
		}
		if seen[code] {
			t.Fatalf("code %q drawn twice in 3000", code)
		}
		seen[code] = true
		for _, r := range code {
			symbols[r]++
		}
	}
	// 24 000 symbols over 31: every one shows up, none wildly more than its share (~774).
	if len(symbols) != len(webhook.ShortCodeAlphabet) {
		t.Errorf("%d distinct symbols; want all %d", len(symbols), len(webhook.ShortCodeAlphabet))
	}
	for r, n := range symbols {
		if n < 550 || n > 1000 {
			t.Errorf("symbol %q drawn %d times of 24000; want ~774", r, n)
		}
	}
	if webhook.ShortCodeAlphabet != "23456789abcdefghjkmnpqrstuvwxyz" {
		t.Errorf("alphabet changed: %q (old codes would stop validating)", webhook.ShortCodeAlphabet)
	}
}

func TestValidShortCode(t *testing.T) {
	for code, want := range map[string]bool{
		"k3pq9abx":  true,
		"23456789":  true,
		"zzzzzzzz":  true,
		"k3pq9ab":   false, // 7
		"k3pq9abxy": false, // 9
		"":          false,
		"K3PQ9ABX":  false, // the resolver lower-cases first
		"k3pq9ab0":  false, // 0
		"k3pq9abl":  false, // l
		"k3pq9abo":  false, // o
		"k3pq9abi":  false, // i
		"k3pq9ab1":  false, // 1
		"k3pq/abx":  false,
		"k3pq9abñ":  false,
		"ejemplo1":  false, // the editor's sample is deliberately not a code
	} {
		if got := webhook.ValidShortCode(code); got != want {
			t.Errorf("ValidShortCode(%q) = %v; want %v", code, got, want)
		}
	}
}

func TestShortLinkValidUntil(t *testing.T) {
	start := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)
	end := start.Add(40 * time.Minute)
	if got := webhook.ShortLinkValidUntil(webhook.ShortLinkRoom, start, end); !got.Equal(end.Add(12 * time.Hour)) {
		t.Errorf("room: %v; want end + 12 h", got)
	}
	if got := webhook.ShortLinkValidUntil(webhook.ShortLinkManage, start, end); !got.Equal(start.Add(12 * time.Hour)) {
		t.Errorf("manage: %v; want start + 12 h", got)
	}
}

// The table keeps a KEYED hash only: not the code, not its plain SHA-256, and another
// instance key cannot resolve it.
func TestCreateShortLink_storesOnlyAKeyedHash(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "")
	ctx := context.Background()
	now := time.Now()

	code, err := e.svc.CreateShortLink(ctx, "bk-wa", webhook.ShortLinkRoom, now)
	if err != nil {
		t.Fatal(err)
	}
	var hash, bookingID, kind, expires, created string
	if err := e.db.QueryRow(`SELECT code_hash, booking_id, kind, expires_at, created_at FROM short_links`).
		Scan(&hash, &bookingID, &kind, &expires, &created); err != nil {
		t.Fatal(err)
	}
	plain := sha256.Sum256([]byte(code))
	if strings.Contains(hash, code) || hash == hex.EncodeToString(plain[:]) || len(hash) != 64 {
		t.Errorf("code_hash = %q for code %q; want a 64-hex keyed hash", hash, code)
	}
	for _, v := range []string{bookingID, kind, expires, created} {
		if strings.Contains(v, code) {
			t.Errorf("a column holds the code: %q", v)
		}
	}
	if bookingID != "bk-wa" || kind != "room" {
		t.Errorf("row = %s/%s", bookingID, kind)
	}
	if exp, _ := time.Parse(time.RFC3339, expires); exp.Sub(now) < 59*24*time.Hour || exp.Sub(now) > 61*24*time.Hour {
		t.Errorf("expires_at = %s; want the 60-day hard cap", expires)
	}

	if got, err := e.svc.ResolveShortLink(ctx, code, webhook.ShortLinkRoom, now); err != nil || got != "bk-wa" {
		t.Errorf("resolve = %q, %v", got, err)
	}
	if _, err := e.svc.ResolveShortLink(ctx, code, webhook.ShortLinkManage, now); !errors.Is(err, webhook.ErrShortLinkNotFound) {
		t.Errorf("a room code resolved as a manage code: %v", err)
	}
	if _, err := e.svc.ResolveShortLink(ctx, code, webhook.ShortLinkRoom, now.Add(61*24*time.Hour)); !errors.Is(err, webhook.ErrShortLinkNotFound) {
		t.Errorf("past the hard cap: %v; want not found", err)
	}
	other, err := webhook.New(e.db, strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ResolveShortLink(ctx, code, webhook.ShortLinkRoom, now); !errors.Is(err, webhook.ErrShortLinkNotFound) {
		t.Errorf("another instance key resolved the code: %v", err)
	}
	if _, err := e.svc.CreateShortLink(ctx, "bk-wa", "admin", now); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestPurgeExpiredShortLinks(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "")
	ctx := context.Background()
	now := time.Now()
	if _, err := e.svc.CreateShortLink(ctx, "bk-wa", webhook.ShortLinkRoom, now.Add(-61*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	live, err := e.svc.CreateShortLink(ctx, "bk-wa", webhook.ShortLinkManage, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := webhook.PurgeExpiredShortLinks(ctx, e.db, now); err != nil {
		t.Fatal(err)
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM short_links`).Scan(&n)
	if n != 1 {
		t.Errorf("rows after purge = %d; want 1", n)
	}
	if _, err := e.svc.ResolveShortLink(ctx, live, webhook.ShortLinkManage, now); err != nil {
		t.Errorf("the live code was purged: %v", err)
	}
	// A deleted booking takes its codes with it.
	for _, q := range []string{`DELETE FROM booking_answers WHERE booking_id = 'bk-wa'`, `DELETE FROM booking_attendees WHERE booking_id = 'bk-wa'`, `DELETE FROM bookings WHERE id = 'bk-wa'`} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	e.db.QueryRow(`SELECT COUNT(*) FROM short_links`).Scan(&n)
	if n != 0 {
		t.Errorf("codes left after deleting the booking: %d", n)
	}
}

// DeleteShortLinks revokes only the codes of that booking and kind.
func TestDeleteShortLinks_revokesOneKindOfOneBooking(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "")
	ctx := context.Background()
	now := time.Now()
	room, err := e.svc.CreateShortLink(ctx, "bk-wa", webhook.ShortLinkRoom, now)
	if err != nil {
		t.Fatal(err)
	}
	manage, err := e.svc.CreateShortLink(ctx, "bk-wa", webhook.ShortLinkManage, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := webhook.DeleteShortLinks(ctx, e.db, "bk-wa", webhook.ShortLinkManage); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveShortLink(ctx, manage, webhook.ShortLinkManage, now); !errors.Is(err, webhook.ErrShortLinkNotFound) {
		t.Errorf("revoked manage code: err %v; want ErrShortLinkNotFound", err)
	}
	if _, err := e.svc.ResolveShortLink(ctx, room, webhook.ShortLinkRoom, now); err != nil {
		t.Errorf("the room code was revoked too: %v", err)
	}
	// A new code made afterwards (the rescheduled message's) resolves.
	fresh, err := e.svc.CreateShortLink(ctx, "bk-wa", webhook.ShortLinkManage, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveShortLink(ctx, fresh, webhook.ShortLinkManage, now); err != nil {
		t.Errorf("a code made after the revocation: %v", err)
	}
}

// Raising ShortCodeLen must not break the codes already sent: they are MinShortCodeLen long.
func TestShortCodeLen_neverBelowTheFirstLength(t *testing.T) {
	if webhook.MinShortCodeLen != 8 || webhook.ShortCodeLen < webhook.MinShortCodeLen {
		t.Errorf("MinShortCodeLen %d, ShortCodeLen %d: codes of 8 characters were issued and must keep validating",
			webhook.MinShortCodeLen, webhook.ShortCodeLen)
	}
	if !webhook.ValidShortCode(strings.Repeat("k", webhook.MinShortCodeLen)) || !webhook.ValidShortCode(strings.Repeat("k", webhook.ShortCodeLen)) {
		t.Error("ValidShortCode refuses a length between MinShortCodeLen and ShortCodeLen")
	}
}

var shortLinkRE = regexp.MustCompile(`https://citas\.example\.com/([ec])/([a-z0-9]{8})\b`)

// shortCodes returns the codes of the short links in msg by route letter ("e", "c").
func shortCodes(t *testing.T, msg string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range shortLinkRE.FindAllStringSubmatch(msg, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// whatsapp_message carries /e and /c short links that resolve to the booking; the payload's
// own location_value and manage_url keep the long ones; every render draws new codes.
func TestEnqueue_whatsAppMessage_shortLinks(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Ventas")
	e.svc.SetShortLinkBaseURL(func() string { return shortBase + "/" })
	ctx := context.Background()
	id := waHook(t, e, []string{"booking.created"},
		[]string{webhook.FieldWhatsAppMessage, webhook.FieldLocation, webhook.FieldManageURL})
	enqueue := func() map[string]any {
		t.Helper()
		if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
			ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed",
			LocationValue: liveKitLong, ManageURL: manageLong,
		}); err != nil {
			t.Fatal(err)
		}
		return lastData(t, e, id)
	}
	data := enqueue()
	msg, _ := data["whatsapp_message"].(string)
	codes := shortCodes(t, msg)
	if !strings.Contains(msg, "Para entrar a la sesión: https://citas.example.com/e/"+codes["e"]+"\n") ||
		!strings.HasSuffix(msg, "Si necesitas cancelar o cambiar la fecha: https://citas.example.com/c/"+codes["c"]) {
		t.Fatalf("whatsapp_message = %q", msg)
	}
	if strings.Contains(msg, "/room/") || strings.Contains(msg, "/manage/") {
		t.Errorf("the long links are still in the text: %q", msg)
	}
	if data["location_value"] != liveKitLong || data["manage_url"] != manageLong {
		t.Errorf("payload links changed: location_value %v, manage_url %v", data["location_value"], data["manage_url"])
	}
	now := time.Now()
	if b, err := e.svc.ResolveShortLink(ctx, codes["e"], webhook.ShortLinkRoom, now); err != nil || b != "bk-wa" {
		t.Errorf("/e code: %q, %v", b, err)
	}
	if b, err := e.svc.ResolveShortLink(ctx, codes["c"], webhook.ShortLinkManage, now); err != nil || b != "bk-wa" {
		t.Errorf("/c code: %q, %v", b, err)
	}

	again := shortCodes(t, enqueue()["whatsapp_message"].(string))
	if again["e"] == codes["e"] || again["c"] == codes["c"] {
		t.Errorf("a second message reused a code: %v then %v", codes, again)
	}
	var rows int
	e.db.QueryRow(`SELECT COUNT(*) FROM short_links WHERE booking_id = 'bk-wa'`).Scan(&rows)
	if rows != 4 {
		t.Errorf("short_links rows = %d; want 4 (two per message)", rows)
	}
}

// Only what gets shorter is shortened: a Meet link stays, a call-back number and an address
// stay, a text without the marker makes no code at all.
func TestEnqueue_whatsAppMessage_shortLinksOnlyWhereTheyHelp(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Ventas")
	e.svc.SetShortLinkBaseURL(func() string { return "https://citas.clubmaestros4x4.com" })
	ctx := context.Background()
	if err := e.svc.SetWhatsAppMessages(ctx, "et-wa", map[string]string{webhook.WhatsAppReminder1h: "Sala: {enlace}"}); err != nil {
		t.Fatal(err)
	}
	id := waHook(t, e, []string{webhook.EventReminder1h, webhook.EventReminder5m}, []string{webhook.FieldWhatsAppMessage})
	for _, loc := range []string{"https://meet.google.com/abc-defg-hij", "tel:+51 987 654 321", "Av. Larco 123, Miraflores"} {
		if err := e.svc.Enqueue(ctx, webhook.EventReminder1h, webhook.BookingPayload{
			ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed", LocationValue: loc,
		}); err != nil {
			t.Fatal(err)
		}
		if got := lastData(t, e, id)["whatsapp_message"]; got != "Sala: "+loc {
			t.Errorf("%q: whatsapp_message = %q; want it unchanged", loc, got)
		}
	}
	// A long web link on the club's domain is shortened.
	if err := e.svc.Enqueue(ctx, webhook.EventReminder1h, webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed",
		LocationValue: "https://us02web.zoom.us/j/81234567890?pwd=QWxhZGRpbjpvcGVuIHNlc2FtZQabcdefghijk",
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := lastData(t, e, id)["whatsapp_message"].(string); !regexp.MustCompile(`^Sala: https://citas\.clubmaestros4x4\.com/e/[a-z0-9]{8}$`).MatchString(got) {
		t.Errorf("long Zoom link: whatsapp_message = %q; want the /e short link", got)
	}
	var before, after int
	e.db.QueryRow(`SELECT COUNT(*) FROM short_links`).Scan(&before)
	// The default 5-minute text uses {enlace} only: no /c code for it.
	if err := e.svc.Enqueue(ctx, webhook.EventReminder5m, webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed",
		LocationValue: liveKitLong, ManageURL: manageLong,
	}); err != nil {
		t.Fatal(err)
	}
	e.db.QueryRow(`SELECT COUNT(*) FROM short_links`).Scan(&after)
	var manageRows int
	e.db.QueryRow(`SELECT COUNT(*) FROM short_links WHERE kind = 'manage'`).Scan(&manageRows)
	if after != before+1 || manageRows != 0 {
		t.Errorf("5-minute text: short_links %d → %d, manage rows %d; want one room code only", before, after, manageRows)
	}
}

// If the code cannot be stored, the message still goes, with the long link, and the failure
// is logged - without the link.
func TestEnqueue_whatsAppMessage_fallsBackToTheLongLinks(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Ventas")
	e.svc.SetShortLinkBaseURL(func() string { return shortBase })
	var logs bytes.Buffer
	e.svc.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	ctx := context.Background()
	id := waHook(t, e, []string{"booking.created"}, []string{webhook.FieldWhatsAppMessage})
	if _, err := e.db.Exec(`DROP TABLE short_links`); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed",
		LocationValue: liveKitLong, ManageURL: manageLong,
	}); err != nil {
		t.Fatal(err)
	}
	msg, _ := lastData(t, e, id)["whatsapp_message"].(string)
	if !strings.Contains(msg, "Para entrar a la sesión: "+liveKitLong+"\n") || !strings.HasSuffix(msg, manageLong) {
		t.Errorf("whatsapp_message = %q; want the long links", msg)
	}
	l := logs.String()
	if strings.Count(l, "short link not created") != 2 || !strings.Contains(l, "booking_id=bk-wa") {
		t.Errorf("logs = %s; want both failures logged with the booking", l)
	}
	if strings.Contains(l, "/room/") || strings.Contains(l, "/manage/") || strings.Contains(l, "0123456789abcdef") {
		t.Errorf("the log holds a link: %s", l)
	}
}

// No base URL (a Service nobody wired) = the long links, and nothing stored.
func TestEnqueue_whatsAppMessage_noBaseURLKeepsTheLongLinks(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "Ventas")
	id := waHook(t, e, []string{"booking.created"}, []string{webhook.FieldWhatsAppMessage})
	if err := e.svc.Enqueue(context.Background(), "booking.created", webhook.BookingPayload{
		ID: "bk-wa", HostID: testUserID, StartAt: "2026-09-29T15:00:00Z", Status: "confirmed",
		LocationValue: liveKitLong, ManageURL: manageLong,
	}); err != nil {
		t.Fatal(err)
	}
	if msg, _ := lastData(t, e, id)["whatsapp_message"].(string); !strings.HasSuffix(msg, manageLong) {
		t.Errorf("whatsapp_message = %q", msg)
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM short_links`).Scan(&n)
	if n != 0 {
		t.Errorf("short_links rows = %d; want 0", n)
	}
}

func TestShortLinkForPreview(t *testing.T) {
	for _, c := range []struct{ kind, long, want string }{
		{webhook.ShortLinkRoom, liveKitLong, shortBase + "/e/ejemplo1"},
		{webhook.ShortLinkManage, manageLong, shortBase + "/c/ejemplo1"},
		{webhook.ShortLinkRoom, "https://meet.google.com/abc-defg-hij", "https://meet.google.com/abc-defg-hij"},
		{webhook.ShortLinkRoom, "", ""},
	} {
		if got := webhook.ShortLinkForPreview(shortBase+"/", c.kind, c.long, "ejemplo1"); got != c.want {
			t.Errorf("ShortLinkForPreview(%s, %q) = %q; want %q", c.kind, c.long, got, c.want)
		}
	}
	if got := webhook.ShortLinkForPreview("", webhook.ShortLinkRoom, liveKitLong, "ejemplo1"); got != liveKitLong {
		t.Errorf("no base: %q", got)
	}
}

func TestEnsureForkSchema_shortLinks(t *testing.T) {
	e := newEnv(t)
	seedWhatsAppBooking(t, e, "")
	if err := webhook.EnsureForkSchema(e.db); err != nil { // idempotent on top of New's
		t.Fatal(err)
	}
	for _, obj := range []struct{ typ, name string }{
		{"table", "short_links"},
		{"index", "idx_short_links_booking"},
		{"index", "idx_short_links_expires"},
	} {
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, obj.typ, obj.name).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s %s: count = %d, err = %v; want 1", obj.typ, obj.name, n, err)
		}
	}
	var triggers int
	e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND sql LIKE '%short_links%'`).Scan(&triggers)
	if triggers != 0 {
		t.Errorf("triggers on short_links = %d; want 0", triggers)
	}
	// The kind is checked by the table too.
	if _, err := e.db.Exec(`INSERT INTO short_links (code_hash, booking_id, kind, expires_at) VALUES ('h', 'bk-wa', 'admin', '2030-01-01T00:00:00Z')`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Error("kind 'admin' accepted by the table")
	}
}
