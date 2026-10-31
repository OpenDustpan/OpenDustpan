package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/internal/policy"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
)

// startFixture is a request of the document of the ADR, in each home, and
// the blueprint version that it pins.
type startFixture struct {
	version       catalog.Version
	doc           catalog.Document
	inOpenDustpan catalog.Request
	inGit         catalog.Request
}

func newStartFixture(t *testing.T) startFixture {
	t.Helper()

	var f startFixture

	f.version.Blueprint = parseBlueprint(t, readFile(t, "postgres.blueprint.json"))
	f.version.Namespace, f.version.Number = "payments", 3
	f.doc = parseDocument(t, string(readFile(t, "orders-db.request.json")))

	f.inOpenDustpan.Unit = unit(t, "databases/orders-db", "ws-1")
	f.inOpenDustpan.ID, f.inOpenDustpan.Ref = "req-1", f.doc.Ref()
	f.inOpenDustpan.Home.Kind = catalog.HomeOpenDustpan
	f.inGit = f.inOpenDustpan
	f.inGit.Home = *f.doc.Home

	return f
}

func (f *startFixture) revision(op catalog.Operation) catalog.Revision {
	var r catalog.Revision

	r.Request, r.Origin, r.State, r.Operation = "req-1", catalog.OriginAPI, catalog.StateAccepted, op
	r.Document, r.Number, r.BlueprintVersion = f.doc, 2, f.doc.Blueprint.Version

	return r
}

func TestStartRequestForARunner(t *testing.T) {
	t.Parallel()

	f := newStartFixture(t)
	rev := f.revision(catalog.OperationCreate)

	got, err := catalog.StartRequestFor(&f.inOpenDustpan, &rev, &f.version, "orders service")
	if err != nil {
		t.Fatalf("StartRequestFor() = %v", err)
	}

	if got.Executor != job.ExecutorRunner || got.Operation != job.OperationApply || got.Pool != "default" ||
		got.IdempotencyKey != "req-1/2" || got.Unit.UnitID() != "ws-1" || got.Edits != nil ||
		got.Reason != "orders service" {
		t.Fatalf("StartRequestFor() = %+v", got)
	}

	rev = f.revision(catalog.OperationDestroy)

	got, err = catalog.StartRequestFor(&f.inOpenDustpan, &rev, &f.version, "")
	if err != nil || got.Operation != job.OperationDestroy {
		t.Fatalf("StartRequestFor(destroy) = %+v, %v", got, err)
	}
}

func TestStartRequestForWriteBack(t *testing.T) {
	t.Parallel()

	f := newStartFixture(t)
	rev := f.revision(catalog.OperationDestroy)

	got, err := catalog.StartRequestFor(&f.inGit, &rev, &f.version, "")
	if err != nil {
		t.Fatalf("StartRequestFor() = %v", err)
	}

	// A write-back job always applies: the CI reads ensure from the render
	// and runs the destroy.
	if got.Executor != job.ExecutorWriteBack || got.Operation != job.OperationApply ||
		got.Delivery != job.DeliveryReviewRequest || got.Pool != "" ||
		got.Location.Directory != "catalog/payments/postgres/orders-db" || got.Location.Ref != "main" {
		t.Fatalf("StartRequestFor() = %+v", got)
	}

	if len(got.Edits) != 1 || got.Edits[0].Path != catalog.RequestFile {
		t.Fatalf("Edits = %+v", got.Edits)
	}

	wantInputsEdit(t, got.Edits[0])

	rev = f.revision(catalog.OperationNone)
	if _, err := catalog.StartRequestFor(&f.inGit, &rev, &f.version, ""); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("StartRequestFor(none) = %v, want ErrInvalid", err)
	}
}

func TestEditableRequestFile(t *testing.T) {
	t.Parallel()

	f := newStartFixture(t)
	rev := f.revision(catalog.OperationUpdate)

	got, err := catalog.StartRequestFor(&f.inGit, &rev, &f.version, "")
	if err != nil {
		t.Fatalf("StartRequestFor() = %v", err)
	}

	// The job model takes the edit with the predicate of the Catalog, which
	// allows the request file and no other file.
	editable := catalog.EditableRequestFile(f.doc.Ref(), got.Location.Directory)
	if err := job.ValidateEdits(got.Location.Directory, got.Edits, editable); err != nil {
		t.Fatalf("ValidateEdits() = %v", err)
	}

	for _, p := range []string{"main.tf", "../orders-db/request.json", "x/request.json", "request.json.tmp"} {
		if editable(p) {
			t.Fatalf("EditableRequestFile() allows %q", p)
		}
	}

	if catalog.EditableRequestFile(f.doc.Ref(), "catalog/payments/redis/orders-db")(catalog.RequestFile) {
		t.Fatal("EditableRequestFile() allows the request file of another directory")
	}
}

