package errcode

import "fmt"

// AccessCollectionUnitCap means that the export of a collection holds more units than its cap allows.
const AccessCollectionUnitCap = "access.collection-unit-cap"

// AccessLastSiteAdministrator means that the change would leave the installation with no site administrator.
const AccessLastSiteAdministrator = "access.last-site-administrator"

// accessSpecs are the codes of a request that the access rules refuse.
func accessSpecs() []Spec {
	return []Spec{
		{
			Code:    AccessCollectionUnitCap,
			Meaning: "the export of a collection holds more units than its cap allows",
			Args: []Arg{
				{Name: "count", Kind: KindQuantity},
				{Name: "cap", Kind: KindQuantity},
				{Name: argKey, Kind: KindIdentifier},
			},
		},
		{
			// A denial that does not carry the action answers without
			// arguments, and the catalog gives every declared argument to
			// every answer of a code. So the code declares none, and the
			// answer of a denial that names the action and the kind of the
			// object takes its two arguments from DeniedArgs.
			Code:    AccessDenied,
			Meaning: "the caller has no permission for the request",
			Args:    nil,
		},
		{
			Code:    AccessIPNotAllowed,
			Meaning: "the request comes from an address outside the IP allowlist of the namespace",
			Args:    nil,
		},
		{
			Code:    AccessLastSiteAdministrator,
			Meaning: "the change would leave the installation with no site administrator",
			Args:    nil,
		},
		{
			Code:    AccessNoAction,
			Meaning: "the route declares no authorization action for the object of the request, so the server refuses it",
			Args:    nil,
		},
	}
}

// DeniedArgs returns the arguments of the answer of a denial: the action that
// the caller lacks and the kind of the object of the action. The code
// AccessDenied declares no argument, because a denial that does not carry the
// action answers without them. It fails for an action or a kind that an
// argument cannot hold, so a caller answers the two arguments only when they
// are valid, and never an argument of another form.
func DeniedArgs(action, objectKind string) (map[string]any, error) {
	values := []Value{Identifier("action", action), Identifier("objectKind", objectKind)}

	args := make(map[string]any, len(values))

	for _, v := range values {
		wire, err := v.wire()
		if err != nil {
			return nil, fmt.Errorf("the denial argument %q: %w", v.name, err)
		}

		args[v.name] = wire
	}

	return args, nil
}
