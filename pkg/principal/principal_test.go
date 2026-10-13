package principal_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

func TestValidateSubject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		subject string
		want    error
	}{
		{"alice", nil},
		{"alice@example.com", nil},
		{"Ünïcode Name", nil},
		{"admin-team", nil},
		{"admin-", nil},
		{"administrator", nil},
		{strings.Repeat("a", 255), nil},
		{"", principal.ErrInvalid},
		{strings.Repeat("a", 256), principal.ErrInvalid},
		{" alice", principal.ErrInvalid},
		{"alice\n", principal.ErrInvalid},
		{"al\tice", principal.ErrInvalid},
		{"\xff", principal.ErrInvalid},
		{"admin", principal.ErrReservedSubject},
		{"admin-2", principal.ErrReservedSubject},
		{"admin-17", principal.ErrReservedSubject},
	}

	for _, tc := range cases {
		t.Run(tc.subject, func(t *testing.T) {
			t.Parallel()

			err := principal.ValidateSubject(tc.subject)
			if tc.want == nil && err != nil {
				t.Fatalf("ValidateSubject(%q) = %v, want nil", tc.subject, err)
			}

			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("ValidateSubject(%q) = %v, want %v", tc.subject, err, tc.want)
			}
		})
	}
}

func TestParseKind(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"person", "service", "workload"} {
		if k, err := principal.ParseKind(s); err != nil || string(k) != s {
			t.Fatalf("ParseKind(%q) = %q, %v", s, k, err)
		}
	}

	for _, s := range []string{"", "Person", "robot", "group"} {
		if _, err := principal.ParseKind(s); !errors.Is(err, principal.ErrInvalid) {
			t.Fatalf("ParseKind(%q) = %v, want ErrInvalid", s, err)
		}
	}
}

func TestValidateGroupName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ops", "Platform_Team", "team-1.0"} {
		if err := principal.ValidateGroupName(name); err != nil {
			t.Fatalf("ValidateGroupName(%q) = %v, want nil", name, err)
		}
	}

	for _, name := range []string{"", "a b", "-ops", ".ops", "ops/dev", strings.Repeat("a", 101)} {
		if err := principal.ValidateGroupName(name); !errors.Is(err, principal.ErrInvalid) {
			t.Fatalf("ValidateGroupName(%q) = %v, want ErrInvalid", name, err)
		}
	}
}

func TestNewServiceNeedsStore(t *testing.T) {
	t.Parallel()

	_, err := principal.NewService(principal.Config{Store: nil, Now: nil, NewID: nil})
	if !errors.Is(err, principal.ErrMissingDependency) {
		t.Fatalf("NewService without a store = %v, want ErrMissingDependency", err)
	}
}
