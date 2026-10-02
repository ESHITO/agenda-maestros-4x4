package webhook_test

// Fork (Agenda Maestros 4x4): phone-data.json indexed (fork_phone_table.go), the Spanish
// country names (fork_countries.go) and the short name (fork_names.go) of the host notices.

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/webhook"
)

// phoneTable parses the real phone-data.json the handler embeds.
func phoneTable(t *testing.T) *webhook.PhoneTable {
	t.Helper()
	raw, err := os.ReadFile("../handler/assets/phone-data.json")
	if err != nil {
		t.Fatal(err)
	}
	pt, err := webhook.ParsePhoneTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	return pt
}

func TestParsePhoneTable_refusesBrokenFiles(t *testing.T) {
	for _, raw := range []string{``, `{}`, `{"countries":[],"tz2cc":{"America/Lima":"PE"}}`,
		`{"countries":[["PE","51"]],"tz2cc":{}}`, `{"countries":[["PE"]],"tz2cc":{"America/Lima":"PE"}}`} {
		if _, err := webhook.ParsePhoneTable([]byte(raw)); err == nil {
			t.Errorf("ParsePhoneTable(%q) accepted a broken file", raw)
		}
	}
}

func TestCountryFor(t *testing.T) {
	pt := phoneTable(t)
	for _, c := range []struct{ phone, tz, want string }{
		{"+51 987 654 321", "", "PE"},
		{"0051987654321", "", "PE"}, // 00 = international prefix
		{"+52 55 1234 5678", "", "MX"},
		{"+57 300 123 4567", "Europe/Madrid", "CO"}, // one country per code: the zone does not matter
		{"+593 99 123 4567", "", "EC"},
		{"+34 612 345 678", "", "ES"},
		// +1 is shared: the Spanish-speaking area codes first, then the zone, then the US.
		{"+1 809 555 1234", "", "DO"},
		{"+1 829 555 1234", "America/New_York", "DO"},
		{"+1 787 555 1234", "", "PR"},
		{"+1 416 555 1234", "America/Toronto", "CA"},
		{"+1 212 555 1234", "America/Mexico_City", "US"}, // a Mexican zone is not a +1 country
		{"+1 212 555 1234", "", "US"},
		{"+7 701 123 4567", "Asia/Almaty", "KZ"},
		{"+7 912 123 4567", "", "RU"},
		{"+44 7700 900123", "Europe/London", "GB"},
		{"+999 123 4567", "", ""}, // no such code
		{"987654321", "", ""},     // no country code at all
		{"", "America/Lima", ""},
	} {
		if got := pt.CountryFor(c.phone, c.tz); got != c.want {
			t.Errorf("CountryFor(%q, %q) = %q; want %q", c.phone, c.tz, got, c.want)
		}
	}
	var nilTable *webhook.PhoneTable
	if got := nilTable.CountryFor("+51987654321", ""); got != "" {
		t.Errorf("a nil table answered %q", got)
	}
}

// Every dial code several countries share is settled by mainCountryForCode, with one of
// those countries.
func TestMainCountryForCode_coversEverySharedCode(t *testing.T) {
	pt := phoneTable(t)
	shared := pt.SharedCodes()
	if len(shared) == 0 {
		t.Fatal("no shared codes found; the table did not load")
	}
	for _, code := range shared {
		cc, ok := webhook.MainCountryForCodeForTest[code]
		if !ok {
			t.Errorf("shared dial code +%s (%v) has no entry in mainCountryForCode", code, pt.CountriesForCode(code))
			continue
		}
		if !slices.Contains(pt.CountriesForCode(code), cc) {
			t.Errorf("mainCountryForCode[%s] = %s, not one of %v", code, cc, pt.CountriesForCode(code))
		}
	}
	for code := range webhook.MainCountryForCodeForTest {
		if !slices.Contains(shared, code) {
			t.Errorf("mainCountryForCode has +%s, which is no longer shared", code)
		}
	}
}

// Every country whose zones tell different times has a hand-picked main zone, which is one
// of its own zones and loads; countries with one offset never need one.
func TestMainZoneByCountry_complete(t *testing.T) {
	pt := phoneTable(t)
	for _, cc := range pt.Countries() {
		if len(pt.Zones(cc)) == 0 || pt.SingleOffset(cc) {
			continue
		}
		z, ok := webhook.MainZoneByCountryForTest[cc]
		if !ok {
			t.Errorf("%s has zones with different offsets %v but no entry in mainZoneByCountry", cc, pt.Zones(cc))
			continue
		}
		if !slices.Contains(pt.Zones(cc), z) {
			t.Errorf("mainZoneByCountry[%s] = %s, not a zone of %s", cc, z, cc)
		}
		if _, err := time.LoadLocation(z); err != nil {
			t.Errorf("mainZoneByCountry[%s] = %s does not load: %v", cc, z, err)
		}
	}
	for cc := range webhook.MainZoneByCountryForTest {
		if pt.SingleOffset(cc) {
			t.Errorf("mainZoneByCountry has %s, whose zones share one offset (the first alphabetical is used)", cc)
		}
	}
}

