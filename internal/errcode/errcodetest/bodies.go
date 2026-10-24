package errcodetest

import (
	"errors"
	"fmt"
	"go/ast"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// bodyMembers returns the JSON names of the fields that make a struct type an
// error body: the status or the code of the Pulumi Cloud-compatible shape and
// the tool-neutral shape, the code of the catalog, and the error list of a
// JSON:API document.
func bodyMembers() []string { return []string{"code", "errorCode", "errors"} }

// BodyTypes returns the struct types of the source in dir that have a field
// with the JSON name code, errorCode or errors, at any depth of the type and
// also when a function declares the type, sorted. A gate compares them with the Bodies of an adapter, so that a new
// error body type cannot hide its literals from UncodedPaths. Test files are
// not read.
func BodyTypes(dir string) ([]string, error) {
	files, err := parseDir(dir)
	if err != nil {
		return nil, err
	}

	var out []string

	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if ts, ok := n.(*ast.TypeSpec); ok && hasBodyMember(ts.Type) {
				out = append(out, ts.Name.Name)
			}

			return true
		})
	}

	slices.Sort(out)

	return out, nil
}

// hasBodyMember reports whether a struct type in n has a field with one of
// bodyMembers as its JSON name.
func hasBodyMember(n ast.Node) bool {
	found := false

	ast.Inspect(n, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if ok && field.Tag != nil && slices.Contains(bodyMembers(), jsonName(field.Tag.Value)) {
			found = true
		}

		return !found
	})

	return found
}

// jsonName returns the JSON name of a field tag literal, or "" when the tag
// has no json key.
func jsonName(literal string) string {
	tag, err := strconv.Unquote(literal)
	if err != nil {
		return ""
	}

	name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")

	return name
}

// ErrIgnoreStale is the error of MissingBodies for an ignored type that is
// not a body type of the adapter, or that is also in its Bodies.
var ErrIgnoreStale = errors.New("the ignored type is no ungated body type of the adapter")

// MissingBodies returns the body types of the adapter a (see BodyTypes) that
// are neither in a.Bodies nor in ignored, sorted. Each name of ignored must be
// a body type of the adapter that a.Bodies does not hold.
func MissingBodies(a Adapter, ignored []string) ([]string, error) {
	found, err := BodyTypes(a.Dir)
	if err != nil {
		return nil, err
	}

	for _, name := range ignored {
		if !slices.Contains(found, name) || slices.Contains(a.Bodies, name) {
			return nil, fmt.Errorf("%w: %s.%s", ErrIgnoreStale, a.Name, name)
		}
	}

	var out []string

	for _, name := range found {
		if !slices.Contains(a.Bodies, name) && !slices.Contains(ignored, name) {
			out = append(out, name)
		}
	}

	return out, nil
}
