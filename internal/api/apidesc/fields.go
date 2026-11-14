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
	// viaPointer is true when the field is promoted through an embedded
	// pointer. The encoder writes none of its fields when that pointer is nil.
	viaPointer bool
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

// isRequired reports whether the member is in the list of required members. A
// field promoted through an embedded pointer is never required: a nil pointer
// leaves it out, and field discovery refuses the tag required on such a field.
func (f field) isRequired() bool {
	if f.viaPointer {
		return false
	}

	switch f.presence {
	case presenceRequired:
		return true
	case presenceOptional, presenceNullable:
		return false
	default:
		return !f.omit
	}
}

// isNullableMap reports whether the field is a map whose nil value the encoder
// writes as null, so that its schema holds null too. A map with omitempty is
// left out when it is nil, and the required and optional tags promise a value
// that is never null, as they do for a pointer.
func (f field) isNullableMap() bool {
	if f.typ.Kind() != reflect.Map {
		return false
	}

	switch f.presence {
	case presenceRequired, presenceOptional:
		return false
	case presenceNullable:
		return true
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
	// viaPointer is true when an embedded pointer is on the way to the type.
	viaPointer bool
}

// structFields returns the fields that encoding/json writes for the struct
// type: the exported fields, the fields of embedded structs one level in, the
// name from the tag, and the winner of two fields with one name. It follows
// the algorithm of encoding/json, so a field that the encoder writes is in
// the list, and a field that it drops is not.
func structFields(t reflect.Type) ([]field, error) {
	var (
		all     []field
		next    = []scan{{typ: t, index: nil, viaPointer: false}}
		visited = map[reflect.Type]bool{}
		// count holds how many times each struct type is reached at the
		// current depth.
		count = map[reflect.Type]int{t: 1}
	)

	for len(next) > 0 {
		lv, err := scanLevel(next, count, visited)
		if err != nil {
			return nil, err
		}

		all = append(all, lv.fields...)
		next, count = lv.next, lv.nextCount
	}

	return dominant(all), nil
}

// level is what the read of one depth of embedded structs finds.
type level struct {
	fields []field
	// next lists the structs of the next depth, and nextCount says how many
	// times each is reached.
	next      []scan
	nextCount map[reflect.Type]int
}

// scanLevel reads the structs of one depth.
func scanLevel(current []scan, count map[reflect.Type]int, visited map[reflect.Type]bool) (level, error) {
	lv := level{fields: nil, next: nil, nextCount: map[reflect.Type]int{}}

	for _, sc := range current {
		if visited[sc.typ] {
			continue
		}

		visited[sc.typ] = true

		fields, deeper, err := scanStruct(sc)
		if err != nil {
			return level{fields: nil, next: nil, nextCount: nil}, err
		}

		lv.fields = append(lv.fields, fields...)
		// A type that two embeddings reach at one depth is listed twice, so
		// that its fields cancel out, as in encoding/json.
		if count[sc.typ] > 1 {
			lv.fields = append(lv.fields, fields...)
		}

		for _, d := range deeper {
			lv.nextCount[d.typ]++
			if lv.nextCount[d.typ] == 1 {
				lv.next = append(lv.next, d)
			}
		}
	}

	return lv, nil
}

// scanStruct reads the fields of one struct on the queue.
func scanStruct(sc scan) ([]field, []scan, error) {
	var (
		fields []field
		deeper []scan
	)

	for i := range sc.typ.NumField() {
		sf := sc.typ.Field(i)

		found, embedded, err := scanField(sc, sf, append(slices.Clone(sc.index), i))
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
func scanField(sc scan, sf reflect.StructField, index []int) ([]field, []scan, error) {
	emb := embeddedStruct(sf)

	parsed, skip, err := readJSONTag(sf)
	if err != nil || skip {
		return nil, nil, err
	}

	if parsed.name == "" && emb.isStruct {
		return nil, []scan{{typ: emb.typ, index: index, viaPointer: sc.viaPointer || emb.isPointer}}, nil
	}

	extra, err := parseDustpanTag(sf.Tag.Get("dustpan"))
	if err != nil {
		return nil, nil, err
	}

	if sc.viaPointer && extra == presenceRequired {
		return nil, nil, fmt.Errorf(
			"%w: the dustpan tag %q cannot apply to a field promoted through an embedded pointer: "+
				"a nil pointer leaves the field out",
			ErrInvalidBody, presenceRequired,
		)
	}

	return []field{{
		name: cmp.Or(parsed.name, sf.Name), typ: sf.Type, omit: parsed.omitempty || parsed.omitzero,
		presence: extra, tagged: parsed.name != "", index: index, viaPointer: sc.viaPointer,
	}}, nil, nil
}

// embedded is the struct that an embedded field holds.
type embedded struct {
	typ reflect.Type
	// isStruct is true when the field is an embedded struct or an embedded
	// pointer to a struct.
	isStruct bool
	// isPointer is true when the field is a pointer.
	isPointer bool
}

// embeddedStruct returns the struct type that an embedded field holds. A
// pointer to a struct counts as the struct.
func embeddedStruct(sf reflect.StructField) embedded {
	ft := sf.Type

	isPointer := ft.Kind() == reflect.Pointer && ft.Name() == ""
	if isPointer {
		ft = ft.Elem()
	}

	return embedded{typ: ft, isStruct: sf.Anonymous && ft.Kind() == reflect.Struct, isPointer: isPointer}
}

// readJSONTag reads the json tag of a field. The bool is true for a field that
// the encoder leaves out. The name is empty when the tag gives no name that
// encoding/json accepts.
func readJSONTag(sf reflect.StructField) (jsonTag, bool, error) {
	raw, hasTag := sf.Tag.Lookup("json")

	if raw == "-" || (!sf.IsExported() && !embeddedStruct(sf).isStruct) {
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
			name:       "",
			typ:        nil,
			omit:       false,
			presence:   "",
			tagged:     false,
			index:      nil,
			viaPointer: false,
		}, false
	}

	return top[0], true
}