func TestMainZone(t *testing.T) {
	pt := phoneTable(t)
	for cc, want := range map[string]string{
		"PE": "America/Lima",
		"US": "America/New_York",
		"MX": "America/Mexico_City",
		"ES": "Europe/Madrid",
		"AR": "America/Argentina/Buenos_Aires", // 17 alias zones, one offset: the first alphabetical
		"XK": "",                               // no zone in tz2cc
		"":   "",
	} {
		if got := pt.MainZone(cc); got != want {
			t.Errorf("MainZone(%q) = %q; want %q", cc, got, want)
		}
	}
}

// Different offsets AT THAT INSTANT, not "several zones": India's two names are one time,
// and Spain tells two (Madrid, Canarias) all year.
func TestMultiOffset(t *testing.T) {
	pt := phoneTable(t)
	summer := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
	for cc, want := range map[string]bool{"IN": false, "PE": false, "AR": false, "US": true, "ES": true, "MX": true, "": false} {
		if got := pt.MultiOffset(cc, summer); got != want {
			t.Errorf("MultiOffset(%q) = %v; want %v", cc, got, want)
		}
	}
}

func TestCountryOfZone(t *testing.T) {
	pt := phoneTable(t)
	for zone, want := range map[string]string{
		"America/Lima": "PE", "America/Chicago": "US", "Asia/Kolkata": "IN",
		"UTC": "", "Etc/UTC": "", "": "", "Mars/Olympus": "",
	} {
		if got := pt.CountryOfZone(zone); got != want {
			t.Errorf("CountryOfZone(%q) = %q; want %q", zone, got, want)
		}
	}
}

// Every country of phone-data.json has a Spanish name, a flag and a label.
func TestCountryNameES_everyCountry(t *testing.T) {
	pt := phoneTable(t)
	for _, cc := range pt.Countries() {
		if webhook.CountryNameES(cc) == "" {
			t.Errorf("%s has no Spanish name: re-run fork_countries.gen.mjs", cc)
		}
		if webhook.FlagEmoji(cc) == "" {
			t.Errorf("%s has no flag", cc)
		}
	}
	if got := webhook.CountryLabel("PE"); got != "Perú 🇵🇪" {
		t.Errorf(`CountryLabel("PE") = %q`, got)
	}
	if got := webhook.CountryLabel("US"); got != "Estados Unidos 🇺🇸" {
		t.Errorf(`CountryLabel("US") = %q`, got)
	}
	if webhook.CountryLabel("") != "" || webhook.FlagEmoji("P1") != "" || webhook.FlagEmoji("PER") != "" {
		t.Error("an empty or malformed code must give no label or flag")
	}
}

// The generated Spanish table is CLDR's, cross-checked against Intl through node (skipped
// without node, like the i18n CLDR checks): a hand edit of fork_countries.go fails here.
func TestCountryNamesES_matchCLDR(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; skipping the CLDR cross-check")
	}
	codes := make([]string, 0, len(webhook.CountryNamesESForTest))
	for cc := range webhook.CountryNamesESForTest {
		codes = append(codes, cc)
	}
	slices.Sort(codes)
	const script = `
const n = new Intl.DisplayNames(['es'], {type: 'region'});
const out = {};
for (const cc of process.argv.slice(1)) out[cc] = n.of(cc);
process.stdout.write(JSON.stringify(out));
`
	raw, err := exec.Command(node, append([]string{"-e", script, "--"}, codes...)...).Output()
	if err != nil {
		t.Fatalf("running the CLDR probe under node failed: %v", err)
	}
	var want map[string]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parsing the CLDR probe output: %v (%q)", err, raw)
	}
	for _, cc := range codes {
		if got := webhook.CountryNameES(cc); got != want[cc] {
			t.Errorf("CountryNameES(%s) = %q; CLDR says %q", cc, got, want[cc])
		}
	}
}

func TestZoneCityES(t *testing.T) {
	for zone, want := range map[string]string{
		"America/Mexico_City":            "Ciudad de México",
		"America/New_York":               "Nueva York",
		"America/Chicago":                "Chicago",
		"America/Argentina/Buenos_Aires": "Buenos Aires",
		"Atlantic/Canary":                "Canarias",
	} {
		if got := webhook.ZoneCityES(zone); got != want {
			t.Errorf("ZoneCityES(%q) = %q; want %q", zone, got, want)
		}
	}
}

func TestShortName(t *testing.T) {
	for in, want := range map[string]string{
		"María Fernanda López García":      "María López",
		"Juan Pérez García":                "Juan Pérez",
		"Ana Gómez":                        "Ana Gómez",
		"María del Carmen Rodríguez López": "María Rodríguez",
		"Juan de la Cruz Pérez":            "Juan de la Cruz",
		"José Ortega y Gasset":             "José Ortega",
		"  luis   QUISPE  ":                "luis QUISPE",
		"Cher":                             "Cher",
		"":                                 "",
		"   ":                              "",
		"María Fernanda López":             "María Fernanda", // documented limit: name + two surnames assumed
		"Ana de":                           "Ana de",         // a trailing particle stays on its own
	} {
		if got := webhook.ShortName(in); got != want {
			t.Errorf("ShortName(%q) = %q; want %q", in, got, want)
		}
	}
	if got := webhook.ShortName("Pedro DE LA Torre"); !strings.HasPrefix(got, "Pedro DE LA Torre") {
		t.Errorf("particles are matched in any case: %q", got)
	}
}
