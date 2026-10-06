package handler

import "time"

// SetTeamAvailabilityLimitsForTest shortens the team calendar's per-person timeout and total
// budget (fork_team_availability.go) and returns the function that restores them.
func SetTeamAvailabilityLimitsForTest(person, total time.Duration) func() {
	oldPerson, oldTotal := teamAvailPersonTimeout, teamAvailTotalBudget
	teamAvailPersonTimeout, teamAvailTotalBudget = person, total
	return func() { teamAvailPersonTimeout, teamAvailTotalBudget = oldPerson, oldTotal }
}

// SetTeamCalendarLimitsForTest shortens the team calendar's per-person timeout and total
// budget (fork_team_calendar.go) and returns the function that restores them.
func SetTeamCalendarLimitsForTest(person, total time.Duration) func() {
	oldPerson, oldTotal := teamCalPersonTimeout, teamCalTotalBudget
	teamCalPersonTimeout, teamCalTotalBudget = person, total
	return func() { teamCalPersonTimeout, teamCalTotalBudget = oldPerson, oldTotal }
}
