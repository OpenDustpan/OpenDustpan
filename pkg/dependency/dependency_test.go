package dependency_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dependency"
	"opendustpan.org/opendustpan/pkg/history"
)

func TestBlocks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	window := dependency.DefaultWindow
	dep := history.Unit{Namespace: "acme", Tool: "pulumi", ID: "stk-app"}

	cases := []struct {
		name     string
		evidence dependency.Evidence
		age      time.Duration
		want     bool
	}{
		{name: "a recent job edge", evidence: dependency.EvidenceJob, age: time.Hour, want: true},
		{name: "a recent lock edge", evidence: dependency.EvidenceLock, age: time.Hour, want: true},
		{name: "a recent declared edge", evidence: dependency.EvidenceDeclared, age: time.Hour, want: true},
		{
			name:     "a lock edge just inside the window",
			evidence: dependency.EvidenceLock,
			age:      window - time.Millisecond,
			want:     true,
		},
		{name: "a lock edge at the end of the window", evidence: dependency.EvidenceLock, age: window, want: false},
		{name: "a recent principal edge", evidence: dependency.EvidencePrincipal, age: time.Hour, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := dependency.Edge{
				FirstSeen: now.Add(-tc.age), LastSeen: now.Add(-tc.age), Dependent: &dep,
				Source: history.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-net"}, Principal: "ci",
				Kind: dependency.KindOutputRead, Evidence: tc.evidence, Reads: 1, Ambiguous: false,
			}

			if got := dependency.Blocks(e, now, window); got != tc.want {
				t.Errorf("Blocks = %v, want %v", got, tc.want)
			}

			if got := len(dependency.Blocking([]dependency.Edge{e}, now, window)) == 1; got != tc.want {
				t.Errorf("Blocking holds the edge = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDue(t *testing.T) {
	t.Parallel()

	seen := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	stored := dependency.Edge{
		FirstSeen: seen,
		LastSeen:  seen,
		Dependent: nil,
		Source:    history.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-net"},
		Principal: "ana",
		Kind:      dependency.KindOutputRead,
		Evidence:  dependency.EvidencePrincipal,
		Reads:     1,
		Ambiguous: false,
	}

	if dependency.Due(stored, seen.Add(dependency.RecordInterval-time.Second)) {
		t.Error("a read inside the interval is due")
	}

	if !dependency.Due(stored, seen.Add(dependency.RecordInterval)) {
		t.Error("a read at the end of the interval is not due")
	}
}
