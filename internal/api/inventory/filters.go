package inventory

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// maxLabelFilters is the largest number of label query values in one
	// request.
	maxLabelFilters = 10
	// maxReferenceFilters is the largest number of reference query values in
	// one request.
	maxReferenceFilters = 20
	// maxStateFilters is the largest number of state query values in one
	// request. The count is of the raw values, before the removal of duplicates.
	maxStateFilters = 9
	// maxLabelKeyLen and maxLabelValueLen are the largest lengths of a label
	// key and of a label value, in characters.
	maxLabelKeyLen   = 128
	maxLabelValueLen = 256
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
// each label, when its reference is one of the references if the filter names
// any, and when it has any of the states if the filter names any.
type unitFilter struct {
	references map[string]struct{}
	states     map[coreinventory.State]struct{}
	labels     []labelFilter
}

// active reports whether the filter selects anything.
func (f unitFilter) active() bool {
	return len(f.labels) > 0 || f.references != nil || f.states != nil
}

// matches reports whether the unit e, with the authorization object uobj,
// passes the filter.
func (f unitFilter) matches(e coreinventory.Entry, uobj identity.Object) bool {
	if f.references != nil {
		if _, ok := f.references[uobj.String()]; !ok {
			return false
		}
	}

	if f.states != nil && !hasAnyState(e, f.states) {
		return false
	}

	for _, want := range f.labels {
		if got, ok := e.Labels[want.key]; !ok || got != want.value {
			return false
		}
	}

	return true
}

// hasAnyState reports whether the unit e has one of the states in want. It
// uses the derivation of the entry, the one that the response uses.
func hasAnyState(e coreinventory.Entry, want map[coreinventory.State]struct{}) bool {
	for s := range want {
		if e.HasState(s) {
			return true
		}
	}

	return false
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
	if len(rawLabels) > maxLabelFilters {
		return unitFilter{}, fmt.Errorf(
			"%w: send at most %d label query values", errInvalidQuery, maxLabelFilters,
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
	if len(rawRefs) > maxReferenceFilters {
		return unitFilter{}, fmt.Errorf(
			"%w: send at most %d reference query values", errInvalidQuery, maxReferenceFilters,
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

	f.states, err = parseStateFilter(query["state"])
	if err != nil {
		return unitFilter{}, err
	}

	return f, nil
}

// parseStateFilter reads the state query values of a unit list. The match is
// exact. More than maxStateFilters raw values, or a value that is not a unit
// state, gives an error that names the valid values. It returns nil when raw
// is empty, and else the set of states without duplicates.
func parseStateFilter(raw []string) (map[coreinventory.State]struct{}, error) {
	if len(raw) > maxStateFilters {
		return nil, fmt.Errorf(
			"%w: send at most %d state query values; the valid values are %s",
			errInvalidQuery, maxStateFilters, validStates(),
		)
	}

	if len(raw) == 0 {
		return nil, nil //nolint:nilnil // No state value means no state filter; the nil map says so.
	}

	set := make(map[coreinventory.State]struct{}, len(raw))

	for i, value := range raw {
		s, err := coreinventory.ParseState(value)
		if err != nil {
			return nil, fmt.Errorf("%w: state query value %d: %w", errInvalidQuery, i+1, err)
		}

		set[s] = struct{}{}
	}

	return set, nil
}

// validStates returns the valid state values for an error text.
func validStates() string {
	states := coreinventory.States()
	names := make([]string, len(states))

	for i, s := range states {
		names[i] = string(s)
	}

	return strings.Join(names, ", ")
}

// parseLabelFilter splits raw at the first equals sign and checks both parts.
func parseLabelFilter(raw string) (labelFilter, error) {
	key, value, ok := strings.Cut(raw, "=")
	if !ok {
		return labelFilter{}, errLabelFormat
	}

	if !validLabelKey(key) {
		return labelFilter{}, errLabelKey
	}

	if !validLabelValue(value) {
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

// validLabelKey reports whether s is a label key: 1 to maxLabelKeyLen
// letters, digits and the characters . _ : / -.
func validLabelKey(s string) bool {
	if s == "" || len(s) > maxLabelKeyLen {
		return false
	}

	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '/', c == '-':
		default:
			return false
		}
	}

	return true
}

// validLabelValue reports whether s is a label value: 0 to maxLabelValueLen
// characters of valid UTF-8, with no control character.
func validLabelValue(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxLabelValueLen {
		return false
	}

	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}

	return true
}
