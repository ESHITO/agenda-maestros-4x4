package handler

import (
	"bytes"
	"embed"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
)

// Country picker data for "phone" intake questions (book.html and embed.js).
//
// Clients and mentors live in many countries, so there is NO fixed default country:
// the picker starts from a best guess (visitorCountry below, then the browser's time
// zone, then its language region — see BookingLogic.phoneDetectCountry) and may start
// with nothing chosen at all.
//
// phone-data.json is libphonenumber's country → calling-code table (245 [ISO, code]
// pairs) plus an IANA time zone → country map. It is generated data: do not hand-edit
// it here. This file owns the ONLY //go:embed of it and the only visitorCountry, shared
// by BookPage (window.__CALNODE_PHONE) and GET /v1/phone-countries (the widget).

//go:embed assets/phone-data.json
var phoneDataRaw []byte

// flagFS holds the flag-icons SVGs (4x3, MIT; licence in assets/flags/LICENSE-flag-icons,
// which the *.svg pattern leaves out). AC and TA have no flag and 404.
//
//go:embed assets/flags/*.svg
var flagFS embed.FS

// phoneData is phone-data.json split once at start-up into its two halves, kept as raw
// JSON so every response re-emits them without a decode/encode round trip. A malformed
// file panics here, at init, like a broken locale file does in internal/i18n: it is an
// embedded build input, and every handler test would fail on it before it ships.
var phoneData = func() (d struct {
	Countries json.RawMessage `json:"countries"`
	TZ2CC     json.RawMessage `json:"tz2cc"`
}) {
	if err := json.Unmarshal(phoneDataRaw, &d); err != nil || len(d.Countries) == 0 || len(d.TZ2CC) == 0 {
		panic("handler: assets/phone-data.json is malformed or missing countries/tz2cc")
	}
	return d
}()

// visitorCountry returns the visitor's ISO 3166-1 alpha-2 country from the CF-IPCountry
// header Cloudflare adds when it sits in front (IP geolocation, the way Calendly guesses
// it), upper-cased; "" when the header is absent or not exactly two ASCII letters, and for
// Cloudflare's "XX" (unknown) and "T1" (Tor) markers.
//
// It is only the picker's starting value: a forged header grants nothing, the visitor can
// change the country anyway. The strict A-Z check is still what matters for safety — the
// value is embedded in a <script> on the booking page, so nothing else may pass through.
// The raw bytes are checked BEFORE upper-casing: strings.ToUpper maps some non-ASCII
// letters onto ASCII ("ſ" → "S"), which would let a 3-byte value through as two letters.
func visitorCountry(r *http.Request) string {
	v := r.Header.Get("CF-IPCountry")
	if len(v) != 2 {
		return ""
	}
	out := make([]byte, 2)
	for i := 0; i < 2; i++ {
		c := v[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out[i] = c
		case c >= 'a' && c <= 'z':
			out[i] = c - ('a' - 'A')
		default:
			return ""
		}
	}
	cc := string(out)
	if cc == "XX" || cc == "T1" {
		return ""
	}
	return cc
}

// phonePageJSON is the window.__CALNODE_PHONE payload BookPage inlines when the event
// type has a "phone" question: {countries, tz2cc, visitorCountry}. The key is camelCase
// here (JS object) and snake_case (visitor_country) in the API response. Built with
// json.Marshal, which compacts the raw halves and escapes <, >, & — the same guarantee
// the page's other inline JSON relies on. nil on the (impossible) marshal error, which
// the template treats as "no phone data".
func phonePageJSON(r *http.Request) []byte {
	b, err := json.Marshal(struct {
		Countries      json.RawMessage `json:"countries"`
		TZ2CC          json.RawMessage `json:"tz2cc"`
		VisitorCountry string          `json:"visitorCountry"`
	}{phoneData.Countries, phoneData.TZ2CC, visitorCountry(r)})
	if err != nil {
		return nil
	}
	return b
}

// PhoneCountries serves GET /v1/phone-countries for the embed widget:
// {"countries":[[ISO, code], ...], "tz2cc":{zone: ISO, ...}, "visitor_country":"PE"}.
// Public and CORS-wrapped like /v1/event-types/{slug}/public (see server.go). The body
// differs per visitor (visitor_country), so it is cacheable only by the browser; Vary is
// Added (not Set) to keep the "Vary: Origin" PublicCORS may already have written.
func (h *Handler) PhoneCountries(w http.ResponseWriter, r *http.Request) {
	// Before writeJSON, which calls WriteHeader straight away.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Add("Vary", "CF-IPCountry")
	h.writeJSON(w, http.StatusOK, struct {
		Countries      json.RawMessage `json:"countries"`
		TZ2CC          json.RawMessage `json:"tz2cc"`
		VisitorCountry string          `json:"visitor_country"`
	}{phoneData.Countries, phoneData.TZ2CC, visitorCountry(r)})
}

// flagNameRe is the only file name GET /assets/flags/{name} accepts: two lower-case
// letters + ".svg". Checked before touching the embedded FS, so "../x.svg", "PE.svg",
// "pee.svg" and LICENSE-flag-icons never reach it.
var flagNameRe = regexp.MustCompile(`^[a-z]{2}\.svg$`)

// FlagAsset serves GET /assets/flags/{name}, a country flag for the phone picker.
// Immutable (the files only change with a deploy that changes their bytes, and the
// picker references them by fixed name). An SVG can carry script, so the response has
// its own CSP that forbids everything but inline styles — opened directly in a tab it
// still cannot run anything — plus nosniff. Content-Type is set explicitly because on
// Windows mime.TypeByExtension reads the registry. Cross-Origin-Resource-Policy lets the
// embed widget show the flags on host sites that enable COEP.
func (h *Handler) FlagAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !flagNameRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	b, err := flagFS.ReadFile("assets/flags/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}
