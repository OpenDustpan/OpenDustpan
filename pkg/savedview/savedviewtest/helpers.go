package savedviewtest

import (
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// Owners used by the cases. The owner of a view is the ID of a principal,
// never its subject.
const (
	ownerA = "prn-aaaaaaaaaaaaaaaaaaaa"
	ownerB = "prn-bbbbbbbbbbbbbbbbbbbb"
	ownerC = "prn-cccccccccccccccccccc"
)

// ownerN returns the ID of the principal number n.
func ownerN(n int) string {
	return fmt.Sprintf("prn-n%019d", n)
}

// testRef is a valid unit reference in the namespace acme.
const testRef = "pulumi:acme/web/prod"

const (
	// testTimeMs is the creation time of the views of the cases, in Unix
	// milliseconds.
	testTimeMs = 1_700_000_000_000
	// labelKeyTeam, labelValuePayments and subjectAlice are the values that the
	// cases use for a label and for the subject of the audit entries.
	labelKeyTeam       = "team"
	labelValuePayments = "payments"
	subjectAlice       = "alice"
)

// newView returns a valid view of owner in ns with a new ID and a query with
// one label.
func newView(ns namespace.Name, owner, name string) savedview.View {
	return savedview.View{
		CreatedAt: time.UnixMilli(testTimeMs).UTC(),
		ID:        savedview.NewID(),
		Namespace: ns,
		Name:      name,
		Owner:     owner,
		Query: savedview.Query{
			Labels:     []savedview.Label{{Key: labelKeyTeam, Value: labelValuePayments}},
			References: nil,
		},
	}
}

// mustCreate stores v with a fence read from the harness.
func mustCreate(t *testing.T, h Harness, v savedview.View) {
	t.Helper()

	fence := h.Namespace(t, v.Namespace.String())

	err := h.Store.Create(t.Context(), v, fence, auditEntry(v.Namespace, "create"))
	if err != nil {
		t.Fatalf("create %q for %s: %v", v.Name, v.Owner, err)
	}
}

// auditEntry returns a valid audit entry for a write of a view in ns.
func auditEntry(ns namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At:            time.UnixMilli(1),
		Namespace:     ns,
		Subject:       subjectAlice,
		IDPSubject:    "",
		TokenID:       "",
		Action:        "view:" + action,
		Object:        ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}
}

// staleFence returns a fence of the namespace of f that is not the current one.
func staleFence(f namespace.Fence) namespace.Fence {
	return namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}
}

// auditCount returns the number of audit entries of the namespace acme.
func auditCount(t *testing.T, h Harness) int {
	t.Helper()

	return len(h.Audit(t, "acme"))
}
