package issuer

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagIssuer is the tag of every operation of this adapter.
const tagIssuer = "issuer"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route. The routes under /oidc/ are outside the versioned API, so the
// description holds no label for them.
func operationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, issuerPath): apidesc.Name("getNamespaceIssuer", tagIssuer).WithETag().
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.Typed[issuerBody](
				"NamespaceIssuer",
				apidesc.Named[settingsBody]("IssuerSettings"), apidesc.Named[keyBody]("IssuerKey"),
				apidesc.Named[copyCheckBody]("IssuerCopyCheck"), apidesc.Named[copyStepBody]("IssuerCopyStep"),
			)),
		apidesc.Pattern(http.MethodPatch, issuerPath): apidesc.Name("updateNamespaceIssuer", tagIssuer).
			WithIfMatchRequired().WithETag().
			On(apidesc.KindNamespace).
			WithBodies(apidesc.Typed[patchBody]("UpdateNamespaceIssuerRequest"), apidesc.Typed[settingsBody]("IssuerSettings")),
		apidesc.Pattern(http.MethodPost, keysPath): apidesc.Name("createNamespaceIssuerKeys", tagIssuer).
			WithStatus(http.StatusCreated).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.Typed[keysBody]("NamespaceIssuerKeys", apidesc.Named[keyBody]("IssuerKey"))),
		apidesc.Pattern(http.MethodPost, rotatePath): apidesc.Name("rotateNamespaceIssuerKeys", tagIssuer).
			On(apidesc.KindNamespace).
			WithBodies(
				apidesc.Typed[rotateRequest]("RotateNamespaceIssuerKeysRequest"),
				apidesc.Typed[rotateBody]("NamespaceIssuerRotation", apidesc.Named[keyBody]("IssuerKey")),
			),
		apidesc.Pattern(http.MethodPost, checkCopyPath): apidesc.Name("checkNamespaceIssuerCopy", tagIssuer).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.Typed[copyCheckBody]("IssuerCopyCheck", apidesc.Named[copyStepBody]("IssuerCopyStep"))),
		apidesc.Pattern(http.MethodGet, exportPath): apidesc.Name("exportNamespaceIssuer", tagIssuer).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.Typed[exportBody]("NamespaceIssuerExport", apidesc.Named[exportFile]("IssuerExportFile"))),
	}
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, rt := range h.routes {
		actions := make([]string, 0, len(rt.Actions))
		for _, action := range rt.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: rt.Method, Path: rt.Path, Actions: actions, NamespaceActions: nil,
			Public: false, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels())
}
