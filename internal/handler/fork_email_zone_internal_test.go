package handler

// Fork (Agenda Maestros 4x4): the handler threads each host's own zone into the booking
// e-mails (mailer.BookingData.HostTimezone). Host notifications print the host's time
// with a zone line; the attendee's print their stored zone, or the host's when the stored
// one is the "UTC" default (same rule as the WhatsApp texts).

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/mailer"
)

func TestBookingEmails_hostZoneThreaded(t *testing.T) {
	lima, err := time.LoadLocation("America/Lima")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	h := New(database, slog.New(slog.DiscardHandler))
	for _, q := range []string{
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u1','mentor@example.com','Mentor','America/Lima',1,1)`,
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,max_future_days) VALUES ('et1','u1','mentoria','Mentoría',40,0)`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// A Wednesday 20:30 Lima a couple of weeks out: Thursday 03:30 (or 02:30) in Madrid.
	y, m, d := time.Now().In(lima).AddDate(0, 0, 14).Date()
	start := time.Date(y, m, d, 20, 30, 0, 0, lima)
	for start.Weekday() != time.Wednesday {
		start = start.AddDate(0, 0, 1)
	}
	for i, c := range []struct{ attendeeTZ string }{{"Europe/Madrid"}, {"UTC"}} {
		start := start.AddDate(0, 0, 7*i)                   // one booking per week: no double-booking
		limaText := start.Format("Mon 2 Jan 2006, 3:04 PM") // the English e-mail format
		attendeeWant := limaText                            // "UTC" is the stored default = unknown: the host's zone
		if c.attendeeTZ != "UTC" {
			attendeeWant = start.In(mustLoc(t, c.attendeeTZ)).Format("Mon 2 Jan 2006, 3:04 PM")
		}
		t.Run(c.attendeeTZ, func(t *testing.T) {
			cap := &captureMailer{}
			email := "ana-" + strings.ToLower(strings.ReplaceAll(c.attendeeTZ, "/", "-")) + "@example.com"
			h.SetMailer(cap, "https://agenda.example.com")
			b, err := h.bookingSvc.Create(context.Background(), booking.CreateParams{
				EventTypeID: "et1", HostIDs: []string{"u1"}, RoutingMode: "fixed",
				StartAt: start.UTC(), EndAt: start.Add(40 * time.Minute).UTC(),
				Organizer: booking.Attendee{Name: "Ana", Email: email, IANATimezone: c.attendeeTZ},
			})
			if err != nil {
				t.Fatal(err)
			}
			h.dispatchBookingConfirmation(b, bookingConfirmationInput{
				EventTypeName: "Mentoría", EventTypeSlug: "mentoria", LocationType: "link",
				OrganizerName: "Ana", OrganizerEmail: email, OrganizerTimezone: c.attendeeTZ,
			})
			host := cap.find("mentor@example.com")
			if host == nil {
				t.Fatalf("no host notification; sent to %v", cap.recipients())
			}
			if !strings.Contains(host.Text, limaText) || !strings.Contains(host.Text, "Timezone: America/Lima") {
				t.Errorf("host notification not in the host's zone (want %q + zone line):\n%s", limaText, host.Text)
			}
			att := cap.find(email)
			if att == nil {
				t.Fatalf("no attendee confirmation; sent to %v", cap.recipients())
			}
			if !strings.Contains(att.Text, attendeeWant) {
				t.Errorf("attendee confirmation (zone %q): want %q in:\n%s", c.attendeeTZ, attendeeWant, att.Text)
			}

			// Cancellation and reschedule mails are built from loadCancellationData.
			cd, err := h.loadCancellationData(context.Background(), b)
			if err != nil {
				t.Fatal(err)
			}
			if cd.HostTimezone != "America/Lima" {
				t.Errorf("loadCancellationData HostTimezone = %q; want America/Lima", cd.HostTimezone)
			}
			hd := h.hostBookingData(context.Background(), mailer.BookingData{}, assignedHost{UserID: "u1"}, time.Now())
			if hd.HostTimezone != "America/Lima" {
				t.Errorf("hostBookingData HostTimezone = %q; want America/Lima", hd.HostTimezone)
			}
		})
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	return loc
}
