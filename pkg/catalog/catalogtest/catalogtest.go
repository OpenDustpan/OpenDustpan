// Package catalogtest is the conformance suite for catalog.Store and
// catalog.BlueprintStore implementations, and, in RunUnitTx, for
// catalog.UnitTx. Every store adapter runs it:
//
//	func TestCatalogConformance(t *testing.T) {
//		t.Parallel()
//		catalogtest.Run(t, func(t *testing.T) catalogtest.Harness { return newHarness(t) })
//	}
//
// The suite comes before the first SQL adapter. Its cases state the rules
// of the ports, and a reference adapter in memory, in the tests of this
// package, runs them.
package catalogtest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/catalog"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/policy"
	"opendustpan.org/opendustpan/pkg/quota"
)

// Harness is one fresh, empty database for one case. The stores share the
// database, as the store adapters of one installation do.
type Harness struct {
	Requests   catalog.Store
	Blueprints catalog.BlueprintStore
	Domain     Domain
}

// Domain makes the rows that the Catalog needs and that other ports own,
// and reads what the stores write to other ports.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// CreateUnit creates a unit of tool in the namespace of fence and
	// returns its reference, with its stable unit ID set.
	CreateUnit(ctx context.Context, fence namespace.Fence, tool, name string) (identity.Object, error)
	// BumpFence increments the failover version of the namespace of fence,
	// as a failover does, and returns the new fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// AuditEntries returns the audit entries of the namespace ns, in the
	// order of their append.
	AuditEntries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error)
	// Operation returns the kind of the durable operation with the start
	// key key, and false when none exists.
	Operation(ctx context.Context, key string) (durable.Kind, bool, error)
	// QuotaUsage returns the usage of the key of each of checks at when, as
	// quota.Counter.Usage gives it from the counter that shares the
	// database of the stores.
	QuotaUsage(ctx context.Context, checks []quota.Check, when time.Time) ([]quota.Usage, error)
	// Unit returns the unit of tool with the tool's own name in the
	// namespace ns, with its stable unit ID set, and false when none exists.
	Unit(ctx context.Context, ns namespace.Name, tool, name string) (identity.Object, bool, error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// The namespace of the cases.
const ns = namespace.Name("payments")

// Run executes the suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness, f fixture)
	}{
		{"a first submit creates the request and an accepted revision", testSubmitCreates},
		{"a submit starts the start operation and appends its audit entries", testSubmitStarts},
		{"an unknown request or revision is ErrNotFound", testNotFound},
		{"a submit while a revision is in flight is ErrInFlight", testInFlight},
		{"a create of a request that exists is ErrConflict", testCreateExists},
		{"a submit on a stale version is ErrConflict", testSubmitStale},
		{"a change to applied sets the applied revision", testChangeApplied},
		{"a terminal create starts the quota operation", testChangeQuota},
		{"a terminal revision does not change", testChangeFinished},
		{"a change on a stale version is ErrConflict", testChangeStale},
		{"a revision after a terminal one gets the next number", testNextRevision},
		{"a stale fence writes nothing", testSubmitFenced},
		{"a submit reserves and records in its transaction", testSubmitQuota},
		{"a failed submit keeps no reservation and no request", testSubmitQuotaFails},
		{"a submit refused by a units limit records no request", testSubmitExceeded},
		{"a revision needs a deadline, and its reservation ends at it", testSubmitDeadline},
		{"a revision from git supersedes a create and takes its unit and reservation", testSupersedeCreate},
		{"a supersede of a change that is in git is ErrConflict", testSupersedeDelivered},
		{"a create from git makes its unit in the submit", testSubmitProvisions},
		{"a revision past its deadline with a running job keeps its unit and its reservation", testExpireRunning},
		{"a state version applies the revision from git at its commit", testObserve},
		{"list selects by blueprint and by state", testList},
		{"a new draft gets version 1, and a stale one is ErrConflict", testDraft},
		{"a publish stores the next version, and a version never changes", testPublish},
		{"a publish of a stale draft is ErrConflict", testPublishStale},
		{"a retire marks a version, once", testRetire},
		{"a list of blueprints gives the newest version of each", testListBlueprints},
		{"a blueprint write on a stale fence writes nothing", testBlueprintFenced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			tc.run(t, h, setUp(t, h))
		})
	}
}

