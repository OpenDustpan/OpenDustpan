package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Masked state read for the console: a paged resource list and one resource
// of a state version. A read replaces each secret value with a marker that
// holds the value path and never the value (ADR 41 decisions 1 to 3 and 8).

const (
	// maxTofuStateLimit bounds one page of the resource list.
	maxTofuStateLimit = 500
	// defaultTofuStateLimit answers the resource list when the client sends no
	// page size.
	defaultTofuStateLimit = 100
	// encryptedStateKey marks a state file the client encrypted: the plain
	// serial and lineage travel outside the encrypted payload under other
	// keys, and the resources hide inside this envelope.
	encryptedStateKey = "encrypted_data"
	// markerSecretKey and markerPathKey are the fields of a secret marker: a
	// value with markerSecretKey set answers as a secret with its value path
	// in markerPathKey, never as plain text.
	markerSecretKey = "secret"
	markerPathKey   = "path"
)

// SensitiveMarks holds the secret paths of one state version, by resource
// instance address: each entry names the JSON Pointer paths, relative to the
// attributes of that instance, that the json-state rendering of the version
// marked sensitive. It holds no value, only paths.
type SensitiveMarks map[string][]string

// StateResource is one entry of the masked resource list: the address as
// stored, the type, the provider, the module and whether it differs from the
// previous version or is new. Module is "" for the root module.
type StateResource struct {
	Address  string
	Type     string
	Provider string
	Module   string
	Changed  bool
}

// StatePage is one page of the masked resource list. NextCursor names the
// cursor of the next page, or is empty when the page is the last one.
// Incomplete says the server cut the list short of paging, for example when
// the state holds entries the server skips.
type StatePage struct {
	Resources  []StateResource
	NextCursor string
	Incomplete bool
}

// StateInstance is one instance of a masked resource with its attributes.
// Each secret value answers as a marker, never as plain text.
type StateInstance struct {
	Attributes any
}

// StateDetail is one resource with its masked instances. Masked holds the
// value path of each marker in the instances. It is authoritative: a real
// value with the shape of a marker but no entry in Masked is a real value.
type StateDetail struct {
	Address   string
	Type      string
	Provider  string
	Module    string
	Instances []StateInstance
	Masked    []string
}

// escapeTofuPointerToken escapes one JSON Pointer token (RFC 6901): `~`
// first, then `/`.
func escapeTofuPointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// decodeState decodes state into a generic object with numbers kept exact.
func decodeState(state []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(state))
	dec.UseNumber()

	var doc any

	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}

	return doc, nil
}

// IsClientEncrypted reports whether state is a state file the client
// encrypted: its resources hide inside the encrypted envelope, so the
// resource routes cannot read it. The outputs list still works, because the
// server keeps the outputs the client declared at creation.
func IsClientEncrypted(state []byte) (bool, error) {
	doc, err := decodeState(state)
	if err != nil {
		return false, err
	}

	top, ok := doc.(map[string]any)
	if !ok {
		return false, nil
	}

	_, encrypted := top[encryptedStateKey]

	return encrypted, nil
}

// tofuResourceEntry is one parsed resource of a state file with its position.
type tofuResourceEntry struct {
	obj   map[string]any
	index int
}

// tofuStateResources reads the resources array of doc. Entries that are not
// objects with a string type and name are skipped; the second result says
// whether the document held entries the server skipped.
func tofuStateResources(doc any) ([]tofuResourceEntry, bool) {
	top, ok := doc.(map[string]any)
	if !ok {
		return nil, true
	}

	raw := top["resources"]
	if raw == nil {
		return nil, false
	}

	list, ok := raw.([]any)
	if !ok {
		return nil, true
	}

	var entries []tofuResourceEntry

	skipped := false

	for i, elem := range list {
		obj, ok := elem.(map[string]any)
		if !ok {
			skipped = true

			continue
		}

		if tofuField(obj, "type") == "" || tofuField(obj, "name") == "" {
			skipped = true

			continue
		}

		entries = append(entries, tofuResourceEntry{obj: obj, index: i})
	}

	return entries, skipped
}

