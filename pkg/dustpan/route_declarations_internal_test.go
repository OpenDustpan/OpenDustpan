package dustpan

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	auditapi "opendustpan.org/opendustpan/internal/api/audit"
	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	notificationsapi "opendustpan.org/opendustpan/internal/api/notifications"
	contextapi "opendustpan.org/opendustpan/internal/api/objectcontext"
	profileapi "opendustpan.org/opendustpan/internal/api/profile"
	"opendustpan.org/opendustpan/internal/api/pulumi"
	registryapi "opendustpan.org/opendustpan/internal/api/registry"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/api/runnerapi"
	selfserviceapi "opendustpan.org/opendustpan/internal/api/selfservice"
	serverapi "opendustpan.org/opendustpan/internal/api/server"
	settingsapi "opendustpan.org/opendustpan/internal/api/settings"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	triggersapi "opendustpan.org/opendustpan/internal/api/triggers"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errUnknownRouteShape is the cause of every error of the hook: a Route type,
// a field or a scope value that it does not know.
var errUnknownRouteShape = errors.New("unknown route shape")

// RouteScope says whether a route is served without a sign-in, as its row
// declares it.
type RouteScope string

const (
	// RouteScopePublic is a row with the public scope of its adapter: the
	// wrapper serves it with no sign-in.
	RouteScopePublic RouteScope = "public"
	// RouteScopeSignedIn is a row with the signed-in scope of its adapter.
	RouteScopeSignedIn RouteScope = "signed-in"
	// RouteScopeCredential is a row with another scope of its adapter: the
	// route checks a credential of its own, for example a join token or a
	// signed URL.
	RouteScopeCredential RouteScope = "credential"
	// RouteScopeNone is a row of an adapter whose Route type has no Scope
	// field.
	RouteScopeNone RouteScope = "none"
)

// RouteDeclaration is what one route table row of an adapter declares.
type RouteDeclaration struct {
	// Adapter is the name of the adapter that serves the route.
	Adapter string
	// Pattern is the ServeMux pattern: the method, a space and the path.
	Pattern string
	// Scope is the scope of the row.
	Scope RouteScope
	// EnforcedFields names the fields of the row that hold at least one
	// action and that the wrapper of the adapter enforces.
	EnforcedFields []string
}

// RouteDeclarations returns the rows of the route table of each mounted
// adapter that has a Routes method. It reads the rows by reflection, because
// the Route type of each adapter is its own. It returns an error for a row
// type, a field, a scope value or a duplicate pattern that it does not know,
// so that a change to a Route type stops the test and names what changed.
func (s *Server) RouteDeclarations() ([]RouteDeclaration, error) {
	if s.mounted == nil {
		return nil, nil
	}

	var out []RouteDeclaration

	seen := map[string]string{}

	for _, h := range s.mounted.handlers() {
		method := reflect.ValueOf(h.handler).MethodByName("Routes")
		if !method.IsValid() {
			continue
		}

		rows := method.Call(nil)[0]
		for i := range rows.Len() {
			decl, err := declarationOf(h.name, rows.Index(i))
			if err != nil {
				return nil, err
			}

			if first, dup := seen[decl.Pattern]; dup {
				return nil, fmt.Errorf(
					"%w: route %q is in the table of %s and of %s", errUnknownRouteShape, decl.Pattern, first, h.name,
				)
			}

			seen[decl.Pattern] = h.name
			out = append(out, decl)
		}
	}

	return out, nil
}

// knownRowTypes lists the Route types that the hook knows. A type that is not
// here fails the hook.
func knownRowTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[serverapi.Route](),
		reflect.TypeFor[inventoryapi.Route](),
		reflect.TypeFor[auditapi.Route](),
		reflect.TypeFor[contextapi.Route](),
		reflect.TypeFor[runnerapi.Route](),
		reflect.TypeFor[profileapi.Route](),
		reflect.TypeFor[route.Route[identity.Object]](),
		reflect.TypeFor[selfserviceapi.Route](),
		reflect.TypeFor[jobsapi.Route](),
		reflect.TypeFor[triggersapi.Route](),
		reflect.TypeFor[settingsapi.Route](),
		reflect.TypeFor[driftapi.Route](),
		reflect.TypeFor[registryapi.Route](),
		reflect.TypeFor[notificationsapi.Route](),
		reflect.TypeFor[pulumi.Route](),
		reflect.TypeFor[tfe.Route](),
		reflect.TypeFor[tofuhttp.Route](),
	}
}

