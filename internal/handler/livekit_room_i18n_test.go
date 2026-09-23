package handler_test

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/i18n"
	"github.com/calnode/calnode/internal/livekit"
)

// The video room (GET /room/{room}) is rendered in the visitor's language by the same
// per-request resolution as book.html/manage.html: ?lang= > calnode_lang cookie >
// Accept-Language > the operator's fallback. These tests pin that, plus the room_* string
// table the page hands to livekit-room.js through window.__CALNODE_I18N.
//
// Expected text comes from the locale tables themselves (the oracle), not from copies of
// the wording here, so a translator refining a sentence does not break the tests - while
// roomString still fails if a key is missing or was left in English.

// newRoomHandler returns a workspace with LiveKit configured, so LiveKitRoom renders the
// page instead of 404ing. The credentials are dummies: rendering the page calls nothing.
func newRoomHandler(t *testing.T) (*handler.Handler, string) {
	t.Helper()
	h, apiKey, _ := setupWorkspace(t)
	h.SetLiveKit(livekit.New("wss://livekit.example.test", "devkey", "devsecret", [32]byte{}))
	return h, apiKey
}

func getRoomPage(t *testing.T, h *handler.Handler, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("room", "booking-test")
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.LiveKitRoom(rec, req)
	mustStatus(t, rec, http.StatusOK, "GET "+target)
	// A failing {{call .T ...}} aborts html/template mid-page; the status is already 200 by
	// then, so the truncated body is the only symptom.
	if body := strings.TrimSpace(rec.Body.String()); !strings.HasSuffix(body, "</html>") {
		t.Fatalf("GET %s: page was cut off mid-render; tail: %q", target, tail(body, 300))
	}
	return rec
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// roomString is code's translation of key, failing loudly when the key is missing (T would
// hand back the key itself) or, outside English, still carries the English text.
func roomString(t *testing.T, code, key string) string {
	t.Helper()
	loc := i18n.Get(code)
	if loc == nil {
		t.Fatalf("locale %q is not shipped", code)
	}
	s := loc.T(key)
	if s == key {
		t.Fatalf("locale %q has no %q: the room_* strings belong in internal/i18n/locales/*.json", code, key)
	}
	if code != i18n.DefaultCode && s == i18n.Default().T(key) {
		t.Fatalf("locale %q's %q is still the English text %q", code, key, s)
	}
	return s
}

// roomKeys lists the room_* keys of the reference (English) table.
func roomKeys(t *testing.T) []string {
	t.Helper()
	b, err := i18n.Default().JSON()
	if err != nil {
		t.Fatalf("english table: %v", err)
	}
	var all map[string]string
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatalf("english table: %v", err)
	}
	var keys []string
	for k := range all {
		if strings.HasPrefix(k, "room_") {
			keys = append(keys, k)
		}
	}
	return keys
}

// pageI18NTable extracts and decodes the window.__CALNODE_I18N table from the page.
func pageI18NTable(t *testing.T, body string) map[string]string {
	t.Helper()
	const open, closing = "window.__CALNODE_I18N = ", ";</script>"
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("page has no window.__CALNODE_I18N table")
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, closing)
	if j < 0 {
		t.Fatalf("window.__CALNODE_I18N table is not terminated")
	}
	// The table sits verbatim (template.JS) inside a <script> block, so it is only safe
	// while it stays HTML-escaped JSON: no raw <, > or & (a "</script>" in a translation
	// would end the block) and no raw U+2028/U+2029. json.Marshal guarantees that; this pins
	// it against a later switch to an encoder with SetEscapeHTML(false). en.json's
	// "Leave & pass host" keeps the & case live.
	if raw := rest[:j]; strings.ContainsAny(raw, "<>&\u2028\u2029") {
		t.Errorf("window.__CALNODE_I18N is not HTML-escaped JSON: %.300s", raw)
	}
	var table map[string]string
	if err := json.Unmarshal([]byte(rest[:j]), &table); err != nil {
		t.Fatalf("window.__CALNODE_I18N is not valid JSON: %v - %.300s", err, rest[:j])
	}
	return table
}

