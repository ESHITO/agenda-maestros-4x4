package handler

import "time"

// SetCancelSideEffectsTimeoutForTest shortens cancelSideEffects' first budget (the e-mails'
// and calendar's, fork_host_notices.go) and returns the function that restores it.
func SetCancelSideEffectsTimeoutForTest(d time.Duration) func() {
	old := cancelSideEffectsTimeout
	cancelSideEffectsTimeout = d
	return func() { cancelSideEffectsTimeout = old }
}
