package storecheck_test

import "testing"

// hostilePrefixes are prefixes that a check must never write under or
// delete. Each layer of the containment gets each of them directly, as if
// every other layer had failed.
func hostilePrefixes() []string {
	return []string{
		"",
		"/",
		"namespaces/",
		"backups/",
		"store-checks/",
		"store-checks/../namespaces/",
		"store-checks/other-check/",
		"store-checks/<check ID>/../../backups/",
	}
}

func TestServiceRefusesAHostilePrefix(t *testing.T) {
	t.Parallel()

	for _, prefix := range hostilePrefixes() {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()
			t.Skip("not implemented")
		})
	}
}

func TestScopedViewStopsAWriteOutsideThePrefix(t *testing.T) {
	t.Parallel()

	for _, prefix := range hostilePrefixes() {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()
			t.Skip("not implemented")
		})
	}
}

func TestPrefixReaperRefusesAHostilePrefix(t *testing.T) {
	t.Parallel()

	for _, prefix := range hostilePrefixes() {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()
			t.Skip("not implemented")
		})
	}
}

func TestRefusalWritesAnAuditEntry(t *testing.T) {
	t.Parallel()
	t.Skip("not implemented")
}

func TestReportHoldsNoEndpointFromAnAdapterError(t *testing.T) {
	t.Parallel()
	t.Skip("not implemented")
}

func TestReportSaysWhenTheStoreRefusesThePrefixDelete(t *testing.T) {
	t.Parallel()
	t.Skip("not implemented")
}
