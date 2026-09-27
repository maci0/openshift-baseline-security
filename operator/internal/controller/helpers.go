package controller

import (
	"time"
)

// graceMinutes renders a grace period as whole minutes for a status message.
// Exact integer arithmetic on the Duration, not int(d.Minutes()): Minutes
// returns a float64 and the conversion truncates toward zero, so a grace that is
// not a whole number of minutes renders SHORTER than the window the code
// actually applies (a 90s grace prints "Pending >1m"). Ceiling keeps the
// message at or above the real window, which is the safe direction for a human
// reading "stuck for >Nm": rounding down would have them check before the
// condition could fire. A non-positive grace reports 0.
func graceMinutes(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	m := d / time.Minute
	if d%time.Minute != 0 {
		m++
	}
	return int(m)
}
