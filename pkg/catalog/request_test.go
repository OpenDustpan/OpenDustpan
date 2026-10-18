package catalog_test

import (
	"encoding/json"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/catalog"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/policy"
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

func TestGateFor(t *testing.T) {
	t.Parallel()

	approve := policy.Decision{
		Effect: policy.EffectApprove, Reasons: nil, Approvals: &policy.Approvals{Groups: []string{"dba"}, Count: 2},
		Engine: "", PolicyVersion: "",
	}

	g, ok := catalog.GateFor(&approve, true)
	if !ok || g.Type != job.GateApproval || g.Approvals != 2 || len(g.Groups) != 1 || g.AllowSelf || !g.BindPlan {
		t.Fatalf("GateFor(approve) = %+v, %v, want 2 approvals by dba, not by the requester", g, ok)
	}

	allow := policy.Decision{Effect: policy.EffectAllow, Reasons: nil, Approvals: nil, Engine: "", PolicyVersion: ""}
	if _, ok := catalog.GateFor(&allow, true); ok {
		t.Fatal("GateFor(allow) gives a gate")
	}
}

func wantInputsEdit(t *testing.T, e job.FileEdit) {
	t.Helper()

	var inputs map[string]string
	if err := json.Unmarshal(e.Keys["inputs"], &inputs); err != nil || inputs["size"] != "small" {
		t.Fatalf("the edit of inputs = %s, %v", e.Keys["inputs"], err)
	}
}
