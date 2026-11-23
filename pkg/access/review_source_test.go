package access_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestReviewRowSources proves that each row of an export names its source: a
// stored grant has source "grant" with its grant ID, creator and time, and
// the bootstrap row has source "code-rule" with the rule "bootstrap admin
// token" and no grant ID, creator or time.
func TestReviewRowSources(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.person(t, "alice")

	acme := identity.NamespaceObject("acme")
	f.grant(t, prn("alice"), access.RoleViewer, acme)

	page, err := f.svc.Review(t.Context(), acme, access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	// The export holds the test grant, the install grant of the agent reaper,
	// which reaches every namespace, and the bootstrap row.
	if len(page.Entries) != 3 {
		t.Fatalf("export of acme = %+v, want the grants of alice and of the install, and the bootstrap row",
			page.Entries)
	}

	grantRows := 0

	for i := range page.Entries {
		entry := &page.Entries[i]
		if entry.Holder == access.BootstrapHolder {
			checkBootstrapSource(t, entry)

			continue
		}

		checkGrantSource(t, entry)

		grantRows++
	}

	if grantRows != 2 {
		t.Fatalf("export of acme = %+v, want two grant rows", page.Entries)
	}
}

// checkGrantSource proves that a row of a stored grant names source "grant"
// with no rule, and keeps the grant ID, the creator and the time.
func checkGrantSource(t *testing.T, grant *access.ReviewEntry) {
	t.Helper()

	if grant.Source != access.SourceGrant || grant.Rule != "" {
		t.Fatalf("grant row = %+v, want source %q and no rule", grant, access.SourceGrant)
	}

	if grant.Row.Grant.ID == "" || grant.Row.Grant.CreatedBy == "" || grant.Row.Grant.CreatedAt.IsZero() {
		t.Fatalf("grant row = %+v, want the grant ID, the creator and the time", grant)
	}
}

// checkBootstrapSource proves that the bootstrap row names source "code-rule"
// with the rule "bootstrap admin token", and keeps no grant ID, no creator
// and no time.
func checkBootstrapSource(t *testing.T, boot *access.ReviewEntry) {
	t.Helper()

	if boot.Source != access.SourceCodeRule || boot.Rule != access.RuleBootstrapAdminToken {
		t.Fatalf("bootstrap row = %+v, want source %q and rule %q",
			boot, access.SourceCodeRule, access.RuleBootstrapAdminToken)
	}

	if boot.Row.Grant.ID != "" || boot.Row.Grant.CreatedBy != "" || !boot.Row.Grant.CreatedAt.IsZero() {
		t.Fatalf("bootstrap row = %+v, want no grant ID, no creator and no time", boot)
	}
}
