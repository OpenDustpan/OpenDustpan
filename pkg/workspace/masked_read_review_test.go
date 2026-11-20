package workspace_test

// Hostile tests for the masked read of a workspace state: the deadline of the
// diff, the repeat of a pointer and the keys below a value that
// sensitive_attributes names. Each case sends its input directly to one layer
// and fails when its guard is removed.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// stateWithInstance returns a state with one resource t.n whose instance JSON
// text is instance.
func stateWithInstance(instance string) string {
	return `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"lin-w","outputs":{},` +
		`"resources":[{"mode":"managed","type":"t","name":"n","provider":"p",` +
		`"instances":[` + instance + `]}]}`
}

// TestDiffStatesFailsClosedOnExpiredContext proves a diff with an expired
// context gives an error and no diff: with the context live, the changed key
// below a marked map is one secret path, and with the context expired no path
// of the key reaches the answer. A masked copy that the end of the context cut
// short would hold no marker, and the key name would leak as a changed path.
func TestDiffStatesFailsClosedOnExpiredContext(t *testing.T) {
	t.Parallel()

	marks := workspace.SensitiveMarks{"t.n": {"/tags"}}
	from := workspace.StateWithMarks{
		State: []byte(maskedStateWithAttributes(`{"tags":{"hunter2":"a"}}`)), Marks: marks,
	}
	to := workspace.StateWithMarks{
		State: []byte(maskedStateWithAttributes(`{"tags":{"hunter2":"b"}}`)), Marks: marks,
	}

	live, err := workspace.DiffStates(t.Context(), from, to, "", 0)
	if err != nil {
		t.Fatalf("live context: err = %v", err)
	}

	if len(live.Changed) != 1 || !slices.Equal(live.Changed[0].ChangedSecrets, []string{"/attributes/tags"}) ||
		len(live.Changed[0].ChangedPaths) != 0 {
		t.Fatalf("live context: changed = %+v, want one secret /attributes/tags", live.Changed)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	diff, err := workspace.DiffStates(ctx, from, to, "", 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expired context: err = %v, want context.Canceled", err)
	}

	if strings.Contains(fmt.Sprintf("%+v", diff), "hunter2") {
		t.Fatalf("expired context: the diff holds the key: %+v", diff)
	}
}

// TestDiffStatesEndOfContextAtAnyCheckLeaksNoKey proves a diff whose context
// ends at any one check of the diff gives an error and no key of the marked
// map. The context is done at one call only, so each check of the diff is
// tested alone: a check that is missing, or an error of the masking that the
// diff drops, gives a diff with the key in the changed paths.
func TestDiffStatesEndOfContextAtAnyCheckLeaksNoKey(t *testing.T) {
	t.Parallel()

	marks := workspace.SensitiveMarks{"t.n": {"/tags"}}
	from := workspace.StateWithMarks{
		State: []byte(maskedStateWithAttributes(`{"tags":{"hunter2":"a"}}`)), Marks: marks,
	}
	to := workspace.StateWithMarks{
		State: []byte(maskedStateWithAttributes(`{"tags":{"hunter2":"b"}}`)), Marks: marks,
	}

	count := &atomic.Int64{}

	if _, err := workspace.DiffStates(singleShotCtx{calls: count, at: 0}, from, to, "", 0); err != nil {
		t.Fatalf("uninterrupted diff: %v", err)
	}

	total := count.Load()
	if total == 0 {
		t.Fatal("the diff checked the context 0 times, want at least one check")
	}

	for k := int64(1); k <= total; k++ {
		diff, err := workspace.DiffStates(singleShotCtx{calls: &atomic.Int64{}, at: k}, from, to, "", 0)
		if err == nil {
			t.Errorf("context ends at check %d of %d: diff answered %+v, want an error", k, total, diff)
		}

		if strings.Contains(fmt.Sprintf("%+v", diff), "hunter2") {
			t.Errorf("context ends at check %d of %d: the diff holds the key: %+v", k, total, diff)
		}
	}
}

// TestMaskedPathsRepeatedPointerAnswersOnce proves a path that
// sensitive_attributes and a kept mark both name answers as one marker path.
func TestMaskedPathsRepeatedPointerAnswersOnce(t *testing.T) {
	t.Parallel()

	instance := `{"schema_version":0,"sensitive_attributes":[[{"type":"get_attr","value":"k"}]],` +
		`"attributes":{"k":"v","other":"w"}}`
	marks := workspace.SensitiveMarks{"t.n": {"/k", "/k"}}

	paths, err := workspace.MaskedReadPaths([]byte(stateWithInstance(instance)), marks)
	if err != nil {
		t.Fatalf("err = %v, want a read", err)
	}

	if want := []string{"/resources/0/instances/0/attributes/k"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

// TestSensitiveAttributesWithNoKeptMarks proves a map that sensitive_attributes
// names as a whole gives one marker with no key below it, with no kept marks
// for the version: in the masked set, in the read of one resource and in the
// diff.
func TestSensitiveAttributesWithNoKeptMarks(t *testing.T) {
	t.Parallel()

	instance := func(value string) string {
		return `{"schema_version":0,"sensitive_attributes":[[{"type":"get_attr","value":"tags"}]],` +
			`"attributes":{"id":"i","tags":{"hunter2":"` + value + `"}}}`
	}

	state := []byte(stateWithInstance(instance("v")))

	paths, err := workspace.MaskedReadPaths(state, nil)
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	want := []string{"/resources/0/instances/0/attributes/id", "/resources/0/instances/0/attributes/tags"}

	slices.Sort(paths)

	if !slices.Equal(paths, want) {
		t.Fatalf("masked paths = %v, want %v", paths, want)
	}

	detail, err := workspace.ReadStateResource(t.Context(), state, nil, "t.n")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	slices.Sort(detail.Masked)

	if !slices.Equal(detail.Masked, want) {
		t.Fatalf("detail masked = %v, want %v", detail.Masked, want)
	}

	if strings.Contains(fmt.Sprintf("%+v", detail), "hunter2") {
		t.Fatalf("the read of the resource holds the key: %+v", detail)
	}

	diff, err := workspace.DiffStates(
		t.Context(),
		workspace.StateWithMarks{State: state, Marks: nil},
		workspace.StateWithMarks{State: []byte(stateWithInstance(instance("w"))), Marks: nil},
		"", 0,
	)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}

	if strings.Contains(fmt.Sprintf("%+v", diff), "hunter2") {
		t.Fatalf("the diff holds the key: %+v", diff)
	}

	if len(diff.Changed) != 1 || !slices.Contains(diff.Changed[0].ChangedSecrets, "/attributes/tags") {
		t.Fatalf("diff changed = %+v, want the secret /attributes/tags", diff.Changed)
	}
}

// wrongCurrentStates is a state store whose current version of every workspace
// is the version of another one, as a store that follows a wrong pointer.
type wrongCurrentStates struct {
	*memStore

	other workspace.StateVersionID
}

// CurrentStateVersion answers the version of the other workspace.
func (w wrongCurrentStates) CurrentStateVersion(ctx context.Context, _ workspace.ID) (workspace.StateVersion, error) {
	return w.GetStateVersion(ctx, w.other)
}

// TestMaskedReadRefusesACurrentVersionOfAnotherWorkspace proves the read of the
// latest version checks the owner of the version that the store names: the
// service is tested alone, over a store that follows a pointer to the version of
// another workspace.
func TestMaskedReadRefusesACurrentVersionOfAnotherWorkspace(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, maskedStateWithAttributes(`{"own":"x"}`), 4<<20)
	others := otherWorkspaces(t, h.f)

	h.f.svc = serviceOverStore(t, h.f, wrongCurrentStates{memStore: h.f.store, other: others.sameNS})

	page, err := h.read(t.Context(), latest(), "")
	if !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("err = %v, want ErrStateVersionNotFound", err)
	}

	if len(page.Markers) != 0 || len(page.ValuePaths) != 0 {
		t.Fatalf("page = %+v, want no marker of the other workspace", page)
	}
}

