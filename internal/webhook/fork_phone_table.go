package webhook

// Fork (Agenda Maestros 4x4): phone-data.json (internal/handler/assets: libphonenumber's
// [ISO, dial code] pairs plus an IANA zone -> ISO map), parsed once, for the host notices
// (fork_host.go): which country a phone number is from, which country a zone is in, and
// which zone to use for a country when a person has no zone of their own.
//
// The file stays where it is: handler/phone_countries.go owns its only //go:embed and other
// scripts read it there. The handler parses it with MustParsePhoneTable and hands the table
// to the Service (SetPhoneTable, from wireWebhookSvc - New and SetWebhookSvc, so the
// separate `calnode mcp` process gets it too). Every method is nil-safe: a Service without a
// table (this package's tests) answers "" and the notices use their fallbacks.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// PhoneTable is phone-data.json, indexed.
type PhoneTable struct {
	byCode map[string][]string // dial code -> ISO countries sharing it, in file order
	tz2cc  map[string]string   // IANA zone -> ISO
	zones  map[string][]string // ISO -> its zones, sorted
	locs   sync.Map            // zone name -> *time.Location (nil when it does not load)
}

// mainCountryForCode settles a dial code several countries share when nothing else does:
// the country most numbers with that code belong to. TestMainCountryForCode_coversEverySharedCode
// requires an entry for every shared code in phone-data.json.
var mainCountryForCode = map[string]string{
	"1": "US", "7": "RU", "39": "IT", "44": "GB", "47": "NO", "61": "AU",
	"212": "MA", "262": "RE", "290": "SH", "358": "FI", "590": "GP", "599": "CW",
}

// nanpSpanish are the +1 area codes of the Spanish-speaking NANP countries - the only +1
// countries this audience needs told apart from the United States.
var nanpSpanish = map[string]string{
	"809": "DO", "829": "DO", "849": "DO",
	"787": "PR", "939": "PR",
}

// mainZoneByCountry is the zone a country's time is told in when the person gave no zone,
// for the countries whose zones do NOT share one offset (TestMainZoneByCountry_complete
// requires every such country, and that each entry is a loadable zone of its country): the
// most populated zone, else the capital's. Countries with a single offset use their first
// zone in alphabetical order - any of them tells the same time.
var mainZoneByCountry = map[string]string{
	"AU": "Australia/Sydney",
	"BR": "America/Sao_Paulo",
	"CA": "America/Toronto",
	"CD": "Africa/Kinshasa",
	"CL": "America/Santiago",
	"CN": "Asia/Shanghai",
	"EC": "America/Guayaquil",
	"ES": "Europe/Madrid",
	"FM": "Pacific/Pohnpei",
	"GL": "America/Nuuk",
	"ID": "Asia/Jakarta",
	"KI": "Pacific/Tarawa",
	"MN": "Asia/Ulaanbaatar",
	"MX": "America/Mexico_City",
	"NZ": "Pacific/Auckland",
	"PF": "Pacific/Tahiti",
	"PG": "Pacific/Port_Moresby",
	"PT": "Europe/Lisbon",
	"RU": "Europe/Moscow",
	"UA": "Europe/Kyiv",
	"US": "America/New_York",
}