// scopeValues says what each value of one Scope type means.
type scopeValues struct {
	public     []int64
	signedIn   []int64
	credential []int64
}

// knownScopes maps each Scope type to the meaning of its values. The values
// are the constants of the adapters, so a rename stops the build and a new
// value stops the hook.
func knownScopes() map[reflect.Type]scopeValues {
	return map[reflect.Type]scopeValues{
		reflect.TypeFor[serverapi.Scope](): {
			public: ints(int64(serverapi.ScopePublic)), signedIn: ints(int64(serverapi.ScopeAuthorized)),
			credential: nil,
		},
		reflect.TypeFor[route.Scope](): {
			public: ints(int64(route.ScopePublic)), signedIn: ints(int64(route.ScopeAuthorized)), credential: nil,
		},
		reflect.TypeFor[jobsapi.Scope](): {
			public: ints(int64(jobsapi.ScopePublic)), signedIn: ints(int64(jobsapi.ScopeAuthorized)),
			credential: nil,
		},
		reflect.TypeFor[triggersapi.Scope](): {
			public: ints(int64(triggersapi.ScopePublic)), signedIn: ints(int64(triggersapi.ScopeAuthorized)),
			credential: nil,
		},
		reflect.TypeFor[settingsapi.Scope](): {
			public: ints(int64(settingsapi.ScopePublic)), signedIn: ints(int64(settingsapi.ScopeAuthorized)),
			credential: nil,
		},
		reflect.TypeFor[driftapi.Scope](): {
			public: ints(int64(driftapi.ScopePublic)), signedIn: ints(int64(driftapi.ScopeAuthorized)),
			credential: nil,
		},
		reflect.TypeFor[runnerapi.Scope](): {
			public:   nil,
			signedIn: ints(int64(runnerapi.ScopeAuthorized)),
			credential: ints(
				int64(runnerapi.ScopeJoinToken), int64(runnerapi.ScopeRunnerKey),
				int64(runnerapi.ScopeHandoffToken), int64(runnerapi.ScopeSessionKey),
			),
		},
		reflect.TypeFor[pulumi.Scope](): {
			public: ints(int64(pulumi.ScopePublic)), signedIn: ints(int64(pulumi.ScopeAuthorized)),
			credential: ints(int64(pulumi.ScopeUpdateLease)),
		},
		reflect.TypeFor[tfe.Scope](): {
			public: ints(int64(tfe.ScopePublic)), signedIn: ints(int64(tfe.ScopeAuthorized)),
			credential: ints(
				int64(tfe.ScopeUpload), int64(tfe.ScopeRegistry), int64(tfe.ScopeSigned), int64(tfe.ScopeConsole),
			),
		},
	}
}

// ints returns its arguments as a list.
func ints(values ...int64) []int64 { return values }

// enforcedActionFields names the fields of a Route type that the wrapper of
// its adapter authorizes before the handler runs.
func enforcedActionFields() []string {
	return []string{"Actions", "Reads", "NamespaceActions", "ServerActions"}
}

// unenforcedActionFields names the fields that list actions which the wrapper
// does not authorize: the job service or the handler checks them. Filters
// are the read actions that a list handler checks on each item. They are
// documentation, so they never count as a declared action.
func unenforcedActionFields() []string {
	return []string{"Writes", "Extra", "Filters"}
}

