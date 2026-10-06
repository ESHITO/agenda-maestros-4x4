package slots

// Fork (Agenda Maestros 4x4): exports for the team calendar (handler/fork_team_calendar.go),
// kept in a file of its own so no upstream file changes.

// MergeIntervals exposes the engine's own sort+merge (fork: team calendar/coverage): the
// working hours the calendar draws are the windows hostsByStart walks, merged the same way.
func MergeIntervals(ivs []Interval) []Interval { return mergeIntervals(ivs) }