// tofuField reads one string field of a resource, or "" when the field is
// missing or of another shape.
func tofuField(obj map[string]any, key string) string {
	text, ok := obj[key].(string)
	if !ok {
		return ""
	}

	return text
}

// tofuResourceBase is the address of a resource without an instance index:
// the module prefix, the data prefix for data resources, and the type and
// name. The empty module is the root module.
func tofuResourceBase(obj map[string]any) string {
	var base strings.Builder

	if module := tofuField(obj, "module"); module != "" {
		base.WriteString(module)
		base.WriteString(".")
	}

	if tofuField(obj, "mode") == "data" {
		base.WriteString("data.")
	}

	base.WriteString(tofuField(obj, "type"))
	base.WriteString(".")
	base.WriteString(tofuField(obj, "name"))

	return base.String()
}

// tofuInstanceAddress is the address of one instance of a resource: the
// resource base with the index key of a count or for_each instance. A single
// instance carries no suffix.
func tofuInstanceAddress(obj, instance map[string]any) string {
	base := tofuResourceBase(obj)

	key, ok := instance["index_key"]
	if !ok || key == nil {
		return base
	}

	switch typed := key.(type) {
	case json.Number:
		return base + "[" + typed.String() + "]"
	case string:
		return base + "[" + strconv.Quote(typed) + "]"
	default:
		return base
	}
}

// tofuInstances reads the instances array of a resource. Entries that are
// not objects are skipped.
func tofuInstances(obj map[string]any) []map[string]any {
	raw, ok := obj["instances"].([]any)
	if !ok {
		return nil
	}

	var out []map[string]any

	for _, elem := range raw {
		instance, ok := elem.(map[string]any)
		if !ok {
			continue
		}

		out = append(out, instance)
	}

	return out
}

// tofuFingerprint returns the comparison shape of one resource instance: the
// raw attributes as encoded. A changed secret still reads changed: the
// comparison never leaks a value, only the changed flag of the list.
func tofuFingerprint(instance map[string]any) string {
	raw, err := json.Marshal(instance["attributes"])
	if err != nil {
		return fmt.Sprintf("%v", instance["attributes"])
	}

	return string(raw)
}

// tofuOffset parses a list cursor: "" starts at the first resource.
func tofuOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: resource cursor %q", ErrInvalidState, cursor)
	}

	return n, nil
}

// tofuLimit normalizes a page size: zero or negative means the default page.
func tofuLimit(size int) (int, error) {
	if size <= 0 {
		return defaultTofuStateLimit, nil
	}

	if size > maxTofuStateLimit {
		return 0, fmt.Errorf("%w: resource page size %d, want 1 to %d", ErrInvalidState, size, maxTofuStateLimit)
	}

	return size, nil
}

// previousTofuFingerprints maps each resource address of previous to the
// fingerprints of its instances, or holds nothing when previous is nil or
// parses no resources.
func previousTofuFingerprints(previous []byte) map[string][]string {
	marks := map[string][]string{}

	if len(previous) == 0 {
		return marks
	}

	doc, err := decodeState(previous)
	if err != nil {
		return marks
	}

	entries, _ := tofuStateResources(doc)

	for _, entry := range entries {
		base := tofuResourceBase(entry.obj)

		for _, instance := range tofuInstances(entry.obj) {
			marks[base] = append(marks[base], tofuFingerprint(instance))
		}
	}

	return marks
}

// tofuChanged reports whether a resource is new or differs from previous:
// an address the previous state holds with other fingerprints reads changed.
func tofuChanged(base string, instances []map[string]any, marks map[string][]string) bool {
	want, ok := marks[base]
	if !ok {
		return true
	}

	if len(want) != len(instances) {
		return true
	}

	got := make([]string, 0, len(instances))
	for _, instance := range instances {
		got = append(got, tofuFingerprint(instance))
	}

	slices.Sort(got)
	sorted := append([]string(nil), want...)
	slices.Sort(sorted)

	return !slices.Equal(got, sorted)
}

