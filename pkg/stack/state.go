package stack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Masked state read for the console: a paged resource list, one resource
// and the stack outputs of a checkpoint. A read replaces each secret value
// with a marker that holds the value path and never the value (ADR 41).

const (
	// secretSigKey marks special values in a deployment: secrets, assets,
	// archives and resource references all carry it. It is the Pulumi
	// signature for special values.
	secretSigKey = "4dabf18193072939515e22adb298388d" //nolint:gosec // Well-known Pulumi signature key, not a credential.
	// secretSigValue marks a secret under the signature key: only an object
	// with this value is a secret, with any secrets provider. Assets,
	// archives and resource references carry other values under the same
	// key, and masking leaves them alone.
	secretSigValue = "1b47061264138c4ac30d75fd1eb44270"
	// stackResourceType is the type of the automatic stack resource. Its
	// outputs are the stack outputs.
	stackResourceType = "pulumi:pulumi:Stack"
	// maxStateLimit bounds one page of the resource list.
	maxStateLimit = 500
)

// ErrResourceNotFound reports that a deployment holds no resource with the
// given URN.
var ErrResourceNotFound = errors.New("resource not found")

// StateResource is one entry of the masked resource list: the identity of
// the resource as stored, and whether it differs from the previous version
// or is new.
type StateResource struct {
	URN      string
	Type     string
	Provider string
	Parent   string
	Changed  bool
}

// StatePage is one page of the masked resource list. NextCursor names the
// cursor of the next page, or is empty when the page is the last one.
// Incomplete with a Reason says the server cut the list short of paging,
// for example when the deployment holds entries the server skips.
type StatePage struct {
	Resources  []StateResource
	NextCursor string
	Incomplete bool
	Reason     string
}

// StateDetail is one resource with its masked inputs and outputs. Masked
// holds the value path of each marker in Inputs and Outputs. It is
// authoritative: a real value with the shape of a marker but no entry in
// Masked is a real value.
type StateDetail struct {
	URN      string
	Type     string
	Provider string
	Parent   string
	Inputs   any
	Outputs  any
	Masked   []string
}

// StateOutputs are the masked outputs of the stack resource of one version.
type StateOutputs struct {
	Outputs any
	Masked  []string
}

// StateDiffChanged is one resource present in both versions with a change:
// the URN, the resource-relative value paths that changed
// (for example "/outputs/greeting" or "/type"), and the paths of secrets
// whose ciphertext changed. A secret value is never in the diff: a changed
// secret is one path in ChangedSecrets. Secrets compare as ciphertext, and
// the CLI may encrypt the same value again with a new nonce, so
// ChangedSecrets may name a secret whose value did not change. The server
// never decrypts for a diff.
type StateDiffChanged struct {
	URN            string
	ChangedPaths   []string
	ChangedSecrets []string
}

// StateDiff is the diff of two deployments: the sorted URNs added and
// removed, and the sorted changed resources with their paths. NextCursor
// names the cursor of the next page, or is empty when the page is the last
// one. Incomplete with a Reason says the server cut the list short of
// paging, for example when a deployment holds entries the server skips.
type StateDiff struct {
	Added      []string
	Removed    []string
	Changed    []StateDiffChanged
	NextCursor string
	Incomplete bool
	Reason     string
}

// EscapePointerToken escapes one JSON Pointer token (RFC 6901): `~` first,
// then `/`.
func EscapePointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// isSecret reports whether v is a secret value: an object whose signature
// key holds the secret value. The value is the same with every secrets
// provider, so the check works with each one. Assets, archives and resource
// references share the key with other values and are not secrets.
func isSecret(v any) bool {
	obj, ok := v.(map[string]any)
	if !ok {
		return false
	}

	text, ok := obj[secretSigKey].(string)

	return ok && text == secretSigValue
}

