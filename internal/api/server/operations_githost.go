package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagGitHost is the tag of the Git host operations.
const tagGitHost = "git-host"

// gitHostOperationLabels names the Git host routes. The key is the pattern of
// the route. Each write takes the Idempotency-Key header, except the check,
// which refuses it.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var gitHostOperationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, gitHostAppPath): apidesc.Name("getGitHostApp", tagGitHost).WithETag().
		On(apidesc.KindServer).
		WithBodies(apidesc.NoBody, gitHostAppSchema),
	apidesc.Pattern(http.MethodPut, gitHostAppPath): apidesc.Name("putGitHostApp", tagGitHost).
		WithStatus(http.StatusOK, http.StatusCreated).
		WithKey().WithIfMatch().WithETag().
		On(apidesc.KindServer).
		WithBodies(apidesc.Typed[gitHostAppRequest]("PutGitHostAppRequest"), gitHostAppSchema),
	apidesc.Pattern(http.MethodDelete, gitHostAppPath): apidesc.Name("deleteGitHostApp", tagGitHost).
		WithKey().WithIfMatch().
		On(apidesc.KindServer).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodGet, gitHostInstallationsPath): apidesc.Name("listGitHostInstallations", tagGitHost).
		On(apidesc.KindServer).
		WithBodies(apidesc.NoBody, apidesc.Typed[gitHostInstallationsDocument](
			"ListGitHostInstallationsResponse", apidesc.Named[gitHostInstallationDocument]("GitHostInstallation"),
		)),
	apidesc.Pattern(http.MethodGet, gitHostInstallationPath): apidesc.Name("getGitHostInstallation", tagGitHost).
		WithETag().
		On(apidesc.KindServer).
		WithBodies(apidesc.NoBody, gitHostInstallationSchema),
	apidesc.Pattern(http.MethodPut, gitHostInstallationPath): apidesc.Name("putGitHostInstallation", tagGitHost).
		WithStatus(http.StatusOK, http.StatusCreated).
		WithKey().WithIfMatch().WithETag().
		On(apidesc.KindServer).
		WithBodies(apidesc.Typed[gitHostInstallationRequest]("PutGitHostInstallationRequest"), gitHostInstallationSchema),
	apidesc.Pattern(http.MethodDelete, gitHostInstallationPath): apidesc.Name("deleteGitHostInstallation", tagGitHost).
		WithKey().WithIfMatch().
		On(apidesc.KindServer).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodGet, gitHostConnectionsPath): apidesc.Name("listGitHostConnections", tagGitHost).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[gitHostConnectionsDocument](
			"ListGitHostConnectionsResponse", apidesc.Named[gitHostConnectionDocument]("GitHostConnection"),
		)),
	apidesc.Pattern(http.MethodPost, gitHostConnectionsPath): apidesc.Name("createGitHostConnection", tagGitHost).
		WithStatus(http.StatusCreated).
		WithKey().WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[gitHostConnectionRequest](
			"CreateGitHostConnectionRequest",
			apidesc.Declare[operatorOwned](map[string]any{
				"description": "Any JSON value. Only the operator sets the field that holds it. " +
					"A request that holds the field is refused.",
			}),
		), gitHostConnectionSchema),
	apidesc.Pattern(http.MethodGet, gitHostConnectionPath): apidesc.Name("getGitHostConnection", tagGitHost).
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, gitHostConnectionSchema),
	apidesc.Pattern(http.MethodDelete, gitHostConnectionPath): apidesc.Name("deleteGitHostConnection", tagGitHost).
		WithKey().WithIfMatch().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodPost, gitHostDefaultPath): apidesc.Name("makeGitHostConnectionDefault", tagGitHost).
		WithKey().WithIfMatch().WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, gitHostConnectionSchema),
	apidesc.Pattern(http.MethodPost, gitHostCheckPath): apidesc.Name("checkGitHostConnection", tagGitHost).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[gitHostCheckDocument](
			"GitHostCheck", apidesc.Named[gitHostAccountDocument]("GitHostAccount"),
		)),
}

// The bodies that several Git host routes share.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant structs.
var (
	gitHostAppSchema = apidesc.Typed[gitHostAppDocument](
		"GitHostApp", apidesc.Named[credentialSetDocument]("GitHostCredentialSet"),
	)
	gitHostInstallationSchema = apidesc.Typed[gitHostInstallationDocument]("GitHostInstallation")
	gitHostConnectionSchema   = apidesc.Typed[gitHostConnectionDocument]("GitHostConnection")
)
