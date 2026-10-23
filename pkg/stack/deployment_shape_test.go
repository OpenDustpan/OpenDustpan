package stack_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// shapeCase is a deployment with the answer that the CLI needs: valid is true
// for an object that the CLI can read.
type shapeCase struct {
	name       string
	deployment string
	sequence   int64
	valid      bool
}

// shapeCases returns deployments that the CLI cannot read, and objects that it
// can. A deployment must be an object, and its known fields must have the JSON
// type that the CLI expects.
func shapeCases() []shapeCase {
	return []shapeCase{
		{name: "an empty object", deployment: `{}`, sequence: 0, valid: true},
		{
			name:       "a full deployment",
			deployment: `{"manifest":{"time":"2026-01-01T00:00:00Z"},"secrets_providers":{"type":"service"},"resources":[],"pending_operations":[]}`,
			sequence:   0,
			valid:      true,
		},
		{name: "null fields", deployment: `{"resources":null,"manifest":null}`, sequence: 0, valid: true},
		{name: "a repeated member", deployment: `{"resources":[],"resources":[]}`, sequence: 0, valid: true},
		{name: "no deployment", deployment: ``, sequence: 0, valid: false},
		{name: "a string", deployment: `"notanobject"`, sequence: 0, valid: false},
		{name: "a number", deployment: `42`, sequence: 0, valid: false},
		{name: "an array", deployment: `[1,2]`, sequence: 0, valid: false},
		{name: "null", deployment: `null`, sequence: 0, valid: false},
		{name: "a boolean", deployment: `true`, sequence: 0, valid: false},
		{name: "text that is not JSON", deployment: `{"resources":`, sequence: 0, valid: false},
		{name: "data after the object", deployment: `{} {}`, sequence: 0, valid: false},
		{name: "resources that are a string", deployment: `{"resources":"x"}`, sequence: 0, valid: false},
		{name: "resources that are an object", deployment: `{"resources":{}}`, sequence: 0, valid: false},
		{name: "a manifest that is an array", deployment: `{"manifest":[]}`, sequence: 0, valid: false},
		{name: "providers that are a number", deployment: `{"secrets_providers":3}`, sequence: 0, valid: false},
		{
			name:       "pending operations that are a string",
			deployment: `{"pending_operations":"x"}`,
			sequence:   0,
			valid:      false,
		},
		{
			name:       "a resource that is an object",
			deployment: `{"resources":[{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev"}]}`,
			sequence:   0,
			valid:      true,
		},
		{name: "a resource that is a number", deployment: `{"resources":[1]}`, sequence: 0, valid: false},
		{name: "a resource that is null", deployment: `{"resources":[null]}`, sequence: 0, valid: false},
		{name: "a second resource that is a string", deployment: `{"resources":[{},"x"]}`, sequence: 0, valid: false},
		{name: "a resource list that is not closed", deployment: `{"resources":[{}`, sequence: 0, valid: false},
		{
			name:       "a pending operation that is a string",
			deployment: `{"pending_operations":["x"]}`,
			sequence:   0,
			valid:      false,
		},
		{
			name:       "a pending operation that is an object",
			deployment: `{"pending_operations":[{}]}`,
			sequence:   0,
			valid:      true,
		},
		{
			name:       "a versioned deployment with a resource that is a number",
			deployment: `{"version":3,"deployment":{"resources":[1]}}`,
			sequence:   1,
			valid:      false,
		},
		{
			name:       "a versioned deployment of a sequenced save",
			deployment: `{"version":3,"deployment":{"resources":[]}}`,
			sequence:   1,
			valid:      true,
		},
		{
			name:       "a versioned deployment that holds a string",
			deployment: `{"version":3,"deployment":"notanobject"}`,
			sequence:   1,
			valid:      false,
		},
		{
			name:       "a versioned deployment with resources that are a string",
			deployment: `{"version":3,"deployment":{"resources":"x"}}`,
			sequence:   1,
			valid:      false,
		},
	}
}

// TestImportRefusesADeploymentThatIsNotAnObject proves that an import stores
// nothing and records no update for a deployment that the CLI cannot read.
func TestImportRefusesADeploymentThatIsNotAnObject(t *testing.T) {
	t.Parallel()

	for _, tc := range shapeCases() {
		if tc.sequence != 0 {
			continue
		}

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkImportShape(t, tc)
		})
	}
}

// checkImportShape imports tc.deployment and checks the answer.
func checkImportShape(t *testing.T, tc shapeCase) {
	t.Helper()

	f := newUpdates(t)

	_, err := f.svc.Import(t.Context(), f.ref, "", checkpoint(0, tc.deployment))
	if tc.valid {
		if err != nil {
			t.Fatalf("import %s: %v", tc.deployment, err)
		}

		return
	}

	if !errors.Is(err, stack.ErrInvalidUpdate) {
		t.Fatalf("import %s: err = %v, want ErrInvalidUpdate", tc.deployment, err)
	}

	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	if n := len(f.store.updates); n != 0 {
		t.Errorf("%d updates recorded for a refused import, want none", n)
	}
}

// TestSaveCheckpointRefusesADeploymentThatIsNotAnObject proves that a
// checkpoint save of a running update, whole or sequenced, stores nothing for a
// deployment that the CLI cannot read, and that the stack keeps its last good
// checkpoint.
func TestSaveCheckpointRefusesADeploymentThatIsNotAnObject(t *testing.T) {
	t.Parallel()

	for _, tc := range shapeCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSaveShape(t, tc)
		})
	}
}

// checkSaveShape saves tc.deployment after a good checkpoint and checks the
// answer.
func checkSaveShape(t *testing.T, tc shapeCase) {
	t.Helper()

	f := newUpdates(t)
	s := f.started(t, stack.KindUpdate)

	const good = `{"resources":[]}`

	if err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, checkpoint(0, good)); err != nil {
		t.Fatalf("save the first checkpoint: %v", err)
	}

	err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, checkpoint(tc.sequence, tc.deployment))
	if tc.valid {
		if err != nil {
			t.Fatalf("save %s: %v", tc.deployment, err)
		}

		return
	}

	if !errors.Is(err, stack.ErrInvalidUpdate) {
		t.Fatalf("save %s: err = %v, want ErrInvalidUpdate", tc.deployment, err)
	}

	assertExport(t, f, good)
}

// TestCheckpointDeltaRefusesAResultThatIsNotAnObject proves that a delta whose
// result the CLI cannot read fails as an invalid delta and saves nothing, so the
// CLI falls back to a whole save.
func TestCheckpointDeltaRefusesAResultThatIsNotAnObject(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	s := f.started(t, stack.KindUpdate)

	const base = `{"version":3,"deployment":{"resources":[]}}`

	if err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, checkpoint(1, base)); err != nil {
		t.Fatalf("save base: %v", err)
	}

	const result = `{"version":3,"deployment":"x"}`

	edits := "[" + editJSON(0, len(base), result) + "]"
	expectInvalidDelta(t, f, s, deltaOf(2, result, edits), "a result that holds a string")

	assertExport(t, f, `{"resources":[]}`)
}