// groupNames is a gate.GroupResolver over groups that can be deleted and made
// again. A group made gets a new ID each time.
type groupNames struct {
	mu     sync.Mutex
	next   int
	groups map[string]string
}

func newGroupNames(names ...string) *groupNames {
	g := &groupNames{mu: sync.Mutex{}, next: 0, groups: map[string]string{}}
	for _, n := range names {
		g.make(n)
	}

	return g
}

func (g *groupNames) ResolveGroup(_ context.Context, name string) (gate.GroupRef, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if strings.ContainsAny(name, " <>/") || name == "" {
		return gate.GroupRef{ID: "", Name: ""}, gate.ErrInvalid
	}

	if id, ok := g.groups[strings.ToLower(name)]; ok {
		return gate.GroupRef{ID: id, Name: strings.ToLower(name)}, nil
	}

	return gate.GroupRef{ID: "", Name: ""}, gate.ErrNotFound
}

// make adds a group called name and returns its new ID.
func (g *groupNames) make(name string) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.next++
	id := fmt.Sprintf("group-%d", g.next)
	g.groups[strings.ToLower(name)] = id

	return id
}

func (g *groupNames) remove(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.groups, strings.ToLower(name))
}

func approveOf(groups ...string) policy.Decision {
	return policy.Decision{
		Effect: policy.EffectApprove, Reasons: nil,
		Approvals: &policy.Approvals{
			Rule: strings.ToLower(strings.Join(groups, "-")), Groups: groups, Count: 1,
		},
		Engine: "", PolicyVersion: "",
	}
}

// approveOfRule is an approve decision of the rule, by the groups.
func approveOfRule(rule string, groups ...string) policy.Decision {
	d := approveOf(groups...)
	d.Approvals.Rule = rule

	return d
}

func TestGateFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		decisions  []policy.Decision
		wantGroups [][]string
		wantIDs    [][]string
	}{
		{
			name:       "two engines with disjoint groups give two gates",
			decisions:  []policy.Decision{approveOf("security"), approveOf("dba")},
			wantGroups: [][]string{{"dba"}, {"security"}},
			wantIDs:    [][]string{{"group-2"}, {"group-1"}},
		},
		{
			name:       "two rules with the same group give two gates",
			decisions:  []policy.Decision{approveOfRule("security", "dba"), approveOfRule("cost", "dba")},
			wantGroups: [][]string{{"dba"}, {"dba"}},
			wantIDs:    [][]string{{"group-2"}, {"group-2"}},
		},
		{
			name:       "a name in another case is the same group",
			decisions:  []policy.Decision{approveOf("DBA", "dba")},
			wantGroups: [][]string{{"dba"}},
			wantIDs:    [][]string{{"group-2"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, err := policy.Combine(tt.decisions)
			if err != nil {
				t.Fatalf("Combine() = %v", err)
			}

			gates, err := catalog.GateFor(t.Context(), &d, true, newGroupNames("security", "dba"))
			if err != nil || len(gates) != len(tt.wantGroups) {
				t.Fatalf("GateFor() = %+v, %v; want %d gates", gates, err, len(tt.wantGroups))
			}

			for i, g := range gates {
				wantGate(t, i, g, tt.wantGroups[i], tt.wantIDs[i])
			}
		})
	}
}

// wantGate fails the test unless g is the gate of one approval by the groups
// with the IDs, not by the requester.
func wantGate(t *testing.T, i int, g job.Gate, groups, ids []string) {
	t.Helper()

	if g.Type != job.GateApproval || g.Approvals != 1 || !reflect.DeepEqual(g.Groups, groups) ||
		!reflect.DeepEqual(g.GroupIDs, ids) || g.AllowSelf || !g.BindPlan {
		t.Fatalf("gate %d = %+v, want groups %v with IDs %v, not by the requester", i, g, groups, ids)
	}
}

