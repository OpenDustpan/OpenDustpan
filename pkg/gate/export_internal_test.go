package gate

import "context"

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
