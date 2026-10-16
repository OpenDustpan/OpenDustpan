package oauth

import "time"

// CheckAuthTimeForTest calls checkAuthTime, so the tests can give it claims that
// the test provider never signs, for example no auth_time claim.
func CheckAuthTimeForTest(claim *int64, p Pending, now time.Time) (time.Time, error) {
	return checkAuthTime(claim, p, now)
}
