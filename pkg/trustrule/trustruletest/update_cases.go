package trustruletest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// updateCases are the cases of the update of a rule and of the bundle
// reference of a rule.
func updateCases() []suiteCase {
	return []suiteCase{
		{"UpdateChangesTheFieldsAndAppendsItsAuditEntry", testUpdate},
		{"UpdateKeepsTheNamespaceTheCreatorAndTheCreationTime", testUpdateKeepsImmutables},
		{"UpdateRefusesAStaleRevision", testUpdateStaleRevision},
		{"UpdateOfAnUnknownRuleIsNotFound", testUpdateUnknown},
		{"UpdateOfARuleOfAnotherNamespaceIsNotFound", testUpdateOtherNamespace},
		{"UpdateRefusesAStaleFence", testUpdateStaleFence},
		{"UpdateRefusesTheFenceOfAnotherNamespace", testUpdateOtherFence},
		{"UpdateRefusesAnEntryOfAnotherNamespace", testUpdateOtherEntry},
		{"UpdateRefusesATakenName", testUpdateTakenName},
		{"UpdateRefusesAnInvalidRule", testUpdateInvalid},
		{"UpdateChecksThePrincipal", testUpdatePrincipal},
		{"CreateRefusesABundleOfAnotherNamespace", testCreateOtherBundle},
		{"UpdateRefusesABundleOfAnotherNamespace", testUpdateOtherBundle},
		{"UpdateStoresTheSpiffePatternAndTheBundle", testUpdateMTLS},
	}
}

// updateEntry returns a valid audit entry for an update of the rule id in ns.
func updateEntry(ns namespace.Name, id trustrule.ID) audit.Entry {
	return auditEntry(ns, "update", id)
}

// changed returns r with a new name and the next revision.
func changed(r trustrule.Rule) trustrule.Rule {
	r.Name += "-2"
	r.Audience = "https://other.example.com"
	r.Revision++

	return r
}

func testUpdate(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	next := changed(r)

	got, err := h.Store.Update(t.Context(), nsAcme, next, fence, updateEntry(nsAcme, r.ID))
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	sameRule(t, got, next)
	sameRule(t, mustGet(t, h, r.ID), next)

	entries := ruleEntries(t, h, nsAcme)
	if len(entries) != 2 || entries[1].Action != "trust-rule:update" || entries[1].ObjectID != string(r.ID) {
		t.Fatalf("audit entries = %+v, want the create and then the update of %s", entries, r.ID)
	}
}

func testUpdateKeepsImmutables(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	next := changed(r)
	next.CreatedBy = "mallory"
	next.CreatedAt = next.CreatedAt.Add(1)

	got, err := h.Store.Update(t.Context(), nsAcme, next, fence, updateEntry(nsAcme, r.ID))
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if got.CreatedBy != r.CreatedBy || !got.CreatedAt.Equal(r.CreatedAt) || got.Namespace != nsAcme {
		t.Errorf("update changed the creator, the creation time or the namespace: %+v", got)
	}
}

func testUpdateStaleRevision(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	for _, revision := range []int64{0, 1, 3, 100} {
		next := changed(r)
		next.Revision = revision

		_, err := h.Store.Update(t.Context(), nsAcme, next, fence, updateEntry(nsAcme, r.ID))
		if !errors.Is(err, trustrule.ErrStale) {
			t.Errorf("update with revision %d = %v, want ErrStale", revision, err)
		}
	}

	sameRule(t, mustGet(t, h, r.ID), r)

	if got := auditCount(t, h, nsAcme); got != 1 {
		t.Errorf("audit entries = %d, want 1", got)
	}
}

func testUpdateUnknown(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	r.Revision = 2

	_, err := h.Store.Update(t.Context(), nsAcme, r, fence, updateEntry(nsAcme, r.ID))
	if !errors.Is(err, trustrule.ErrNotFound) {
		t.Fatalf("update of an unknown rule = %v, want ErrNotFound", err)
	}

	if got := auditCount(t, h, nsAcme); got != 0 {
		t.Errorf("audit entries = %d, want 0", got)
	}
}

func testUpdateOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	other := h.Namespace(t, nsOther)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	next := changed(r)
	next.Namespace = nsOther

	_, err := h.Store.Update(t.Context(), nsOther, next, other, updateEntry(nsOther, r.ID))
	if !errors.Is(err, trustrule.ErrNotFound) {
		t.Fatalf("update through another namespace = %v, want ErrNotFound", err)
	}

	sameRule(t, mustGet(t, h, r.ID), r)
}

func testUpdateStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	_, err := h.Store.Update(t.Context(), nsAcme, changed(r), staleFence(fence), updateEntry(nsAcme, r.ID))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("update with a stale fence = %v, want ErrFenced", err)
	}

	sameRule(t, mustGet(t, h, r.ID), r)

	if got := auditCount(t, h, nsAcme); got != 1 {
		t.Errorf("audit entries = %d, want 1", got)
	}
}

func testUpdateOtherFence(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	other := h.Namespace(t, nsOther)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	_, err := h.Store.Update(t.Context(), nsAcme, changed(r), other, updateEntry(nsOther, r.ID))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("update with the fence of another namespace = %v, want ErrFenced", err)
	}

	sameRule(t, mustGet(t, h, r.ID), r)
}

func testUpdateOtherEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	h.Namespace(t, nsOther)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	_, err := h.Store.Update(t.Context(), nsAcme, changed(r), fence, updateEntry(nsOther, r.ID))
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("update with an entry of another namespace = %v, want audit.ErrInvalidEntry", err)
	}

	sameRule(t, mustGet(t, h, r.ID), r)
}

func testUpdateTakenName(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	p := workload(t, h, nsAcme)
	a := newRule(nsAcme, "deploy", p)
	b := newRule(nsAcme, "build", p)
	mustCreate(t, h, a)
	mustCreate(t, h, b)

	next := b
	next.Name = a.Name
	next.Revision++

	_, err := h.Store.Update(t.Context(), nsAcme, next, fence, updateEntry(nsAcme, b.ID))
	if !errors.Is(err, trustrule.ErrExists) {
		t.Fatalf("update to a taken name = %v, want ErrExists", err)
	}

	sameRule(t, mustGet(t, h, b.ID), b)

	// The rule keeps its own name.
	keep := b
	keep.Audience = "https://other.example.com"
	keep.Revision++

	if _, err := h.Store.Update(t.Context(), nsAcme, keep, fence, updateEntry(nsAcme, b.ID)); err != nil {
		t.Fatalf("update that keeps the name: %v", err)
	}
}

func testUpdateInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	bad := changed(r)
	bad.Conditions = nil

	_, err := h.Store.Update(t.Context(), nsAcme, bad, fence, updateEntry(nsAcme, r.ID))
	if !errors.Is(err, trustrule.ErrInvalid) {
		t.Fatalf("update to a rule with no condition = %v, want ErrInvalid", err)
	}

	bad = changed(r)
	bad.SpiffeIDPattern = "https://not-spiffe.example.com/x"

	_, err = h.Store.Update(t.Context(), nsAcme, bad, fence, updateEntry(nsAcme, r.ID))
	if !errors.Is(err, trustrule.ErrInvalid) {
		t.Fatalf("update to a bad SPIFFE ID pattern = %v, want ErrInvalid", err)
	}

	sameRule(t, mustGet(t, h, r.ID), r)
}

func testUpdatePrincipal(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	h.Namespace(t, nsOther)

	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	for name, id := range map[string]string{
		"a person":                  h.Principal(t, nsAcme, principal.KindPerson),
		"a principal of another ns": workload(t, h, nsOther),
		"an unknown principal":      "prn-unknown",
	} {
		next := changed(r)
		next.PrincipalID = id

		_, err := h.Store.Update(t.Context(), nsAcme, next, fence, updateEntry(nsAcme, r.ID))
		if !errors.Is(err, trustrule.ErrPrincipal) {
			t.Errorf("update to %s = %v, want ErrPrincipal", name, err)
		}
	}

	sameRule(t, mustGet(t, h, r.ID), r)
}

// bundleOf saves a bundle in ns and returns its ID.
func bundleOf(t *testing.T, h Harness, ns namespace.Name) string {
	t.Helper()

	fence := h.Namespace(t, ns.String())

	b, err := h.Bundles.SaveBundle(t.Context(), ns, saveFields(testCA(t)), fence,
		bundleEntry(ns, "save"))
	if err != nil {
		t.Fatalf("save bundle: %v", err)
	}

	return string(b.ID)
}

func testCreateOtherBundle(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	foreign := bundleOf(t, h, nsOther)

	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	r.SpiffeIDPattern = "spiffe://example.org/ns/acme"
	r.Bundle = foreign

	err := h.Store.Create(t.Context(), r, fence, auditEntry(nsAcme, "create", r.ID))
	if !errors.Is(err, trustrule.ErrBundle) {
		t.Fatalf("create with the bundle of another namespace = %v, want ErrBundle", err)
	}

	r.Bundle = string(trustrule.NewBundleID())

	err = h.Store.Create(t.Context(), r, fence, auditEntry(nsAcme, "create", r.ID))
	if !errors.Is(err, trustrule.ErrBundle) {
		t.Fatalf("create with an unknown bundle = %v, want ErrBundle", err)
	}

	mustBeGone(t, h, nsAcme, r.ID)
}

func testUpdateOtherBundle(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	foreign := bundleOf(t, h, nsOther)

	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	next := changed(r)
	next.Bundle = foreign

	_, err := h.Store.Update(t.Context(), nsAcme, next, fence, updateEntry(nsAcme, r.ID))
	if !errors.Is(err, trustrule.ErrBundle) {
		t.Fatalf("update with the bundle of another namespace = %v, want ErrBundle", err)
	}

	sameRule(t, mustGet(t, h, r.ID), r)
}

func testUpdateMTLS(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	own := bundleOf(t, h, nsAcme)

	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	next := changed(r)
	next.SpiffeIDPattern = "spiffe://example.org/ns/acme/*"
	next.Bundle = own

	got, err := h.Store.Update(t.Context(), nsAcme, next, fence, updateEntry(nsAcme, r.ID))
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	sameRule(t, got, next)
	sameRule(t, mustGet(t, h, r.ID), next)

	list, err := h.Store.List(t.Context(), nsAcme)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d rules, %v, want 1", len(list), err)
	}

	sameRule(t, list[0], next)
}
