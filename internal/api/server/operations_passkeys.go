package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// schemaDescription is the member of a schema that holds its description.
const schemaDescription = "description"

// passkeyJSONText is the description of a JSON object of the WebAuthn
// protocol in the API description.
const passkeyJSONText = "A JSON object of the WebAuthn protocol. The members are the ones that the " +
	"PublicKeyCredentialCreationOptions and the PublicKeyCredential of the protocol name."

// passkeyJSONSchema declares the JSON of a ceremony member. Its members are
// the ones that the WebAuthn protocol names, and the browser makes them.
func passkeyJSONSchema() apidesc.Declaration {
	return apidesc.Declare[passkeyJSON](map[string]any{schemaDescription: passkeyJSONText})
}

// passkeyOperationLabels names the passkey routes in the description.
func passkeyOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodPost, passkeyBeginPath): apidesc.Name("beginPasskeyRegistration", tagPasskeys).
			On(apidesc.KindCaller).
			WithBodies(apidesc.NoBody, apidesc.Typed[passkeyCreationOptions](
				"PasskeyCreationOptions", passkeyJSONSchema(),
			)),
		apidesc.Pattern(http.MethodPost, passkeyFinishPath): apidesc.Name("finishPasskeyRegistration", tagPasskeys).
			WithStatus(http.StatusCreated).
			On(apidesc.KindCaller).
			WithBodies(
				apidesc.Typed[finishPasskeyRequest]("FinishPasskeyRegistrationRequest", passkeyJSONSchema()),
				apidesc.Typed[passkeyBody]("Passkey"),
			),
		apidesc.Pattern(http.MethodGet, ownKeysPath): apidesc.Name("listPasskeys", tagPasskeys).
			On(apidesc.KindCaller).
			WithBodies(apidesc.NoBody, apidesc.Typed[passkeysResponse]("ListPasskeysResponse",
				apidesc.Named[passkeyBody]("Passkey"))),
		apidesc.Pattern(http.MethodDelete, passkeyPath): apidesc.Name("removePasskey", tagPasskeys).
			On(apidesc.KindCaller).
			WithBodies(apidesc.NoBody, apidesc.NoBody),
		apidesc.Pattern(http.MethodPost, passkeySignInBeginPath): apidesc.Name("beginPasskeySignIn", tagPasskeys).
			WithBodies(apidesc.NoBody, apidesc.Typed[passkeyRequestOptions](
				"PasskeyRequestOptions", passkeyJSONSchema(),
			)),
		apidesc.Pattern(http.MethodPost, passkeySignInFinishPath): apidesc.Name("finishPasskeySignIn", tagPasskeys).
			WithBodies(apidesc.Typed[passkeySignInRequest]("PasskeySignInRequest", passkeyJSONSchema()), apidesc.NoBody),
	}
}
