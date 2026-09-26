package i18n

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestClock12hMatchesCLDR holds the fork's 12-hour rendering (fork_clock.go) to what the
// browser prints for the same moment with hourCycle "h12" — which is exactly what
// book.html, manage.html and embed.js ask Intl for when the server says hour12. Emails,
// WhatsApp texts and the pages must read the same "5:00 p. m.".
//
// fr-CA is skipped: its CLDR 12-hour pattern is "5 h 00 p.m.", not "h:mm <period>", and
// this fork serves Spanish (FORCE_LOCALE=es); the Go side renders "5:00 PM" there.
func TestClock12hMatchesCLDR(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; skipping CLDR cross-check")
	}
	moments := []time.Time{
		time.Date(2026, time.September, 25, 0, 30, 0, 0, time.UTC),
		time.Date(2026, time.September, 25, 9, 5, 0, 0, time.UTC),
		time.Date(2026, time.September, 25, 12, 30, 0, 0, time.UTC),
		time.Date(2026, time.September, 25, 17, 0, 0, 0, time.UTC),
	}
	const script = `
const [codes, isos] = [JSON.parse(process.argv[1]), JSON.parse(process.argv[2])];
const out = {};
for (const code of codes) {
  const f = new Intl.DateTimeFormat(code, {hour: 'numeric', minute: '2-digit', hourCycle: 'h12', timeZone: 'UTC'});
  out[code] = isos.map(iso => f.format(new Date(iso)));
}
process.stdout.write(JSON.stringify(out));
`
	var codes []string
	for _, c := range supportedCodes {
		if c != "fr-CA" {
			codes = append(codes, c)
		}
	}
	isos := make([]string, len(moments))
	for i, m := range moments {
		isos[i] = m.Format(time.RFC3339)
	}
	cj, _ := json.Marshal(codes)
	ij, _ := json.Marshal(isos)
	raw, err := exec.Command(node, "-e", script, "--", string(cj), string(ij)).Output()
	if err != nil {
		t.Fatalf("running the CLDR probe under node failed: %v", err)
	}
	var want map[string][]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parsing the CLDR probe output failed: %v (output %q)", err, raw)
	}
	// Browsers put a no-break or narrow no-break space inside "p. m."; the text is the same.
	norm := strings.NewReplacer(" ", " ", " ", " ")
	for _, code := range codes {
		loc := Get(code)
		for i, m := range moments {
			exp := norm.Replace(want[code][i])
			if got := loc.formatClock(m, true); got != exp {
				t.Errorf("%s 12h %s = %q, CLDR says %q", code, m.Format("15:04"), got, exp)
			}
		}
	}
}

// With the override off, a locale is back on its own clock_format; with it on, every
// locale is on the 12-hour clock.
func TestUses12h_followsOverrideThenClockFormat(t *testing.T) {
	defer SetClock12h(Clock12h())
	SetClock12h(true)
	if !Get("es").Uses12h() {
		t.Error("override on: Spanish must use the 12-hour clock")
	}
	SetClock12h(false)
	if Get("es").Uses12h() {
		t.Error("override off: Spanish's clock_format is 24h")
	}
	if !Default().Uses12h() {
		t.Error("override off: English's clock_format is 12h")
	}
}
