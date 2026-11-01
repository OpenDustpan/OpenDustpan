package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// LocalUserBreakGlass means that a break-glass account cannot have a password.
	LocalUserBreakGlass = "local-user.break-glass"
	// LocalUserCodeInvalid means that the set-up code or the reset code is not valid, was used or expired.
	LocalUserCodeInvalid = "local-user.code-invalid"
	// LocalUserCurrentPasswordRequired means that the change needs the current password or a fresh authentication.
	LocalUserCurrentPasswordRequired = "local-user.current-password-required"
	// LocalUserCurrentPasswordWrong means that the current password of the change is wrong.
	LocalUserCurrentPasswordWrong = "local-user.current-password-wrong"
	// LocalUserDemoFixed means that the credentials of the demo user cannot change.
	LocalUserDemoFixed = "local-user.demo-fixed"
	// LocalUserExists means that another local user has the user name.
	LocalUserExists = "local-user.exists"
	// LocalUserHashBusy means that the password hash budget of the server is full.
	LocalUserHashBusy = "local-user.hash-busy"
	// LocalUserLastCredential means that the change removes the last sign-in credential of the person.
	LocalUserLastCredential = "local-user.last-credential"
	// LocalUserNameInvalid means that the user name does not match the pattern of a user name.
	LocalUserNameInvalid = "local-user.name-invalid"
	// LocalUserNotFound means that the person has no local user.
	LocalUserNotFound = "local-user.not-found"
	// LocalUserOwnAccount means that an administrator cannot reset or remove the own password.
	LocalUserOwnAccount = "local-user.own-account"
	// LocalUserPasswordSignInOff means that the password sign-in is off on the installation.
	LocalUserPasswordSignInOff = "local-user.password-sign-in-off"
	// LocalUserReasonRequired means that the change needs a reason.
	LocalUserReasonRequired = "local-user.reason-required"
	// LocalUserSecondFactorRequired means that the password sign-in needs a second factor.
	LocalUserSecondFactorRequired = "local-user.second-factor-required"
	// LocalUserSignInFailed means that the user name or the password is wrong.
	LocalUserSignInFailed = "local-user.sign-in-failed"
	// LocalUserUnavailable means that the server keeps no local users, or a store of the local users cannot answer.
	LocalUserUnavailable = "local-user.unavailable"
)

// localUserSpecs are the codes of the local users and of the password
// sign-in.
func localUserSpecs() []Spec {
	return []Spec{
		{Code: LocalUserBreakGlass, Meaning: "a break-glass account cannot have a password", Args: nil},
		{
			Code:    LocalUserCodeInvalid,
			Meaning: "the set-up code or the reset code is not valid, was used or expired",
			Args:    nil,
		},
		{
			Code:    LocalUserCurrentPasswordRequired,
			Meaning: "the change needs the current password or a fresh authentication",
			Args:    nil,
		},
		{Code: LocalUserCurrentPasswordWrong, Meaning: "the current password of the change is wrong", Args: nil},
		{Code: LocalUserDemoFixed, Meaning: "the credentials of the demo user cannot change", Args: nil},
		{Code: LocalUserExists, Meaning: "another local user has the user name", Args: nil},
		{Code: LocalUserHashBusy, Meaning: "the password hash budget of the server is full", Args: nil},
		{
			Code:    LocalUserLastCredential,
			Meaning: "the change removes the last sign-in credential of the person",
			Args:    nil,
		},
		{Code: LocalUserNameInvalid, Meaning: "the user name does not match the pattern of a user name", Args: nil},
		{Code: LocalUserNotFound, Meaning: "the person has no local user", Args: nil},
		{Code: LocalUserOwnAccount, Meaning: "an administrator cannot reset or remove the own password", Args: nil},
		{Code: LocalUserPasswordSignInOff, Meaning: "the password sign-in is off on the installation", Args: nil},
		{Code: LocalUserReasonRequired, Meaning: "the change needs a reason", Args: nil},
		{Code: LocalUserSecondFactorRequired, Meaning: "the password sign-in needs a second factor", Args: nil},
		{Code: LocalUserSignInFailed, Meaning: "the user name or the password is wrong", Args: nil},
		{
			Code:    LocalUserUnavailable,
			Meaning: "the server keeps no local users, or a store of the local users cannot answer",
			Args:    nil,
		},
	}
}
