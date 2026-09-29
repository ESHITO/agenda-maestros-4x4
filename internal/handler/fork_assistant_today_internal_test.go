package handler

// Fork (Agenda Maestros 4x4): the assistant's "Today is" date is the visitor's calendar
// day, since find_available_slots reads date_from/date_to as days in the visitor's zone.
// With a UTC date, a Lima visitor at 21:00 on Tue 29 Sep was told "Today is 2026-09-30",
// so "mañana" became Thu 1 Oct instead of Wed 30 Sep.

import (
	"testing"
	"time"
)

func TestAssistantToday_isTheVisitorsDay(t *testing.T) {
	if _, err := time.LoadLocation("America/Lima"); err != nil {
		t.Skip("no tzdata:", err)
	}
	now := time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC) // Tue 29 Sep 21:00 Lima
	for _, c := range []struct{ tz, want string }{
		{"America/Lima", "2026-09-29"},
		{"Europe/Madrid", "2026-09-30"},
		{"Pacific/Honolulu", "2026-09-29"},
		{"", "2026-09-30"},             // computeSlots reads "" as UTC
		{"Mars/Olympus", "2026-09-30"}, // unknown: UTC, never a panic
	} {
		if got := assistantToday(now, c.tz); got != c.want {
			t.Errorf("assistantToday(%s, %q) = %s; want %s", now.Format(time.RFC3339), c.tz, got, c.want)
		}
	}
}
