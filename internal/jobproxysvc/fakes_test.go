package jobproxysvc_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/jobproxysvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	nsAcme  = namespace.Name("acme")
	unitWeb = "u-web"
)

// memStore is an in-memory rulestore.RuleStore that keeps the audit entries.
type memStore struct {
	mu      sync.Mutex
	sets    map[rulestore.Scope]rulestore.Stored
	entries []audit.Entry
	// failPut makes each Put fail with this error, as a lost audit entry does.
	failPut error
}

func newMemStore() *memStore {
	return &memStore{sets: map[rulestore.Scope]rulestore.Stored{}}
}

func (m *memStore) Get(_ context.Context, scope rulestore.Scope) (rulestore.Stored, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sets[scope]
	if !ok {
		return rulestore.Stored{}, rulestore.ErrNotFound
	}

	s.Document = bytes.Clone(s.Document)

	return s, nil
}

func (m *memStore) Put(
	_ context.Context, c rulestore.Change, _ namespace.Fence, e audit.Entry,
) (rulestore.Stored, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failPut != nil {
		return rulestore.Stored{}, m.failPut
	}

	if m.sets[c.Scope].Version != c.Expected {
		return rulestore.Stored{}, rulestore.ErrConflict
	}

	s := rulestore.Stored{
		Scope: c.Scope, Document: bytes.Clone(c.Document), Digest: rulestore.DocumentDigest(c.Document),
		Version: c.Expected + 1, UpdatedAt: e.At.UnixMilli(),
	}
	m.sets[c.Scope] = s
	m.entries = append(m.entries, e)

	return s, nil
}

func (m *memStore) Delete(context.Context, rulestore.Scope, int64, namespace.Fence, audit.Entry) error {
	return nil
}

func (m *memStore) auditEntries() []audit.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]audit.Entry(nil), m.entries...)
}

// fakeNamespaces knows the namespace acme only.
type fakeNamespaces struct{}

func (fakeNamespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return fakeNamespaces{}.WritableHere(context.Background(), name)
}

func (fakeNamespaces) WritableHere(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name != nsAcme {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{Name: nsAcme, FailoverVersion: 1}, nil
}

// fakeUnits knows the unit u-web of acme.
type fakeUnits struct{}

func (fakeUnits) GetUnit(_ context.Context, unitID string) (inventory.Entry, error) {
	if unitID != unitWeb {
		return inventory.Entry{}, inventory.ErrUnitNotFound
	}

	return inventory.Entry{Namespace: nsAcme, Tool: "pulumi", Name: "web/prod", UnitID: unitWeb}, nil
}

func newService(t *testing.T, store *memStore) *jobproxysvc.Service {
	t.Helper()

	svc, err := jobproxysvc.New(jobproxysvc.Config{
		Store: store, Namespaces: fakeNamespaces{}, Units: fakeUnits{},
		Now: func() time.Time { return time.UnixMilli(1_790_000_000_000) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return svc
}

// entryOf returns the audit entry that the handler would hand to the service.
func entryOf() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "alice", IDPSubject: "", TokenID: "", Action: "namespace:manage-proxy-rules",
		Object: "acme", Version: "", ValuePath: "", ValueCount: nil, Changes: []audit.Change{{Key: "session", Old: nil, New: "console", Layer: "", TakesEffect: ""}}, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
}

func nsScope() rulestore.Scope { return rulestore.Scope{Namespace: nsAcme, UnitID: ""} }

func unitScope() rulestore.Scope { return rulestore.Scope{Namespace: nsAcme, UnitID: unitWeb} }

// doc returns a valid document with one proxy rule of id and host.
func doc(id, host string, ops ...jobproxy.Operation) []byte {
	if len(ops) == 0 {
		ops = []jobproxy.Operation{jobproxy.OperationPlan}
	}

	list := ""

	for i, op := range ops {
		if i > 0 {
			list += ","
		}

		list += `"` + string(op) + `"`
	}

	return []byte(`{"rules":[{"id":"` + id + `","operations":[` + list + `],"host":"` + host +
		`","mode":"inspect","methods":["GET"],"paths":["/v1/**"]}]}`)
}
