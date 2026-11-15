package server

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// reviewEntryOf returns an export row with the holder, source and rule, and
// nothing else: the source check reads only these three fields.
func reviewEntryOf(holder, source, rule string) access.ReviewEntry {
	return access.ReviewEntry{
		InheritedFrom: nil, EffectiveUntil: nil, EffectiveFrom: time.Time{}, ObjectState: "",
		Holder: holder, Source: source, Rule: rule, CanGrantAccess: false,
		Row: access.ReviewRow{
			RevokedAt: nil, LastAccessAt: nil, MemberCount: nil, Via: nil, SubjectName: "", RevokedBy: "",
			Grant: access.Grant{
				CreatedAt: time.Time{}, Condition: nil, Object: identity.Object{}, ID: "", Role: "",
				CreatedBy: "", Subject: access.Subject{Kind: "", ID: ""},
			},
			LastAccessTracked: false,
		},
	}
}

// TestReviewRowOfRejectsUnknownSource proves the second-layer source check of
// reviewRowOf: each row below answers errReviewSource and no row. Grant rows
// carry no rule, and the one code-rule row is the bootstrap admin token.
func TestReviewRowOfRejectsUnknownSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		entry access.ReviewEntry
	}{
		{"empty source", reviewEntryOf("", "", "")},
		{"unknown source", reviewEntryOf("", "ldap-forged", "")},
		{"grant row with a rule", reviewEntryOf("", access.SourceGrant, "some rule")},
		{
			"code-rule row with another holder",
			reviewEntryOf("someone", access.SourceCodeRule, access.RuleBootstrapAdminToken),
		},
		{
			"code-rule row with another rule",
			reviewEntryOf(access.BootstrapHolder, access.SourceCodeRule, "another rule"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row, err := reviewRowOf(tc.entry)
			if !errors.Is(err, errReviewSource) {
				t.Fatalf("reviewRowOf(%+v) = (%v, %v), want errReviewSource", tc.entry, row, err)
			}

			if row.Source != "" || row.Rule != "" {
				t.Fatalf("reviewRowOf(%+v) answers source %q rule %q, want no row", tc.entry, row.Source, row.Rule)
			}
		})
	}
}
