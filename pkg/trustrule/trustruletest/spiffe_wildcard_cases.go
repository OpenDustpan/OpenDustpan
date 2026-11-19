package trustruletest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// spiffeWildcardPatterns returns the SPIFFE ID patterns with a * segment. A
// rule for the mTLS exchange names an exact SPIFFE ID, so the store holds
// none of them.
func spiffeWildcardPatterns() []string {
	return []string{
		"spiffe://example.org/ns/acme/*",
		"spiffe://td/*",
		"spiffe://td/*/x/*",
	}
}

// spiffeWildcardCases are the refusals of a SPIFFE ID pattern with a *.
func spiffeWildcardCases() []suiteCase {
	return []suiteCase{
		{"CreateRefusesAWildcardSpiffePattern", testCreateWildcardSpiffe},
		{"UpdateRefusesAWildcardSpiffePattern", testUpdateWildcardSpiffe},
	}
}

// testCreateWildcardSpiffe sends each wildcard pattern to Create: the store
// refuses it with ErrSpiffeWildcard, keeps no rule and writes no audit
// entry.
func testCreateWildcardSpiffe(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	own := bundleOf(t, h, nsAcme)

	for _, pattern := range spiffeWildcardPatterns() {
		r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
		r.SpiffeIDPattern = pattern
		r.Bundle = own

		err := h.Store.Create(t.Context(), r, fence, auditEntry(nsAcme, "create", r.ID))
		if !errors.Is(err, trustrule.ErrSpiffeWildcard) || !errors.Is(err, trustrule.ErrInvalid) {
			t.Errorf("create with %q = %v, want ErrSpiffeWildcard and ErrInvalid", pattern, err)
		}

		mustBeGone(t, h, nsAcme, r.ID)
	}

	rules, err := h.Store.List(t.Context(), nsAcme)
	if err != nil || len(rules) != 0 {
		t.Errorf("list = %d rules, %v, want no rule", len(rules), err)
	}

	if got := auditCount(t, h, nsAcme); got != 0 {
		t.Errorf("audit entries = %d, want 0", got)
	}
}

// testUpdateWildcardSpiffe sends each wildcard pattern to Update: the store
// refuses it with ErrSpiffeWildcard, the rule stays as it was and no new
// audit entry exists.
func testUpdateWildcardSpiffe(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	own := bundleOf(t, h, nsAcme)

	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	before := auditCount(t, h, nsAcme)

	for _, pattern := range spiffeWildcardPatterns() {
		next := changed(r)
		next.SpiffeIDPattern = pattern
		next.Bundle = own

		_, err := h.Store.Update(t.Context(), nsAcme, next, fence, updateEntry(nsAcme, r.ID))
		if !errors.Is(err, trustrule.ErrSpiffeWildcard) || !errors.Is(err, trustrule.ErrInvalid) {
			t.Errorf("update with %q = %v, want ErrSpiffeWildcard and ErrInvalid", pattern, err)
		}
	}

	sameRule(t, mustGet(t, h, r.ID), r)

	if got := auditCount(t, h, nsAcme); got != before {
		t.Errorf("audit entries = %d, want %d", got, before)
	}
}
