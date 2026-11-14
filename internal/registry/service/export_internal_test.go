package service

// ReasonOfRefusal returns the reason class of a refused publish, and reports
// whether the error is a refusal.
func ReasonOfRefusal(err error) (string, bool) {
	return refusalReason(err)
}
