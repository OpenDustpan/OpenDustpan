package principal_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

func TestIsBootstrapSubject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		subject string
		want    bool
	}{
		{subject: "admin", want: true},
		{subject: "admin-2", want: true},
		{subject: "Admin", want: true},
		{subject: "admin1", want: true},
		{subject: "administrator", want: false},
		{subject: "alice", want: false},
		{subject: "", want: false},
	}

	for _, tc := range cases {
		t.Run("subject "+tc.subject, func(t *testing.T) {
			t.Parallel()

			if got := principal.IsBootstrapSubject(tc.subject); got != tc.want {
				t.Fatalf("IsBootstrapSubject(%q) = %v, want %v", tc.subject, got, tc.want)
			}
		})
	}
}
