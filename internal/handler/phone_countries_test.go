package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// flagReq calls FlagAsset directly with the given {name} path value, the way the mux
// would after routing GET /assets/flags/{name}.
func flagReq(t *testing.T, method, name string) *httptest.ResponseRecorder {
	t.Helper()
	h := &handler.Handler{}
	req := httptest.NewRequest(method, "/assets/flags/x", nil)
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	h.FlagAsset(rec, req)
	return rec
}

func TestFlagAsset_servesSVGWithLockedDownHeaders(t *testing.T) {
	rec := flagReq(t, http.MethodGet, "pe.svg")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	want := map[string]string{
		"Content-Type":           "image/svg+xml",
		"Cache-Control":          "public, max-age=31536000, immutable",
		"X-Content-Type-Options": "nosniff",
		// An SVG can carry script: its own CSP neutralises it even when opened directly.
		"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q; want %q", k, got, v)
		}
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Errorf("body is not an SVG: %.80q", rec.Body.String())
	}

	// A GET route also answers HEAD: same headers, no body.
	head := flagReq(t, http.MethodHead, "pe.svg")
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Errorf("HEAD: status %d, %d body bytes; want 200 and none", head.Code, head.Body.Len())
	}
}

// Anything but exactly ^[a-z]{2}\.svg$ naming an existing flag is a 404, checked before
// the embedded FS is touched. AC and TA are real calling-code countries with no flag.
func TestFlagAsset_rejectsEverythingElse(t *testing.T) {
	for _, name := range []string{
		"../x.svg", "p.svg", "pee.svg", "PE.svg", "Pe.svg", "zz.svg",
		"ac.svg", "ta.svg", "pe.SVG", "pe.svg\n", "pe", "", "LICENSE-flag-icons",
		"../flags/pe.svg", "pe.svg/", "..svg",
	} {
		rec := flagReq(t, http.MethodGet, name)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q: status = %d; want 404", name, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct == "image/svg+xml" {
			t.Errorf("%q: a 404 must not claim to be an SVG", name)
		}
	}
}

type phoneCountriesBody struct {
	Countries      [][2]string       `json:"countries"`
	TZ2CC          map[string]string `json:"tz2cc"`
	VisitorCountry *string           `json:"visitor_country"`
}

func getPhoneCountries(t *testing.T, cfIPCountry string, presetVary bool) (*httptest.ResponseRecorder, phoneCountriesBody) {
	t.Helper()
	h := &handler.Handler{}
	req := httptest.NewRequest(http.MethodGet, "/v1/phone-countries", nil)
	if cfIPCountry != "" {
		req.Header.Set("CF-IPCountry", cfIPCountry)
	}
	rec := httptest.NewRecorder()
	if presetVary {
		// What PublicCORS leaves behind when EMBED_ALLOWED_ORIGINS is set.
		rec.Header().Add("Vary", "Origin")
	}
	h.PhoneCountries(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 - %s", rec.Code, rec.Body.String())
	}
	var body phoneCountriesBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v - %.200s", err, rec.Body.String())
	}
	return rec, body
}