// TestMaskedPageRefusesANegativeOffset proves the guard of the page refuses a
// negative offset that a caller of the page passes with no cursor check.
func TestMaskedPageRefusesANegativeOffset(t *testing.T) {
	t.Parallel()

	page, err := workspace.MaskedPageAt(testCursorKey(), []string{"/a", "/b"}, -1)
	if !errors.Is(err, workspace.ErrBadPath) || len(page.Markers) != 0 {
		t.Fatalf("page %+v, err %v, want ErrBadPath and no marker", page, err)
	}
}

// TestMaskedReadsRefuseUnreadableInstanceShapes proves an instances value that
// is no array, an instance that is no object, and scalar attributes with kept
// marks or sensitive_attributes are refused with ErrUnreadableState, in the
// masked set and in the read of one resource.
func TestMaskedReadsRefuseUnreadableInstanceShapes(t *testing.T) {
	t.Parallel()

	marks := workspace.SensitiveMarks{"t.n": {"/a"}}
	head := `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"l","outputs":{},` +
		`"resources":[{"mode":"managed","type":"t","name":"n","provider":"p",`

	tests := []struct {
		name  string
		state string
		marks workspace.SensitiveMarks
	}{
		{name: "instances is a string", state: head + `"instances":"hunter2"}]}`, marks: marks},
		{name: "instance is a string", state: head + `"instances":["hunter2"]}]}`, marks: marks},
		{
			name:  "scalar attributes with marks",
			state: head + `"instances":[{"attributes":"hunter2"}]}]}`, marks: marks,
		},
		{
			name:  "scalar attributes with sensitive_attributes",
			state: head + `"instances":[{"attributes":"hunter2","sensitive_attributes":[]}]}]}`, marks: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := workspace.MaskedReadPaths([]byte(tc.state), tc.marks)
			if !errors.Is(err, workspace.ErrUnreadableState) {
				t.Errorf("masked paths: err = %v, want ErrUnreadableState", err)
			}

			_, err = workspace.ReadStateResource(t.Context(), []byte(tc.state), tc.marks, "t.n")
			if !errors.Is(err, workspace.ErrUnreadableState) {
				t.Errorf("read resource: err = %v, want ErrUnreadableState", err)
			}

			if err != nil && strings.Contains(err.Error(), "hunter2") {
				t.Errorf("the error holds the value: %v", err)
			}
		})
	}
}

// TestMaskedPathsMalformedOutputs proves an outputs value of a shape that the
// server cannot read is a classified error whose text holds no output name.
func TestMaskedPathsMalformedOutputs(t *testing.T) {
	t.Parallel()

	for _, outputs := range []string{`"hunter2"`, `{"hunter2":"x"}`, `{"hunter2":{"sensitive":"yes"}}`} {
		state := `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"l","outputs":` + outputs +
			`,"resources":[]}`

		_, err := workspace.MaskedStateAndOutputPaths(t.Context(), []byte(state), nil)
		if !errors.Is(err, workspace.ErrUnreadableState) {
			t.Errorf("outputs %s: err = %v, want ErrUnreadableState", outputs, err)

			continue
		}

		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("outputs %s: the error holds the output name: %v", outputs, err)
		}
	}
}
