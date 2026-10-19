// Package unitimporttest holds the conformance suites of the ports of
// unitimport. RunStore checks a Store and a UnitStore, and RunSource checks a
// Source.
package unitimporttest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/unitimport"
)

// Harness is one fresh, empty store for one subtest.
//
// Namespace makes the namespace name in the store and returns its fence.
// The Tool port writes the ledger rows and the target of an import unit, in
// the transactions of its own writes, so the suite cannot reach them
// through the two ports. SeedLedger stores one ledger row in the namespace
// ns, and SetTarget records the target of an import unit, as a Tool does.
// Audited returns the audit entries of the namespace ns that have the
// action prefix "import:", oldest first.
type Harness struct {
	Store      unitimport.Store
	Units      unitimport.UnitStore
	Namespace  func(t *testing.T, name string) namespace.Fence
	SeedLedger func(t *testing.T, ns namespace.Name, row unitimport.LedgerRow)
	SetTarget  func(t *testing.T, ns namespace.Name, key unitimport.UnitKey, target history.Unit)
	Audited    func(t *testing.T, ns namespace.Name) []audit.Entry
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

// at returns the time of the fixtures.
func at() time.Time {
	return time.UnixMilli(1_800_000_000_000).UTC()
}

// entry returns the audit entry of a write in the namespace ns.
func entry(ns namespace.Name, action string) audit.Entry {
	return entryAt(ns, action, at())
}

// entryAt returns the audit entry of a write in the namespace ns at time
// when.
func entryAt(ns namespace.Name, action string, when time.Time) audit.Entry {
	return audit.Entry{
		At:           when,
		Namespace:    ns,
		Subject:      "user:ada",
		Action:       action,
		IDPSubject:   "",
		TokenID:      "",
		Object:       ns.String(),
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

// emptyPlan returns a plan of the given count of units and nothing else.
func emptyPlan(units int) unitimport.Plan {
	return unitimport.Plan{
		At:           time.Time{},
		Units:        units,
		Versions:     0,
		Bytes:        0,
		Capabilities: nil,
		Findings:     nil,
		Requests:     nil,
		Reservations: nil,
	}
}

// newImport returns a valid new import of the source kind s3 in ns.
func newImport(ns namespace.Name, id string) unitimport.Import {
	return unitimport.Import{
		ID:          id,
		Namespace:   ns,
		Kind:        unitimport.SourceS3,
		Address:     "s3://bucket/prefix",
		Selection:   unitimport.Selection{Names: []string{"web", "db"}},
		Credential:  unitimport.CredentialProfile,
		Profile:     "aws-read",
		ProfilePath: "login.s3",
		Options: unitimport.Options{
			HistoryLimit: 20,
			Since:        at().Add(-24 * time.Hour),
			LockSource:   true,
		},
		State:     unitimport.StatePlanning,
		Reason:    "",
		CreatedBy: "user:ada",
		CreatedAt: at(),
		Plan:      emptyPlan(0),
		EndedAt:   time.Time{},
	}
}

// newUploadImport returns a valid new import of the source kind upload.
func newUploadImport(ns namespace.Name, id string) unitimport.Import {
	imp := newImport(ns, id)
	imp.Kind, imp.Address, imp.Credential, imp.Profile, imp.ProfilePath =
		unitimport.SourceUpload, "", unitimport.CredentialNone, "", ""

	return imp
}

// newUnit returns a valid new import unit with the ID id.
func newUnit(id string) unitimport.ImportUnit {
	return unitimport.ImportUnit{
		ID:         id,
		ImportID:   "",
		Source:     unitimport.SourceUnit{Tool: "opentofu", Name: "acme/" + id},
		TargetName: "t-" + id,
		Target:     history.Unit{Namespace: "", Tool: "", ID: ""},
		State:      unitimport.UnitPlanned,
		ErrorCode:  "",
		Versions:   3,
		Committed:  0,
		Bytes:      1024,
		Encryption: "client",
		Findings: []unitimport.Finding{{
			Code: "secrets.unsupported", Severity: unitimport.SeverityWarning,
			Message: "The source holds a secret that the import cannot read.", Help: "Set the secret again.",
		}},
	}
}

// ledgerRow returns the ledger row of the version v of the unit unitID of
// the import imp-1, committed at when.
func ledgerRow(unitID, v string, when time.Time) unitimport.LedgerRow {
	return unitimport.LedgerRow{
		ImportID:     "imp-1",
		ImportUnitID: unitID,
		Version:      unitimport.VersionID(v),
		SourceSHA256: "src-" + v,
		StoredSHA256: "dst-" + v,
		Item: history.ItemRef{
			Unit: history.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-" + unitID},
			Kind: secret.ItemKindState,
			Item: "item-" + v,
		},
		CommittedAt: when,
	}
}

// setup makes the namespace acme and one import in it. It returns the
// fence.
func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence := h.Namespace(t, "acme")

	_, err := h.Store.Create(
		t.Context(),
		fence,
		newImport("acme", "imp-1"),
		entry("acme", "import:run"),
	)
	if err != nil {
		t.Fatalf("create import: %v", err)
	}

	return fence
}

// putUnits stores the units in the import imp-1.
func putUnits(t *testing.T, h Harness, fence namespace.Fence, units ...unitimport.ImportUnit) {
	t.Helper()

	if err := h.Units.PutUnits(t.Context(), fence, "imp-1", units, entry("acme", "import:run")); err != nil {
		t.Fatalf("put units: %v", err)
	}
}

// walk moves the import id through the states in order. The first state is
// the state that the import is in.
func walk(t *testing.T, h Harness, fence namespace.Fence, id string, states ...unitimport.State) {
	t.Helper()

	for n := 1; n < len(states); n++ {
		m := unitimport.Move{From: states[n-1], To: states[n], Reason: ""}

		if err := h.Store.Transition(t.Context(), fence, id, m, entry(fence.Namespace, "import:run")); err != nil {
			t.Fatalf("transition %s to %s: %v", m.From, m.To, err)
		}
	}
}

// unitKey names the unit unitID of the import imp-1.
func unitKey(unitID string) unitimport.UnitKey {
	return unitimport.UnitKey{ImportID: "imp-1", UnitID: unitID}
}

// mustGet returns the import id of acme.
func mustGet(t *testing.T, h Harness, id string) unitimport.Import {
	t.Helper()

	imp, err := h.Store.Get(t.Context(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}

	return imp
}

// mustUnits returns the units of the import imp-1.
func mustUnits(t *testing.T, h Harness) []unitimport.ImportUnit {
	t.Helper()

	units, err := h.Units.Units(t.Context(), "acme", "imp-1")
	if err != nil {
		t.Fatalf("units: %v", err)
	}

	return units
}

// mustUnit returns the unit unitID of the import imp-1.
func mustUnit(t *testing.T, h Harness, unitID string) unitimport.ImportUnit {
	t.Helper()

	u, err := h.Units.Unit(t.Context(), "acme", "imp-1", unitID)
	if err != nil {
		t.Fatalf("unit %s: %v", unitID, err)
	}

	return u
}

func mustIs(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: error = %v, want %v", what, err, want)
	}
}