// ParsePhoneTable parses phone-data.json ({"countries": [[ISO, code], ...], "tz2cc": {...}}).
func ParsePhoneTable(raw []byte) (*PhoneTable, error) {
	var d struct {
		Countries [][]string        `json:"countries"`
		TZ2CC     map[string]string `json:"tz2cc"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("webhook: phone table: %w", err)
	}
	if len(d.Countries) == 0 || len(d.TZ2CC) == 0 {
		return nil, errors.New("webhook: phone table: missing countries or tz2cc")
	}
	t := &PhoneTable{byCode: map[string][]string{}, tz2cc: d.TZ2CC, zones: map[string][]string{}}
	for _, c := range d.Countries {
		if len(c) != 2 || c[0] == "" || c[1] == "" {
			return nil, fmt.Errorf("webhook: phone table: bad country entry %v", c)
		}
		t.byCode[c[1]] = append(t.byCode[c[1]], c[0])
	}
	for zone, cc := range d.TZ2CC {
		t.zones[cc] = append(t.zones[cc], zone)
	}
	for cc := range t.zones {
		sort.Strings(t.zones[cc])
	}
	return t, nil
}

// MustParsePhoneTable is ParsePhoneTable for an embedded build input: it panics on a
// malformed file, at init, as handler/phone_countries.go already does.
func MustParsePhoneTable(raw []byte) *PhoneTable {
	t, err := ParsePhoneTable(raw)
	if err != nil {
		panic(err)
	}
	return t
}

// SetPhoneTable hands the Service the parsed phone-data.json. Call during boot.
func (s *Service) SetPhoneTable(t *PhoneTable) { s.phones = t }

// CountryFor is the ISO country of the international number e164 ("+51 987..." or any form
// NormalizePhone accepts; "" when it has no country code), or "". The dial code is the
// longest prefix (1 to 3 digits) phone-data.json knows. A code shared by several countries
// is settled by, in order: the Spanish-speaking +1 area codes (Dominican Republic, Puerto
// Rico), the zone tz when it is one of those countries, then mainCountryForCode.
func (t *PhoneTable) CountryFor(e164, tz string) string {
	if t == nil {
		return ""
	}
	_, digits := NormalizePhone(e164)
	if digits == "" {
		return ""
	}
	for n := 3; n >= 1; n-- {
		if len(digits) <= n {
			continue
		}
		code := digits[:n]
		cands := t.byCode[code]
		switch {
		case len(cands) == 0:
			continue
		case len(cands) == 1:
			return cands[0]
		}
		if code == "1" && len(digits) >= 4 {
			if cc, ok := nanpSpanish[digits[1:4]]; ok {
				return cc
			}
		}
		if cc := t.tz2cc[strings.TrimSpace(tz)]; cc != "" {
			for _, c := range cands {
				if c == cc {
					return cc
				}
			}
		}
		if cc := mainCountryForCode[code]; cc != "" {
			return cc
		}
		return cands[0]
	}
	return ""
}

// CountryOfZone is the ISO country of the IANA zone, or "" - also for "UTC", "Etc/UTC" and
// "": a stored UTC means "unknown" here (see AttendeeZone).
func (t *PhoneTable) CountryOfZone(zone string) string {
	zone = strings.TrimSpace(zone)
	if t == nil || zone == "" || zone == "UTC" || zone == "Etc/UTC" {
		return ""
	}
	return t.tz2cc[zone]
}

// load is time.LoadLocation, cached (nil when the zone does not load).
func (t *PhoneTable) load(zone string) *time.Location {
	if v, ok := t.locs.Load(zone); ok {
		loc, _ := v.(*time.Location)
		return loc
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = nil
	}
	t.locs.Store(zone, loc)
	return loc
}

// offsetsAt is the set of UTC offsets (seconds) cc's loadable zones have at the instant at.
func (t *PhoneTable) offsetsAt(cc string, at time.Time) map[int]bool {
	out := map[int]bool{}
	for _, z := range t.zones[cc] {
		if loc := t.load(z); loc != nil {
			_, off := at.In(loc).Zone()
			out[off] = true
		}
	}
	return out
}

// singleOffset reports whether every zone of cc has the same offset in January and in July
// (of a fixed year, so the answer does not move with the calendar).
func (t *PhoneTable) singleOffset(cc string) bool {
	jan := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	jul := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	return len(t.offsetsAt(cc, jan)) <= 1 && len(t.offsetsAt(cc, jul)) <= 1
}

// MainZone is the IANA zone a person of country cc is told the time in when they gave no
// zone of their own, or "" (no zone known for cc): the first zone alphabetically when all
// of cc's zones keep one offset, else mainZoneByCountry.
func (t *PhoneTable) MainZone(cc string) string {
	if t == nil || len(t.zones[cc]) == 0 {
		return ""
	}
	if !t.singleOffset(cc) {
		if z := mainZoneByCountry[cc]; z != "" && t.load(z) != nil {
			return z
		}
	}
	for _, z := range t.zones[cc] {
		if t.load(z) != nil {
			return z
		}
	}
	return ""
}

// MultiOffset reports whether cc's zones tell different times at the instant at - with
// daylight saving time taken into account, so it can change during the year. A country
// whose zones are only aliases of each other (India: Calcutta and Kolkata) is not.
func (t *PhoneTable) MultiOffset(cc string, at time.Time) bool {
	if t == nil || cc == "" {
		return false
	}
	return len(t.offsetsAt(cc, at)) > 1
}

// countries returns every ISO code of the table, sorted (tests).
func (t *PhoneTable) countries() []string {
	seen := map[string]bool{}
	var out []string
	for _, ccs := range t.byCode {
		for _, cc := range ccs {
			if !seen[cc] {
				seen[cc] = true
				out = append(out, cc)
			}
		}
	}
	sort.Strings(out)
	return out
}
