package sqlite_test

import (
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forgelink/forgelinktest"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// forgeLinkPrincipals makes the principals of the cases of the suite, one for
// each name.
type forgeLinkPrincipals struct {
	mu  sync.Mutex
	ids map[string]string
}

// person makes the person with the name, or returns the ID of the one that exists.
func (p *forgeLinkPrincipals) person(t *testing.T, s *sqlite.Store, name string) string {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	if id, ok := p.ids[name]; ok {
		return id
	}

	at := time.UnixMilli(1_700_000_000_000).UTC()
	pr := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: principal.IDPrefix + name,
		Subject: name + "@example.com", DisplayName: name, Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	e := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "root", IDPSubject: "", TokenID: "",
		Action: "server:manage-principals", Object: principal.Ref(pr.ID), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	if err := s.CreatePrincipal(t.Context(), pr, e); err != nil {
		t.Fatalf("create principal %s: %v", name, err)
	}

	p.ids[name] = pr.ID

	return pr.ID
}

func TestForgeLinkConformance(t *testing.T) {
	t.Parallel()

	forgelinktest.Run(t, func(t *testing.T) forgelinktest.Harness {
		t.Helper()

		s := open(t)
		people := &forgeLinkPrincipals{mu: sync.Mutex{}, ids: map[string]string{}}

		return forgelinktest.Harness{
			Store: s.ForgeLinks(),
			Principal: func(t *testing.T, name string) string {
				t.Helper()

				return people.person(t, s, name)
			},
			AuditEntries: func(t *testing.T) []audit.Entry {
				t.Helper()

				return forgeLinkEntries(t, s)
			},
		}
	})
}

// forgeLinkEntries returns the audit entries of the links in the server scope.
// The principals of the cases write their own entries there.
func forgeLinkEntries(t *testing.T, s *sqlite.Store) []audit.Entry {
	t.Helper()

	return slices.DeleteFunc(allAuditEntries(t, s, audit.ServerScope), func(e audit.Entry) bool {
		return e.Action != "caller:manage-git-host-links"
	})
}
