package gate

// CheckUserNameForTest is checkUserName, for the tests of the black-box
// package gate_test.
func CheckUserNameForTest(name string) error {
	return checkUserName(name)
}
