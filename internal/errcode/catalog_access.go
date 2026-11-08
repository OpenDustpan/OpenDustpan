package errcode

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
		{Code: AccessDenied, Meaning: "the caller has no permission for the request", Args: nil},
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