// declarationOf reads one row of a route table.
func declarationOf(adapter string, row reflect.Value) (RouteDeclaration, error) {
	if !slices.Contains(knownRowTypes(), row.Type()) {
		return RouteDeclaration{}, fmt.Errorf(
			"%w: adapter %s: the hook does not know the row type %s",
			errUnknownRouteShape,
			adapter,
			row.Type(),
		)
	}

	method, path := row.FieldByName("Method"), row.FieldByName("Path")
	if !method.IsValid() || !path.IsValid() || method.Kind() != reflect.String || path.Kind() != reflect.String {
		return RouteDeclaration{}, fmt.Errorf(
			"%w: adapter %s: the row type %s has no string Method and Path",
			errUnknownRouteShape,
			adapter,
			row.Type(),
		)
	}

	pattern := method.String() + " " + path.String()

	scope, err := scopeOf(adapter, pattern, row)
	if err != nil {
		return RouteDeclaration{}, err
	}

	fields, err := enforcedFieldsOf(adapter, pattern, row)
	if err != nil {
		return RouteDeclaration{}, err
	}

	return RouteDeclaration{Adapter: adapter, Pattern: pattern, Scope: scope, EnforcedFields: fields}, nil
}

// scopeOf reads the scope of a row.
func scopeOf(adapter, pattern string, row reflect.Value) (RouteScope, error) {
	field := row.FieldByName("Scope")
	if !field.IsValid() {
		if row.Type() == reflect.TypeFor[tofuhttp.Route]() {
			return RouteScopeNone, nil
		}

		return "", fmt.Errorf(
			"%w: adapter %s, route %q: the row type %s has no Scope field",
			errUnknownRouteShape,
			adapter,
			pattern,
			row.Type(),
		)
	}

	values, ok := knownScopes()[field.Type()]
	if !ok || !field.CanInt() {
		return "", fmt.Errorf(
			"%w: adapter %s, route %q: the hook does not know the scope type %s",
			errUnknownRouteShape,
			adapter,
			pattern,
			field.Type(),
		)
	}

	switch value := field.Int(); {
	case slices.Contains(values.public, value):
		return RouteScopePublic, nil
	case slices.Contains(values.signedIn, value):
		return RouteScopeSignedIn, nil
	case slices.Contains(values.credential, value):
		return RouteScopeCredential, nil
	default:
		return "", fmt.Errorf(
			"%w: adapter %s, route %q: the hook does not know the scope value %d of %s",
			errUnknownRouteShape,
			adapter,
			pattern,
			value,
			field.Type(),
		)
	}
}

// enforcedFieldsOf returns the names of the enforced action fields of a row
// that hold an action. It returns an error for a list of actions in a field
// that is on neither list of names, and for a function that returns actions
// and is not ActionsFor.
func enforcedFieldsOf(adapter, pattern string, row reflect.Value) ([]string, error) {
	var out []string

	actionList := reflect.TypeFor[[]identity.Action]()
	rowType := row.Type()

	for i := range row.NumField() {
		field, info := row.Field(i), rowType.Field(i)

		switch {
		case field.Type() == actionList:
			counted, err := countField(adapter, pattern, rowType, info.Name, field.Len())
			if err != nil {
				return nil, err
			}

			if counted {
				out = append(out, info.Name)
			}
		case info.Anonymous && field.Kind() == reflect.Struct:
			inner, err := enforcedFieldsOf(adapter, pattern, field)
			if err != nil {
				return nil, err
			}

			out = append(out, inner...)
		case field.Kind() == reflect.Func && returnsActions(field.Type()) && info.Name != "ActionsFor":
			return nil, fmt.Errorf(
				"%w: adapter %s, route %q: the hook does not know the function field %s of %s",
				errUnknownRouteShape,
				adapter,
				pattern,
				info.Name,
				rowType,
			)
		default:
		}
	}

	return out, nil
}

// countField says whether a list of actions in a field counts as a declared
// action: the field is enforced and holds an action. It returns an error for a
// field that is on neither list of names.
func countField(adapter, pattern string, rowType reflect.Type, name string, length int) (bool, error) {
	switch {
	case slices.Contains(enforcedActionFields(), name):
		return length > 0, nil
	case slices.Contains(unenforcedActionFields(), name):
		return false, nil
	default:
		return false, fmt.Errorf("%w: adapter %s, route %q: the hook does not know the action field %s of %s",
			errUnknownRouteShape, adapter, pattern, name, rowType)
	}
}

// returnsActions reports whether a function type returns a list of actions.
func returnsActions(fn reflect.Type) bool {
	for out := range fn.Outs() {
		if out == reflect.TypeFor[[]identity.Action]() {
			return true
		}
	}

	return false
}
