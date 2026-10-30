package trustruletest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// writeCases are the cases of the fence and the audit entry of a write.
func writeCases() []suiteCase {
	return []suiteCase{
		{"CreateAppendsItsAuditEntry", testCreateAudit},
		{"CreateRefusesAStaleFence", testCreateStaleFence},
		{"CreateRefusesTheFenceOfAnotherNamespace", testCreateOtherFence},
		{"CreateRefusesAnEntryOfAnotherNamespace", testCreateOtherEntry},
		{"DeleteRemovesTheRuleAndAppendsItsAuditEntry", testDelete},
		{"DeleteOfAnUnknownRuleIsNotFound", testDeleteUnknown},
		{"DeleteOfARuleOfAnotherNamespaceIsNotFound", testDeleteOtherNamespace},
		{"DeleteRefusesAStaleFence", testDeleteStaleFence},
	}
}

func testCreateAudit(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	entries := ruleEntries(t, h, nsAcme)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}

	if e := entries[0]; e.Action != "trust-rule:create" || e.ObjectID != string(r.ID) || e.Namespace != nsAcme {
		t.Errorf("audit entry = %s %s in %s, want trust-rule:create %s in %s",
			e.Action, e.ObjectID, e.Namespace, r.ID, nsAcme)
	}
}

func testCreateStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))

	err := h.Store.Create(t.Context(), r, staleFence(fence), auditEntry(nsAcme, "create", r.ID))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create with a stale fence = %v, want ErrFenced", err)
	}

	mustBeGone(t, h, nsAcme, r.ID)

	if got := auditCount(t, h, nsAcme); got != 0 {
		t.Errorf("audit entries = %d, want 0", got)
	}
}

func testCreateOtherFence(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	other := h.Namespace(t, nsOther)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))

	// The entry names the namespace of the fence, so only the check of the
	// namespace of the rule against the fence can refuse the write.
	err := h.Store.Create(t.Context(), r, other, auditEntry(nsOther, "create", r.ID))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create with the fence of another namespace = %v, want ErrFenced", err)
	}

	mustBeGone(t, h, nsAcme, r.ID)

	if got := auditCount(t, h, nsOther) + auditCount(t, h, nsAcme); got != 0 {
		t.Errorf("audit entries = %d, want 0", got)
	}
}

func testCreateOtherEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	h.Namespace(t, nsOther)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))

	err := h.Store.Create(t.Context(), r, fence, auditEntry(nsOther, "create", r.ID))
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("create with an entry of another namespace = %v, want audit.ErrInvalidEntry", err)
	}

	mustBeGone(t, h, nsAcme, r.ID)
}

func testDelete(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	if err := h.Store.Delete(t.Context(), nsAcme, r.ID, fence, auditEntry(nsAcme, "delete", r.ID)); err != nil {
		t.Fatalf("delete: %v", err)
	}

	mustBeGone(t, h, nsAcme, r.ID)

	entries := ruleEntries(t, h, nsAcme)
	if len(entries) != 2 || entries[1].Action != "trust-rule:delete" || entries[1].ObjectID != string(r.ID) {
		t.Fatalf("audit entries = %+v, want the create and then the delete of %s", entries, r.ID)
	}

	// The name is free again.
	mustCreate(t, h, newRule(nsAcme, "deploy", r.PrincipalID))
}

func testDeleteUnknown(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	id := trustrule.NewID()

	err := h.Store.Delete(t.Context(), nsAcme, id, fence, auditEntry(nsAcme, "delete", id))
	if !errors.Is(err, trustrule.ErrNotFound) {
		t.Fatalf("delete of an unknown rule = %v, want ErrNotFound", err)
	}

	if got := auditCount(t, h, nsAcme); got != 0 {
		t.Errorf("audit entries = %d, want 0", got)
	}
}

func testDeleteOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	other := h.Namespace(t, nsOther)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	err := h.Store.Delete(t.Context(), nsOther, r.ID, other, auditEntry(nsOther, "delete", r.ID))
	if !errors.Is(err, trustrule.ErrNotFound) {
		t.Fatalf("delete through another namespace = %v, want ErrNotFound", err)
	}

	mustGet(t, h, r.ID)

	if got := auditCount(t, h, nsOther); got != 0 {
		t.Errorf("audit entries of %s = %d, want 0", nsOther, got)
	}
}

func testDeleteStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	err := h.Store.Delete(t.Context(), nsAcme, r.ID, staleFence(fence), auditEntry(nsAcme, "delete", r.ID))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("delete with a stale fence = %v, want ErrFenced", err)
	}

	mustGet(t, h, r.ID)

	if got := auditCount(t, h, nsAcme); got != 1 {
		t.Errorf("audit entries = %d, want 1", got)
	}
}
