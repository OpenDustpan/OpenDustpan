package apidesc

import "net/http"

// UnitsStatePath is the route of the masked read of the state of a unit. The
// wildcard ref is the unit reference <tool>:<namespace>/<name>, with each
// slash of it escaped as %2F, so that it fills one path segment.
const UnitsStatePath = Prefix + "units/{ref}/state"

// The query parameters of the masked read of the state of a unit.
const (
	// UnitsStateVersion names the version of the read. Without it, the read
	// answers the current version.
	UnitsStateVersion = "version"
	// UnitsStateCursor is the cursor of the next page, from an earlier page of
	// the same read.
	UnitsStateCursor = "cursor"
)

// MaskedStateMarker is the marker that stands for one secret value of a
// state version in the masked read. Path is the JSON Pointer value path of the
// value. Secret is always true: the answer never holds the value itself.
type MaskedStateMarker struct {
	Secret bool   `json:"secret"`
	Path   string `json:"path"`
}

// UnitStatePage is the body of one page of the masked read of the state of a
// unit. Unit is the unit reference, Version is the resolved version of the
// state, Markers and ValuePaths hold the markers of this page in the same
// order, and NextCursor is the cursor of the next page, or empty on the last
// page.
type UnitStatePage struct {
	Unit       string              `json:"unit"`
	Version    string              `json:"version"`
	Markers    []MaskedStateMarker `json:"markers"`
	ValuePaths []string            `json:"valuePaths"`
	NextCursor string              `json:"nextCursor"`
}

// UnitsStateLabel returns the label of the masked read of the state of a unit.
// The marker has its own named schema, so that no other route changes its
// schema.
func UnitsStateLabel() Label {
	return Name("getUnitState", "state").
		On(KindUnit).
		WithQuery(
			StringParam(UnitsStateVersion, "The version of the state: the number of a Pulumi update, "+
				"or the ID of an OpenTofu state version. Without it, the read answers the current version."),
			StringParam(UnitsStateCursor, "The cursor of the next page, from the nextCursor of an earlier page."),
		).
		WithBodies(NoBody, Typed[UnitStatePage]("UnitStatePage", Named[MaskedStateMarker]("MaskedStateMarker")))
}

// UnitsStateLabels returns the label table of the masked read, keyed by
// Pattern.
func UnitsStateLabels() map[string]Label {
	return map[string]Label{Pattern(http.MethodGet, UnitsStatePath): UnitsStateLabel()}
}
