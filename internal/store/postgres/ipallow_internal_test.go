package postgres

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestNamespacesWithListQueryLimit checks the LIMIT of the query itself: with
// three lists and a limit of 2, the query returns 2 rows. The test calls the
// query directly, so that the check in NamespacesWithList cannot hide a query
// without a limit.
func TestNamespacesWithListQueryLimit(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s, err := Open(ctx, membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	list, parseErr := ipallow.ParseList([]string{"192.0.2.0/24"})
	if parseErr != nil {
		t.Fatalf("parse list: %v", parseErr)
	}

	for _, name := range []namespace.Name{"acme", "beta", "gamma"} {
		ns := namespace.Namespace{
			ID:                  namespace.NewID(),
			CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
			Name:                name,
			PrimaryInstallation: "eu-west",
			FailoverVersion:     1,
			Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		}
		if createErr := s.CreateNamespace(ctx, ns); createErr != nil {
			t.Fatalf("create namespace %s: %v", name, createErr)
		}

		if setErr := s.SetNamespaceList(ctx, name, list, ns.Fence(), ipallowLimitEntry(name)); setErr != nil {
			t.Fatalf("set IP allowlist of %s: %v", name, setErr)
		}
	}

	got, err := s.queries.NamespacesWithList(ctx, 2)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("query with limit 2 returned %d rows, want 2", len(got))
	}
}

// ipallowLimitEntry returns a valid audit entry for a list change in ns.
func ipallowLimitEntry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: ns, Subject: "alice@example.com", IDPSubject: "",
		TokenID: "", Action: "namespace:set-ip-allowlist", Object: ns.String(), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "ipallow-limit-test",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
		Origin: nil,
	}
}
