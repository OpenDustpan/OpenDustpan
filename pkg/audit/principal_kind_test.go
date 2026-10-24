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

// TestAuditAcceptsEveryIdentityKind proves that the audit log accepts every
// kind of identity.Kinds, so that a new kind cannot make the entries of that
// principal fail to append, and that each audit value is a known kind.
func TestAuditAcceptsEveryIdentityKind(t *testing.T) {
	t.Parallel()

	for _, k := range identity.Kinds() {
		e := testEntry()
		e.PrincipalKind = string(k)

		if err := e.Validate(); err != nil {
			t.Errorf("principal kind %q: %v", k, err)
		}
	}

	for _, k := range []string{audit.PrincipalKindPerson, audit.PrincipalKindService, audit.PrincipalKindWorkload} {
		if !identity.Kind(k).Known() {
			t.Errorf("audit principal kind %q is not a kind of the identity package", k)
		}
	}
}
