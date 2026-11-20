package gate

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// CheckUserNameForTest is checkUserName, for the tests of the black-box
// package gate_test.
func CheckUserNameForTest(name string) error {
	return checkUserName(name)
}

// ReadApproverPolicyForTest is readApproverPolicy, for the tests of the
// black-box package gate_test.
func ReadApproverPolicyForTest(ctx context.Context, kinds KindReader, isDev DevSubjectMatcher) (ApproverPolicy, error) {
	return readApproverPolicy(ctx, kinds, isDev)
}

// PrepareGateForTest is prepare with the audit names of a gate set, for the
// tests of the black-box package gate_test.
func (s *ConfigService) PrepareGateForTest(
	ctx context.Context, scope Scope, e audit.Entry,
) (namespace.Fence, audit.Entry, error) {
	return s.prepare(ctx, scope, e, scope.Key()+"/gates/x", auditSetGate)
}

// PrepareApprovalDefaultForTest is prepare with the audit names of an approval
// default set, which the installation has no name for.
func (s *ConfigService) PrepareApprovalDefaultForTest(
	ctx context.Context, scope Scope, e audit.Entry,
) (namespace.Fence, audit.Entry, error) {
	return s.prepare(ctx, scope, e, scope.Key()+"/approval-default", auditSetApprovalDefault)
}