// matchTofuFilter reports whether a resource matches the list filter: an
// empty filter matches every resource, otherwise the filter is a substring
// of the address or of the resource type. It never matches attribute values.
func matchTofuFilter(address, typ, filter string) bool {
	if filter == "" {
		return true
	}

	return strings.Contains(address, filter) || strings.Contains(typ, filter)
}

// ListStateResources returns one page of the masked resource list of the
// current state. Previous is the state of the previous version, or nil when
// there is none: every resource then counts as new. The list answers the
// structure with no attribute value, so it needs no marks. Cursor is "" or
// the NextCursor of an earlier page; size is the page size, or zero for the
// default page. Filter is a substring of the address or of the resource
// type, or "" for no filter: it never matches attribute values. Resources
// keep the order of the state; matching across versions uses the address.
// Paging applies after the filter, so a page holds only matching resources.
func ListStateResources(
	current, previous []byte, cursor string, size int, filter string,
) (StatePage, error) {
	offset, err := tofuOffset(cursor)
	if err != nil {
		return StatePage{}, err
	}

	limit, err := tofuLimit(size)
	if err != nil {
		return StatePage{}, err
	}

	doc, err := decodeState(current)
	if err != nil {
		return StatePage{}, err
	}

	entries, skipped := tofuStateResources(doc)
	if entries == nil && skipped {
		return StatePage{Resources: []StateResource{}, NextCursor: "", Incomplete: true}, nil
	}

	fingerprints := previousTofuFingerprints(previous)

	summaries := make([]StateResource, 0, len(entries))

	for _, entry := range entries {
		address := tofuResourceBase(entry.obj)
		typ := tofuField(entry.obj, "type")

		if !matchTofuFilter(address, typ, filter) {
			continue
		}

		instances := tofuInstances(entry.obj)
		summaries = append(summaries, StateResource{
			Address:  address,
			Type:     typ,
			Provider: tofuField(entry.obj, "provider"),
			Module:   tofuField(entry.obj, "module"),
			Changed:  tofuChanged(address, instances, fingerprints),
		})
	}

	page := StatePage{Resources: []StateResource{}, NextCursor: "", Incomplete: skipped}

	if offset >= len(summaries) {
		return page, nil
	}

	end := min(offset+limit, len(summaries))
	page.Resources = summaries[offset:end]

	if end < len(summaries) {
		page.NextCursor = strconv.Itoa(end)
	}

	return page, nil
}

// ctyPathToPointer renders one cty path of sensitive_attributes as a JSON
// Pointer relative to the attributes. Each step is an attribute name, an
// array index, or one cty path step object of the state file: get_attr with
// a string value names an attribute, index with a numeric value names an
// array position and with a string value names a map key. Older states may
// hold the step directly as a string or a number. A path of another shape
// reports false.
func ctyPathToPointer(path []any) (string, bool) {
	var base strings.Builder

	for _, step := range path {
		token, ok := ctyStepToken(step)
		if !ok {
			return "", false
		}

		base.WriteString("/")
		base.WriteString(escapeTofuPointerToken(token))
	}

	return base.String(), true
}

// ctyStepToken renders one cty path step as a pointer token: a plain string
// or number step, or a step object the CLI writes. An index step with a
// numeric value answers the decimal position.
func ctyStepToken(step any) (string, bool) {
	switch typed := step.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case map[string]any:
		return ctyObjectToken(typed)
	default:
		return "", false
	}
}

// ctyObjectToken renders one cty path step object as a pointer token: a
// get_attr step with a string value, or an index step with a numeric or
// string value. A step of another type reports false.
func ctyObjectToken(step map[string]any) (string, bool) {
	kind, ok := step["type"].(string)
	if !ok {
		return "", false
	}

	switch kind {
	case "get_attr":
		name, ok := step["value"].(string)
		if !ok {
			return "", false
		}

		return name, true
	case "index":
		switch key := step["value"].(type) {
		case json.Number:
			return key.String(), true
		case string:
			return key, true
		default:
			return "", false
		}
	default:
		return "", false
	}
}

