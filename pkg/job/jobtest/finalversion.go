package jobtest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// finalAtMillis is the time of the state versions of the finalVersion cases.
const finalAtMillis = 1_800_000_000_000

// finalVersionCases are the cases of job.FinalVersionReader.
func finalVersionCases() []suiteCase {
	return []suiteCase{
		{"final version of a unit with no final version is false", testFinalVersionNone},
		{"final version after a final observe", testFinalVersionAfterFinal},
		{"an intermediate version is not recorded", testFinalVersionIntermediate},
		{"a newer final version replaces the older", testFinalVersionReplaces},
		{"commit, job and time round trip", testFinalVersionRoundTrip},
		{"the answer is per namespace", testFinalVersionPerNamespace},
		{"empty unit ID is ErrInvalid", testFinalVersionEmptyUnit},
		{"empty namespace is ErrInvalid", testFinalVersionEmptyNamespace},
	}
}

// observe records v with the fence of the namespace, and fails the test on an
// error.
func observe(t *testing.T, st job.Store, fence namespace.Fence, v job.StateVersion) {
	t.Helper()

	if _, err := st.ObserveStateVersion(t.Context(), v, fence); err != nil {
		t.Fatalf("ObserveStateVersion %s: %v", v.VersionID, err)
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

	observe(t, h.Jobs, f.fence, version(f.unit, svOne, time.UnixMilli(finalAtMillis).UTC(), true))

	got, found := readFinal(t, r, "acme", f.unit.UnitID())
	if !found {
		t.Fatalf("FinalVersion found no version, want %s", svOne)
	}

	if got.VersionID != svOne {
		t.Fatalf("VersionID = %q, want %q", got.VersionID, svOne)
	}

	if got.UnitID != f.unit.UnitID() {
		t.Fatalf("UnitID = %q, want %q", got.UnitID, f.unit.UnitID())
	}
}

func testFinalVersionIntermediate(t *testing.T, h Harness) {
	t.Helper()

	checkLastAfterPair(t, h, false, svOne)
}

func testFinalVersionReplaces(t *testing.T, h Harness) {
	t.Helper()

	checkLastAfterPair(t, h, true, svTwo)
}

// checkLastAfterPair observes svOne as final, then svTwo with the finality
// secondFinal. It checks that the last final version of the unit is want.
func checkLastAfterPair(t *testing.T, h Harness, secondFinal bool, want string) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)
	at := time.UnixMilli(finalAtMillis).UTC()

	observe(t, h.Jobs, f.fence, version(f.unit, svOne, at, true))
	observe(t, h.Jobs, f.fence, version(f.unit, svTwo, at, secondFinal))

	got, found := readFinal(t, r, "acme", f.unit.UnitID())
	if !found || got.VersionID != want {
		t.Fatalf("FinalVersion = %q (found %t), want %q", got.VersionID, found, want)
	}
}

func testFinalVersionRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)
	want := time.UnixMilli(finalAtMillis).UTC()

	observe(t, h.Jobs, f.fence, job.StateVersion{
		Unit:      f.unit,
		VersionID: svOne,
		Commit:    "c0ffee",
		Job:       "job-7",
		At:        want,
		Final:     true,
	})

	got, found := readFinal(t, r, "acme", f.unit.UnitID())
	if !found {
		t.Fatalf("FinalVersion found no version, want %s", svOne)
	}

	if got.Commit != "c0ffee" {
		t.Fatalf("Commit = %q, want %q", got.Commit, "c0ffee")
	}

	if got.Job != "job-7" {
		t.Fatalf("Job = %q, want %q", got.Job, "job-7")
	}

	if !got.At.Equal(want) {
		t.Fatalf("At = %v, want %v", got.At, want)
	}
}

func testFinalVersionPerNamespace(t *testing.T, h Harness) {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	f := setUp(t, h)

	observe(t, h.Jobs, f.fence, version(f.unit, svOne, time.UnixMilli(finalAtMillis).UTC(), true))

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