// TestGateForRefusesAGroupThatCannotBeResolved proves that a policy that names
// a group that does not exist, or a name that is not valid, builds no gate.
func TestGateForRefusesAGroupThatCannotBeResolved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		group string
		want  gate.ApproversReason
	}{
		{"a group that does not exist", "ghost", gate.ApproversGroupMissing},
		{"a name that is not valid", "bad name", gate.ApproversGroupInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Built by hand, as a caller other than Combine can build it:
			// GateFor refuses the group on its own, also when Combine
			// would have refused the decision first.
			d := policy.Combined{
				Effect: policy.EffectApprove, Reasons: nil,
				Approvals: []policy.Approvals{{Rule: "ops", Groups: []string{tt.group}, Count: 1}},
			}

			gates, err := catalog.GateFor(t.Context(), &d, true, newGroupNames("security", "dba"))

			var ae *gate.ApproversError
			if !errors.As(err, &ae) || ae.Reason != tt.want || len(gates) != 0 {
				t.Fatalf("GateFor() = %+v, %v; want reason %v and no gate", gates, err, tt.want)
			}
		})
	}
}

func TestGateForGivesNoGateForAnAllow(t *testing.T) {
	t.Parallel()

	allow := policy.Combined{Effect: policy.EffectAllow, Reasons: nil, Approvals: nil}

	got, err := catalog.GateFor(t.Context(), &allow, true, newGroupNames())
	if err != nil || len(got) != 0 {
		t.Fatalf("GateFor(allow) = %+v, %v; want no gate", got, err)
	}
}

// TestGateForKeepsTheGroupThatItResolved proves that a gate follows the group
// that had the name when the gate was built: a group that someone deletes and
// makes again with the same name has another ID, and the gate does not name
// it. A gate that is built after the new group exists names the new group.
func TestGateForKeepsTheGroupThatItResolved(t *testing.T) {
	t.Parallel()

	groups := newGroupNames("dba")

	d, err := policy.Combine([]policy.Decision{approveOf("dba")})
	if err != nil {
		t.Fatalf("Combine() = %v", err)
	}

	before, err := catalog.GateFor(t.Context(), &d, false, groups)
	if err != nil || len(before) != 1 {
		t.Fatalf("GateFor() = %+v, %v; want one gate", before, err)
	}

	groups.remove("dba")
	remade := groups.make("dba")

	if slices.Contains(before[0].GroupIDs, remade) {
		t.Fatalf("gate IDs = %v, want the group that was made again (%s) not among them", before[0].GroupIDs, remade)
	}

	after, err := catalog.GateFor(t.Context(), &d, false, groups)
	if err != nil || len(after) != 1 || !reflect.DeepEqual(after[0].GroupIDs, []string{remade}) {
		t.Fatalf("GateFor() = %+v, %v; want the new group %s", after, err, remade)
	}
}

// TestGateForOpensWithApprovals proves that the gates of a valid request work
// through the job gate: they wait, never count the requester, open with an
// approval by another person, and a rejection rejects. The service checks the
// groups of an approver when it records the approval.
func TestGateForOpensWithApprovals(t *testing.T) {
	t.Parallel()

	d, err := policy.Combine([]policy.Decision{approveOf("security"), approveOf("dba")})
	if err != nil {
		t.Fatalf("Combine() = %v", err)
	}

	gates, err := catalog.GateFor(t.Context(), &d, false, newGroupNames("security", "dba"))
	if err != nil {
		t.Fatalf("GateFor() = %v", err)
	}

	approval := func(subject string, v job.Verdict) job.Approval {
		return job.Approval{
			Gate: job.GateApproval, Subject: subject, Agent: "", Verdict: v, Comment: "", Plan: nil,
			At: time.Time{}, Auth: "", AuthAt: time.Time{},
		}
	}

	tests := []struct {
		name      string
		approvals []job.Approval
		want      job.GateStatus
	}{
		{"no approval", nil, job.GateWaiting},
		{"the requester never counts", []job.Approval{approval("requester", job.VerdictApprove)}, job.GateWaiting},
		{"one person who is not the requester", []job.Approval{approval("alice", job.VerdictApprove)}, job.GateOpen},
		{"a rejection", []job.Approval{approval("alice", job.VerdictReject)}, job.GateRejected},
	}

	devPolicy := gate.ApproverPolicy{Production: false, IsDev: nil}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := job.GatesStatus(gates, tt.approvals, "requester", devPolicy); got != tt.want {
				t.Fatalf("GatesStatus() = %s, want %s", got, tt.want)
			}
		})
	}
}

func wantInputsEdit(t *testing.T, e job.FileEdit) {
	t.Helper()

	var inputs map[string]string
	if err := json.Unmarshal(e.Keys["inputs"], &inputs); err != nil || inputs["size"] != "small" {
		t.Fatalf("the edit of inputs = %s, %v", e.Keys["inputs"], err)
	}
}
