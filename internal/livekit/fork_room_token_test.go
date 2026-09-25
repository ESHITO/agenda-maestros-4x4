package livekit

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Fork: the unique host link verifies exactly like an upstream one (same room, role and
// expiry) but differs on every mint, which the plain token never does.
func TestSignRoomTokenUnique(t *testing.T) {
	c := testClient()
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	a := c.SignRoomTokenUnique("booking-1", "host", exp)
	b := c.SignRoomTokenUnique("booking-1", "host", exp)
	if a == b {
		t.Fatal("two unique tokens are identical")
	}
	if c.SignRoomToken("booking-1", "host", exp) != c.SignRoomToken("booking-1", "host", exp) {
		t.Fatal("the upstream token is expected to be deterministic (the reason for the nonce)")
	}
	for _, tok := range []string{a, b} {
		room, role, gotExp, err := c.VerifyRoomToken(tok)
		if err != nil || room != "booking-1" || role != "host" || !gotExp.Equal(exp) {
			t.Errorf("VerifyRoomToken = %q %q %v %v", room, role, gotExp, err)
		}
	}
	// A tampered nonce breaks the MAC like any other field.
	if _, _, _, err := c.VerifyRoomToken("x" + a); err == nil {
		t.Error("a tampered token verified")
	}

	link := c.BookingJoinURLUnique("https://agenda.example.com/", "booking-1", "host", exp)
	u, err := url.Parse(link)
	if err != nil || !strings.HasPrefix(link, "https://agenda.example.com/room/booking-1?t=") {
		t.Fatalf("link = %q (%v)", link, err)
	}
	if _, role, _, err := c.VerifyRoomToken(u.Query().Get("t")); err != nil || role != "host" {
		t.Errorf("link token: role %q, %v", role, err)
	}
}
