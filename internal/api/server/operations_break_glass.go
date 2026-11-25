package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// breakGlassOperationLabels names the break-glass routes in the description.
func breakGlassOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, breakGlassPath): apidesc.Name("listBreakGlassAccounts", tagBreakGlass).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[breakGlassListResponse](
				"ListBreakGlassAccountsResponse",
				apidesc.Named[breakGlassAccountResponse]("BreakGlassAccount"),
				apidesc.Named[passkeyBody]("Passkey"),
			)),
		apidesc.Pattern(http.MethodPost, breakGlassPath): apidesc.Name("addBreakGlassAccount", tagBreakGlass).
			WithStatus(http.StatusCreated).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[addBreakGlassRequest]("AddBreakGlassAccountRequest"),
				apidesc.Typed[breakGlassAccountResponse]("BreakGlassAccount",
					apidesc.Named[passkeyBody]("Passkey")),
			),
		apidesc.Pattern(http.MethodPost, breakGlassAccountPath+"/rotate"): apidesc.Name("rotateBreakGlassPasskey", tagBreakGlass).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[rotateBreakGlassRequest]("RotateBreakGlassPasskeyRequest"),
				apidesc.NoBody,
			),
	}
}
