package runnerapi_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// assignmentFields lists the JSON fields of protocol.Assignment of each
// assignment version. A change that adds a field to the assignment raises
// runner.AssignmentVersion and adds a row. The proxy session has its own
// message, protocol.ProxyAssignment, so it adds no field here.
func assignmentFields() map[int][]string {
	return map[int][]string{
		1: {
			"namespace", "unit", "tool", "job", "phase", "sweep", "serverURL", "source", "steps", "plan",
			"gitCredentials", "gitHelper", "profile",
		},
	}
}

// TestAssignmentVersionFields checks that the fields of protocol.Assignment
// are the fields of the row of runner.AssignmentVersion.
func TestAssignmentVersionFields(t *testing.T) {
	t.Parallel()

	want, ok := assignmentFields()[runner.AssignmentVersion]
	if !ok {
		t.Fatalf("assignment version %d has no row", runner.AssignmentVersion)
	}

	if runner.AssignmentVersion != 1 {
		t.Errorf("runner.AssignmentVersion = %d, want 1: the proxy session adds no field to the assignment",
			runner.AssignmentVersion)
	}

	fields := reflect.VisibleFields(reflect.TypeFor[protocol.Assignment]())
	got := make([]string, 0, len(fields))

	for _, f := range fields {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		got = append(got, name)
	}

	if !slices.Equal(got, want) {
		t.Errorf("fields of protocol.Assignment = %v, want %v of version %d", got, want, runner.AssignmentVersion)
	}
}
