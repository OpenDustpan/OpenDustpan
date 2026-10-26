package gate_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	principalpkg "opendustpan.org/opendustpan/pkg/principal"
)

func rule(a gate.Approvers) gate.ApprovalRule {
	var r gate.ApprovalRule

	r.Approvers = a

	return r
}

func owned(mode gate.Mode, a gate.Approvers) gate.OwnershipRule {
	var r gate.OwnershipRule

	r.Mode, r.Approvers = mode, a

	return r
}

func preset(a gate.Approvers) gate.ApprovalDefault {
	var d gate.ApprovalDefault

	d.Approvers = a

	return d
}

func TestGateRefusesRuleWithoutApprover(t *testing.T) {
	t.Parallel()

	none := gate.Approvers{Groups: nil, GroupIDs: nil, Users: nil, UnitOwners: false}
	nobody := gate.Approvers{Groups: nil, GroupIDs: nil, Users: []string{gate.NoApprover}, UnitOwners: false}
	mixed := gate.Approvers{Groups: nil, GroupIDs: nil, Users: []string{gate.NoApprover, "alice"}, UnitOwners: false}
	group := gate.Approvers{Groups: []string{"sre"}, GroupIDs: nil, Users: []string{gate.NoApprover}, UnitOwners: false}
	owners := gate.Approvers{Groups: nil, GroupIDs: nil, Users: []string{gate.NoApprover}, UnitOwners: true}

	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"approval rule, empty list", rule(none).Unmeetable(), true},
		{"approval rule, NoApprover only", rule(nobody).Unmeetable(), true},
		{"approval rule, NoApprover and a user", rule(mixed).Unmeetable(), false},
		{"approval rule, NoApprover and a group", rule(group).Unmeetable(), false},
		{"approval rule, NoApprover and unit owners", rule(owners).Unmeetable(), false},
		{"ownership rule enforce, empty list", owned(gate.ModeEnforce, none).Unmeetable(), true},
		{"ownership rule enforce, NoApprover only", owned(gate.ModeEnforce, nobody).Unmeetable(), true},
		{"ownership rule enforce, a user", owned(gate.ModeEnforce, mixed).Unmeetable(), false},
		{"ownership rule evaluate, NoApprover only", owned(gate.ModeEvaluate, nobody).Unmeetable(), false},
		{"approval default, NoApprover only", preset(nobody).Unmeetable(), true},
		{"approval default, empty list keeps its meaning", preset(none).Unmeetable(), false},
		{"approval default, NoApprover and a user", preset(mixed).Unmeetable(), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.got != tc.want {
				t.Errorf("Unmeetable = %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func TestRuleApproversReadsAnEmptyListAsNoApprover(t *testing.T) {
	t.Parallel()

	got := gate.RuleApprovers(gate.Approvers{Groups: nil, GroupIDs: nil, Users: nil, UnitOwners: false})
	if len(got.Users) != 1 || got.Users[0] != gate.NoApprover || len(got.Groups) != 0 || got.UnitOwners {
		t.Errorf("RuleApprovers(empty) = %+v, want Users [NoApprover]", got)
	}

	keep := gate.Approvers{Groups: []string{"sre"}, GroupIDs: nil, Users: nil, UnitOwners: false}
	if got := gate.RuleApprovers(keep); len(got.Users) != 0 || len(got.Groups) != 1 {
		t.Errorf("RuleApprovers(groups) = %+v, want unchanged", got)
	}
}

func TestApprovalIgnoresDevSignatureInProduction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		subject    string
		production bool
		want       bool
	}{
		{"production, dev lower case", "dev:admin", true, false},
		{"production, dev mixed case", "DEV:Admin", true, false},
		{"production, NoApprover", gate.NoApprover, true, false},
		{"production, a person", "alice", true, true},
		{"dev installation, dev lower case", "dev:admin", false, true},
		{"dev installation, dev mixed case", "DEV:Admin", false, true},
		{"dev installation, NoApprover", gate.NoApprover, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := gate.ApproverPolicy{Production: tc.production, IsDev: principalpkg.IsDevSubject}
			if got := policy.CountsAsSignature(tc.subject); got != tc.want {
				t.Errorf("CountsAsSignature(%q, production=%v) = %v, want %v", tc.subject, tc.production, got, tc.want)
			}
		})
	}

	// Fail closed: a production check without a matcher counts no signature.
	closed := gate.ApproverPolicy{Production: true, IsDev: nil}
	if closed.CountsAsSignature("alice") {
		t.Error("CountsAsSignature with no matcher on production = true, want false")
	}
}

func TestApproverPolicyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		users      []string
		production bool
		wantErr    bool
	}{
		{"person", []string{"alice"}, true, false},
		{"NoApprover", []string{"alice", gate.NoApprover}, false, true},
		{"exclamation mark", []string{"!x"}, false, true},
		{"dev on production", []string{"dev:admin"}, true, true},
		{"dev mixed case on production", []string{"DEV:x"}, true, true},
		{"dev on dev installation", []string{"dev:admin"}, false, false},
		{"none", nil, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := gate.ApproverPolicy{Production: tc.production, IsDev: principalpkg.IsDevSubject}
			err := policy.Validate(gate.Approvers{Groups: nil, GroupIDs: nil, Users: tc.users, UnitOwners: false})
			if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, gate.ErrInvalid)) {
				t.Errorf("ValidateApprovers(%v) = %v, want error %v wrapping ErrInvalid", tc.users, err, tc.wantErr)
			}
		})
	}
}
