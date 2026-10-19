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

	return f, nil
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