// tofuSensitivePointers returns the attribute-relative JSON Pointers that
// the instance marks sensitive: each path of sensitive_attributes, and each
// kept mark of the instance address or its resource base.
func tofuSensitivePointers(instance map[string]any, address, base string, marks SensitiveMarks) []string {
	var out []string

	if raw, ok := instance["sensitive_attributes"].([]any); ok {
		for _, elem := range raw {
			steps, ok := elem.([]any)
			if !ok {
				continue
			}

			if pointer, ok := ctyPathToPointer(steps); ok {
				out = append(out, pointer)
			}
		}
	}

	for _, key := range []string{address, base} {
		out = append(out, marks[key]...)
	}

	return out
}

// newTofuMarker returns a secret marker for path: a value that answers as a
// secret with its value path, never as plain text.
func newTofuMarker(path string) map[string]any {
	return map[string]any{markerSecretKey: true, markerPathKey: path}
}

// splitPointer renders a relative pointer as unescaped tokens.
func splitPointer(relative string) []string {
	tokens := strings.Split(strings.TrimPrefix(relative, "/"), "/")

	unescaped := make([]string, 0, len(tokens))
	for _, token := range tokens {
		unescaped = append(unescaped, strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~"))
	}

	return unescaped
}

// dynamicWrapperSize is the key count of a wrapped dynamic value: "value"
// and "type".
const dynamicWrapperSize = 2

// isDynamicWrapper reports whether obj is a dynamic value of the state file:
// an object the CLI stores as its value under "value" with its type under
// "type", as for the input and the output of a terraform_data resource. The
// json-state rendering answers the value unwrapped, so a mark below such an
// object names the path inside the wrapped value.
func isDynamicWrapper(obj map[string]any) bool {
	if len(obj) != dynamicWrapperSize {
		return false
	}

	_, hasValue := obj["value"]

	_, hasType := obj["type"]

	return hasValue && hasType
}

// maskTokens replaces the value at tokens under node with a marker for path,
// and reports whether it replaced anything. A missing key reads as no
// replacement, except inside a dynamic wrapper: the mark names the unwrapped
// path, so one level of "value" is transparent. A mark for a path the state
// does not hold masks nothing.
func maskTokens(node any, tokens []string, path string) bool {
	if len(tokens) == 0 {
		return false
	}

	switch typed := node.(type) {
	case map[string]any:
		return maskMapAt(typed, tokens, path)
	case []any:
		return maskListAt(typed, tokens, path)
	default:
		return false
	}
}

// maskMapAt replaces the value at tokens under doc with a marker for path.
func maskMapAt(doc map[string]any, tokens []string, path string) bool {
	if len(tokens) == 1 {
		if _, ok := doc[tokens[0]]; !ok {
			return false
		}

		doc[tokens[0]] = newTofuMarker(path)

		return true
	}

	next, ok := doc[tokens[0]]
	if !ok || next == nil {
		return false
	}

	if maskTokens(next, tokens[1:], path) {
		return true
	}

	if wrapped, ok := next.(map[string]any); ok && isDynamicWrapper(wrapped) {
		return maskTokens(wrapped["value"], tokens[1:], path)
	}

	return false
}

// maskListAt replaces the value at tokens under list with a marker for path.
// An index past the end reads as no replacement.
func maskListAt(list []any, tokens []string, path string) bool {
	index, err := strconv.Atoi(tokens[0])
	if err != nil || index < 0 || index >= len(list) {
		return false
	}

	if len(tokens) == 1 {
		list[index] = newTofuMarker(path)

		return true
	}

	return maskTokens(list[index], tokens[1:], path)
}

// maskPointer replaces the value at the relative pointer under attributes
// with a marker for the full value path, and reports whether it replaced
// anything.
func maskPointer(attributes any, relative, full string) bool {
	return maskTokens(attributes, splitPointer(relative), full)
}

// lookupTokens returns the value at tokens under node, or false when the
// node does not hold the path. It follows the same transparent dynamic
// wrapper as the masking, so the caller snapshots what a mark masks.
func lookupTokens(node any, tokens []string) (any, bool) {
	if len(tokens) == 0 {
		return node, true
	}

	switch typed := node.(type) {
	case map[string]any:
		return lookupMapAt(typed, tokens)
	case []any:
		return lookupListAt(typed, tokens)
	default:
		return nil, false
	}
}

// lookupMapAt returns the value at tokens under doc.
func lookupMapAt(doc map[string]any, tokens []string) (any, bool) {
	next, present := doc[tokens[0]]
	if !present {
		return nil, false
	}

	if len(tokens) == 1 {
		return next, true
	}

	if found, foundOK := lookupTokens(next, tokens[1:]); foundOK {
		return found, true
	}

	if wrapped, wrappedOK := next.(map[string]any); wrappedOK && isDynamicWrapper(wrapped) {
		return lookupTokens(wrapped["value"], tokens[1:])
	}

	return nil, false
}

// lookupListAt returns the value at tokens under list.
func lookupListAt(list []any, tokens []string) (any, bool) {
	index, err := strconv.Atoi(tokens[0])
	if err != nil || index < 0 || index >= len(list) {
		return nil, false
	}

	if len(tokens) == 1 {
		return list[index], true
	}

	return lookupTokens(list[index], tokens[1:])
}

// lookupPointer returns the value at the relative pointer under attributes,
// or false when the attributes do not hold the path.
func lookupPointer(attributes any, relative string) (any, bool) {
	return lookupTokens(attributes, splitPointer(relative))
}

// propagableSecret reports whether a masked value propagates to its
// duplicates: a non-empty string or a non-empty object or array. A duplicate
// of such a value answers masked too, so a derived copy no mark names still
// reads as a marker. Bare numbers, booleans and nulls rely on their explicit
// marks: their duplicates stay plain.
func propagableSecret(value any) bool {
	switch typed := value.(type) {
	case string:
		return typed != ""
	case map[string]any:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return false
	}
}

// maskDuplicateAt replaces child with a marker for path when it duplicates a
// masked secret, and reports whether it replaced anything.
func maskDuplicateAt(child any, path string, seen map[string]bool, masked *[]string, secrets []any) bool {
	if seen[path] {
		return false
	}

	for _, secret := range secrets {
		if reflect.DeepEqual(child, secret) {
			*masked = append(*masked, path)

			return true
		}
	}

	return false
}

// maskDuplicates masks every value under node that duplicates a masked
// secret: the CLI copies a sensitive input into a derived attribute the
// rendering does not mark, as the output of a terraform_data resource, so
// the copy answers masked too. Path is the value path of node; seen holds
// the paths already masked, and masked collects each new marker path.
func maskDuplicates(node any, path string, seen map[string]bool, masked *[]string, secrets []any) {
	switch typed := node.(type) {
	case map[string]any:
		maskDuplicateMap(typed, path, seen, masked, secrets)
	case []any:
		maskDuplicateList(typed, path, seen, masked, secrets)
	default:
	}
}

// maskDuplicateMap masks each duplicating entry of doc.
func maskDuplicateMap(
	doc map[string]any, path string, seen map[string]bool, masked *[]string, secrets []any,
) {
	for key, child := range doc {
		childPath := path + "/" + escapeTofuPointerToken(key)

		if maskDuplicateAt(child, childPath, seen, masked, secrets) {
			doc[key] = newTofuMarker(childPath)
			seen[childPath] = true

			continue
		}

		maskDuplicates(child, childPath, seen, masked, secrets)
	}
}

// maskDuplicateList masks each duplicating element of list.
func maskDuplicateList(
	list []any, path string, seen map[string]bool, masked *[]string, secrets []any,
) {
	for i, child := range list {
		childPath := path + "/" + strconv.Itoa(i)

		if maskDuplicateAt(child, childPath, seen, masked, secrets) {
			list[i] = newTofuMarker(childPath)
			seen[childPath] = true

			continue
		}

		maskDuplicates(child, childPath, seen, masked, secrets)
	}
}

// maskAllLeaves replaces each leaf value under v with a marker for its path,
// and appends each marker path to masked. Containers keep their shape.
func maskAllLeaves(v any, base string, masked *[]string) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, elem := range typed {
			out[key] = maskAllLeaves(elem, base+"/"+escapeTofuPointerToken(key), masked)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, elem := range typed {
			out[i] = maskAllLeaves(elem, base+"/"+strconv.Itoa(i), masked)
		}

		return out
	default:
		*masked = append(*masked, base)

		return newTofuMarker(base)
	}
}