// maskValue copies v with each secret replaced by a marker for base, and
// appends each marker path to masked. Map keys escape per RFC 6901;
// array indices stay decimal.
func maskValue(v any, base string, masked *[]string) any {
	if isSecret(v) {
		*masked = append(*masked, base)

		return map[string]any{"secret": true, "path": base}
	}

	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, elem := range typed {
			out[key] = maskValue(elem, base+"/"+EscapePointerToken(key), masked)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, elem := range typed {
			out[i] = maskValue(elem, base+"/"+strconv.Itoa(i), masked)
		}

		return out
	default:
		return v
	}
}

// normalizeValue copies v with each secret replaced by one fixed marker, so
// two resources compare equal when only secret values differ.
func normalizeValue(v any) any {
	if isSecret(v) {
		return map[string]any{"secret": true}
	}

	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, elem := range typed {
			out[key] = normalizeValue(elem)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, elem := range typed {
			out[i] = normalizeValue(elem)
		}

		return out
	default:
		return v
	}
}

// decodeDeployment decodes deployment into a generic object with numbers
// kept exact.
func decodeDeployment(deployment []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(deployment))
	dec.UseNumber()

	var doc any

	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode deployment: %w", err)
	}

	return doc, nil
}

// resourceField reads one string field of a resource, or "" when the field
// is missing or of another shape.
func resourceField(obj map[string]any, key string) string {
	text, ok := obj[key].(string)
	if !ok {
		return ""
	}

	return text
}

// resourceList reads the resources array of doc. Entries that are not
// objects with a string URN are skipped; the second result counts them.
func resourceList(doc any) ([]map[string]any, int) {
	top, ok := doc.(map[string]any)
	if !ok {
		return nil, 1
	}

	raw := top["resources"]
	if raw == nil {
		return nil, 0
	}

	list, ok := raw.([]any)
	if !ok {
		return nil, 1
	}

	var entries []map[string]any

	skipped := 0

	for _, elem := range list {
		obj, ok := elem.(map[string]any)
		if !ok {
			skipped++

			continue
		}

		if resourceField(obj, "urn") == "" {
			skipped++

			continue
		}

		entries = append(entries, obj)
	}

	return entries, skipped
}

// fingerprintOf returns the comparison shape of one resource: its identity
// fields (type, provider, parent) with normalized inputs and outputs.
// Other stored fields, for example protect or aliases, do not mark a
// resource changed; secrets compare by marker, so a new ciphertext alone
// still reads unchanged.
func fingerprintOf(obj map[string]any) string {
	shaped := map[string]any{
		"type":     resourceField(obj, "type"),
		"provider": resourceField(obj, "provider"),
		"parent":   resourceField(obj, "parent"),
		"inputs":   normalizeValue(obj["inputs"]),
		"outputs":  normalizeValue(obj["outputs"]),
	}

	raw, err := json.Marshal(shaped)
	if err != nil {
		return fmt.Sprintf("%v", shaped)
	}

	return string(raw)
}

// matchStateFilter reports whether a resource matches the list filter: an
// empty filter matches every resource, otherwise the filter is a substring
// of the URN or of the resource type. It never matches attribute values.
func matchStateFilter(urn, typ, filter string) bool {
	if filter == "" {
		return true
	}

	return strings.Contains(urn, filter) || strings.Contains(typ, filter)
}

