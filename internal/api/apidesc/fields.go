package apidesc

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"
)

// field is one member of the JSON object of a struct.
type field struct {
	name string
	typ  reflect.Type
	// omit is true when the encoder can leave the field out.
	omit bool
	// presence is the word of the dustpan tag of the field, or empty. See
	// parseDustpanTag.
	presence string
	tagged   bool
	index    []int
}

// valueType returns the type whose schema the field holds. A pointer that the
// encoder leaves out when it is nil, and a pointer with the tag required, are
// never null, so the schema is the one of the value.
func (f field) valueType() reflect.Type {
	if f.typ.Kind() != reflect.Pointer {
		return f.typ
	}

	switch f.presence {
	case presenceRequired, presenceOptional:
		return f.typ.Elem()
	case presenceNullable:
		return f.typ
	default:
		if f.omit {
			return f.typ.Elem()
		}

		return f.typ
	}
}

// isRequired reports whether the member is in the list of required members.
func (f field) isRequired() bool {
	switch f.presence {
	case presenceRequired:
		return true
	case presenceOptional, presenceNullable:
		return false
	default:
		return !f.omit
	}
}

// jsonTag is the parsed json tag of a field.
type jsonTag struct {
	name      string
	omitempty bool
	omitzero  bool
	asString  bool
}

func parseJSONTag(tag string) jsonTag {
	name, rest, _ := strings.Cut(tag, ",")
	parsed := jsonTag{name: name, omitempty: false, omitzero: false, asString: false}

	for opt := range strings.SplitSeq(rest, ",") {
		switch opt {
		case "omitempty":
			parsed.omitempty = true
		case "omitzero":
			parsed.omitzero = true
		case "string":
			parsed.asString = true
		default:
			// Other options change nothing that the description shows.
		}
	}

	return parsed
}

// The words of the dustpan tag. The tag says what a handler needs from a field
// of a body that it reads, and it takes the place of the json options for the
// presence of the field:
//
//   - required: the body must hold the field, and its value is never null.
//   - optional: the body can leave the field out, and its value is never null.
//   - nullable: the body can leave the field out or send null.
//
// A handler uses a pointer to tell a missing field from an empty one. A field
// without the tag follows the rules of encoding/json.
const (
	presenceRequired = "required"
	presenceOptional = "optional"
	presenceNullable = "nullable"
)

// parseDustpanTag reads the dustpan tag of a field. It refuses any word other
// than the three above, so a misspelled tag never reads as none.
func parseDustpanTag(tag string) (string, error) {
	switch tag {
	case "", presenceRequired, presenceOptional, presenceNullable:
		return tag, nil
	default:
		return "", fmt.Errorf(
			"%w: the dustpan tag %q is not %s, %s or %s",
			ErrInvalidBody, tag, presenceRequired, presenceOptional, presenceNullable,
		)
	}
}

// scan is a struct type on the queue of the search for fields.
type scan struct {
	typ   reflect.Type
	index []int
}

// structFields returns the fields that encoding/json writes for the struct
// type: the exported fields, the fields of embedded structs one level in, the
// name from the tag, and the winner of two fields with one name. It follows
// the algorithm of encoding/json, so a field that the encoder writes is in
// the list, and a field that it drops is not.
func structFields(t reflect.Type) ([]field, error) {
	var (
		all     []field
		current []scan
		next    = []scan{{typ: t, index: nil}}
		visited = map[reflect.Type]bool{}
	)

	for len(next) > 0 {
		current, next = next, current[:0]

		for _, sc := range current {
			if visited[sc.typ] {
				continue
			}

			visited[sc.typ] = true

			fields, deeper, err := scanStruct(sc)
			if err != nil {
				return nil, err
			}

			all = append(all, fields...)
			next = append(next, deeper...)
		}
	}

	return dominant(all), nil
}

