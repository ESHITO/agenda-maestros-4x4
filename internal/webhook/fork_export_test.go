package webhook

// Test-only views of the fork's lookup tables (fork_phone_table.go, fork_countries.go), so
// the external tests can check them for completeness.

import "sort"

// MainCountryForCodeForTest is mainCountryForCode.
var MainCountryForCodeForTest = mainCountryForCode

// MainZoneByCountryForTest is mainZoneByCountry.
var MainZoneByCountryForTest = mainZoneByCountry

// CountryNamesESForTest is the generated Spanish name table.
var CountryNamesESForTest = countryNamesES

// SharedCodes returns every dial code several countries share, sorted.
func (t *PhoneTable) SharedCodes() []string {
	var out []string
	for code, ccs := range t.byCode {
		if len(ccs) > 1 {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

// CountriesForCode returns the countries of a dial code, in file order.
func (t *PhoneTable) CountriesForCode(code string) []string { return t.byCode[code] }

// Countries returns every country of the table, sorted.
func (t *PhoneTable) Countries() []string { return t.countries() }

// Zones returns cc's zones, sorted.
func (t *PhoneTable) Zones(cc string) []string { return t.zones[cc] }

// SingleOffset is singleOffset.
func (t *PhoneTable) SingleOffset(cc string) bool { return t.singleOffset(cc) }
