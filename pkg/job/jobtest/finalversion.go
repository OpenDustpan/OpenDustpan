package jobtest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// finalVersionCases are the cases of job.FinalVersionReader that need no
// state write.
func finalVersionCases() []suiteCase {
	return []suiteCase{
		{"final version of a unit with no final version is false", testFinalVersionNone},
		{"empty unit ID is ErrInvalid", testFinalVersionEmptyUnit},
		{"empty namespace is ErrInvalid", testFinalVersionEmptyNamespace},
	}
}

// readFinal returns the final version of unitID in ns, and fails the test on
// an error.
func readFinal(t *testing.T, r job.FinalVersionReader, ns namespace.Name, unitID string) (job.FinalVersion, bool) {
	t.Helper()

	got, found, err := r.FinalVersion(t.Context(), ns, unitID)
	if err != nil {
		t.Fatalf("FinalVersion: %v", err)
	}

	return got, found
}

func testFinalVersionNone(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)

	if _, found := readFinal(t, r, "acme", f.unit.UnitID()); found {
		t.Fatal("FinalVersion found a version, want none")
	}
}

func testFinalVersionAfterFinal(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)

	version := writeState(t, h, f, true)

	got, found := readFinal(t, r, "acme", f.unit.UnitID())
	if !found {
		t.Fatalf("FinalVersion found no version, want %s", version)
	}

	if got.VersionID != version {
		t.Fatalf("VersionID = %q, want %q", got.VersionID, version)
	}

	if got.UnitID != f.unit.UnitID() {
		t.Fatalf("UnitID = %q, want %q", got.UnitID, f.unit.UnitID())
	}
}

func testFinalVersionIntermediate(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)

	first := writeState(t, h, f, true)
	writeState(t, h, f, false)

	got, found := readFinal(t, r, "acme", f.unit.UnitID())
	if !found || got.VersionID != first {
		t.Fatalf("FinalVersion = %q (found %t), want the final write %s", got.VersionID, found, first)
	}
}

func testFinalVersionReplaces(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)

	writeState(t, h, f, true)
	second := writeState(t, h, f, true)

	got, found := readFinal(t, r, "acme", f.unit.UnitID())
	if !found || got.VersionID != second {
		t.Fatalf("FinalVersion = %q (found %t), want the newer final write %s", got.VersionID, found, second)
	}
}

func testFinalVersionRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)

	version := writeState(t, h, f, true)

	got, found := readFinal(t, r, "acme", f.unit.UnitID())
	if !found {
		t.Fatalf("FinalVersion found no version, want %s", version)
	}

	if got.Commit != "" {
		t.Fatalf("Commit = %q, want the empty commit of a write with no token", got.Commit)
	}

	if got.Job != "" {
		t.Fatalf("Job = %q, want the empty job of a write with no token", got.Job)
	}

	if got.At.IsZero() {
		t.Fatal("At is zero, want the time of the write")
	}
}

func testFinalVersionPerNamespace(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)

	writeState(t, h, f, true)

	if _, err := h.Domain.CreateNamespace(t.Context(), "beta"); err != nil {
		t.Fatalf("CreateNamespace beta: %v", err)
	}

	if _, found := readFinal(t, r, "beta", f.unit.UnitID()); found {
		t.Fatal("FinalVersion in beta found a version of acme, want none")
	}
}

func testFinalVersionEmptyUnit(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	_ = setUp(t, h)

	_, _, err := r.FinalVersion(t.Context(), "acme", "")
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("FinalVersion with an empty unit ID: %v, want ErrInvalid", err)
	}
}

func testFinalVersionEmptyNamespace(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)

	_, _, err := r.FinalVersion(t.Context(), "", f.unit.UnitID())
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("FinalVersion with an empty namespace: %v, want ErrInvalid", err)
	}
}
