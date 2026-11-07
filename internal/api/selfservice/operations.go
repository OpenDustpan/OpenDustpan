package selfservice

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/selfservice"
)

// tag is the tag of the routes of the blueprint API.
const tag = "self-service"

// namespaceRead is the action that every route checks on the namespace first.
const namespaceRead = "namespace:read"

// operationLabels names the routes of this adapter. The server refuses to
// start without a label for a route under apidesc.Prefix.
func operationLabels() map[string]apidesc.Label {
	// The blueprint document is the body of a draft write, and the draft of a
	// read names the same type. The write parses it with the strict reader.
	blueprint := apidesc.Typed[selfservice.Blueprint]("SelfServiceBlueprint", blueprintTypes()...)
	version := apidesc.Typed[versionMetadata]("SelfServiceVersion")
	draftDeclared := append(blueprintTypes(), apidesc.Named[selfservice.Blueprint]("SelfServiceBlueprint"))
	draft := apidesc.Typed[draftResponse]("SelfServiceDraft", draftDeclared...)
	write := apidesc.Typed[writeResponse]("SelfServiceDraftWrite")
	publish := apidesc.Typed[publishBody]("SelfServicePublishRequest")

	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, BlueprintsPath): apidesc.Name("listBlueprints", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.Typed[listResponse]("SelfServiceVersionList",
				apidesc.Named[versionMetadata]("SelfServiceVersion"))),
		apidesc.Pattern(http.MethodGet, DraftPath): apidesc.Name("getBlueprintDraft", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, draft),
		apidesc.Pattern(http.MethodPut, DraftPath): apidesc.Name("writeBlueprintDraft", tag).WithIfMatchRequired().
			On(apidesc.KindNamespace).
			WithBodies(blueprint, write),
		apidesc.Pattern(http.MethodPost, VersionsPath): apidesc.Name("publishBlueprint", tag).
			On(apidesc.KindNamespace).
			WithBodies(publish, version),
		apidesc.Pattern(http.MethodGet, VersionPath): apidesc.Name("getBlueprintVersion", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, version),
		apidesc.Pattern(http.MethodPost, RetirePath): apidesc.Name("retireBlueprintVersion", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.NoBody),
	}
}

// Operations returns the routes of this adapter for the description of the
// tool-neutral REST API. Each row lists namespace:read first, which the
// wrapper checks before the actions of the route. Every action of a row is
// checked on the namespace that holds the blueprint.
func (h *Handler) Operations() []apidesc.Operation {
	rows := h.Routes()
	described := make([]apidesc.Row, 0, len(rows))

	for i := range rows {
		rw := &rows[i]

		actions := make([]string, 0, 1+len(rw.Reads)+len(rw.Actions))
		actions = append(actions, namespaceRead)
		for _, action := range rw.Reads {
			actions = append(actions, string(action))
		}

		for _, action := range rw.Actions {
			actions = append(actions, string(action))
		}

		described = append(described, apidesc.Row{
			Method: rw.Method, Path: rw.Path, Actions: actions, NamespaceActions: actions,
			Public: false, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(described, operationLabels())
}