// ListStateResources returns one page of the masked resource list of the
// current deployment. Previous is the deployment of the previous version,
// or nil when there is none: every resource then counts as new. Cursor is
// "" or the NextCursor of an earlier page; limit is 1 to 500. Filter is a
// substring of the URN or of the resource type, or "" for no filter: it
// never matches attribute values. Resources keep the order of the
// deployment; matching across versions uses the URN. Paging applies after
// the filter, so a page holds only matching resources.
func ListStateResources(current, previous []byte, cursor string, limit int, filter string) (StatePage, error) {
	offset, err := stateOffset(cursor)
	if err != nil {
		return StatePage{}, err
	}

	if limit <= 0 || limit > maxStateLimit {
		return StatePage{}, fmt.Errorf("%w: resource limit %d, want 1 to %d", ErrInvalidUpdate, limit, maxStateLimit)
	}

	doc, err := decodeDeployment(current)
	if err != nil {
		return StatePage{}, err
	}

	entries, skipped := resourceList(doc)
	if entries == nil && skipped > 0 {
		return StatePage{
			Resources:  []StateResource{},
			NextCursor: "",
			Incomplete: true,
			Reason:     "the deployment holds no resources array",
		}, nil
	}

	marks := previousFingerprints(previous)

	summaries := stateSummaries(entries, marks, filter)

	page := StatePage{Resources: []StateResource{}, NextCursor: "", Incomplete: skipped > 0, Reason: ""}
	if skipped > 0 {
		page.Reason = "the deployment holds entries without a URN that the server skips"
	}

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

// stateSummaries returns the resource entries as summaries in deployment
// order, with only the entries that match the filter. Matching across
// versions uses the URN: a resource is new when marks holds no fingerprint
// for it, or when the fingerprint differs.
func stateSummaries(entries []map[string]any, marks map[string]string, filter string) []StateResource {
	summaries := make([]StateResource, 0, len(entries))

	for _, obj := range entries {
		urn := resourceField(obj, "urn")
		typ := resourceField(obj, "type")

		if !matchStateFilter(urn, typ, filter) {
			continue
		}

		mark, ok := marks[urn]

		summaries = append(summaries, StateResource{
			URN:      urn,
			Type:     typ,
			Provider: resourceField(obj, "provider"),
			Parent:   resourceField(obj, "parent"),
			Changed:  !ok || mark != fingerprintOf(obj),
		})
	}

	return summaries
}

// previousFingerprints maps each URN of previous to its fingerprint, or
// holds nothing when previous is nil or has no resources.
func previousFingerprints(previous []byte) map[string]string {
	marks := map[string]string{}

	if len(previous) == 0 {
		return marks
	}

	doc, err := decodeDeployment(previous)
	if err != nil {
		return marks
	}

	entries, _ := resourceList(doc)

	for _, obj := range entries {
		marks[resourceField(obj, "urn")] = fingerprintOf(obj)
	}

	return marks
}

// stateOffset parses a list cursor: "" starts at the first resource.
func stateOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: resource cursor %q", ErrInvalidUpdate, cursor)
	}

	return n, nil
}

// findResource returns the index in the resources array and the object of
// the resource with urn, or ErrResourceNotFound.
func findResource(deployment []byte, urn string) (int, map[string]any, error) {
	doc, err := decodeDeployment(deployment)
	if err != nil {
		return 0, nil, err
	}

	top, ok := doc.(map[string]any)
	if !ok {
		return 0, nil, fmt.Errorf("%w: %q", ErrResourceNotFound, urn)
	}

	list, ok := top["resources"].([]any)
	if !ok {
		return 0, nil, fmt.Errorf("%w: %q", ErrResourceNotFound, urn)
	}

	for i, elem := range list {
		obj, ok := elem.(map[string]any)
		if !ok {
			continue
		}

		if text := resourceField(obj, "urn"); text != "" && text == urn {
			return i, obj, nil
		}
	}

	return 0, nil, fmt.Errorf("%w: %q", ErrResourceNotFound, urn)
}

