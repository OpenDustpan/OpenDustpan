package protocol_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestNewClaimLeaseRejectsHostileInput proves that the constructor of the
// claim lease refuses an empty holder, a stale fence and a non-positive
// expiry.
func TestNewClaimLeaseRejectsHostileInput(t *testing.T) {
	t.Parallel()

	fresh := namespace.Fence{Namespace: namespace.Name("acme"), FailoverVersion: 1}
	stale := namespace.Fence{Namespace: namespace.Name("acme"), FailoverVersion: 0}
	now := time.UnixMilli(1_700_000_000_000).UTC()
	expiry := now.Add(time.Duration(protocol.ClaimLease) * time.Millisecond)

	if err := protocol.NewClaimLeaseForTest("rn-1", fresh, fresh, expiry); err != nil {
		t.Fatalf("newClaimLease with valid input: %v", err)
	}

	if err := protocol.NewClaimLeaseForTest("", fresh, fresh, expiry); err == nil {
		t.Fatal("newClaimLease with an empty holder accepted the lease")
	}

	if err := protocol.NewClaimLeaseForTest("rn-1", stale, fresh, expiry); err == nil {
		t.Fatal("newClaimLease with a stale fence accepted the lease")
	}

	if err := protocol.NewClaimLeaseForTest("rn-1", fresh, fresh, time.Time{}); err == nil {
		t.Fatal("newClaimLease with a non-positive expiry accepted the lease")
	}
}
