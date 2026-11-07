package gate_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

// The user name check sees hostile input directly, as if the HTTP adapter and
// every other layer had failed.
func TestConfigServiceRefusesHiddenRunesInApproverUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    rune
	}{
		{"control", 0x07},
		{"format right-to-left override", 0x202e},
		{"line separator", 0x2028},
		{"paragraph separator", 0x2029},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, _ := newConfigService(t)

			def := goodDefault(gate.NamespaceScope("acme"))
			def.Approvers = gate.Approvers{
				Groups:     nil,
				GroupIDs:   nil,
				Users:      []string{"a" + string(tc.r) + "b"},
				UnitOwners: false,
			}
			def.Count = 1

			_, err := svc.PutApprovalDefault(t.Context(), def, 0, callerEntry())

			got, ok := errors.AsType[*gate.ApproversError](err)
			if !ok || got.Reason != gate.ApproversUserInvalid {
				t.Fatalf("error = %v, want reason ApproversUserInvalid", err)
			}
		})
	}
}

// The user name check refuses invalid UTF-8 on its own: HasHidden does not,
// because Go decodes an invalid byte as U+FFFD.
func TestConfigServiceRefusesInvalidUTF8InApproverUser(t *testing.T) {
	t.Parallel()

	svc, _ := newConfigService(t)

	def := goodDefault(gate.NamespaceScope("acme"))
	def.Approvers = gate.Approvers{Groups: nil, GroupIDs: nil, Users: []string{"a\xffb"}, UnitOwners: false}
	def.Count = 1

	_, err := svc.PutApprovalDefault(t.Context(), def, 0, callerEntry())

	got, ok := errors.AsType[*gate.ApproversError](err)
	if !ok || got.Reason != gate.ApproversUserInvalid {
		t.Fatalf("error = %v, want reason ApproversUserInvalid", err)
	}
}
