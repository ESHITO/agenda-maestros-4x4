package mailer_test

import (
	"testing"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
)

// The e-mail zone rule and the WhatsApp one must agree, or one client gets two different
// times for one booking (a stored "UTC" read literally by e-mail, as the host's zone by
// WhatsApp).
func TestAttendeeZoneMatchesWebhook(t *testing.T) {
	zones := []string{"", " ", "UTC", "Etc/UTC", "America/Lima", "Europe/Madrid", "Mars/Olympus", "Etc/Unknown", " America/Lima "}
	for _, a := range zones {
		for _, h := range zones {
			got := mailer.AttendeeZoneForTest(a, h).String()
			want := webhook.AttendeeZone(a, h).String()
			if got != want {
				t.Errorf("attendee %q, host %q: mailer %s, webhook %s", a, h, got, want)
			}
		}
	}
}
