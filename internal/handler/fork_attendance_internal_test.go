package handler

import (
	"testing"
	"time"
)

// Fork (Agenda Maestros 4x4): the attendance status rule (fork_attendance.go), one case per
// state, on a 10:00-10:30 booking (window 09:45 - 12:30).
func TestComputeAttendance_states(t *testing.T) {
	day := func(hm string) time.Time {
		tt, err := time.Parse(time.RFC3339, "2027-05-10T"+hm+":00Z")
		if err != nil {
			t.Fatal(err)
		}
		return tt
	}
	start, end := day("10:00"), day("10:30")
	base := func() attendanceInput {
		return attendanceInput{
			status: "confirmed", locationType: "livekit", room: "booking-b1",
			start: start, end: end, since: day("00:00"),
			mints: map[string]attendMint{},
		}
	}
	mint := func(in *attendanceInput, id, kind string, verified bool, at string) {
		in.mints[id] = attendMint{kind: kind, verified: verified, mintedAt: day(at)}
	}
	sess := func(in *attendanceInput, id, from, to string) {
		s := attendSession{identity: id, joinedAt: day(from)}
		if to != "" {
			s.leftAt = day(to)
		}
		in.sessions = append(in.sessions, s)
	}
	after := day("13:00") // past the window: final states

	cases := []struct {
		name        string
		build       func(in *attendanceInput)
		now         time.Time
		want        string
		wantMinutes int // -1 = null
		wantHost    string
		wantClient  string
	}{
		{"cancelled", func(in *attendanceInput) { in.status = "cancelled" }, after, attendNotApplicable, -1, "", ""},
		{"not livekit", func(in *attendanceInput) { in.locationType = "zoom" }, after, attendNotApplicable, -1, "", ""},
		{"no room", func(in *attendanceInput) { in.room = "" }, after, attendNotApplicable, -1, "", ""},
		{"started before attendance_since", func(in *attendanceInput) { in.since = day("10:05") }, after, attendNotApplicable, -1, "", ""},
		{"before the window", func(in *attendanceInput) {}, day("09:30"), attendPending, -1, "", ""},
		{"window open, nobody yet", func(in *attendanceInput) {}, day("10:10"), attendPending, -1, "", ""},
		{"in progress", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "09:58")
			sess(in, "h", "09:58", "")
		}, day("10:05"), attendInProgress, -1, "2027-05-10T09:58:00Z", ""},
		{"attended, clipped to the window", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "09:30")
			mint(in, "c", attendKindAttendee, false, "10:02")
			sess(in, "h", "09:30", "10:40") // clipped to 09:45
			sess(in, "c", "10:02", "10:40")
		}, after, attendAttended, 38, "2027-05-10T09:45:00Z", "2027-05-10T10:02:00Z"},
		{"an open session ends at the window's end", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "10:00")
			mint(in, "c", attendKindAttendee, false, "10:00")
			sess(in, "h", "10:00", "")
			sess(in, "c", "12:00", "")
		}, after, attendAttended, 30, "2027-05-10T10:00:00Z", "2027-05-10T12:00:00Z"},
		{"unverified host counts as the host", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, false, "10:00")
			mint(in, "c", attendKindAttendee, false, "10:00")
			sess(in, "h", "10:00", "10:20")
			sess(in, "c", "10:05", "10:30")
		}, after, attendAttended, 15, "2027-05-10T10:00:00Z", "2027-05-10T10:05:00Z"},
		{"two attendees overlapped, no verified host", func(in *attendanceInput) {
			mint(in, "a1", attendKindAttendee, false, "10:00")
			mint(in, "a2", attendKindAttendee, false, "10:01")
			sess(in, "a1", "10:00", "10:30")
			sess(in, "a2", "10:01", "10:25")
		}, after, attendUnverified, 24, "", "2027-05-10T10:00:00Z"},
		{"a client reloading is not two people", func(in *attendanceInput) {
			mint(in, "a1", attendKindAttendee, false, "10:00")
			mint(in, "a2", attendKindAttendee, false, "10:10")
			sess(in, "a1", "10:00", "10:10")
			sess(in, "a2", "10:09", "10:20") // a minute of overlap while LiveKit dropped the old one
		}, after, attendHostAbsent, -1, "", "2027-05-10T10:00:00Z"},
		{"client absent", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "10:00")
			sess(in, "h", "10:00", "10:20")
		}, after, attendClientAbsent, -1, "2027-05-10T10:00:00Z", ""},
		{"host absent", func(in *attendanceInput) {
			mint(in, "c", attendKindAttendee, false, "10:00")
			sess(in, "c", "10:00", "10:20")
		}, after, attendHostAbsent, -1, "", "2027-05-10T10:00:00Z"},
		{"both came, never together: the host waited", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "10:00")
			mint(in, "c", attendKindAttendee, false, "10:25")
			sess(in, "h", "10:00", "10:20")
			sess(in, "c", "10:25", "10:30")
		}, after, attendClientAbsent, -1, "2027-05-10T10:00:00Z", "2027-05-10T10:25:00Z"},
		{"staff never counts", func(in *attendanceInput) {
			mint(in, "s", attendKindStaff, false, "10:00")
			mint(in, "h", attendKindHost, true, "10:00")
			sess(in, "s", "10:00", "10:30")
			sess(in, "h", "10:00", "10:30")
		}, after, attendClientAbsent, -1, "2027-05-10T10:00:00Z", ""},
		{"staff alone is nobody", func(in *attendanceInput) {
			mint(in, "s", attendKindStaff, false, "10:00")
			sess(in, "s", "10:00", "10:30")
		}, after, attendNobody, -1, "", ""},
		{"nobody", func(in *attendanceInput) {}, after, attendNobody, -1, "", ""},
		{"a session days before is outside the window", func(in *attendanceInput) {
			mint(in, "c", attendKindAttendee, false, "10:00")
			in.sessions = append(in.sessions, attendSession{identity: "c",
				joinedAt: day("10:00").Add(-72 * time.Hour), leftAt: day("10:05").Add(-72 * time.Hour)})
		}, after, attendNobody, -1, "", ""},
		{"with webhook data a mint alone is not a join", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "10:00")
			mint(in, "c", attendKindAttendee, false, "10:01") // never connected
			sess(in, "h", "10:00", "10:30")
		}, after, attendClientAbsent, -1, "2027-05-10T10:00:00Z", ""},
		{"only mints: both entered (no false nobody)", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "09:59")
			mint(in, "c", attendKindAttendee, false, "10:01")
		}, after, attendAttended, -1, "2027-05-10T09:59:00Z", "2027-05-10T10:01:00Z"},
		{"only mints: during the session", func(in *attendanceInput) {
			mint(in, "c", attendKindAttendee, false, "10:01")
		}, day("10:10"), attendInProgress, -1, "", "2027-05-10T10:01:00Z"},
		{"only mints: host only", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "10:00")
		}, after, attendClientAbsent, -1, "2027-05-10T10:00:00Z", ""},
		{"only mints: far before the start do not count", func(in *attendanceInput) {
			mint(in, "c", attendKindAttendee, false, "08:59")
			in.mints["c2"] = attendMint{kind: attendKindAttendee, mintedAt: day("10:00").Add(-72 * time.Hour)}
		}, after, attendNobody, -1, "", ""},
		{"only mints: after the window do not count", func(in *attendanceInput) {
			mint(in, "c", attendKindAttendee, false, "12:31")
		}, after, attendNobody, -1, "", ""},
		{"only mints: the host entered 20 min early and stayed", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "09:40")
			mint(in, "c", attendKindAttendee, false, "10:01")
		}, after, attendAttended, -1, "2027-05-10T09:45:00Z", "2027-05-10T10:01:00Z"},
		{"only mints: the client entered from the 1 h reminder and waited", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "10:00")
			mint(in, "c", attendKindAttendee, false, "09:00")
		}, after, attendAttended, -1, "2027-05-10T10:00:00Z", "2027-05-10T09:45:00Z"},
		{"only mints: both entered early", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "09:40")
			mint(in, "c", attendKindAttendee, false, "09:42")
		}, after, attendAttended, -1, "2027-05-10T09:45:00Z", "2027-05-10T09:45:00Z"},
		{"only mints: early entries are in the room during the session", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "09:40")
			mint(in, "c", attendKindAttendee, false, "09:42")
		}, day("10:10"), attendInProgress, -1, "2027-05-10T09:45:00Z", "2027-05-10T09:45:00Z"},
		{"only mints: an early host alone is client absent", func(in *attendanceInput) {
			mint(in, "h", attendKindHost, true, "09:30")
		}, after, attendClientAbsent, -1, "2027-05-10T09:45:00Z", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			c.build(&in)
			got := computeAttendance(in, c.now)
			if got.Status != c.want {
				t.Fatalf("status = %q; want %q (%+v)", got.Status, c.want, got)
			}
			switch {
			case c.wantMinutes < 0 && got.MinutesTogether != nil:
				t.Errorf("minutes_together = %d; want null", *got.MinutesTogether)
			case c.wantMinutes >= 0 && (got.MinutesTogether == nil || *got.MinutesTogether != c.wantMinutes):
				t.Errorf("minutes_together = %v; want %d", got.MinutesTogether, c.wantMinutes)
			}
			str := func(p *string) string {
				if p == nil {
					return ""
				}
				return *p
			}
			if str(got.HostJoinedAt) != c.wantHost || str(got.ClientJoinedAt) != c.wantClient {
				t.Errorf("joined host %q client %q; want %q %q", str(got.HostJoinedAt), str(got.ClientJoinedAt), c.wantHost, c.wantClient)
			}
		})
	}
}
