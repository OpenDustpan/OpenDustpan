package access

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// entryOf returns an export row with the holder, source and rule, and
// nothing else: the source check reads only these three fields.
func entryOf(holder, source, rule string) ReviewEntry {
	return ReviewEntry{
		InheritedFrom: nil, EffectiveUntil: nil, EffectiveFrom: time.Time{}, ObjectState: ObjectLive,
		Holder: holder, Source: source, Rule: rule, CanGrantAccess: false,
		Row: ReviewRow{
			RevokedAt: nil, LastAccessAt: nil, MemberCount: nil, Via: nil, SubjectName: "", RevokedBy: "",
			Grant: Grant{
				CreatedAt: time.Time{}, Condition: nil, Object: identity.Object{}, ID: "", Role: "",
				CreatedBy: "", Subject: Subject{Kind: "", ID: ""},
			},
			LastAccessTracked: false,
		},
	}
}

// TestCheckEntrySource proves that the source check of an export row fails
// closed: a stored grant and the bootstrap code rule pass, and any other
// source, a grant that names a rule, or a code rule that names no known
// rule answers ErrReviewSource.
func TestCheckEntrySource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		entry ReviewEntry
		want  error
	}{
		{name: "a stored grant passes", entry: entryOf("", SourceGrant, ""), want: nil},
		{
			name:  "the bootstrap code rule passes",
			entry: entryOf(BootstrapHolder, SourceCodeRule, RuleBootstrapAdminToken),
			want:  nil,
		},
		{name: "no source fails", entry: entryOf("", "", ""), want: ErrReviewSource},
		{
			name:  "an unknown source fails",
			entry: entryOf("", "ledger", ""),
			want:  ErrReviewSource,
		},
		{
			name:  "a grant that names a rule fails",
			entry: entryOf("", SourceGrant, RuleBootstrapAdminToken),
			want:  ErrReviewSource,
		},
		{
			name:  "a grant row with a holder fails",
			entry: entryOf(BootstrapHolder, SourceGrant, ""),
			want:  ErrReviewSource,
		},
		{
			name:  "a code rule with no rule fails",
			entry: entryOf(BootstrapHolder, SourceCodeRule, ""),
			want:  ErrReviewSource,
		},
		{
			name:  "a code rule with an unknown rule fails",
			entry: entryOf(BootstrapHolder, SourceCodeRule, "ledger"),
			want:  ErrReviewSource,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := checkEntrySource(tc.entry); !errors.Is(err, tc.want) {
				t.Fatalf("checkEntrySource(%+v) = %v, want %v", tc.entry, err, tc.want)
			}
		})
	}
}
