package outputsharetest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// latestStateCases are the cases of the latest state and of the state that
// the client encrypted, for a read by a principal.
func latestStateCases() []suiteCase {
	return []suiteCase{
		{testLatestReads, "latest: a request with the ID of the latest state reads"},
		{testLatestEmpty, "latest: a request with no state ID is denied by the latest-state check"},
		{testLatestOlder, "latest: a request with the ID of an older state is denied by the latest-state check"},
		{
			testLatestOtherUnit,
			"latest: a request with the ID of a state of another unit is denied by the latest-state check",
		},
		{testLatestStale, "latest: a new state between two reads makes the old ID stale"},
		{
			testEncryptedGrantFirst,
			"client-encrypted: a principal without the grant is denied by the grant check before the client-encrypted check",
		},
		{
			testEncryptedWithRead,
			"client-encrypted: a principal with unit:read and unit:read-outputs reads a client-encrypted state as before",
		},
		{
			testEncryptedOutputsOnly,
			"client-encrypted: a principal with unit:read-outputs and not unit:read is refused with ErrClientEncrypted",
		},
		{testEncryptedThenPlain, "client-encrypted: a plain state after a client-encrypted state reads again"},
	}
}

// testLatestReads proves that the ID of the latest state reads.
func testLatestReads(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, false)
		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		dec, err := readOutputs(t, h, unit, id, false)
		wantAllowed(t, dec, err)
	})
}

// testLatestEmpty proves that a request with no state ID is denied, also for a
// unit with no state.
func testLatestEmpty(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		_, err := readOutputs(t, h, unit, "", false)
		wantDenied(t, err, outputshare.CheckLatest, identity.ErrDenied)

		h.SaveState(t, unit, false)

		_, err = readOutputs(t, h, unit, "", false)
		wantDenied(t, err, outputshare.CheckLatest, identity.ErrDenied)
	})
}

// testLatestOlder proves that the ID of an older state is denied.
func testLatestOlder(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		older := h.SaveState(t, unit, false)
		latest := h.SaveState(t, unit, false)
		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		_, err := readOutputs(t, h, unit, older, false)
		wantDenied(t, err, outputshare.CheckLatest, identity.ErrDenied)

		dec, err := readOutputs(t, h, unit, latest, false)
		wantAllowed(t, dec, err)
	})
}

// testLatestOtherUnit proves that the ID of the latest state of another unit
// is denied.
func testLatestOtherUnit(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, tool string, unit outputshare.Placement) {
		t.Helper()

		other := h.Unit(t, tool, acme, "staging", "")
		otherID := h.SaveState(t, other, false)
		h.SaveState(t, unit, false)
		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		_, err := readOutputs(t, h, unit, otherID, false)
		wantDenied(t, err, outputshare.CheckLatest, identity.ErrDenied)
	})
}

// testLatestStale proves that a state saved between two reads makes the ID of
// the first read stale.
func testLatestStale(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		first := h.SaveState(t, unit, false)
		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		dec, err := readOutputs(t, h, unit, first, false)
		wantAllowed(t, dec, err)

		second := h.SaveState(t, unit, false)

		_, err = readOutputs(t, h, unit, first, false)
		wantDenied(t, err, outputshare.CheckLatest, identity.ErrDenied)

		dec, err = readOutputs(t, h, unit, second, false)
		wantAllowed(t, dec, err)
	})
}

// openTofu runs fn for a unit of OpenTofu, the one tool whose client encrypts
// the state.
func openTofu(t *testing.T, h Harness, fn func(t *testing.T, unit outputshare.Placement)) {
	t.Helper()

	h.Namespace(t, acme.String())

	fn(t, h.Unit(t, toolOpenTofu, acme, "prod", ""))
}

// testEncryptedGrantFirst proves that a principal with no grant gets the
// grant check and never learns that the state is encrypted.
func testEncryptedGrantFirst(t *testing.T, h Harness) {
	t.Helper()

	openTofu(t, h, func(t *testing.T, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, true)

		_, err := readOutputs(t, h, unit, id, false)
		wantDenied(t, err, outputshare.CheckGrant, identity.ErrDenied)
	})
}

// testEncryptedWithRead proves that a principal that holds unit:read reads a
// state that the client encrypted as before: it is not a caller of outputs
// only.
func testEncryptedWithRead(t *testing.T, h Harness) {
	t.Helper()

	openTofu(t, h, func(t *testing.T, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, true)
		grantReader(t, h, unit, identity.ActionUnitRead, identity.ActionUnitReadOutputs)

		dec, err := readOutputs(t, h, unit, id, false)
		wantAllowed(t, dec, err)
	})
}

// testEncryptedOutputsOnly proves that a principal that reads outputs only is
// refused with ErrClientEncrypted and not with identity.ErrDenied, and that
// the refusal writes an audit entry.
func testEncryptedOutputsOnly(t *testing.T, h Harness) {
	t.Helper()

	openTofu(t, h, func(t *testing.T, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, true)
		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		var before int
		if h.Audited != nil {
			before = len(h.Audited(t, acme.String()))
		}

		_, err := readOutputs(t, h, unit, id, false)
		wantDenied(t, err, outputshare.CheckClientEncrypted, outputshare.ErrClientEncrypted)

		if errors.Is(err, identity.ErrDenied) {
			t.Error("the refusal unwraps to identity.ErrDenied, want ErrClientEncrypted only")
		}

		if h.Audited != nil {
			if after := len(h.Audited(t, acme.String())); after != before+1 {
				t.Errorf("audit entries = %d, want %d", after, before+1)
			}
		}
	})
}

