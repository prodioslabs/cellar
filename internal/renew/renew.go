package renew

import "time"

// DefaultThreshold is the fraction of remaining lifetime below which renewal is needed.
// Matching the plan: renew when less than 20% of lifetime remains.
const DefaultThreshold = 0.20

// Needed reports whether a certificate should be renewed based on NotBefore/NotAfter.
func Needed(notBefore, notAfter, now time.Time, threshold float64) bool {
	if threshold <= 0 || threshold >= 1 {
		threshold = DefaultThreshold
	}
	if !now.Before(notAfter) {
		return true
	}
	lifetime := notAfter.Sub(notBefore)
	if lifetime <= 0 {
		return true
	}
	remaining := notAfter.Sub(now)
	return float64(remaining)/float64(lifetime) < threshold
}

// NextCheck returns a suggested interval for the next renewal check.
// Checks at least hourly; sooner if already within the renewal window.
func NextCheck(notBefore, notAfter, now time.Time, threshold float64) time.Duration {
	const hourly = time.Hour
	if Needed(notBefore, notAfter, now, threshold) {
		return 5 * time.Minute
	}
	lifetime := notAfter.Sub(notBefore)
	if lifetime <= 0 {
		return 5 * time.Minute
	}
	// Time until we enter the renewal window.
	windowStart := notAfter.Add(-time.Duration(float64(lifetime) * threshold))
	until := windowStart.Sub(now)
	if until < hourly {
		if until < time.Minute {
			return time.Minute
		}
		return until
	}
	return hourly
}
