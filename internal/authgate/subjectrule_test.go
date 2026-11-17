package authgate_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
)

// TestPasswordSubjectRuleEqualsTheNameRuleExceptForTheLimit proves that the
// rule of a subject has the limit of the argument and the other fields of the
// rule of a name.
func TestPasswordSubjectRuleEqualsTheNameRuleExceptForTheLimit(t *testing.T) {
	t.Parallel()

	if authgate.DefaultSubjectFailureLimit != 5 || authgate.MinSubjectFailureLimit != 1 ||
		authgate.MaxSubjectFailureLimit != 100 {
		t.Fatalf("limits = %d, %d, %d, want 5, 1, 100", authgate.DefaultSubjectFailureLimit,
			authgate.MinSubjectFailureLimit, authgate.MaxSubjectFailureLimit)
	}

	for _, limit := range []int{1, authgate.DefaultSubjectFailureLimit, 100} {
		want := authgate.PasswordNameRule()
		want.Limit = limit

		if got := authgate.PasswordSubjectRule(limit); got != want {
			t.Fatalf("PasswordSubjectRule(%d) = %+v, want %+v", limit, got, want)
		}
	}
}
