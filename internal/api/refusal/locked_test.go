package refusal_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// TestLockedArguments fails when the answer of a manual lock holds the reason
// of the lock, or lacks the holder, the time or the locked object.
func TestLockedArguments(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)

	tests := []struct {
		name       string
		target     manuallock.Target
		wantTarget string
		wantKind   string
	}{
		{"namespace", manuallock.NamespaceTarget("acme"), "acme", "namespace"},
		{
			"collection", manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "prod"}),
			"acme/prod", "collection",
		},
		{
			"unit", manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"}),
			"pulumi:acme/web/prod", "unit",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := &manuallock.LockedError{
				Lock: manuallock.Lock{Since: since, Target: tc.target, Holder: "user-1", Reason: "secret-reason-text"},
				Noun: "stack", Name: "acme/web/prod",
			}

			a, err := refusal.Locked(e)
			if err != nil {
				t.Fatalf("Locked: %v", err)
			}

			want := map[string]any{
				"holder": "user-1", "since": "2026-10-23T09:30:00Z", "target": tc.wantTarget, "targetKind": tc.wantKind,
				"unit": "acme/web/prod",
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

// TestLockedRejectsAnEmptyHolder fails when a lock with no holder gives an
// answer: the adapter then answers server.internal.
func TestLockedRejectsAnEmptyHolder(t *testing.T) {
	t.Parallel()

	lock := manuallock.Lock{
		Since: time.Now(), Target: manuallock.NamespaceTarget("acme"), Holder: "", Reason: "",
	}

	if _, err := refusal.Locked(&manuallock.LockedError{Lock: lock, Noun: "", Name: ""}); err == nil {
		t.Fatal("Locked accepted an empty holder")
	}
}
