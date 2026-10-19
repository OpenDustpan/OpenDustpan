package audit_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestPrincipalKindValuesAgree proves that the kind of a request principal,
// the kind of a principal record and the value of an audit entry spell each
// kind the same way, so a conversion between them never changes a value.
func TestPrincipalKindValuesAgree(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		identity identity.Kind
		record   principal.Kind
		entry    string
	}{
		{"person", identity.KindPerson, principal.KindPerson, audit.PrincipalKindPerson},
		{"service", identity.KindService, principal.KindService, audit.PrincipalKindService},
		{"workload", identity.KindWorkload, principal.KindWorkload, audit.PrincipalKindWorkload},
	} {
		if string(tc.identity) != tc.name || string(tc.record) != tc.name || tc.entry != tc.name {
			t.Errorf("kind %q: identity %q, record %q, entry %q; want all equal",
				tc.name, tc.identity, tc.record, tc.entry)
		}
	}
}
