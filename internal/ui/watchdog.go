package ui

import "time"

func shouldRecalibrate(silent, window, sinceLast, cooldown time.Duration, attempts, maxAttempts int) bool {
	if silent < window {
		return false
	}
	if maxAttempts > 0 && attempts >= maxAttempts {
		return false
	}
	return sinceLast >= cooldown
}

func sinkChurnActive(now, until time.Time) bool {
	return now.Before(until)
}
