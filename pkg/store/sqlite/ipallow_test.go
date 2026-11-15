package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestNamespacesWithListRefusesAnInvalidName checks the read refuses a stored
// name that no create path accepts, and returns no partial list.
func TestNamespacesWithListRefusesAnInvalidName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hostile string
	}{
		{name: "a name with a space", hostile: "bad name"},
		{name: "a name that starts with a dot", hostile: ".bad"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkNamespacesWithListRefusesAnInvalidName(t, open(t), tc.hostile)
		})
	}
}

// checkNamespacesWithListRefusesAnInvalidName runs one hostile name against a
// fresh store.
func checkNamespacesWithListRefusesAnInvalidName(t *testing.T, s *sqlite.Store, hostile string) {
	t.Helper()

	ns := namespace.Name("acme")
	fence := ensureNamespace(t, s, ns)

	list, err := ipallow.ParseList([]string{"192.0.2.0/24"})
	if err != nil {
		t.Fatalf("parse list: %v", err)
	}

	if setErr := s.SetNamespaceList(t.Context(), ns, list, fence, namespacesWithListEntry(ns)); setErr != nil {
		t.Fatalf("set IP allowlist: %v", setErr)
	}

	plantErr := s.ExecForTest(t.Context(), `UPDATE namespaces SET name = ? WHERE name = ?`, hostile, ns.String())
	if plantErr != nil {
		t.Fatalf("plant hostile name: %v", plantErr)
	}

	got, err := s.NamespacesWithList(t.Context())
	if !errors.Is(err, namespace.ErrInvalidName) {
		t.Fatalf("NamespacesWithList error = %v, want ErrInvalidName", err)
	}

	if got != nil {
		t.Fatalf("NamespacesWithList = %v, want nil", got)
	}
}

// namespacesWithListEntry returns a valid audit entry for a list change in ns.
func namespacesWithListEntry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At:            time.UnixMilli(1_700_000_000_000).UTC(),
		Namespace:     ns,
		Subject:       "alice@example.com",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "namespace:set-ip-allowlist",
		Object:        ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "storetest",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}
