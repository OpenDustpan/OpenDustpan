package profile_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/profile"
)

func TestParseName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		in     string
		wantOK bool
	}{
		{"one segment", "aws-prod", true},
		{"folder and name", "payments/aws-prod", true},
		{"dots and underscores", "a.b_c/d-e", true},
		{"empty", "", false},
		{"three segments", "a/b/c", false},
		{"empty segment", "a//b", false},
		{"trailing slash", "a/", false},
		{"upper case", "Aws", false},
		{"space", "a b", false},
		{"id prefix", "prf-abc", false},
		{"101 characters", strings.Repeat("a", 101), false},
		{"100 characters", strings.Repeat("a", 100), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := profile.ParseName(tc.in)
			if tc.wantOK != (err == nil) {
				t.Fatalf("ParseName(%q) error = %v, want ok %v", tc.in, err, tc.wantOK)
			}

			if err != nil && !errors.Is(err, profile.ErrInvalid) {
				t.Errorf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseRef(t *testing.T) {
	t.Parallel()

	id := profile.NewID()

	ref, err := profile.ParseRef(string(id))
	if err != nil || ref.ID != id {
		t.Fatalf("ParseRef(id) = %+v, %v", ref, err)
	}

	ref, err = profile.ParseRef("team/prod")
	if err != nil || ref.Name != "team/prod" || ref.Name.Folder() != "team" {
		t.Fatalf("ParseRef(name) = %+v, %v", ref, err)
	}

	if _, err = profile.ParseRef("prf-BAD"); !errors.Is(err, profile.ErrInvalid) {
		t.Errorf("ParseRef(bad id) error = %v, want ErrInvalid", err)
	}
}
