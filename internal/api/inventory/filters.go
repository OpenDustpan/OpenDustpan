package inventory

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var (
	errLabelFormat        = errors.New("use key=value")
	errLabelKey           = errors.New("the key has 1 to 128 characters: letters, digits and . _ : / -")
	errLabelValue         = errors.New("the value has at most 256 characters and no control character")
	errReferenceFormat    = errors.New("use a unit reference, for example pulumi:acme/web/prod")
	errReferenceNamespace = errors.New("the reference must name the namespace of the path")
)

// labelFilter is one key and value that a unit must have.
type labelFilter struct {
	key   string
	value string
}

// unitFilter holds the filters of a unit list. A unit matches when it has
// each label and, if the filter names references, when its reference is one
// of them.
type unitFilter struct {
	references map[string]struct{}
	labels     []labelFilter
}

// active reports whether the filter selects anything.
func (f unitFilter) active() bool {
	return len(f.labels) > 0 || f.references != nil
}

// matches reports whether the unit e, with the authorization object uobj,
// passes the filter.
func (f unitFilter) matches(e coreinventory.Entry, uobj identity.Object) bool {
	if f.references != nil {
		if _, ok := f.references[uobj.String()]; !ok {
			return false
		}
	}

	for _, want := range f.labels {
		if got, ok := e.Labels[want.key]; !ok || got != want.value {
			return false
		}
	}

	return true
}

// parseUnitFilter reads the label and reference query values of a unit list
// in the namespace ns. Each label value is key=value. Each reference value is
// a unit reference in ns. A bad value answers 400.
func parseUnitFilter(r *http.Request, ns namespace.Name) (unitFilter, error) {
	var f unitFilter

	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return unitFilter{}, fmt.Errorf("%w: the query string is not valid", errInvalidQuery)
	}

	rawLabels := query["label"]
	if len(rawLabels) > coreinventory.MaxLabelFilters {
		return unitFilter{}, fmt.Errorf(
			"%w: send at most %d label query values", errInvalidQuery, coreinventory.MaxLabelFilters,
		)
	}

	for i, raw := range rawLabels {
		pair, err := parseLabelFilter(raw)
		if err != nil {
			return unitFilter{}, fmt.Errorf("%w: label query value %d: %w", errInvalidQuery, i+1, err)
		}

		f.labels = append(f.labels, pair)
	}

	rawRefs := query["reference"]
	if len(rawRefs) > coreinventory.MaxReferenceFilters {
		return unitFilter{}, fmt.Errorf(
			"%w: send at most %d reference query values", errInvalidQuery, coreinventory.MaxReferenceFilters,
		)
	}

	if len(rawRefs) > 0 {
		f.references = make(map[string]struct{}, len(rawRefs))
	}

	for i, raw := range rawRefs {
		obj, err := parseReferenceFilter(raw, ns)
		if err != nil {
			return unitFilter{}, fmt.Errorf("%w: reference query value %d: %w", errInvalidQuery, i+1, err)
		}

		f.references[obj.String()] = struct{}{}
	}

	return f, nil
}

// parseLabelFilter splits raw at the first equals sign and checks both parts.
func parseLabelFilter(raw string) (labelFilter, error) {
	key, value, ok := strings.Cut(raw, "=")
	if !ok {
		return labelFilter{}, errLabelFormat
	}

	if !coreinventory.ValidLabelKey(key) {
		return labelFilter{}, errLabelKey
	}

	if !coreinventory.ValidLabelValue(value) {
		return labelFilter{}, errLabelValue
	}

	return labelFilter{key: key, value: value}, nil
}

// parseReferenceFilter reads a unit reference and checks that it names ns.
func parseReferenceFilter(raw string, ns namespace.Name) (identity.Object, error) {
	obj, err := identity.ParseObject(raw)
	if err != nil || !obj.IsUnit() {
		return identity.Object{}, errReferenceFormat
	}

	if obj.Namespace() != ns {
		return identity.Object{}, errReferenceNamespace
	}

	return obj, nil
}