// testEncryptedThenPlain proves that the next plain state reads again.
func testEncryptedThenPlain(t *testing.T, h Harness) {
	t.Helper()

	openTofu(t, h, func(t *testing.T, unit outputshare.Placement) {
		t.Helper()

		encrypted := h.SaveState(t, unit, true)
		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		_, err := readOutputs(t, h, unit, encrypted, false)
		wantDenied(t, err, outputshare.CheckClientEncrypted, outputshare.ErrClientEncrypted)

		plain := h.SaveState(t, unit, false)

		dec, err := readOutputs(t, h, unit, plain, false)
		wantAllowed(t, dec, err)
	})
}

// statesCases are the cases of the States port.
func statesCases() []suiteCase {
	return []suiteCase{
		{testStatesLatest, "states: latest answers the ID of the latest state of a unit of each tool"},
		{testStatesNewState, "states: latest after a new state answers the new ID"},
		{testStatesNoState, "states: latest of a unit with no state answers an empty ID"},
		{testStatesEncrypted, "states: latest of a state that the client encrypted is client-encrypted"},
		{testStatesUnknown, "states: latest of an unknown unit answers ErrUnknownProducer"},
		{
			testStatesNoStableID,
			"states: latest of a reference with no stable ID answers ErrUnknownProducer after the same lookup",
		},
		{testStatesOtherID, "states: latest of a stable ID that names another unit answers ErrUnknownProducer"},
		{testStatesNotAUnit, "states: latest of an object that is not a unit answers ErrInvalid"},
	}
}

// latestOf returns the latest state of unit, or fails the test.
func latestOf(t *testing.T, h Harness, unit identity.Object) outputshare.State {
	t.Helper()

	state, err := h.States.Latest(t.Context(), unit)
	if err != nil {
		t.Fatalf("Latest(%q) = %v", unit, err)
	}

	return state
}

// testStatesLatest proves that the port answers the ID that the save of the
// state answered.
func testStatesLatest(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, false)

		if got := latestOf(t, h, unit.Unit); got.ID != id || got.ClientEncrypted {
			t.Errorf("Latest = %+v, want ID %q and a state the server reads", got, id)
		}
	})
}

// testStatesNewState proves that a new state moves the ID.
func testStatesNewState(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		first := h.SaveState(t, unit, false)
		second := h.SaveState(t, unit, false)

		if first == second {
			t.Fatalf("two saves gave the same state ID %q", first)
		}

		if got := latestOf(t, h, unit.Unit); got.ID != second {
			t.Errorf("Latest.ID = %q, want %q", got.ID, second)
		}
	})
}

// testStatesNoState proves that a unit with no state has an empty ID and no
// error.
func testStatesNoState(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		if got := latestOf(t, h, unit.Unit); got.ID != "" || got.ClientEncrypted {
			t.Errorf("Latest = %+v, want the zero state", got)
		}
	})
}

// testStatesEncrypted proves that the port tells a state that the client
// encrypted.
func testStatesEncrypted(t *testing.T, h Harness) {
	t.Helper()

	openTofu(t, h, func(t *testing.T, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, true)

		if got := latestOf(t, h, unit.Unit); got.ID != id || !got.ClientEncrypted {
			t.Errorf("Latest = %+v, want ID %q and a client-encrypted state", got, id)
		}
	})
}

// testStatesUnknown proves that a unit that does not exist is an unknown
// producer.
func testStatesUnknown(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, tool string, unit outputshare.Placement) {
		t.Helper()

		name := "ghost"
		if tool == toolPulumi {
			name = "web/ghost"
		}

		ghost, err := identity.UnitObject(tool, acme, name)
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		objects := []identity.Object{ghost, ghost.WithUnitID(unit.Unit.UnitID())}

		for i := range objects {
			object := objects[i]

			if _, err := h.States.Latest(t.Context(), object); !errors.Is(err, outputshare.ErrUnknownProducer) {
				t.Errorf("Latest(%q) = %v, want ErrUnknownProducer", object, err)
			}
		}
	})
}

// testStatesNoStableID proves that a reference with no stable ID is an
// unknown producer, also when a unit has that reference.
func testStatesNoStableID(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, tool string, unit outputshare.Placement) {
		t.Helper()

		h.SaveState(t, unit, false)

		bare, err := identity.UnitObject(tool, acme, unit.Unit.UnitName())
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		if _, err := h.States.Latest(t.Context(), bare); !errors.Is(err, outputshare.ErrUnknownProducer) {
			t.Errorf("Latest(%q) = %v, want ErrUnknownProducer", bare, err)
		}
	})
}

// testStatesOtherID proves that a stable ID of another unit is an unknown
// producer, so that a reference cannot borrow the ID of another unit.
func testStatesOtherID(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, tool string, unit outputshare.Placement) {
		t.Helper()

		other := h.Unit(t, tool, acme, "staging", "")

		mixed := unit.Unit.WithUnitID(other.Unit.UnitID())
		if _, err := h.States.Latest(t.Context(), mixed); !errors.Is(err, outputshare.ErrUnknownProducer) {
			t.Errorf("Latest(%q) = %v, want ErrUnknownProducer", mixed, err)
		}
	})
}

// testStatesNotAUnit proves that an object that is not a unit is refused.
func testStatesNotAUnit(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme.String())

	objects := []identity.Object{
		identity.NamespaceObject(acme), identity.CollectionObject(acme, "apps"), identity.ServerObject(), {},
	}

	for i := range objects {
		object := objects[i]

		if _, err := h.States.Latest(t.Context(), object); !errors.Is(err, outputshare.ErrInvalid) {
			t.Errorf("Latest(%q) = %v, want ErrInvalid", object, err)
		}
	}
}
