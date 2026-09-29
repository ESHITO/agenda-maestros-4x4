package mailer

// Fork (Agenda Maestros 4x4): which zone booking e-mails print their times in (audit,
// Sep 2026). Host notifications used the CLIENT's zone ("Thu 1 Oct 2026, 3:30 AM CEST"
// to a Lima mentor for their Wed 20:30 session); attendee e-mails printed UTC when the
// stored attendee zone was "UTC" (API/MCP bookings), while WhatsApp used the host's zone.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Wed 30 Sep 2026, 20:30 Lima = Thu 1 Oct 01:30Z = Thu 1 Oct 03:30 Madrid.
func zoneFixture(attendeeTZ, hostTZ string) BookingData {
	d := testBookingData()
	d.StartAt = time.Date(2026, 10, 1, 1, 30, 0, 0, time.UTC)
	d.EndAt = d.StartAt.Add(40 * time.Minute)
	d.PreviousStartAt = d.StartAt.Add(-24 * time.Hour)
	d.PreviousEndAt = d.PreviousStartAt.Add(40 * time.Minute)
	d.OrganizerTimezone = attendeeTZ
	d.HostTimezone = hostTZ
	return d
}

func TestHostEmailsUseTheHostsZone(t *testing.T) {
	for name, send := range map[string]func(context.Context, Mailer, BookingData) error{
		"confirmation": SendConfirmationToHost,
		"cancellation": SendCancellationToHost,
		"reschedule":   SendRescheduleToHost,
	} {
		for _, attendee := range []string{"Europe/Madrid", "America/Argentina/Buenos_Aires", "America/Mexico_City", "UTC", ""} {
			m := &captureMailer{}
			if err := send(context.Background(), m, zoneFixture(attendee, "America/Lima")); err != nil {
				t.Fatal(err)
			}
			msgs := m.all()
			if len(msgs) != 1 {
				t.Fatalf("%s: %d messages", name, len(msgs))
			}
			for part, body := range map[string]string{"text": msgs[0].Text, "html": msgs[0].HTML} {
				if !strings.Contains(body, "Wed 30 Sep 2026, 8:30 PM") {
					t.Errorf("%s to host (attendee %q), %s: no \"Wed 30 Sep 2026, 8:30 PM\" (Lima):\n%s", name, attendee, part, body)
				}
				if !strings.Contains(body, "America/Lima") {
					t.Errorf("%s to host (attendee %q), %s: no zone label America/Lima", name, attendee, part)
				}
				if strings.Contains(body, "3:30 AM") || strings.Contains(body, "Thu 1 Oct") {
					t.Errorf("%s to host (attendee %q), %s: still shows the client's time", name, attendee, part)
				}
			}
		}
	}
}

// A host whose zone could not be read falls back to the attendee rule (the old
// behaviour), never to a silent UTC when the attendee's zone is known.
func TestHostEmailWithoutHostZoneFallsBackToTheAttendees(t *testing.T) {
	m := &captureMailer{}
	if err := SendConfirmationToHost(context.Background(), m, zoneFixture("Europe/Madrid", "")); err != nil {
		t.Fatal(err)
	}
	if body := m.all()[0].Text; !strings.Contains(body, "Thu 1 Oct 2026, 3:30 AM") || !strings.Contains(body, "Europe/Madrid") {
		t.Errorf("host email without a host zone:\n%s", body)
	}
}

// Attendee e-mails keep the attendee's own zone, and fall back to the host's when the
// stored zone is unknown - the same rule as webhook.AttendeeZone.
func TestAttendeeEmailsZoneRule(t *testing.T) {
	for _, c := range []struct {
		attendee, want string
	}{
		{"Europe/Madrid", "Thu 1 Oct 2026, 3:30 AM"},
		{"America/Lima", "Wed 30 Sep 2026, 8:30 PM"},
		{"UTC", "Wed 30 Sep 2026, 8:30 PM"},          // stored default = unknown -> host (Lima)
		{"", "Wed 30 Sep 2026, 8:30 PM"},             // no zone -> host
		{"Mars/Olympus", "Wed 30 Sep 2026, 8:30 PM"}, // unloadable -> host, not UTC
	} {
		for name, send := range map[string]func(context.Context, Mailer, BookingData) error{
			"confirmation": SendConfirmationToAttendee,
			"reminder":     SendReminder,
			"reschedule":   SendRescheduleToAttendee,
			"cancellation": SendCancellationToAttendee,
		} {
			m := &captureMailer{}
			if err := send(context.Background(), m, zoneFixture(c.attendee, "America/Lima")); err != nil {
				t.Fatal(err)
			}
			msg := m.all()[0]
			if !strings.Contains(msg.Text, c.want) || !strings.Contains(msg.HTML, c.want) {
				t.Errorf("%s to attendee %q: want %q in text and html:\n%s", name, c.attendee, c.want, msg.Text)
			}
		}
	}
}
