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
	apidesc.Pattern(http.MethodGet, gitHostAppPath): apidesc.Name("getGitHostApp", tagGitHost).WithETag(),
	apidesc.Pattern(http.MethodPut, gitHostAppPath): apidesc.Name("putGitHostApp", tagGitHost).
		WithKey().WithIfMatch().WithETag(),
	apidesc.Pattern(http.MethodDelete, gitHostAppPath): apidesc.Name("deleteGitHostApp", tagGitHost).
		WithKey().WithIfMatch(),
	apidesc.Pattern(http.MethodGet, gitHostInstallationsPath): apidesc.Name("listGitHostInstallations", tagGitHost),
	apidesc.Pattern(http.MethodGet, gitHostInstallationPath): apidesc.Name("getGitHostInstallation", tagGitHost).
		WithETag(),
	apidesc.Pattern(http.MethodPut, gitHostInstallationPath): apidesc.Name("putGitHostInstallation", tagGitHost).
		WithKey().WithIfMatch().WithETag(),
	apidesc.Pattern(http.MethodDelete, gitHostInstallationPath): apidesc.Name("deleteGitHostInstallation", tagGitHost).
		WithKey().WithIfMatch(),
	apidesc.Pattern(http.MethodGet, gitHostConnectionsPath): apidesc.Name("listGitHostConnections", tagGitHost),
	apidesc.Pattern(http.MethodPost, gitHostConnectionsPath): apidesc.Name("createGitHostConnection", tagGitHost).
		WithKey().WithETag(),
	apidesc.Pattern(http.MethodGet, gitHostConnectionPath): apidesc.Name("getGitHostConnection", tagGitHost).
		WithETag(),
	apidesc.Pattern(http.MethodDelete, gitHostConnectionPath): apidesc.Name("deleteGitHostConnection", tagGitHost).
		WithKey().WithIfMatch(),
	apidesc.Pattern(http.MethodPost, gitHostDefaultPath): apidesc.Name("makeGitHostConnectionDefault", tagGitHost).
		WithKey().WithIfMatch().WithETag(),
	apidesc.Pattern(http.MethodPost, gitHostCheckPath): apidesc.Name("checkGitHostConnection", tagGitHost),
}
