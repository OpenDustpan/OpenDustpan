package job_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

func TestStartRequirements(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		executor job.ExecutorKind
		op       job.Operation
		want     identity.Action
		err      error
	}{
		{job.ExecutorRunner, job.OperationPlan, job.ActionUnitRunPlan, nil},
		{job.ExecutorRunner, job.OperationApply, job.ActionUnitRunApply, nil},
		{job.ExecutorRunner, job.OperationRefresh, job.ActionUnitRunApply, nil},
		{job.ExecutorRunner, job.OperationDestroy, job.ActionUnitRunDestroy, nil},
		{job.ExecutorWriteBack, job.OperationApply, job.ActionUnitWriteBack, nil},
		{job.ExecutorWriteBack, job.OperationDestroy, "", job.ErrUnsupported},
		{job.ExecutorRunner, "import", "", job.ErrInvalid},
		{"", job.OperationApply, "", job.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(string(tt.executor)+"/"+string(tt.op), func(t *testing.T) {
			t.Parallel()

			got, err := job.StartRequirements(newSpec(unit, tt.op, tt.executor), nil)
			if !errors.Is(err, tt.err) {
				t.Fatalf("StartRequirements() error = %v, want %v", err, tt.err)
			}

			if tt.err != nil {
				return
			}

			if len(got) != 1 || got[0].Action != tt.want || got[0].Object.String() != unit.String() {
				t.Fatalf("StartRequirements() = %v, want %s on %s", got, tt.want, unit)
			}
		})
	}
}

func TestStartRequirementsWithBreakGlass(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatal(err)
	}

	got, err := job.StartRequirements(newSpec(unit, job.OperationApply, job.ExecutorRunner),
		&job.BreakGlass{Reason: "incident 12", Freeze: "frz-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].Action != job.ActionUnitRunApply || got[1].Action != job.ActionNamespaceBreakFreeze ||
		got[1].Object.String() != identity.NamespaceObject("acme").String() {
		t.Fatalf("StartRequirements() with break-glass = %v", got)
	}
}

// newSpec returns a spec with only a unit, an operation and an executor.
func newSpec(unit identity.Object, op job.Operation, k job.ExecutorKind) job.Spec {
	return job.Spec{
		Unit:           unit,
		Operation:      op,
		Purpose:        "",
		Executor:       k,
		Location:       job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
		LocationSource: "",
		Steps:          nil,
		Profile:        "",
		Pool:           "",
		Edits:          nil,
		Delivery:       "",
		Defaulted:      nil,
	}
}

func TestStartRequirementsNeedsAUnit(t *testing.T) {
	t.Parallel()

	spec := newSpec(identity.NamespaceObject("acme"), job.OperationPlan, job.ExecutorRunner)
	if _, err := job.StartRequirements(spec, nil); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("StartRequirements() = %v, want ErrInvalid", err)
	}
}

func TestOperationWritesState(t *testing.T) {
	t.Parallel()

	want := map[job.Operation]bool{
		job.OperationPlan: false, job.OperationApply: true, job.OperationDestroy: true, job.OperationRefresh: true,
		"import": false,
	}

	for op, w := range want {
		if got := op.WritesState(); got != w {
			t.Errorf("%s.WritesState() = %t, want %t", op, got, w)
		}
	}
}
