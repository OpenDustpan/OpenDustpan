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

// blueprintDocument is the Go type of the body of a draft write. Its JSON is
// an object: the handler parses it with the strict blueprint reader.
type blueprintDocument struct{}

// operationLabels names the routes of this adapter. The server refuses to
// start without a label for a route under apidesc.Prefix.
func operationLabels() map[string]apidesc.Label {
	// The blueprint document is an object. Its fields are not described yet:
	// the schema names only the object.
	document := apidesc.Declare[selfservice.Blueprint](map[string]any{"type": "object"})
	documentBody := apidesc.Typed[blueprintDocument]("SelfServiceBlueprint",
		apidesc.Declare[blueprintDocument](map[string]any{"type": "object"}))
	version := apidesc.Typed[versionMetadata]("SelfServiceVersion")
	draft := apidesc.Typed[draftResponse]("SelfServiceDraft", document)
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
		apidesc.Pattern(http.MethodPut, DraftPath): apidesc.Name("writeBlueprintDraft", tag).
			On(apidesc.KindNamespace).
			WithBodies(documentBody, write),
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
	rows := h.rows()
	described := make([]apidesc.Row, 0, len(rows))

	for i := range rows {
		rw := &rows[i]

		actions := make([]string, 0, 1+len(rw.reads)+len(rw.writes))
		actions = append(actions, namespaceRead)
		for _, action := range rw.reads {
			actions = append(actions, string(action))
		}

		for _, action := range rw.writes {
			actions = append(actions, string(action))
		}

		described = append(described, apidesc.Row{
			Method: rw.Method, Path: rw.Path, Actions: actions, NamespaceActions: actions,
			Public: false, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(described, operationLabels())
}

// RegisteredPatterns returns the ServeMux patterns that the handler serves, in
// registration order. The description checks them against the route table.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}