// maskTofuInstance returns the masked attributes of one instance at position
// of the resource at resource index in the state: each path that
// sensitive_attributes or the kept marks name answers as a marker with its
// value path. A value that duplicates a masked secret answers masked too,
// so a derived copy no mark names still reads as a marker. With no kept
// marks for the version, every attribute value answers as a marker. The
// second result holds each marker path, without duplicates.
func maskTofuInstance(
	instance map[string]any, resource, position int, address string, marks SensitiveMarks,
) (any, []string) {
	base := "/resources/" + strconv.Itoa(resource) + "/instances/" + strconv.Itoa(position) + "/attributes"

	attributes := instance["attributes"]
	if attributes == nil {
		attributes = map[string]any{}
	}

	if len(marks) == 0 {
		var masked []string

		out := maskAllLeaves(attributes, base, &masked)
		if masked == nil {
			masked = []string{}
		}

		return out, masked
	}

	seen := map[string]bool{}

	var masked []string

	var secrets []any

	for _, relative := range tofuSensitivePointers(instance, address, tofuInstanceBase(address), marks) {
		full := base + relative
		if seen[full] {
			continue
		}

		original, found := lookupPointer(attributes, relative)

		if maskPointer(attributes, relative, full) {
			seen[full] = true
			masked = append(masked, full)

			if found && propagableSecret(original) {
				secrets = append(secrets, original)
			}
		}
	}

	if len(secrets) > 0 {
		maskDuplicates(attributes, base, seen, &masked, secrets)
	}

	if masked == nil {
		masked = []string{}
	}

	return attributes, masked
}

