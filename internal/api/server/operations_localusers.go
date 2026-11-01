package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// localUserOperationLabels names the local user routes and the password
// sign-in in the description.
func localUserOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodPost, localSignInPath): apidesc.Name("signInWithPassword", tagLocalUsers).
			WithBodies(apidesc.Typed[passwordSignInRequest]("PasswordSignInRequest"), apidesc.NoBody),
		apidesc.Pattern(http.MethodPost, localUserRedeemPath): apidesc.Name("redeemLocalUserCode", tagLocalUsers).
			WithBodies(apidesc.Typed[redeemRequest]("RedeemLocalUserCodeRequest"), apidesc.NoBody),
		apidesc.Pattern(http.MethodPost, localUsersPath): apidesc.Name("createLocalUser", tagLocalUsers).
			WithStatus(http.StatusCreated).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[createLocalUserRequest]("CreateLocalUserRequest"),
				apidesc.Typed[createLocalUserResponse]("CreateLocalUserResponse"),
			),
		apidesc.Pattern(http.MethodGet, localUserPolicyPath): apidesc.Name("getLocalUserPolicy", tagLocalUsers).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[localUserPolicyResponse]("LocalUserPolicy")),
		apidesc.Pattern(http.MethodPost, localUserResetPath): apidesc.Name("resetLocalUserPassword", tagLocalUsers).
			WithStatus(http.StatusCreated).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[resetLocalUserRequest]("ResetLocalUserPasswordRequest"),
				apidesc.Typed[resetLocalUserResponse]("ResetLocalUserPasswordResponse"),
			),
		apidesc.Pattern(http.MethodPost, ownSignInChangePath): apidesc.Name("changeOwnPassword", tagLocalUsers).
			On(apidesc.KindCaller).
			WithBodies(apidesc.Typed[changeOwnPasswordRequest]("ChangeOwnPasswordRequest"), apidesc.NoBody),
	}
}
