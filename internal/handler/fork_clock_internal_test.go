package handler

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/i18n"
)

// Fork (owner: "DEBE DECIR AM Y PM EN CUANTO A LA HORA"): the 12-hour choice is made once,
// in Go (internal/i18n/fork_clock.go), and handed to the pages as HOUR12, so their
// BookingLogic.formatTime renders "5:00 p. m." exactly like the emails and WhatsApp.
func TestManageTemplate_passesTheServerClockChoice(t *testing.T) {
	for _, hour12 := range []bool{true, false} {
		var buf bytes.Buffer
		data := managePageData{
			Token: "tok123", EventTypeSlug: "20-min-call", EventTypeName: "20-minute call",
			CurrentStartISO: "2026-09-25T22:00:00Z", OrganizerTZ: "America/Lima",
			Status: "confirmed", MaxFutureDays: 60, DurationMinutes: 20,
			T: i18n.Get("es").T, Locale: "es", Hour12: hour12,
		}
		if err := manageTmpl.Execute(&buf, data); err != nil {
			t.Fatalf("render manage template: %v", err)
		}
		out := buf.String()
		// html/template pads a JS value with spaces (" true "); the value is what counts.
		want := `const HOUR12\s*=\s*` + map[bool]string{true: "true", false: "false"}[hour12] + `\s*;`
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("manage page: %s not found", want)
		}
		// Every time on the page goes through fmtTime, which passes HOUR12 on.
		if !strings.Contains(out, "BookingLogic.formatTime(iso, TZ, LOCALE, HOUR12)") {
			t.Error("manage page: fmtTime does not pass HOUR12 to BookingLogic.formatTime")
		}
		if strings.Contains(out, "toLocaleTimeString(") {
			t.Error("manage page formats a time outside fmtTime (it would ignore HOUR12)")
		}
	}
}

func TestLabelSlots_serverWritesTheClientsClock(t *testing.T) {
	if !i18n.Clock12h() {
		t.Fatal("the fork's 12-hour override must be on by default")
	}
	// 22:00Z is 5:00 p. m. in Lima (UTC-5): the label follows the visitor's zone.
	got := labelSlots([]slotJSON{{Start: "2026-09-25T22:00:00Z", End: "2026-09-25T23:00:00Z"}, {Start: "bad"}},
		i18n.Get("es"), "America/Lima")
	if got[0].Label != "vie 25 sept 2026, 5:00 p. m." {
		t.Errorf("label = %q; want the 12h Spanish clock in Lima", got[0].Label)
	}
	if got[1].Label != "" {
		t.Errorf("an unparseable start got a label: %q", got[1].Label)
	}
	// The wire shape keeps start/end/host_ids flat, plus label.
	b, _ := json.Marshal(got[0])
	if s := string(b); !strings.Contains(s, `"start":"2026-09-25T22:00:00Z"`) || !strings.Contains(s, `"label":"vie 25 sept 2026, 5:00 p. m."`) {
		t.Errorf("assistant slot JSON = %s", s)
	}
}

func TestAssistantClockRule_asksForAmPm(t *testing.T) {
	if rule := assistantClockRule(i18n.Get("es")); !strings.Contains(rule, "12-hour") || !strings.Contains(rule, `"5:00 p. m."`) {
		t.Errorf("rule = %q", rule)
	}
	i18n.SetClock12h(false)
	defer i18n.SetClock12h(true)
	if rule := assistantClockRule(i18n.Get("es")); strings.Contains(rule, "12-hour") || !strings.Contains(rule, `"17:00"`) {
		t.Errorf("override off: rule = %q", rule)
	}
}