// tofuInstanceBase strips one trailing instance index from an address, so a
// kept mark for the resource base still matches an indexed instance.
func tofuInstanceBase(address string) string {
	open := strings.LastIndex(address, "[")
	if open < 0 || !strings.HasSuffix(address, "]") {
		return address
	}

	return address[:open]
}

// findTofuResource returns the position and the object of the resource with
// address, or ErrResourceNotFound. The address matches verbatim: the
// resource base for a single instance, or one instance address.
func findTofuResource(doc any, address string) (int, map[string]any, error) {
	entries, _ := tofuStateResources(doc)

	for _, entry := range entries {
		base := tofuResourceBase(entry.obj)
		if base == address {
			return entry.index, entry.obj, nil
		}

		for _, instance := range tofuInstances(entry.obj) {
			if tofuInstanceAddress(entry.obj, instance) == address {
				return entry.index, entry.obj, nil
			}
		}
	}

	return 0, nil, fmt.Errorf("%w: %q", ErrResourceNotFound, address)
}

// ReadStateResource returns one resource of state with its instances masked.
// Missing attributes read as an empty object.
func ReadStateResource(state []byte, marks SensitiveMarks, address string) (StateDetail, error) {
	doc, err := decodeState(state)
	if err != nil {
		return StateDetail{}, err
	}

	position, obj, err := findTofuResource(doc, address)
	if err != nil {
		return StateDetail{}, err
	}

	base := tofuResourceBase(obj)

	detail := StateDetail{
		Address:   base,
		Type:      tofuField(obj, "type"),
		Provider:  tofuField(obj, "provider"),
		Module:    tofuField(obj, "module"),
		Instances: []StateInstance{},
		Masked:    []string{},
	}

	for i, instance := range tofuInstances(obj) {
		attributes, masked := maskTofuInstance(instance, position, i, tofuInstanceAddress(obj, instance), marks)
		detail.Instances = append(detail.Instances, StateInstance{Attributes: attributes})
		detail.Masked = append(detail.Masked, masked...)
	}

	if detail.Masked == nil {
		detail.Masked = []string{}
	}

	return detail, nil
}

