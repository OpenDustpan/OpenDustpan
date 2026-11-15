package jobproxysvc_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
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

// errAuditAppend is the error of a store that loses the audit entry of a save.
var errAuditAppend = errors.New("audit append failed")

// mustPut saves document at scope and fails the test when the save fails.
func mustPut(t *testing.T, svc *jobproxysvc.Service, scope rulestore.Scope, document []byte) {
	t.Helper()

	_, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: scope, Document: document, IfMatch: "", Entry: entryOf(),
	})
	if err != nil {
		t.Fatalf("Put %+v: %v", scope, err)
	}
}

// memStore is an in-memory rulestore.RuleStore that keeps the audit entries.
type memStore struct {
	mu      sync.Mutex
	sets    map[rulestore.Scope]rulestore.Stored
	entries []audit.Entry
	// failPut makes each Put fail with this error, as a lost audit entry does.
	failPut error
}

func newMemStore() *memStore {
	return &memStore{mu: sync.Mutex{}, sets: map[rulestore.Scope]rulestore.Stored{}, entries: nil, failPut: nil}
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

func (f fakeNamespaces) Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	return f.WritableHere(ctx, name)
}

func (fakeNamespaces) WritableHere(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	var ns namespace.Namespace

	if name != nsAcme {
		return ns, namespace.ErrNotFound
	}

	ns.Name = nsAcme
	ns.FailoverVersion = 1

	return ns, nil
}

// fakeUnits knows the unit u-web of acme.
type fakeUnits struct{}

func (fakeUnits) GetUnit(_ context.Context, unitID string) (inventory.Entry, error) {
	var e inventory.Entry

	if unitID != unitWeb {
		return e, inventory.ErrUnitNotFound
	}

	e.Namespace = nsAcme
	e.Tool = "pulumi"
	e.Name = "web/prod"
	e.UnitID = unitWeb

	return e, nil
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
	session := audit.Change{Key: "session", Old: nil, New: "console", Layer: "", TakesEffect: ""}

	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "alice", IDPSubject: "", TokenID: "",
		Action: "namespace:manage-proxy-rules", Object: "acme", Version: "", ValuePath: "", ValueCount: nil,
		Changes: []audit.Change{session}, SourceIP: "", UserAgent: "",
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

	quoted := make([]string, 0, len(ops))
	for _, op := range ops {
		quoted = append(quoted, `"`+string(op)+`"`)
	}

	list := strings.Join(quoted, ",")

	return []byte(`{"rules":[{"id":"` + id + `","operations":[` + list + `],"host":"` + host +
		`","mode":"inspect","methods":["GET"],"paths":["/v1/**"]}]}`)
}