// assertRoomPageIn checks one rendered page against locale code end to end: <html lang>,
// the server-rendered markup, the injected table, and (outside English) that no English
// string the template renders was left behind.
func assertRoomPageIn(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	body := rec.Body.String()

	if want := fmt.Sprintf(`<html lang="%s">`, code); !strings.Contains(body, want) {
		t.Errorf("want %s; head: %.200s", want, body)
	}

	join := template.HTMLEscapeString(roomString(t, code, "room_join_button"))
	if want := `class="join-btn">` + join + `</button>`; !strings.Contains(body, want) {
		t.Errorf("join button not in %s: want %q", code, want)
	}
	title := template.HTMLEscapeString(roomString(t, code, "room_prejoin_title"))
	if want := "<h1>" + title + "</h1>"; !strings.Contains(body, want) {
		t.Errorf("prejoin title not in %s: want %q", code, want)
	}
	placeholder := template.HTMLEscapeString(roomString(t, code, "room_name_placeholder"))
	if want := `placeholder="` + placeholder + `"`; !strings.Contains(body, want) {
		t.Errorf("name placeholder (an attribute) not in %s: want %q", code, want)
	}

	// The table livekit-room.js reads: every room_* key, in this locale, and nothing else.
	table := pageI18NTable(t, body)
	keys := roomKeys(t)
	if len(table) != len(keys) {
		t.Errorf("i18n table has %d entries; want the %d room_* keys", len(table), len(keys))
	}
	for k := range table {
		if !strings.HasPrefix(k, "room_") {
			t.Errorf("i18n table carries non-room key %q", k)
		}
	}
	loc := i18n.Get(code)
	for _, k := range keys {
		if got, want := table[k], loc.T(k); got != want {
			t.Errorf("i18n table[%q] = %q; want %q (%s)", k, got, want, code)
		}
	}

	// A {{call .T "room_..."}} whose key is in no locale file renders the key itself. Outside
	// the injected table (where the keys are JSON object keys) no "room_" may reach the page.
	markup := body
	if i := strings.Index(markup, "window.__CALNODE_I18N = "); i >= 0 {
		if j := strings.Index(markup[i:], ";</script>"); j >= 0 {
			markup = markup[:i] + markup[i+j:]
		}
	}
	if i := strings.Index(markup, "room_"); i >= 0 {
		t.Errorf("%s page renders a raw i18n key near %q: add it to internal/i18n/locales/*.json", code, markup[max(0, i-40):min(len(markup), i+60)])
	}

	if code == i18n.DefaultCode {
		return
	}
	// No English left in the markup: for every room_* string this locale translates
	// differently, the English text must not appear as element text or attribute value.
	en := i18n.Default()
	for _, k := range keys {
		enText := en.T(k)
		if loc.T(k) == enText {
			continue // same word in both languages (e.g. "Chat")
		}
		esc := template.HTMLEscapeString(enText)
		if strings.Contains(body, ">"+esc+"<") || strings.Contains(body, `="`+esc+`"`) {
			t.Errorf("%s page still shows the English %q (%s)", code, enText, k)
		}
	}
}

func TestLiveKitRoom_spanishFromAcceptLanguage(t *testing.T) {
	h, _ := newRoomHandler(t)
	rec := getRoomPage(t, h, "/room/booking-test?t=x", http.Header{
		"Accept-Language": {"es-MX,es;q=0.9,en;q=0.5"},
	})
	assertRoomPageIn(t, rec, "es")
	if v := rec.Header().Get("Vary"); !strings.Contains(v, "Accept-Language") || !strings.Contains(v, "Cookie") {
		t.Errorf("Vary = %q; the page varies by Accept-Language and the language cookie", v)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q; the room page must stay no-store", cc)
	}
	if sc := rec.Header().Get("Set-Cookie"); sc != "" {
		t.Errorf("Set-Cookie = %q; only an explicit ?lang= pins the language", sc)
	}
}

func TestLiveKitRoom_englishFromAcceptLanguage(t *testing.T) {
	h, _ := newRoomHandler(t)
	rec := getRoomPage(t, h, "/room/booking-test?t=x", http.Header{
		"Accept-Language": {"en-US,en;q=0.9"},
	})
	assertRoomPageIn(t, rec, "en")
	// "&" reaches the markup escaped once, never double-escaped.
	pass := template.HTMLEscapeString(roomString(t, "en", "room_leave_and_pass_host"))
	if !strings.Contains(rec.Body.String(), ">"+pass+"</button>") {
		t.Errorf("want %q in the leave modal", pass)
	}
}

// Every shipped locale (fr-CA included) renders the whole room page in its own language:
// no key missing, none left in English, the table complete.
func TestLiveKitRoom_everyShippedLocale(t *testing.T) {
	h, _ := newRoomHandler(t)
	for _, opt := range i18n.SupportedLocales() {
		t.Run(opt.Code, func(t *testing.T) {
			rec := getRoomPage(t, h, "/room/booking-test?t=x&lang="+opt.Code, nil)
			assertRoomPageIn(t, rec, opt.Code)
		})
	}
}

// ?lang= wins over Accept-Language and is remembered in the calnode_lang cookie.
func TestLiveKitRoom_langQueryOverridesAndPersists(t *testing.T) {
	h, _ := newRoomHandler(t)
	rec := getRoomPage(t, h, "/room/booking-test?t=x&lang=pt", http.Header{
		"Accept-Language": {"es-MX,es;q=0.9"},
	})
	assertRoomPageIn(t, rec, "pt")
	if sc := rec.Header().Get("Set-Cookie"); !strings.Contains(sc, "calnode_lang=pt") {
		t.Errorf("Set-Cookie = %q; want calnode_lang=pt", sc)
	}
}

// The cookie set by the booking page's language switcher (Path=/) carries into the room,
// so a visitor who booked in Spanish joins a Spanish room whatever their browser says.
func TestLiveKitRoom_languageCookieFromBookingPage(t *testing.T) {
	h, _ := newRoomHandler(t)
	rec := getRoomPage(t, h, "/room/booking-test?t=x", http.Header{
		"Accept-Language": {"en-US,en;q=0.9"},
		"Cookie":          {"calnode_lang=es"},
	})
	assertRoomPageIn(t, rec, "es")
}

// A browser asking only for languages we do not ship gets the operator's fallback.
func TestLiveKitRoom_respectsFallbackLocale(t *testing.T) {
	requireUnsupported(t)
	h, apiKey := newRoomHandler(t)
	preq := authReq(http.MethodPatch, "/v1/settings/branding", `{"fallback_locale":"es"}`, apiKey)
	prec := httptest.NewRecorder()
	h.RequireAuth(h.PatchBranding)(prec, preq)
	mustStatus(t, prec, http.StatusOK, "set fallback")

	rec := getRoomPage(t, h, "/room/booking-test?t=x", http.Header{
		"Accept-Language": {fmt.Sprintf("%s;q=0.9,%s;q=0.5", unsupportedLocaleCodes[0], unsupportedLocaleCodes[1])},
	})
	assertRoomPageIn(t, rec, "es")
}
