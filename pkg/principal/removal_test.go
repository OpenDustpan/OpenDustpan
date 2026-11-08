package principal_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
)

// removalAt is the fixed time of the removal checks. No check reads the
// wall clock.
func removalAt() time.Time {
	return time.Date(2026, time.October, 13, 21, 0, 0, 0, time.UTC)
}

// validRemoval returns a removal that every check accepts.
func validRemoval() principal.Removal {
	return principal.Removal{
		Source:        principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: "idp-1"},
		GroupID:       "grp-eng",
		Removed:       3,
		MembersBefore: 10,
		At:            removalAt(),
	}
}

// TestRemovalValidate checks each rule of Removal.Validate with one row that
// breaks it.
func TestRemovalValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		edit func(r *principal.Removal)
		want error
	}{
		{
			name: "a removal of some of the members of a group",
			edit: func(*principal.Removal) {},
			want: nil,
		},
		{
			name: "a removal of every member of a group",
			edit: func(r *principal.Removal) { r.Removed, r.MembersBefore = 10, 10 },
			want: nil,
		},
		{
			name: "a source ID and a group ID of 256 bytes",
			edit: func(r *principal.Removal) {
				r.Source.ID, r.GroupID = strings.Repeat("a", 256), strings.Repeat("b", 256)
			},
			want: nil,
		},
		{
			name: "a removal of a SCIM token",
			edit: func(r *principal.Removal) { r.Source.Kind = principal.RemovalSourceSCIMToken },
			want: nil,
		},
		{
			name: "an unknown source kind",
			edit: func(r *principal.Removal) { r.Source.Kind = "saml" },
			want: principal.ErrInvalid,
		},
		{
			name: "an empty source kind",
			edit: func(r *principal.Removal) { r.Source.Kind = "" },
			want: principal.ErrInvalid,
		},
		{
			name: "an empty source ID",
			edit: func(r *principal.Removal) { r.Source.ID = "" },
			want: principal.ErrInvalid,
		},
		{
			name: "a source ID of 257 bytes",
			edit: func(r *principal.Removal) { r.Source.ID = strings.Repeat("a", 257) },
			want: principal.ErrInvalid,
		},
		{
			name: "a source ID with a control character",
			edit: func(r *principal.Removal) { r.Source.ID = "idp-\n1" },
			want: principal.ErrInvalid,
		},
		{
			name: "a source ID that is not valid UTF-8",
			edit: func(r *principal.Removal) { r.Source.ID = "idp-\xff" },
			want: principal.ErrInvalid,
		},
		{
			name: "an empty group ID",
			edit: func(r *principal.Removal) { r.GroupID = "" },
			want: principal.ErrInvalid,
		},
		{
			name: "a group ID of 257 bytes",
			edit: func(r *principal.Removal) { r.GroupID = strings.Repeat("a", 257) },
			want: principal.ErrInvalid,
		},
		{
			name: "a group ID with a control character",
			edit: func(r *principal.Removal) { r.GroupID = "grp-\x00eng" },
			want: principal.ErrInvalid,
		},
		{
			name: "a removal of no members",
			edit: func(r *principal.Removal) { r.Removed = 0 },
			want: principal.ErrInvalid,
		},
		{
			name: "a removal of a negative number of members",
			edit: func(r *principal.Removal) { r.Removed = -1 },
			want: principal.ErrInvalid,
		},
		{
			name: "a removal of more members than the group held",
			edit: func(r *principal.Removal) { r.Removed, r.MembersBefore = 11, 10 },
			want: principal.ErrInvalid,
		},
		{
			name: "a removal with no time",
			edit: func(r *principal.Removal) { r.At = time.Time{} },
			want: principal.ErrInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := validRemoval()
			tc.edit(&r)

			err := r.Validate()
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Validate(%+v) = %v, want nil", r, err)
				}

				return
			}

			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate(%+v) = %v, want %v", r, err, tc.want)
			}
		})
	}
}

// TestValidateRemovalWindow checks the bounds of the window of a removal
// total.
func TestValidateRemovalWindow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		window time.Duration
		want   error
	}{
		{"a window of one hour", time.Hour, nil},
		{"a window of one millisecond", time.Millisecond, nil},
		{"the maximum window", principal.MaxRemovalWindow, nil},
		{"a window of zero", 0, principal.ErrInvalid},
		{"a negative window", -time.Second, principal.ErrInvalid},
		{"a window above the maximum", principal.MaxRemovalWindow + time.Millisecond, principal.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := principal.ValidateRemovalWindow(tc.window)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("ValidateRemovalWindow(%s) = %v, want nil", tc.window, err)
				}

				return
			}

			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateRemovalWindow(%s) = %v, want %v", tc.window, err, tc.want)
			}
		})
	}
}