// showModule is one module of the json-state rendering: its resources and
// its child modules.
type showModule struct {
	Resources    []showResource `json:"resources"`
	ChildModules []showModule   `json:"child_modules"` //nolint:tagliatelle // The OpenTofu wire name; it must not change.
}

// showResource is one resource instance of the json-state rendering with its
// values and the matching sensitive marks.
type showResource struct {
	Address         string `json:"address"`
	Values          any    `json:"values"`
	SensitiveValues any    `json:"sensitive_values"` //nolint:tagliatelle // The OpenTofu wire name; it must not change.
}

// collectShowResources appends every resource of module and its children, in
// document order.
func collectShowResources(module showModule, out *[]showResource) {
	*out = append(*out, module.Resources...)

	for _, child := range module.ChildModules {
		collectShowResources(child, out)
	}
}

// sensitiveLeaves appends the relative JSON Pointers of the values that
// sensitive marks: each true leaf of sensitive with the matching shape of
// values. Map keys escape per RFC 6901; array indices stay decimal.
func sensitiveLeaves(values, sensitive any, base string, out *[]string) {
	if flag, ok := sensitive.(bool); ok {
		if flag {
			*out = append(*out, base)
		}

		return
	}

	sensitiveMapLeaves(values, sensitive, base, out)
	sensitiveListLeaves(values, sensitive, base, out)
}

// sensitiveMapLeaves appends the marked paths of two objects: each key the
// sensitive object names.
func sensitiveMapLeaves(values, sensitive any, base string, out *[]string) {
	valuesMap, valuesOK := values.(map[string]any)
	sensitiveMap, sensitiveOK := sensitive.(map[string]any)

	if !valuesOK || !sensitiveOK {
		return
	}

	for key, marked := range sensitiveMap {
		sensitiveLeaves(valuesMap[key], marked, base+"/"+escapeTofuPointerToken(key), out)
	}
}

// sensitiveListLeaves appends the marked paths of two arrays: each index the
// sensitive array names within the values array.
func sensitiveListLeaves(values, sensitive any, base string, out *[]string) {
	valuesList, valuesOK := values.([]any)
	sensitiveList, sensitiveOK := sensitive.([]any)

	if !valuesOK || !sensitiveOK {
		return
	}

	for i := range sensitiveList {
		if i >= len(valuesList) {
			break
		}

		sensitiveLeaves(valuesList[i], sensitiveList[i], base+"/"+strconv.Itoa(i), out)
	}
}

// showValues is the values object of the json-state rendering: the root
// module the resources hang under.
type showValues struct {
	RootModule showModule `json:"root_module"` //nolint:tagliatelle // The OpenTofu wire name; it must not change.
}

// showDocument is the json-state rendering of a state version: only the
// values carry marks, so only they are decoded.
type showDocument struct {
	Values showValues `json:"values"`
}

// ExtractSensitiveMarks reads the paths that sensitive_values marks in the
// json-state rendering of a state version, by resource instance address. It
// keeps only the paths and never a value. A rendering without values reads
// as no marks: the read then masks every attribute value.
func ExtractSensitiveMarks(rendering []byte) (SensitiveMarks, error) {
	var doc showDocument

	dec := json.NewDecoder(bytes.NewReader(rendering))
	dec.UseNumber()

	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode json-state: %w", err)
	}

	var resources []showResource

	collectShowResources(doc.Values.RootModule, &resources)

	marks := SensitiveMarks{}

	for _, resource := range resources {
		if resource.Address == "" || resource.SensitiveValues == nil {
			continue
		}

		var paths []string

		sensitiveLeaves(resource.Values, resource.SensitiveValues, "", &paths)

		if len(paths) > 0 {
			marks[resource.Address] = append(marks[resource.Address], paths...)
		}
	}

	return marks, nil
}