// fixture is the namespace and the unit of one case.
type fixture struct {
	unit  identity.Object
	fence namespace.Fence
}

func setUp(t *testing.T, h Harness) fixture {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	u, err := h.Domain.CreateUnit(t.Context(), fence, "opentofu", "databases/orders-db")
	if err != nil {
		t.Fatalf("CreateUnit() = %v", err)
	}

	return fixture{unit: u, fence: fence}
}

func ref() catalog.RequestRef {
	return catalog.RequestRef{Namespace: ns, Blueprint: "postgres", Name: "orders-db"}
}

func document(size string) catalog.Document {
	return catalog.Document{
		Blueprint: catalog.BlueprintPin{Name: "postgres", Version: 1},
		Ensure:    catalog.EnsurePresent,
		Home: &catalog.Home{
			Kind: catalog.HomeGit, Connection: "", Repository: "payments/infra", Ref: "main", Delivery: "",
		},
		Inputs:        map[string]json.RawMessage{"size": json.RawMessage(`"` + size + `"`)},
		Name:          "orders-db",
		Namespace:     ns,
		SchemaVersion: 1,
	}
}

func entry(action identity.Action, object string) audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: ns, Subject: "alice", IDPSubject: "", TokenID: "",
		Action: string(action), Object: object, Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0,
	}
}

// submission returns a submit of size on req, which is the zero request for
// a first submit.
func submission(f fixture, req catalog.Request, op catalog.Operation, size string) *catalog.Submission {
	if req.ID == "" {
		req = catalog.Request{
			CreatedAt: time.Time{},
			UpdatedAt: time.Time{},
			Unit:      f.unit,
			ID:        "",
			Ref:       ref(),
			Home: catalog.Home{
				Kind:       catalog.HomeGit,
				Connection: "",
				Repository: "payments/infra",
				Ref:        "main",
				Delivery:   "",
			},
			Ensure:    "",
			UnitName:  "databases/orders-db",
			QuotaKeys: nil,
			Last:      0,
			Applied:   0,
			Version:   0,
		}
	}

	return &catalog.Submission{
		Request: req,
		Revision: catalog.Revision{
			CreatedAt: time.Time{}, UpdatedAt: time.Time{}, BreakGlass: nil, Request: req.ID,
			Origin: catalog.OriginAPI, State: catalog.StateAccepted, Operation: op, Job: "", JobVersion: 0,
			Commit: "", Reason: "", SubmittedBy: "alice", Agent: "", Reservation: "",
			Document:  document(size),
			Decision:  policy.Combined{Effect: policy.EffectAllow, Reasons: nil, Approvals: nil},
			Decisions: nil, Failures: nil, Usage: nil, Number: 0, BlueprintVersion: 1,
			Deadline: catalog.RevisionDeadline(catalog.OriginAPI, time.Now().UTC()),
		},
		Audit:      []audit.Entry{entry(catalog.ActionCatalogRequest, "payments/postgres")},
		Quota:      nil,
		Provision:  nil,
		Supersedes: 0,
	}
}

func submit(t *testing.T, h Harness, s *catalog.Submission, f fixture) (catalog.Request, catalog.Revision) {
	t.Helper()

	req, rev, err := h.Requests.Submit(t.Context(), s, f.fence)
	if err != nil {
		t.Fatalf("Submit() = %v", err)
	}

	return req, rev
}

// change returns a change of the first revision of req to to.
func change(req *catalog.Request, to catalog.RevisionState) catalog.RevisionChange {
	return catalog.RevisionChange{
		Ref: req.Ref, To: to, Job: "", JobVersion: 0, Reason: "", Failures: nil, Unit: identity.Object{},
		Number: 1, Version: req.Version,
	}
}

// none is the zero request, for a first submit.
func none() catalog.Request {
	var r catalog.Request

	return r
}

func get(t *testing.T, h Harness) catalog.Request {
	t.Helper()

	req, err := h.Requests.Get(t.Context(), ref())
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}

	return req
}