// ReadStateResource returns one resource of deployment with its inputs and
// outputs masked. Missing inputs or outputs read as empty objects.
func ReadStateResource(deployment []byte, urn string) (StateDetail, error) {
	index, obj, err := findResource(deployment, urn)
	if err != nil {
		return StateDetail{}, err
	}

	masked := []string{}

	inputs := obj["inputs"]
	if inputs == nil {
		inputs = map[string]any{}
	}

	outputs := obj["outputs"]
	if outputs == nil {
		outputs = map[string]any{}
	}

	base := "/resources/" + strconv.Itoa(index)

	detail := StateDetail{
		URN:      resourceField(obj, "urn"),
		Type:     resourceField(obj, "type"),
		Provider: resourceField(obj, "provider"),
		Parent:   resourceField(obj, "parent"),
		Inputs:   maskValue(inputs, base+"/inputs", &masked),
		Outputs:  maskValue(outputs, base+"/outputs", &masked),
		Masked:   masked,
	}

	if detail.Masked == nil {
		detail.Masked = []string{}
	}

	return detail, nil
}

// ReadStateOutputs returns the masked outputs of the stack resource of
// deployment. A deployment without a stack resource or without outputs
// reads as empty.
func ReadStateOutputs(deployment []byte) (StateOutputs, error) {
	doc, err := decodeDeployment(deployment)
	if err != nil {
		return StateOutputs{}, err
	}

	top, ok := doc.(map[string]any)
	if !ok {
		return StateOutputs{Outputs: map[string]any{}, Masked: []string{}}, nil
	}

	list, ok := top["resources"].([]any)
	if !ok {
		return StateOutputs{Outputs: map[string]any{}, Masked: []string{}}, nil
	}

	for i, elem := range list {
		obj, ok := elem.(map[string]any)
		if !ok {
			continue
		}

		if resourceField(obj, "type") != stackResourceType {
			continue
		}

		masked := []string{}

		outputs := obj["outputs"]
		if outputs == nil {
			outputs = map[string]any{}
		}

		out := StateOutputs{
			Outputs: maskValue(outputs, "/resources/"+strconv.Itoa(i)+"/outputs", &masked),
			Masked:  masked,
		}

		if out.Masked == nil {
			out.Masked = []string{}
		}

		return out, nil
	}

	return StateOutputs{Outputs: map[string]any{}, Masked: []string{}}, nil
}

// secretEqual reports whether two secret values hold the same ciphertext.
// The comparison never decrypts: it compares the stored objects as encoded.
func secretEqual(a, b any) bool {
	rawA, err := json.Marshal(a)
	if err != nil {
		return false
	}

	rawB, err := json.Marshal(b)
	if err != nil {
		return false
	}

	return bytes.Equal(rawA, rawB)
}

// scalarEqual reports whether two plain values are equal. Numbers keep their
// exact encoding, so "1" and "1.0" differ.
func scalarEqual(a, b any) bool {
	switch typedA := a.(type) {
	case nil:
		return b == nil
	case string:
		text, ok := b.(string)

		return ok && text == typedA
	case bool:
		flag, ok := b.(bool)

		return ok && flag == typedA
	case json.Number:
		num, ok := b.(json.Number)

		return ok && string(num) == string(typedA)
	default:
		rawA, err := json.Marshal(a)
		if err != nil {
			return false
		}

		rawB, err := json.Marshal(b)
		if err != nil {
			return false
		}

		return bytes.Equal(rawA, rawB)
	}
}

// diffSecret handles a value pair where either side is a secret: an equal
// ciphertext is no change, a different one is one entry in secrets. It
// reports whether it handled the pair.
func diffSecret(a, b any, base string, secrets *[]string) bool {
	if !isSecret(a) && !isSecret(b) {
		return false
	}

	if isSecret(a) && isSecret(b) && secretEqual(a, b) {
		return true
	}

	*secrets = append(*secrets, base)

	return true
}

// diffMaps appends the child paths under base that differ between two
// objects.
func diffMaps(mapA, mapB map[string]any, base string, paths, secrets *[]string) {
	seen := map[string]bool{}
	for key := range mapA {
		seen[key] = true
	}

	for key := range mapB {
		seen[key] = true
	}

	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	for _, key := range keys {
		child := base + "/" + EscapePointerToken(key)
		valA, inA := mapA[key]
		valB, inB := mapB[key]

		if !inA || !inB {
			// A secret added or removed is a changed secret, as a
			// changed ciphertext is.
			if isSecret(valA) || isSecret(valB) {
				*secrets = append(*secrets, child)
			} else {
				*paths = append(*paths, child)
			}

			continue
		}

		diffValues(valA, valB, child, paths, secrets)
	}
}