// scanStruct reads the fields of one struct on the queue.
func scanStruct(sc scan) ([]field, []scan, error) {
	var (
		fields []field
		deeper []scan
	)

	for i := range sc.typ.NumField() {
		sf := sc.typ.Field(i)

		found, embedded, err := scanField(sf, append(slices.Clone(sc.index), i))
		if err != nil {
			return nil, nil, fmt.Errorf("%s.%s: %w", sc.typ, sf.Name, err)
		}

		fields = append(fields, found...)
		deeper = append(deeper, embedded...)
	}

	return fields, deeper, nil
}

// scanField reads one struct field. It returns the field to write, or the
// embedded struct to search, or neither.
func scanField(sf reflect.StructField, index []int) ([]field, []scan, error) {
	embedded, isEmbedded := embeddedStruct(sf)

	parsed, skip, err := readJSONTag(sf)
	if err != nil || skip {
		return nil, nil, err
	}

	if parsed.name == "" && isEmbedded {
		return nil, []scan{{typ: embedded, index: index}}, nil
	}

	extra, err := parseDustpanTag(sf.Tag.Get("dustpan"))
	if err != nil {
		return nil, nil, err
	}

	return []field{{
		name: cmp.Or(parsed.name, sf.Name), typ: sf.Type, omit: parsed.omitempty || parsed.omitzero,
		presence: extra, tagged: parsed.name != "", index: index,
	}}, nil, nil
}

// embeddedStruct returns the struct type that an embedded field holds, and
// false for a field that is not an embedded struct. A pointer to a struct
// counts as the struct.
func embeddedStruct(sf reflect.StructField) (reflect.Type, bool) {
	ft := sf.Type
	if ft.Kind() == reflect.Pointer && ft.Name() == "" {
		ft = ft.Elem()
	}

	return ft, sf.Anonymous && ft.Kind() == reflect.Struct
}

// readJSONTag reads the json tag of a field. The bool is true for a field that
// the encoder leaves out. The name is empty when the tag gives no name that
// encoding/json accepts.
func readJSONTag(sf reflect.StructField) (jsonTag, bool, error) {
	raw, hasTag := sf.Tag.Lookup("json")

	_, embedded := embeddedStruct(sf)
	if raw == "-" || (!sf.IsExported() && !embedded) {
		return jsonTag{name: "", omitempty: false, omitzero: false, asString: false}, true, nil
	}

	parsed := parseJSONTag(raw)
	if !hasTag || !validTagName(parsed.name) {
		parsed.name = ""
	}

	if parsed.asString {
		return parsed, false, fmt.Errorf("%w: the string option of the json tag changes the type", ErrInvalidBody)
	}

	return parsed, false, nil
}

// validTagName reports whether a name in a json tag is one that encoding/json
// accepts: letters, digits and a small set of marks.
func validTagName(name string) bool {
	if name == "" {
		return false
	}

	for _, c := range name {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case unicode.IsLetter(c), unicode.IsDigit(c):
		default:
			return false
		}
	}

	return true
}

// dominant resolves fields that share a name. The field at the least depth
// wins. At one depth a tagged field wins. When that does not decide, the name
// is dropped, as encoding/json drops it. The result is in the order of the
// index sequences, which is the order of the declarations.
func dominant(all []field) []field {
	byName := map[string][]field{}

	for _, f := range all {
		byName[f.name] = append(byName[f.name], f)
	}

	var out []field

	for _, group := range byName {
		if winner, ok := winnerOf(group); ok {
			out = append(out, winner)
		}
	}

	slices.SortFunc(out, func(a, b field) int { return slices.Compare(a.index, b.index) })

	return out
}

// winnerOf returns the field of a group of one name that the encoder writes,
// or false when the name is dropped.
func winnerOf(group []field) (field, bool) {
	minDepth := len(group[0].index)
	for _, f := range group {
		minDepth = min(minDepth, len(f.index))
	}

	var top []field

	for _, f := range group {
		if len(f.index) == minDepth {
			top = append(top, f)
		}
	}

	if len(top) > 1 {
		top = slices.DeleteFunc(top, func(f field) bool { return !f.tagged })
	}

	if len(top) != 1 {
		return field{
			name:     "",
			typ:      nil,
			omit:     false,
			presence: "",
			tagged:   false,
			index:    nil,
		}, false
	}

	return top[0], true
}
