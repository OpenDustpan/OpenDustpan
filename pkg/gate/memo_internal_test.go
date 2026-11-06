package gate

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// memoStore serves one configuration and counts its reads.
type memoStore struct {
	Store

	reads int
}

func (m *memoStore) Snapshot(_ context.Context, scopes []Scope) (Config, error) {
	m.reads++

	gates := make([]Gate, 0, len(scopes))
	for _, sc := range scopes {
		gates = append(gates, Gate{
			Scope: sc, ID: "g", Version: 1, DecisionPoint: PointStateWrite, Condition: "", Unless: "",
			Outcome: OutcomeWarn, Message: "", Mode: ModeEnforce, OnError: OnErrorFail, Final: false,
			CheckedWith: CheckedWith{Engine: "", Schema: SchemaVersion{Subject: "", Major: 0, Minor: 0}},
			State:       "", Reads: nil,
		})
	}

	return Config{
		Gates: gates, AccessLimits: nil, ApprovalRules: nil, OwnershipRules: nil, PathSets: nil,
		ApprovalDefaults: nil, CarryOver: nil,
	}, nil
}

// memoChain returns the chain of the unit web, with the stable ID id when id
// is not empty.
func memoChain(t *testing.T, id string) []Scope {
	t.Helper()

	unit, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	if id != "" {
		unit = unit.WithUnitID(id)
	}

	chain, err := UnitChain(unit, "")
	if err != nil {
		t.Fatal(err)
	}

	return chain
}

// TestSnapshotMemo checks the memo of the configuration of one call: a second
// read of the same scopes comes from the memo, a change of a returned list
// does not change the next read, a unit scope with another stable ID is
// another key, and a context with no memo reads the store each time.
func TestSnapshotMemo(t *testing.T) {
	t.Parallel()

	chain, byID := memoChain(t, ""), memoChain(t, "ws-1")
	st := &memoStore{Store: nil, reads: 0}
	s := &Service{
		store: st, engine: nil, producers: nil, units: nil, subjects: nil, fences: nil, now: nil, log: nil,
	}
	ctx := withFactMemo(t.Context())

	first, err := s.snapshot(ctx, chain)
	if err != nil {
		t.Fatal(err)
	}

	first.Gates[0].Outcome = OutcomeDeny

	second, err := s.snapshot(ctx, chain)
	if err != nil || st.reads != 1 {
		t.Fatalf("second read = %v after %d store reads, want 1 store read", err, st.reads)
	}

	if second.Gates[0].Outcome != OutcomeWarn {
		t.Fatalf("second read outcome = %s, want warn: a change of a returned list leaks", second.Gates[0].Outcome)
	}

	if _, err = s.snapshot(ctx, byID); err != nil || st.reads != 2 {
		t.Fatalf("read with a stable ID = %v after %d store reads, want 2: the ID is part of the key", err, st.reads)
	}

	for range 2 {
		if _, err = s.snapshot(t.Context(), chain); err != nil {
			t.Fatal(err)
		}
	}

	if st.reads != 4 {
		t.Fatalf("store reads = %d, want 4: a context with no memo reads the store each time", st.reads)
	}
}