// diffLists appends the child paths under base that differ between two
// arrays: a length change is one entry for the array itself.
func diffLists(listA, listB []any, base string, paths, secrets *[]string) {
	if len(listA) != len(listB) {
		*paths = append(*paths, base)

		return
	}

	for i := range listA {
		diffValues(listA[i], listB[i], base+"/"+strconv.Itoa(i), paths, secrets)
	}
}

// diffValues appends the resource-relative paths under base that differ
// between a and b. A secret on either side compares as ciphertext: an equal
// ciphertext is no change, a different one is one entry in secrets, never in
// paths and never a value.
func diffValues(a, b any, base string, paths, secrets *[]string) {
	if diffSecret(a, b, base, secrets) {
		return
	}

	if mapA, ok := a.(map[string]any); ok {
		if mapB, ok := b.(map[string]any); ok {
			diffMaps(mapA, mapB, base, paths, secrets)

			return
		}
	}

	if listA, ok := a.([]any); ok {
		if listB, ok := b.([]any); ok {
			diffLists(listA, listB, base, paths, secrets)

			return
		}
	}

	if !scalarEqual(a, b) {
		*paths = append(*paths, base)
	}
}

// resourceDiff holds the changed paths and secret paths of one resource
// present in both versions.
type resourceDiff struct {
	paths   []string
	secrets []string
}

// diffResource returns the changed paths and secret paths of one resource
// present in both versions. Only the identity fields (type, provider,
// parent) with the inputs and outputs mark a resource changed; other stored
// fields do not. The result holds empty slices, never nil, when nothing
// changed.
func diffResource(from, to map[string]any) resourceDiff {
	out := resourceDiff{paths: []string{}, secrets: []string{}}

	for _, field := range []string{"type", "provider", "parent"} {
		if resourceField(from, field) != resourceField(to, field) {
			out.paths = append(out.paths, "/"+field)
		}
	}

	fromInputs := from["inputs"]
	if fromInputs == nil {
		fromInputs = map[string]any{}
	}

	toInputs := to["inputs"]
	if toInputs == nil {
		toInputs = map[string]any{}
	}

	diffValues(fromInputs, toInputs, "/inputs", &out.paths, &out.secrets)

	fromOutputs := from["outputs"]
	if fromOutputs == nil {
		fromOutputs = map[string]any{}
	}

	toOutputs := to["outputs"]
	if toOutputs == nil {
		toOutputs = map[string]any{}
	}

	diffValues(fromOutputs, toOutputs, "/outputs", &out.paths, &out.secrets)

	slices.Sort(out.paths)
	slices.Sort(out.secrets)

	return out
}

// resourcesByURN maps each URN of entries to its object.
func resourcesByURN(entries []map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(entries))
	for _, obj := range entries {
		out[resourceField(obj, "urn")] = obj
	}

	return out
}

// diffMismatch holds the sorted diff entries of two deployments before
// paging, with the count of skipped entries without a URN.
type diffMismatch struct {
	added     []string
	removed   []string
	changed   []StateDiffChanged
	differing []string
	skipped   int
}