func TestPhoneCountries_payload(t *testing.T) {
	rec, body := getPhoneCountries(t, "pe", true)

	if n := len(body.Countries); n != 245 {
		t.Errorf("countries = %d; want the 245 libphonenumber entries", n)
	}
	dial := make(map[string]string, len(body.Countries))
	for _, c := range body.Countries {
		dial[c[0]] = c[1]
	}
	// Spot checks, including the two the old hand-written list got wrong (+1809, +1787):
	// the Dominican Republic and Puerto Rico dial +1 like the US and Canada.
	for iso, want := range map[string]string{"PE": "51", "ES": "34", "US": "1", "CA": "1", "DO": "1", "PR": "1", "MX": "52", "BR": "55"} {
		if got := dial[iso]; got != want {
			t.Errorf("%s dials %q; want %q", iso, got, want)
		}
	}
	for _, c := range body.Countries {
		if strings.HasPrefix(c[1], "+") {
			t.Errorf("%s code %q carries a '+'; codes are bare digits", c[0], c[1])
			break
		}
	}
	if got := body.TZ2CC["America/Lima"]; got != "PE" {
		t.Errorf("tz2cc[America/Lima] = %q; want PE", got)
	}
	if got := body.TZ2CC["Europe/Madrid"]; got != "ES" {
		t.Errorf("tz2cc[Europe/Madrid] = %q; want ES", got)
	}
	if body.VisitorCountry == nil || *body.VisitorCountry != "PE" {
		t.Errorf("visitor_country = %v; want \"PE\" from CF-IPCountry: pe", body.VisitorCountry)
	}

	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=3600" {
		t.Errorf("Cache-Control = %q; want private, max-age=3600 (visitor_country is per visitor)", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	vary := strings.Join(rec.Header().Values("Vary"), ", ")
	if !strings.Contains(vary, "Origin") || !strings.Contains(vary, "CF-IPCountry") {
		t.Errorf("Vary = %q; want PublicCORS's Origin kept and CF-IPCountry added", vary)
	}
}

// Every zone in tz2cc names a country the picker offers, so the time-zone step of the
// detection can land on it. The first generated file walked every AA..ZZ code, and ICU
// still knows withdrawn ones that sort first: DD (East Germany) took Europe/Berlin from DE,
// VD took Asia/Ho_Chi_Minh, and a Berlin visitor with an en-US browser and no CF-IPCountry
// started on +1. phone-data.gen.mjs now builds tz2cc from the offered countries only; this
// is the guard against a regeneration bringing the old walk back.
func TestPhoneCountries_everyZoneNamesAnOfferedCountry(t *testing.T) {
	_, body := getPhoneCountries(t, "", false)
	offered := make(map[string]bool, len(body.Countries))
	for _, c := range body.Countries {
		offered[c[0]] = true
	}
	for zone, cc := range body.TZ2CC {
		if !offered[cc] {
			t.Errorf("tz2cc[%s] = %q, which the picker does not offer", zone, cc)
		}
	}
	for zone, want := range map[string]string{
		"Europe/Berlin": "DE", "Europe/Belgrade": "RS", "Asia/Ho_Chi_Minh": "VN", "Asia/Saigon": "VN",
		"Asia/Yangon": "MM", "Africa/Harare": "ZW", "Asia/Aden": "YE", "America/Curacao": "CW",
		"Pacific/Efate": "VU", "America/Sao_Paulo": "BR", "America/Mexico_City": "MX",
	} {
		if got := body.TZ2CC[zone]; got != want {
			t.Errorf("tz2cc[%s] = %q; want %q", zone, got, want)
		}
	}
	// A floor, not an exact count: the zone list comes from ICU and shifts between releases.
	if n := len(body.TZ2CC); n < 400 {
		t.Errorf("tz2cc has %d zones; want 400+ (a truncated phone-data.json?)", n)
	}
}

// visitorCountry, seen through the API: upper-cased, and only two ASCII letters that are
// not Cloudflare's "unknown" (XX) or "Tor" (T1). It is embedded in a <script> on the
// booking page, so the filter is also what keeps a forged header from injecting markup.
func TestPhoneCountries_visitorCountryFilter(t *testing.T) {
	for in, want := range map[string]string{
		"pe": "PE", "PE": "PE", "Es": "ES",
		"XX": "", "xx": "", "T1": "", "t1": "",
		"PER": "", "P": "", "": "", "P1": "", "1P": "",
		"</": "", `"}`: "",
		"pſ": "", // strings.ToUpper("ſ") == "S": must be rejected on the raw bytes
	} {
		_, body := getPhoneCountries(t, in, false)
		got := "<missing>"
		if body.VisitorCountry != nil {
			got = *body.VisitorCountry
		}
		if got != want {
			t.Errorf("CF-IPCountry %q -> visitor_country %q; want %q", in, got, want)
		}
	}
}

// Every country the picker can show has a flag the route actually serves, except AC and
// TA, which flag-icons does not draw. The widget and the page build the flag URL from the
// ISO code alone, so a gap here is a broken image on some visitor's screen.
func TestPhoneCountries_everyCountryHasAServedFlag(t *testing.T) {
	_, body := getPhoneCountries(t, "", false)
	noFlag := map[string]bool{"AC": true, "TA": true}
	served := 0
	for _, c := range body.Countries {
		rec := flagReq(t, http.MethodGet, strings.ToLower(c[0])+".svg")
		switch {
		case noFlag[c[0]] && rec.Code != http.StatusNotFound:
			t.Errorf("%s: status %d; it has no flag, want 404", c[0], rec.Code)
		case !noFlag[c[0]] && rec.Code != http.StatusOK:
			t.Errorf("%s: status %d; want its flag", c[0], rec.Code)
		case rec.Code == http.StatusOK:
			served++
		}
	}
	if served != 243 {
		t.Errorf("served %d flags; want 243", served)
	}
}

// phonePayloadRe matches the inline window.__CALNODE_PHONE assignment with its data, not
// a mere mention of the name (the page's own script may read window.__CALNODE_PHONE on
// every page).
var phonePayloadRe = regexp.MustCompile(`window\.__CALNODE_PHONE\s*=\s*\{"countries":\[\["AC","247"\]`)

func bookPage(t *testing.T, h *handler.Handler, slug, cfIPCountry string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	if cfIPCountry != "" {
		req.Header.Set("CF-IPCountry", cfIPCountry)
	}
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	body := rec.Body.String()
	// A template error streams a 200 and stops mid-page; the closing tag and the
	// calendar/submit markup after the questions prove the render finished.
	if !strings.Contains(body, "</html>") {
		t.Fatalf("page is truncated. Last 200 bytes: %q", body[max(0, len(body)-200):])
	}
	for _, marker := range []string{"submit-btn", "tz-select"} {
		if !strings.Contains(body, marker) {
			t.Errorf("%q missing: the page stopped rendering before it", marker)
		}
	}
	return rec, body
}

func TestBookPage_phoneQuestionInlinesCountryData(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	addQuestion(t, h, apiKey, slug, `{"label": "WhatsApp", "type": "phone", "required": true}`)

	rec, body := bookPage(t, h, slug, "pe")
	if !phonePayloadRe.MatchString(body) {
		t.Fatal("window.__CALNODE_PHONE = {countries, ...} is missing from a page with a phone question")
	}
	if !strings.Contains(body, `"visitorCountry":"PE"`) {
		t.Error(`visitorCountry from CF-IPCountry "pe" should be inlined as "PE"`)
	}
	if !strings.Contains(body, `"tz2cc":{`) {
		t.Error("tz2cc missing from the inline payload")
	}
	if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "CF-IPCountry") ||
		!strings.Contains(vary, "Accept-Language") || !strings.Contains(vary, "Cookie") {
		t.Errorf("Vary = %q; the body now depends on CF-IPCountry too", vary)
	}

	// A hostile header never reaches the <script>.
	_, body = bookPage(t, h, slug, "</script>")
	if !strings.Contains(body, `"visitorCountry":""`) {
		t.Error(`an invalid CF-IPCountry should inline visitorCountry ""`)
	}
}

func TestBookPage_noPhoneQuestionNoCountryData(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	addQuestion(t, h, apiKey, slug, `{"label": "Anything else?", "type": "text", "required": false}`)

	rec, body := bookPage(t, h, slug, "pe")
	if phonePayloadRe.MatchString(body) || strings.Contains(body, `"tz2cc":{`) || strings.Contains(body, `"visitorCountry"`) {
		t.Error("a page without a phone question must not carry the ~13 KB country payload")
	}
	if vary := rec.Header().Get("Vary"); vary != "Accept-Language, Cookie" {
		t.Errorf("Vary = %q; want the unchanged Accept-Language, Cookie", vary)
	}
}
