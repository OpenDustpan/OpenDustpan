package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
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

// StateDiffChanged is one resource present in both versions with a change:
// the address as stored, the resource-relative value paths that changed
// (for example "/attributes/input/value"), and the paths of secrets that
// changed. A secret value is never in the diff: a changed secret is one path
// in ChangedSecrets. The server compares plain values in memory only to
// compute the paths and never answers a value. A path is sensitive when the
// masked read would mask it on either side.
type StateDiffChanged struct {
	Address        string
	ChangedPaths   []string
	ChangedSecrets []string
}

// StateDiff is the diff of two states: the sorted addresses added and
// removed, and the sorted changed resources with their paths. NextCursor
// names the cursor of the next page, or is empty when the page is the last
// one. Incomplete says the server cut the list: entries the server skips, or
// a page cut short of the full diff.
type StateDiff struct {
	Added      []string
	Removed    []string
	Changed    []StateDiffChanged
	NextCursor string
	Incomplete bool
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

// IsClientEncrypted reports whether state is exactly the encryption envelope
// of a state that the client encrypted: the members meta, encrypted_data and
// encryption_version of OpenTofu, with the plain serial and lineage. The
// resources hide inside the envelope, so the resource routes cannot read it.
// The outputs list still works, because the server keeps the outputs the
// client declared at creation.
//
// A state with no encrypted_data member is plaintext and gives false. The
// check fails closed on an input that it cannot classify, and never answers
// "not encrypted" for it: not valid JSON, a document that is not a JSON
// object, a document that has encrypted_data and a member outside the
// envelope, an envelope whose members have another kind of value, and an
// envelope with a version value other than v0. Each of these fails with an
// error that wraps ErrInvalidState. A document that has encrypted_data and any
// other shape is a look-alike: the server must not read it as encrypted,
// because that would hide its resources from the gates, and it must not read
// it as plaintext, because the client may hold the key.
func IsClientEncrypted(state []byte) (bool, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(state, &doc); err != nil || doc == nil {
		return false, fmt.Errorf("%w: state is not a JSON object", ErrInvalidState)
	}

	if _, has := doc[encryptedStateKey]; !has {
		return false, nil
	}

	if err := checkEnvelopeShape(doc); err != nil {
		return false, fmt.Errorf("%w: state has %s and %w", ErrInvalidState, encryptedStateKey, err)
	}

	return true, nil
}

// The top-level members of the state that OpenTofu encrypts on the client: the
// envelope, and the plain serial and lineage that a backend needs to order
// versions.
const (
	stateKeySerial  = "serial"
	stateKeyLineage = "lineage"
	stateKeyMeta    = "meta"
	stateKeyVersion = "encryption_version"
	// stateEncryptionVersion is the only encryption_version value the check
	// knows. An envelope with another value may hold another shape, so the
	// check refuses it.
	stateEncryptionVersion = "v0"
)

// isEnvelopeKey reports whether k is a member of the encryption envelope.
func isEnvelopeKey(k string) bool {
	switch k {
	case stateKeySerial, stateKeyLineage, stateKeyMeta, encryptedStateKey, stateKeyVersion:
		return true
	default:
		return false
	}
}

// errNotEnvelope is the cause of a document that has encrypted_data and is not
// the encryption envelope.
var errNotEnvelope = errors.New("is not an encryption envelope")

// checkEnvelopeShape checks the members of a document that has encrypted_data:
// no member outside the envelope, the three members of the envelope with their
// kinds, the known version value, and the plain serial and lineage with theirs.
func checkEnvelopeShape(doc map[string]json.RawMessage) error {
	if err := checkEnvelopeMembers(doc); err != nil {
		return err
	}

	if err := checkEnvelopeVersion(doc); err != nil {
		return err
	}

	return checkEnvelopePlainKeys(doc)
}

// checkEnvelopeMembers checks that no member sits outside the envelope and
// that the three members of the envelope have their kinds.
func checkEnvelopeMembers(doc map[string]json.RawMessage) error {
	for k := range doc {
		if !isEnvelopeKey(k) {
			return fmt.Errorf("%w: the member %q", errNotEnvelope, k)
		}
	}

	kinds := map[string]byte{encryptedStateKey: '"', stateKeyMeta: '{', stateKeyVersion: '"'}
	for k, first := range kinds {
		if !rawIsKind(doc[k], first) {
			return fmt.Errorf("%w: the member %q has another kind of value", errNotEnvelope, k)
		}
	}

	return nil
}

// checkEnvelopeVersion checks the version value of the envelope.
func checkEnvelopeVersion(doc map[string]json.RawMessage) error {
	// Defense in depth: the kind check of checkEnvelopeMembers already refuses
	// a version that is not text. This check stays so that the value is
	// compared only after it decoded, and a new member cannot reach the
	// comparison unchecked.
	var version string
	if err := json.Unmarshal(doc[stateKeyVersion], &version); err != nil || version != stateEncryptionVersion {
		return fmt.Errorf("%w: the encryption version is not %s", errNotEnvelope, stateEncryptionVersion)
	}

	return nil
}

// checkEnvelopePlainKeys checks the plain serial and lineage members of the
// envelope.
func checkEnvelopePlainKeys(doc map[string]json.RawMessage) error {
	if raw, ok := doc[stateKeyLineage]; ok && !rawIsKind(raw, '"') {
		return fmt.Errorf("%w: the lineage is not text", errNotEnvelope)
	}

	if raw, ok := doc[stateKeySerial]; ok && (len(raw) == 0 || raw[0] < '0' || raw[0] > '9') {
		return fmt.Errorf("%w: the serial is not a number", errNotEnvelope)
	}

	return nil
}

// rawIsKind reports whether the JSON value raw starts with first: a quote for a
// string, a brace for an object.
func rawIsKind(raw json.RawMessage, first byte) bool {
	t := bytes.TrimSpace(raw)

	return len(t) > 0 && t[0] == first
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

// routeTokens returns the physical route of tokens under node, or false
// when the node does not hold the path. It resolves exactly like
// lookupTokens: a key read direct first, one transparent dynamic wrapper
// level, and list indexes parsed with Atoi. The route holds the keys read
// from objects, the decimal index of each list step, and the extra "value"
// step when the wrapper rule applies. No tokens left is an empty route and
// true. It never changes the tree.
func routeTokens(node any, tokens []string) ([]string, bool) {
	if len(tokens) == 0 {
		return []string{}, true
	}

	switch typed := node.(type) {
	case map[string]any:
		return routeMapAt(typed, tokens)
	case []any:
		return routeListAt(typed, tokens)
	default:
		return []string{}, false
	}
}

// routeMapAt returns the physical route of tokens under doc.
func routeMapAt(doc map[string]any, tokens []string) ([]string, bool) {
	next, present := doc[tokens[0]]
	if !present {
		return []string{}, false
	}

	if len(tokens) == 1 {
		return []string{tokens[0]}, true
	}

	if sub, ok := routeTokens(next, tokens[1:]); ok {
		return append([]string{tokens[0]}, sub...), true
	}

	if wrapped, ok := next.(map[string]any); ok && isDynamicWrapper(wrapped) {
		if sub, ok := routeTokens(wrapped["value"], tokens[1:]); ok {
			return append([]string{tokens[0], "value"}, sub...), true
		}
	}

	return []string{}, false
}

// routeListAt returns the physical route of tokens under list.
func routeListAt(list []any, tokens []string) ([]string, bool) {
	index, err := strconv.Atoi(tokens[0])
	if err != nil || index < 0 || index >= len(list) {
		return []string{}, false
	}

	if len(tokens) == 1 {
		return []string{strconv.Itoa(index)}, true
	}

	if sub, ok := routeTokens(list[index], tokens[1:]); ok {
		return append([]string{strconv.Itoa(index)}, sub...), true
	}

	return []string{}, false
}

// belowAppliedRoute reports whether route sits strictly below an applied
// route: an applied route is a proper prefix of route.
func belowAppliedRoute(applied [][]string, route []string) bool {
	for _, done := range applied {
		if isTokenPrefix(done, route) && len(done) < len(route) {
			return true
		}
	}

	return false
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

// maskCollectors carries the collectors of maskTofuInstance across one
// pointer: the full paths already masked, each marker path, each masked
// secret for the duplicate pass, and the physical route of each pointer
// that masked something.
type maskCollectors struct {
	seen    map[string]bool
	masked  *[]string
	secrets *[]any
	applied *[][]string
}

// maskOnePointer masks the relative pointer under attributes with a marker
// for full: it records full in the collectors, the masked secret for the
// duplicate pass, and the physical route. A pointer below an already
// applied route masks nothing. A pointer at the same node is masked again.
func maskOnePointer(attributes any, relative, full string, out *maskCollectors) {
	route, resolved := routeTokens(attributes, splitPointer(relative))
	if resolved && belowAppliedRoute(*out.applied, route) {
		return
	}

	original, found := lookupPointer(attributes, relative)

	if !maskPointer(attributes, relative, full) {
		return
	}

	out.seen[full] = true
	*out.masked = append(*out.masked, full)

	if resolved {
		*out.applied = append(*out.applied, route)
	}

	if found && propagableSecret(original) {
		*out.secrets = append(*out.secrets, original)
	}
}

// maskTofuInstance returns the masked attributes of one instance at position
// of the resource at resource index in the state: each path that
// sensitive_attributes or the kept marks name answers as a marker with its
// value path. A value that duplicates a masked secret answers masked too,
// so a derived copy no mark names still reads as a marker. With no kept
// marks for the version, every attribute value answers as a marker. The
// second result holds each marker path, without duplicates. A pointer below
// an already masked pointer masks nothing. A pointer at the same node is
// masked again.
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

	var applied [][]string

	out := maskCollectors{seen: seen, masked: &masked, secrets: &secrets, applied: &applied}

	for _, relative := range tofuSensitivePointers(instance, address, tofuInstanceBase(address), marks) {
		full := base + relative
		if seen[full] {
			continue
		}

		maskOnePointer(attributes, relative, full, &out)
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

// isTofuMarker reports whether v is a secret marker of a masked copy: an
// object with the secret flag set. The diff compares plain values with their
// masked copies side by side, so a marker on either side marks the path
// sensitive.
func isTofuMarker(v any) bool {
	marker, ok := v.(map[string]any)
	if !ok {
		return false
	}

	secret, ok := marker[markerSecretKey].(bool)

	return ok && secret
}

// tofuMaskedChild returns the child of masked under key, or nil when masked
// is no object or holds no such key.
func tofuMaskedChild(masked any, key string) any {
	obj, ok := masked.(map[string]any)
	if !ok {
		return nil
	}

	return obj[key]
}

// tofuMaskedAt returns the element of masked at index, or nil when masked is
// no array or holds no such element.
func tofuMaskedAt(masked any, index int) any {
	list, ok := masked.([]any)
	if !ok || index < 0 || index >= len(list) {
		return nil
	}

	return list[index]
}

// tofuDiffer compares plain state values with their masked copies side by
// side. It appends the resource-relative paths that differ: plain changes to
// paths, and changes with a marker on either masked side to secrets.
type tofuDiffer struct {
	paths   *[]string
	secrets *[]string
}

// secret handles a value pair where either masked side is a marker: equal
// plain values are no change, different ones are one entry in secrets. It
// reports whether it handled the pair.
func (d *tofuDiffer) secret(from, to, fromMasked, toMasked any, base string) bool {
	if !isTofuMarker(fromMasked) && !isTofuMarker(toMasked) {
		return false
	}

	if reflect.DeepEqual(from, to) {
		return true
	}

	*d.secrets = append(*d.secrets, base)

	return true
}

// single handles a value pair where a key lives on one side only: one entry
// for the key, in secrets when the present masked side marks it, else in
// paths. It never recurses into the added or removed value, so no nested
// value of it ever answers.
func (d *tofuDiffer) single(fromMasked, toMasked any, child string) {
	if isTofuMarker(fromMasked) || isTofuMarker(toMasked) {
		*d.secrets = append(*d.secrets, child)
	} else {
		*d.paths = append(*d.paths, child)
	}
}

// maps appends the child paths under base that differ between two objects.
func (d *tofuDiffer) maps(from, to map[string]any, fromMasked, toMasked any, base string) {
	for _, key := range tofuSortedKeys(from, to) {
		child := base + "/" + escapeTofuPointerToken(key)
		valFrom, inFrom := from[key]
		valTo, inTo := to[key]

		if !inFrom || !inTo {
			d.single(tofuMaskedChild(fromMasked, key), tofuMaskedChild(toMasked, key), child)

			continue
		}

		d.values(valFrom, valTo, tofuMaskedChild(fromMasked, key), tofuMaskedChild(toMasked, key), child)
	}
}

// tofuSortedKeys returns the sorted union of the keys of two objects.
func tofuSortedKeys(from, to map[string]any) []string {
	seen := map[string]bool{}
	for key := range from {
		seen[key] = true
	}

	for key := range to {
		seen[key] = true
	}

	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

// lists appends the child paths under base that differ between two arrays: a
// length change is one entry for the array itself, in secrets when either
// masked side marks the array.
func (d *tofuDiffer) lists(from, to []any, fromMasked, toMasked any, base string) {
	if len(from) != len(to) {
		d.single(fromMasked, toMasked, base)

		return
	}

	for i := range from {
		child := base + "/" + strconv.Itoa(i)
		d.values(from[i], to[i], tofuMaskedAt(fromMasked, i), tofuMaskedAt(toMasked, i), child)
	}
}

// unwrapped handles two dynamic values the CLI stores wrapped with their
// type: they compare transparently on their value, so a mark below such an
// object names the unwrapped path. It reports whether both sides wrapped.
func (d *tofuDiffer) unwrapped(from, to, fromMasked, toMasked any, base string) bool {
	fromWrapped, ok := from.(map[string]any)
	if !ok || !isDynamicWrapper(fromWrapped) {
		return false
	}

	toWrapped, ok := to.(map[string]any)
	if !ok || !isDynamicWrapper(toWrapped) {
		return false
	}

	d.values(fromWrapped["value"], toWrapped["value"], tofuValueMask(fromMasked), tofuValueMask(toMasked), base)

	return true
}

// tofuValueMask returns the mask of the value inside a wrapped dynamic mask,
// or nil when the mask is no wrapper.
func tofuValueMask(masked any) any {
	wrapped, ok := masked.(map[string]any)
	if !ok || isTofuMarker(masked) {
		return nil
	}

	return wrapped["value"]
}

// shaped compares two values of the same container shape: two objects or two
// arrays. It reports whether both sides shared a shape.
func (d *tofuDiffer) shaped(from, to, fromMasked, toMasked any, base string) bool {
	if fromMap, ok := from.(map[string]any); ok {
		if toMap, ok := to.(map[string]any); ok {
			d.maps(fromMap, toMap, fromMasked, toMasked, base)

			return true
		}
	}

	if fromList, ok := from.([]any); ok {
		if toList, ok := to.([]any); ok {
			d.lists(fromList, toList, fromMasked, toMasked, base)

			return true
		}
	}

	return false
}

// values appends the resource-relative paths under base that differ between
// from and to. A marker on either masked side compares the plain values:
// equal values are no change, different ones are one entry in secrets, never
// in paths and never a value.
func (d *tofuDiffer) values(from, to, fromMasked, toMasked any, base string) {
	if d.secret(from, to, fromMasked, toMasked, base) {
		return
	}

	if d.unwrapped(from, to, fromMasked, toMasked, base) {
		return
	}

	if d.shaped(from, to, fromMasked, toMasked, base) {
		return
	}

	if !reflect.DeepEqual(from, to) {
		*d.paths = append(*d.paths, base)
	}
}

// tofuDiffResource holds the changed paths and secret paths of one resource
// present in both versions.
type tofuDiffResource struct {
	paths   []string
	secrets []string
}

// maskedInstanceCopy returns the masked attributes of one instance without
// touching the decoded state: it masks a copy, so the diff still compares
// the plain values. Position and resource name the value paths of the
// markers. With no kept marks for the version every attribute value answers
// as a marker.
func maskedInstanceCopy(
	instance map[string]any, resource, position int, address string, marks SensitiveMarks,
) any {
	copied, err := json.Marshal(instance)
	if err != nil {
		return instance["attributes"]
	}

	var fresh map[string]any

	dec := json.NewDecoder(bytes.NewReader(copied))
	dec.UseNumber()

	if err := dec.Decode(&fresh); err != nil {
		return instance["attributes"]
	}

	masked, _ := maskTofuInstance(fresh, resource, position, address, marks)

	return masked
}

// tofuDiffSide is one side of an instance diff: the sorted instances with
// their addresses and their masked attribute copies.
type tofuDiffSide struct {
	instances []map[string]any
	addrs     []string
	masked    []any
}

// tofuInstanceAttrs returns the attributes of an instance, or an empty object
// when the instance holds none. Attributes are always objects in the state
// file; anything else reads as empty, so the diff still answers.
func tofuInstanceAttrs(instance map[string]any) any {
	attrs, ok := instance["attributes"].(map[string]any)
	if !ok || attrs == nil {
		return map[string]any{}
	}

	return attrs
}

// diffSingleInstance returns the changed paths of one instance on each side:
// the attributes compare under "/attributes".
func diffSingleInstance(from, to tofuDiffSide) tofuDiffResource {
	out := tofuDiffResource{paths: []string{}, secrets: []string{}}
	differ := tofuDiffer{paths: &out.paths, secrets: &out.secrets}

	var fromMasked, toMasked any

	if len(from.masked) == 1 {
		fromMasked = from.masked[0]
	}

	if len(to.masked) == 1 {
		toMasked = to.masked[0]
	}

	differ.values(
		tofuInstanceAttrs(from.instances[0]), tofuInstanceAttrs(to.instances[0]),
		fromMasked, toMasked, "/attributes",
	)

	slices.Sort(out.paths)
	slices.Sort(out.secrets)

	return out
}

// diffMultiInstance returns the changed paths of several instances on each
// side: each pair compares under "/instances/<position>/attributes" in
// address order. A differing address set is one entry for "/instances".
func diffMultiInstance(from, to tofuDiffSide) tofuDiffResource {
	out := tofuDiffResource{paths: []string{}, secrets: []string{}}

	for i := range from.instances {
		if from.addrs[i] != to.addrs[i] {
			out.paths = append(out.paths, "/instances")

			return out
		}
	}

	differ := tofuDiffer{paths: &out.paths, secrets: &out.secrets}

	for i := range from.instances {
		var fromMasked, toMasked any

		if i < len(from.masked) {
			fromMasked = from.masked[i]
		}

		if i < len(to.masked) {
			toMasked = to.masked[i]
		}

		base := "/instances/" + strconv.Itoa(i) + "/attributes"
		fromAttrs := tofuInstanceAttrs(from.instances[i])
		toAttrs := tofuInstanceAttrs(to.instances[i])
		differ.values(fromAttrs, toAttrs, fromMasked, toMasked, base)
	}

	slices.Sort(out.paths)
	slices.Sort(out.secrets)

	return out
}

// diffTofuInstances returns the changed paths of the instances of one
// resource present in both versions. Instances match by address: with one
// instance on each side the attributes compare under "/attributes". With
// several, each pair compares under "/instances/<position>/attributes" in
// address order. A differing instance set is one entry for "/instances".
func diffTofuInstances(from, to tofuDiffSide) tofuDiffResource {
	if len(from.instances) != len(to.instances) {
		return tofuDiffResource{paths: []string{"/instances"}, secrets: []string{}}
	}

	if len(from.instances) == 1 {
		return diffSingleInstance(from, to)
	}

	return diffMultiInstance(from, to)
}

// sortTofuInstances orders instances by address, returning the instances with
// their addresses in that order.
func sortTofuInstances(obj map[string]any, instances []map[string]any) ([]map[string]any, []string) {
	type addressed struct {
		address  string
		instance map[string]any
	}

	ordered := make([]addressed, 0, len(instances))

	for _, instance := range instances {
		ordered = append(ordered, addressed{address: tofuInstanceAddress(obj, instance), instance: instance})
	}

	slices.SortFunc(ordered, func(a, b addressed) int {
		return strings.Compare(a.address, b.address)
	})

	sorted := make([]map[string]any, 0, len(ordered))
	addrs := make([]string, 0, len(ordered))

	for _, entry := range ordered {
		sorted = append(sorted, entry.instance)
		addrs = append(addrs, entry.address)
	}

	return sorted, addrs
}

// tofuResourceSide is one side of a resource diff: the resource object with
// its kept marks and its position in its own state.
type tofuResourceSide struct {
	obj   map[string]any
	marks SensitiveMarks
	index int
}

// tofuResourcePair is one resource present in both versions.
type tofuResourcePair struct {
	from tofuResourceSide
	to   tofuResourceSide
}

// maskSide returns the sorted instances of side with their addresses and
// their masked attribute copies.
func maskSide(side tofuResourceSide) tofuDiffSide {
	instances := tofuInstances(side.obj)
	sorted, addrs := sortTofuInstances(side.obj, instances)

	masked := make([]any, 0, len(sorted))

	for i, instance := range sorted {
		masked = append(masked, maskedInstanceCopy(instance, side.index, i, addrs[i], side.marks))
	}

	return tofuDiffSide{instances: sorted, addrs: addrs, masked: masked}
}

// diffTofuResource returns the changed paths and secret paths of one
// resource present in both versions. The identity fields (type, provider,
// module, mode) compare as plain paths; the instances compare with their
// masked copies side by side, so a sensitive change lands in secrets. The
// result holds empty slices, never nil, when nothing changed.
func diffTofuResource(pair tofuResourcePair) tofuDiffResource {
	out := tofuDiffResource{paths: []string{}, secrets: []string{}}

	for _, field := range []string{"type", "provider", "module", "mode"} {
		if tofuField(pair.from.obj, field) != tofuField(pair.to.obj, field) {
			out.paths = append(out.paths, "/"+field)
		}
	}

	entry := diffTofuInstances(maskSide(pair.from), maskSide(pair.to))
	out.paths = append(out.paths, entry.paths...)
	out.secrets = append(out.secrets, entry.secrets...)

	slices.Sort(out.paths)
	slices.Sort(out.secrets)

	return out
}

// tofuResourcesByBase maps each resource base of entries to its object. Later
// entries with the same base win, as each base names one resource.
func tofuResourcesByBase(entries []tofuResourceEntry) map[string]map[string]any {
	out := make(map[string]map[string]any, len(entries))
	for _, entry := range entries {
		out[tofuResourceBase(entry.obj)] = entry.obj
	}

	return out
}

// tofuResourceIndex maps each resource base of entries to its position in the
// state, so masked copies name the value paths of their own version.
func tofuResourceIndex(entries []tofuResourceEntry) map[string]int {
	out := make(map[string]int, len(entries))
	for _, entry := range entries {
		out[tofuResourceBase(entry.obj)] = entry.index
	}

	return out
}

// tofuDiffMismatch holds the sorted diff entries of two states before paging,
// with the count of skipped entries the server could not read.
type tofuDiffMismatch struct {
	added     []string
	removed   []string
	changed   []StateDiffChanged
	differing []string
	skipped   int
}

// tofuVersionSide is one state of a diff: its resources by base address with
// their positions, and its kept marks.
type tofuVersionSide struct {
	byBase map[string]map[string]any
	index  map[string]int
	marks  SensitiveMarks
}

// collectTofuDiff compares the resources by base address: added holds the
// addresses only in to, removed those only in from, and changed those in
// both with a change. Differing holds the sorted addresses that differ in
// any way.
func collectTofuDiff(from, to tofuVersionSide) tofuDiffMismatch {
	var out tofuDiffMismatch

	for address := range to.byBase {
		if _, ok := from.byBase[address]; !ok {
			out.added = append(out.added, address)
			out.differing = append(out.differing, address)
		}
	}

	for address := range from.byBase {
		if _, ok := to.byBase[address]; !ok {
			out.removed = append(out.removed, address)
			out.differing = append(out.differing, address)
		}
	}

	for address, fromObj := range from.byBase {
		toObj, ok := to.byBase[address]
		if !ok {
			continue
		}

		pair := tofuResourcePair{
			from: tofuResourceSide{obj: fromObj, marks: from.marks, index: from.index[address]},
			to:   tofuResourceSide{obj: toObj, marks: to.marks, index: to.index[address]},
		}

		entry := diffTofuResource(pair)
		if len(entry.paths) == 0 && len(entry.secrets) == 0 {
			continue
		}

		out.changed = append(
			out.changed,
			StateDiffChanged{Address: address, ChangedPaths: entry.paths, ChangedSecrets: entry.secrets},
		)
		out.differing = append(out.differing, address)
	}

	slices.Sort(out.added)
	slices.Sort(out.removed)
	slices.SortFunc(out.changed, func(a, b StateDiffChanged) int {
		return strings.Compare(a.Address, b.Address)
	})
	slices.Sort(out.differing)

	return out
}

// pageTofuDiff returns the page of mismatch at offset with at most limit
// differing addresses.
func pageTofuDiff(mismatch tofuDiffMismatch, offset, limit int) StateDiff {
	page := StateDiff{
		Added:      []string{},
		Removed:    []string{},
		Changed:    []StateDiffChanged{},
		NextCursor: "",
		Incomplete: mismatch.skipped > 0,
	}

	if offset >= len(mismatch.differing) {
		return page
	}

	end := min(offset+limit, len(mismatch.differing))
	window := map[string]bool{}

	for _, address := range mismatch.differing[offset:end] {
		window[address] = true
	}

	for _, address := range mismatch.added {
		if window[address] {
			page.Added = append(page.Added, address)
		}
	}

	for _, address := range mismatch.removed {
		if window[address] {
			page.Removed = append(page.Removed, address)
		}
	}

	for _, entry := range mismatch.changed {
		if window[entry.Address] {
			page.Changed = append(page.Changed, entry)
		}
	}

	if end < len(mismatch.differing) {
		page.NextCursor = strconv.Itoa(end)
		page.Incomplete = true
	}

	return page
}

// StateWithMarks is one side of a diff: the plain state with the kept
// sensitive marks of its version. A version with no kept marks masks every
// attribute value, so any change then lands in changed secrets.
type StateWithMarks struct {
	State []byte
	Marks SensitiveMarks
}

// emptyTofuDiff returns an empty diff that still reads cut short.
func emptyTofuDiff() StateDiff {
	return StateDiff{
		Added:      []string{},
		Removed:    []string{},
		Changed:    []StateDiffChanged{},
		NextCursor: "",
		Incomplete: true,
	}
}

// tofuVersionSideOf reads one side of a diff: its resources by base address
// with their positions, and its kept marks.
func tofuVersionSideOf(entries []tofuResourceEntry, marks SensitiveMarks) tofuVersionSide {
	return tofuVersionSide{
		byBase: tofuResourcesByBase(entries),
		index:  tofuResourceIndex(entries),
		marks:  marks,
	}
}

// DiffStates returns the diff of two states: from is the older side, to is
// the newer one. Added holds the sorted addresses only in to, Removed the
// sorted addresses only in from, and Changed the sorted resources in both
// with a change. No value leaves the diff: a changed, added or removed
// sensitive value is one path in ChangedSecrets. Cursor is "" or the
// NextCursor of an earlier page; size is the page size, or zero for the
// default page, and counts differing addresses. Resources match by base
// address across versions.
func DiffStates(from, to StateWithMarks, cursor string, size int) (StateDiff, error) {
	offset, err := tofuOffset(cursor)
	if err != nil {
		return StateDiff{}, err
	}

	limit, err := tofuLimit(size)
	if err != nil {
		return StateDiff{}, err
	}

	fromDoc, err := decodeState(from.State)
	if err != nil {
		return StateDiff{}, err
	}

	toDoc, err := decodeState(to.State)
	if err != nil {
		return StateDiff{}, err
	}

	fromEntries, fromSkipped := tofuStateResources(fromDoc)
	toEntries, toSkipped := tofuStateResources(toDoc)

	if (fromEntries == nil && fromSkipped) || (toEntries == nil && toSkipped) {
		return emptyTofuDiff(), nil
	}

	mismatch := collectTofuDiff(
		tofuVersionSideOf(fromEntries, from.Marks),
		tofuVersionSideOf(toEntries, to.Marks),
	)

	if fromSkipped || toSkipped {
		mismatch.skipped = 1
	}

	return pageTofuDiff(mismatch, offset, limit), nil
}