// collectDiff compares the resources by URN: added holds the URNs only in
// to, removed those only in from, and changed those in both with a change.
// Differing holds the sorted URNs that differ in any way.
func collectDiff(fromByURN, toByURN map[string]map[string]any) diffMismatch {
	var out diffMismatch

	for urn := range toByURN {
		if _, ok := fromByURN[urn]; !ok {
			out.added = append(out.added, urn)
			out.differing = append(out.differing, urn)
		}
	}

	for urn := range fromByURN {
		if _, ok := toByURN[urn]; !ok {
			out.removed = append(out.removed, urn)
			out.differing = append(out.differing, urn)
		}
	}

	for urn, fromObj := range fromByURN {
		toObj, ok := toByURN[urn]
		if !ok {
			continue
		}

		entry := diffResource(fromObj, toObj)
		if len(entry.paths) == 0 && len(entry.secrets) == 0 {
			continue
		}

		out.changed = append(
			out.changed,
			StateDiffChanged{URN: urn, ChangedPaths: entry.paths, ChangedSecrets: entry.secrets},
		)
		out.differing = append(out.differing, urn)
	}

	// Sort for a stable page across calls.
	slices.Sort(out.added)
	slices.Sort(out.removed)
	slices.SortFunc(out.changed, func(a, b StateDiffChanged) int {
		return strings.Compare(a.URN, b.URN)
	})
	slices.Sort(out.differing)

	return out
}

// pageDiff returns the page of mismatch at offset with at most limit
// differing URNs.
func pageDiff(mismatch diffMismatch, offset, limit int) StateDiff {
	page := StateDiff{
		Added:      []string{},
		Removed:    []string{},
		Changed:    []StateDiffChanged{},
		NextCursor: "",
		Incomplete: mismatch.skipped > 0,
		Reason:     "",
	}
	if mismatch.skipped > 0 {
		page.Reason = "the deployment holds entries without a URN that the server skips"
	}

	if offset >= len(mismatch.differing) {
		return page
	}

	end := min(offset+limit, len(mismatch.differing))
	window := map[string]bool{}
	for _, urn := range mismatch.differing[offset:end] {
		window[urn] = true
	}

	for _, urn := range mismatch.added {
		if window[urn] {
			page.Added = append(page.Added, urn)
		}
	}

	for _, urn := range mismatch.removed {
		if window[urn] {
			page.Removed = append(page.Removed, urn)
		}
	}

	for _, entry := range mismatch.changed {
		if window[entry.URN] {
			page.Changed = append(page.Changed, entry)
		}
	}

	if end < len(mismatch.differing) {
		page.NextCursor = strconv.Itoa(end)
	}

	return page
}

// DiffStates returns the diff of two deployments: from is the older
// deployment, to is the newer one. Added holds the sorted URNs only in to,
// Removed the sorted URNs only in from, and Changed the sorted resources in
// both with a change. Cursor is "" or the NextCursor of an earlier page;
// limit is 1 to 500 and counts differing URNs. Resources keep sorted URN
// order across versions; matching across versions uses the URN.
func DiffStates(from, to []byte, cursor string, limit int) (StateDiff, error) {
	offset, err := stateOffset(cursor)
	if err != nil {
		return StateDiff{}, err
	}

	if limit <= 0 || limit > maxStateLimit {
		return StateDiff{}, fmt.Errorf("%w: resource limit %d, want 1 to %d", ErrInvalidUpdate, limit, maxStateLimit)
	}

	fromDoc, err := decodeDeployment(from)
	if err != nil {
		return StateDiff{}, err
	}

	toDoc, err := decodeDeployment(to)
	if err != nil {
		return StateDiff{}, err
	}

	fromEntries, fromSkipped := resourceList(fromDoc)
	toEntries, toSkipped := resourceList(toDoc)

	if (fromEntries == nil && fromSkipped > 0) || (toEntries == nil && toSkipped > 0) {
		return StateDiff{
			Added:      []string{},
			Removed:    []string{},
			Changed:    []StateDiffChanged{},
			NextCursor: "",
			Incomplete: true,
			Reason:     "the deployment holds no resources array",
		}, nil
	}

	mismatch := collectDiff(resourcesByURN(fromEntries), resourcesByURN(toEntries))
	mismatch.skipped = fromSkipped + toSkipped

	return pageDiff(mismatch, offset, limit), nil
}
