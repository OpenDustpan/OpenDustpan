package refusal_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// webProd is the refused Pulumi stack of the tests.
func webProd() collection.UnitRef {
	return collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"}
}

// TestLockedArguments fails when the answer of a manual lock holds the reason
// of the lock, or lacks the holder, the time, the locked object or the refused
// unit, or when an argument has another form than namespace or
// namespace/name with the word of its tool.
func TestLockedArguments(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	network := collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "network"}

	tests := []struct {
		name        string
		target      manuallock.Target
		refused     collection.UnitRef
		wantKind    string
		wantLocked  string
		wantRefKind string
		wantRefused string
	}{
		{
			name: "namespace", target: manuallock.NamespaceTarget("acme"), refused: webProd(),
			wantKind: "namespace", wantLocked: "acme", wantRefKind: "stack", wantRefused: "acme/web/prod",
		},
		{
			name:   "collection",
			target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "prod"}), refused: network,
			wantKind: "collection", wantLocked: "acme/prod", wantRefKind: "workspace", wantRefused: "acme/network",
		},
		{
			name: "stack", target: manuallock.UnitTarget(webProd()), refused: webProd(),
			wantKind: "stack", wantLocked: "acme/web/prod", wantRefKind: "stack", wantRefused: "acme/web/prod",
		},
		{
			name: "workspace", target: manuallock.UnitTarget(network), refused: network,
			wantKind: "workspace", wantLocked: "acme/network", wantRefKind: "workspace", wantRefused: "acme/network",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := &manuallock.LockedError{
				Lock: manuallock.Lock{Since: since, Target: tc.target, Holder: "user-1", Reason: "secret-reason-text"},
				Unit: tc.refused, Noun: "stack", Name: "free text that the answer never reads",
			}

			a, err := refusal.Locked(e)
			if err != nil {
				t.Fatalf("Locked: %v", err)
			}

			want := map[string]any{
				"holder": "user-1", "since": "2026-10-23T09:30:00Z", "lockedKind": tc.wantKind,
				"locked": tc.wantLocked, "refusedKind": tc.wantRefKind, "refused": tc.wantRefused,
			}

			if a.Code() != errcode.LockLocked || len(a.Args()) != len(want) {
				t.Fatalf("answer = %q %v", a.Code(), a.Args())
			}

			for k, v := range want {
				if a.Args()[k] != v {
					t.Errorf("arg %s = %v, want %v", k, a.Args()[k], v)
				}
			}
		})
	}
}

// TestLockedRefusesAnUnknownUnit fails when a refusal with a refused unit or a
// locked unit that has no namespace, no name or a tool with no word gives an
// answer other than server.internal with an error.
func TestLockedRefusesAnUnknownUnit(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	nsLock := manuallock.NamespaceTarget("acme")

	tests := []struct {
		name    string
		target  manuallock.Target
		refused collection.UnitRef
	}{
		{name: "no refused unit", target: nsLock, refused: collection.UnitRef{Namespace: "", Tool: "", Name: ""}},
		{
			name: "refused unit with no namespace", target: nsLock,
			refused: collection.UnitRef{Namespace: "", Tool: "pulumi", Name: "web/prod"},
		},
		{
			name: "refused unit with no name", target: nsLock,
			refused: collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: ""},
		},
		{
			name: "refused unit of an unknown tool", target: nsLock,
			refused: collection.UnitRef{Namespace: "acme", Tool: "terraform", Name: "web"},
		},
		{
			name:    "locked unit of an unknown tool",
			target:  manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "Pulumi", Name: "web/prod"}),
			refused: webProd(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := &manuallock.LockedError{
				Lock: manuallock.Lock{Since: since, Target: tc.target, Holder: "user-1", Reason: ""},
				Unit: tc.refused, Noun: "", Name: "",
			}

			a, err := refusal.Locked(e)
			if err == nil {
				t.Fatalf("Locked = %q %v, want an error", a.Code(), a.Args())
			}

			if a.Code() != errcode.ServerInternal {
				t.Fatalf("answer code = %q, want %q", a.Code(), errcode.ServerInternal)
			}
		})
	}
}

// TestLockedRejectsAnEmptyHolder fails when a lock with no holder gives an
// answer, or when the refusal gives an answer with no code: the answer is
// then server.internal, so an adapter that ignores the error still sends a
// code.
func TestLockedRejectsAnEmptyHolder(t *testing.T) {
	t.Parallel()

	lock := manuallock.Lock{
		Since: time.Now(), Target: manuallock.NamespaceTarget("acme"), Holder: "", Reason: "",
	}

	a, err := refusal.Locked(&manuallock.LockedError{Lock: lock, Unit: webProd(), Noun: "", Name: ""})
	if err == nil {
		t.Fatal("Locked accepted an empty holder")
	}

	if a.Code() != errcode.ServerInternal {
		t.Fatalf("answer code = %q, want %q", a.Code(), errcode.ServerInternal)
	}
}
