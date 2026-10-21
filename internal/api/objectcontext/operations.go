package objectcontext

import (
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// tagContext is the tag of every operation of this adapter.
const tagContext = "context"

const (
	namespaceContextPath = "/api/dustpan/v1/namespaces/{namespace}/context"
	unitContextPath      = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/context"
)

// operationLabels names the routes of this adapter. The key is the pattern of
// the route.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, namespaceContextPath): apidesc.Name("getNamespaceContext", tagContext).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, contextDocument),
	apidesc.Pattern(http.MethodPatch, namespaceContextPath): apidesc.Name("updateNamespaceContext", tagContext).
		On(apidesc.KindNamespace).
		WithBodies(updateRequest, contextEntry),
	apidesc.Pattern(http.MethodDelete, namespaceContextPath): apidesc.Name("deleteNamespaceContext", tagContext).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodGet, unitContextPath): apidesc.Name("getUnitContext", tagContext).
		On(apidesc.KindUnit).
		WithBodies(apidesc.NoBody, contextDocument),
	apidesc.Pattern(http.MethodPatch, unitContextPath): apidesc.Name("updateUnitContext", tagContext).
		On(apidesc.KindUnit).
		WithBodies(updateRequest, contextEntry),
	apidesc.Pattern(http.MethodDelete, unitContextPath): apidesc.Name("deleteUnitContext", tagContext).
		On(apidesc.KindUnit).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
}

// The bodies that several routes share.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant structs.
var (
	contextDocument = apidesc.Typed[readResponse](
		"ContextDocument", append(contextEnums(),
			apidesc.DeclareAs[objectcontext.Answer, answerWire](),
			apidesc.Named[answerWire]("ContextAnswer"),
			apidesc.Named[objectcontext.ObjectID]("ContextObject"),
			apidesc.Named[objectcontext.LayerStatus]("ContextLayerStatus"),
		)...,
	)
	contextEntry = apidesc.Typed[objectcontext.Entry](
		"ContextEntry", append(contextEnums(),
			apidesc.DeclareAs[objectcontext.Entry, entryWire](),
			apidesc.Named[objectcontext.ObjectID]("ContextObject"),
			apidesc.Named[objectcontext.Fields]("ContextFields"),
			apidesc.Named[objectcontext.Link]("ContextLink"),
		)...,
	)
	updateRequest = apidesc.Typed[patchBody](
		"UpdateContextRequest", append(contextEnums(), apidesc.Named[objectcontext.Link]("ContextLink"))...,
	)
)

// contextEnums declares the value sets of the string types of the context
// API. A set can grow, so each is an open enum: a client keeps a value that it
// does not know. The values are the constants of the domain package.
func contextEnums() []apidesc.Declaration {
	return []apidesc.Declaration{
		apidesc.DeclareOpenEnum[objectcontext.Kind]("ContextObjectKind",
			string(objectcontext.KindNamespace), string(objectcontext.KindCollection),
			string(objectcontext.KindUnit), string(objectcontext.KindResource), string(objectcontext.KindBlueprint)),
		apidesc.DeclareOpenEnum[objectcontext.Source]("ContextSource",
			string(objectcontext.SourceAPI), string(objectcontext.SourceOutput)),
		apidesc.DeclareOpenEnum[objectcontext.Status]("ContextStatus",
			string(objectcontext.StatusOK), string(objectcontext.StatusPartial),
			string(objectcontext.StatusDenied), string(objectcontext.StatusUnavailable)),
		apidesc.DeclareOpenEnum[objectcontext.ObjectRole]("ContextObjectRole",
			string(objectcontext.RoleTarget), string(objectcontext.RoleEachLayer),
			string(objectcontext.RoleEachNamed), string(objectcontext.RoleNamespace)),
		apidesc.DeclareOpenEnum[objectcontext.LinkKind]("ContextLinkKind",
			string(objectcontext.LinkDocs), string(objectcontext.LinkRunbook), string(objectcontext.LinkDashboard),
			string(objectcontext.LinkRepository), string(objectcontext.LinkOther)),
	}
}

// answerWire is the JSON of objectcontext.Answer for the description. The data
// of a section is one of eight objects, and the description shows it as an
// object with any member.
type answerWire struct {
	Data   map[string]any              `json:"data,omitempty"`
	Action string                      `json:"action"`
	Object objectcontext.ObjectRole    `json:"object"`
	Status objectcontext.Status        `json:"status"`
	Layers []objectcontext.LayerStatus `json:"layers,omitempty"`
	Hidden int                         `json:"hidden,omitempty"`
}

// entryWire is the JSON of objectcontext.Entry for the description.
type entryWire struct {
	UpdatedAt *time.Time             `json:"updatedAt,omitempty"`
	Object    objectcontext.ObjectID `json:"object"`
	Source    objectcontext.Source   `json:"source"`
	Readme    string                 `json:"readme,omitempty"`
	Item      string                 `json:"item,omitempty"`
	Warning   string                 `json:"warning,omitempty"`
	UpdatedBy string                 `json:"updatedBy,omitempty"`
	Fields    objectcontext.Fields   `json:"fields"`
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		// The wrapper reads the namespace of the path before the reads on the
		// object, for each route that is not public.
		var namespaceActions []string

		actions := make([]string, 0, len(route.Reads)+len(route.Actions)+1)

		if route.Scope != ScopePublic {
			namespaceActions = []string{string(identity.ActionNamespaceRead)}
			actions = append(actions, namespaceActions...)
		}

		for _, action := range route.Reads {
			actions = append(actions, string(action))
		}

		for _, action := range route.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: route.Method, Path: route.Path, Actions: actions, NamespaceActions: namespaceActions,
			Public: route.Scope == ScopePublic, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
