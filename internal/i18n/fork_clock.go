package i18n

// Fork (Agenda Maestros 4x4): every time a client sees is on the 12-hour clock with the
// language's own AM/PM suffix - "9:00 a. m.", "5:00 p. m.", "12:30 p. m." - because the
// owner asked for it ("DEBE DECIR AM Y PM EN CUANTO A LA HORA"), although CLDR, and so
// es.json's clock_format, says Spanish writes "17:00". es.json stays CLDR-true (that is what
// TestDateTablesMatchCLDR polices); the override lives here, in a fork-owned file, so
// upstream merges of the locale files and datetime.go stay clean.
//
// This is the ONE switch. FormatTimeOfDay (emails, calendar invite text, the webhook's
// start_local* fields and the WhatsApp {hora}/{fecha}) reads it through Uses12h, and the
// handler hands the same Uses12h answer to the browser surfaces (book.html, manage.html,
// embed.js via the /public payload) as "hour12", so no page decides the clock on its own.
// Config: CLOCK_12H (default on in this fork; "false" returns to each locale's own
// clock_format). The admin SPA is not affected: it follows each user's profile.

import (
	"strings"
	"sync/atomic"
	"time"
)

var clock12h atomic.Bool

func init() { clock12h.Store(true) } // fork default: ON, no deploy variable needed

// SetClock12h turns the fork's 12-hour override on or off (config CLOCK_12H). Call during
// boot, before serving.
func SetClock12h(on bool) { clock12h.Store(on) }

// Clock12h reports whether the fork's 12-hour override is on.
func Clock12h() bool { return clock12h.Load() }

// Uses12h reports whether times in this locale render on the 12-hour clock: always while
// the fork override is on, otherwise when the locale's clock_format says so.
func (l *Locale) Uses12h() bool {
	return clock12h.Load() || l.T("clock_format") == "12h"
}

// dayPeriods holds CLDR's abbreviated AM/PM markers - exactly what Intl.DateTimeFormat
// prints with hourCycle "h12" - for the shipped languages whose markers are not "AM"/"PM".
// Keyed by base language; anything missing gets "AM"/"PM", which is what CLDR gives en,
// fr, de, it and pt. TestClock12hMatchesCLDR cross-checks these against Intl.
var dayPeriods = map[string][2]string{
	"es": {"a. m.", "p. m."},
	"nl": {"a.m.", "p.m."},
	"sv": {"fm", "em"},
}

// formatClock renders t's hour and minute on the 12-hour ("3:04 p. m.", no leading zero,
// 12 for noon and midnight) or 24-hour ("15:04") clock.
func (l *Locale) formatClock(t time.Time, twelve bool) string {
	if !twelve {
		return t.Format("15:04")
	}
	am, pm := "AM", "PM"
	base, _, _ := strings.Cut(l.Code, "-")
	if p, ok := dayPeriods[base]; ok {
		am, pm = p[0], p[1]
	}
	if t.Hour() < 12 {
		return t.Format("3:04") + " " + am
	}
	return t.Format("3:04") + " " + pm
}
