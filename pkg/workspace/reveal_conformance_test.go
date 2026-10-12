package workspace_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// Agreement test for the masked read and the reveal lookup. Both sides use
// the same masking code today. A later change replaces the reveal lookup
// with a streaming lookup, so this test pins that the two agree.

// conformanceState wraps one attributes object and its sensitive list in a
// state with one managed terraform_data resource named x.
func conformanceState(attributes, sensitive string) string {
	return `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"x",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":` + attributes + `,"sensitive_attributes":` + sensitive + `}]}]}`
}

// conformanceOtherMarks keeps marks non-empty without matching the test
// address, so only sensitive_attributes drives masking.
func conformanceOtherMarks() workspace.SensitiveMarks {
	return workspace.SensitiveMarks{"other.none": {"/id"}}
}

// escapeConformanceToken escapes one pointer token: ~ first, then /.
func escapeConformanceToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// unescapeConformanceToken decodes one pointer token: ~1 first, then ~0.
func unescapeConformanceToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
}

// splitConformancePath splits a full value path into unescaped tokens.
func splitConformancePath(path string) []string {
	raw := strings.Split(strings.TrimPrefix(path, "/"), "/")
	out := make([]string, 0, len(raw))
	for _, token := range raw {
		out = append(out, unescapeConformanceToken(token))
	}
	return out
}

// isConformanceWrapper reports a dynamic wrapper: a map with only the keys
// value and type.
func isConformanceWrapper(obj map[string]any) bool {
	if len(obj) != 2 {
		return false
	}
	_, hasValue := obj["value"]
	_, hasType := obj["type"]
	return hasValue && hasType
}

// isMarkerShaped reports a marker-shaped value: a map with secret set to
// the bool true.
func isMarkerShaped(v any) bool {
	obj, ok := v.(map[string]any)
	if !ok {
		return false
	}
	flag, ok := obj["secret"].(bool)
	return ok && flag
}

// decodeConformanceDoc decodes state with numbers kept exact.
func decodeConformanceDoc(state []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(state))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, false
	}
	return doc, true
}

// conformanceResourceList reads the resources array of doc.
func conformanceResourceList(doc any) []any {
	top, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	list, ok := top["resources"].([]any)
	if !ok {
		return nil
	}
	return list
}

// conformanceInstanceAt returns the kth object instance of resource, skipping
// entries that are not objects.
func conformanceInstanceAt(resource map[string]any, want int) (map[string]any, bool) {
	raw, ok := resource["instances"].([]any)
	if !ok {
		return nil, false
	}
	count := 0
	for _, elem := range raw {
		obj, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		if count == want {
			return obj, true
		}
		count++
	}
	return nil, false
}

// conformancePath is a value path below one instance.
type conformancePath struct {
	resource int
	position int
	rest     []string
}

// parseConformancePath reads P as a value path below one instance.
func parseConformancePath(path string) (conformancePath, bool) {
	if path == "" || path[0] != '/' {
		return conformancePath{resource: 0, position: 0, rest: nil}, false
	}
	tokens := splitConformancePath(path)
	if len(tokens) < 5 || tokens[0] != "resources" || tokens[2] != "instances" ||
		tokens[4] != "attributes" {
		return conformancePath{resource: 0, position: 0, rest: nil}, false
	}
	resource, errR := strconv.Atoi(tokens[1])
	position, errP := strconv.Atoi(tokens[3])
	if errR != nil || resource < 0 || errP != nil || position < 0 {
		return conformancePath{resource: 0, position: 0, rest: nil}, false
	}
	return conformancePath{resource: resource, position: position, rest: tokens[5:]}, true
}

// oracleMap walks tokens under a map, with the wrapper rule.
func oracleMap(doc map[string]any, tokens []string) (any, bool) {
	next, present := doc[tokens[0]]
	if !present {
		return nil, false
	}
	if len(tokens) == 1 {
		return next, true
	}
	if found, ok := oracleLookup(next, tokens[1:]); ok {
		return found, true
	}
	if wrapped, ok := next.(map[string]any); ok && isConformanceWrapper(wrapped) {
		return oracleLookup(wrapped["value"], tokens[1:])
	}
	return nil, false
}

// oracleList walks tokens under a list. The token uses Atoi as the code
// does, with no wrapper rule for list elements.
func oracleList(list []any, tokens []string) (any, bool) {
	index, err := strconv.Atoi(tokens[0])
	if err != nil || index < 0 || index >= len(list) {
		return nil, false
	}
	if len(tokens) == 1 {
		return list[index], true
	}
	return oracleLookup(list[index], tokens[1:])
}

// oracleLookup returns the value at tokens under node, or false.
func oracleLookup(node any, tokens []string) (any, bool) {
	if len(tokens) == 0 {
		return node, true
	}
	switch typed := node.(type) {
	case map[string]any:
		return oracleMap(typed, tokens)
	case []any:
		return oracleList(typed, tokens)
	default:
		return nil, false
	}
}

// plainAt is the oracle: the plain value at P in state, or false.
func plainAt(state []byte, path string) (any, bool) {
	parsed, ok := parseConformancePath(path)
	if !ok {
		return nil, false
	}
	doc, ok := decodeConformanceDoc(state)
	if !ok {
		return nil, false
	}
	list := conformanceResourceList(doc)
	if parsed.resource < 0 || parsed.resource >= len(list) {
		return nil, false
	}
	obj, ok := list[parsed.resource].(map[string]any)
	if !ok {
		return nil, false
	}
	instance, ok := conformanceInstanceAt(obj, parsed.position)
	if !ok {
		return nil, false
	}
	attrs, present := instance["attributes"]
	if !present {
		attrs = map[string]any{}
	}
	return oracleLookup(attrs, parsed.rest)
}

// marshalExact marshals v for plain-value compare.
func marshalExact(v any) (string, bool) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// conformanceWalk collects node paths below attributes.
type conformanceWalk struct {
	paths  []string
	tokens [][]string
}

// walkConformanceTree appends each node path below attributes to out.
func walkConformanceTree(node any, base string, tokens []string, out *conformanceWalk) {
	switch typed := node.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := base + "/" + escapeConformanceToken(key)
			childTokens := append(append([]string(nil), tokens...), key)
			out.paths = append(out.paths, childPath)
			out.tokens = append(out.tokens, childTokens)
			walkConformanceTree(child, childPath, childTokens, out)
		}
	case []any:
		for i, child := range typed {
			childPath := base + "/" + strconv.Itoa(i)
			childTokens := append(append([]string(nil), tokens...), strconv.Itoa(i))
			out.paths = append(out.paths, childPath)
			out.tokens = append(out.tokens, childTokens)
			walkConformanceTree(child, childPath, childTokens, out)
		}
	default:
	}
}

// directChildAt follows tokens without the wrapper rule.
func directChildAt(node any, tokens []string) (any, bool) {
	current := node
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[token]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

// stripIndices lists token positions where a wrapper value step sits.
func stripIndices(attrs any, tokens []string) []int {
	var out []int
	for i, token := range tokens {
		if token != "value" {
			continue
		}
		parent, ok := directChildAt(attrs, tokens[:i])
		if !ok {
			continue
		}
		obj, ok := parent.(map[string]any)
		if !ok || !isConformanceWrapper(obj) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// strippedPath joins tokens under base, dropping the marked positions.
func strippedPath(base string, tokens []string, drop map[int]bool) string {
	var sb strings.Builder
	sb.WriteString(base)
	for i, token := range tokens {
		if drop[i] {
			continue
		}
		sb.WriteString("/")
		sb.WriteString(escapeConformanceToken(token))
	}
	return sb.String()
}

// logicalVariants returns paths made by dropping wrapper value steps.
func logicalVariants(base string, attrs any, tokens []string) []string {
	strips := stripIndices(attrs, tokens)
	if len(strips) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, pos := range strips {
		path := strippedPath(base, tokens, map[int]bool{pos: true})
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	dropAll := map[int]bool{}
	for _, pos := range strips {
		dropAll[pos] = true
	}
	if full := strippedPath(base, tokens, dropAll); !seen[full] {
		seen[full] = true
		out = append(out, full)
	}
	return out
}

// physicalForInstance collects physical paths and token slices of one
// instance.
func physicalForInstance(attrs any, base string) ([]string, [][]string) {
	out := &conformanceWalk{paths: nil, tokens: nil}
	walkConformanceTree(attrs, base, nil, out)
	return out.paths, out.tokens
}

// instanceBase is the attributes base path of one instance.
func instanceBase(resource, position int) string {
	return "/resources/" + strconv.Itoa(resource) +
		"/instances/" + strconv.Itoa(position) + "/attributes"
}

// decoyPaths returns fixed paths that name no secret.
func decoyPaths(base string, masked []string) []string {
	trailing := base + "/nope/"
	if len(masked) > 0 {
		trailing = masked[0] + "/"
	}
	return []string{
		base + "/nope",
		base + "/01",
		base + "/-0",
		trailing,
		"/resources/5/instances/0/attributes/id",
		"/resources/0/instances/7/attributes/id",
		base,
		base + "/bad~2key",
	}
}

// dedupStrings drops duplicate paths, keeping order.
func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, path := range in {
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// candidatePaths builds the paths to check: masked, physical, logical and
// decoys.
func candidatePaths(state []byte, masked []string) []string {
	doc, ok := decodeConformanceDoc(state)
	if !ok {
		return dedupStrings(masked)
	}
	var out []string
	out = append(out, masked...)
	list := conformanceResourceList(doc)
	for r := range list {
		obj, ok := list[r].(map[string]any)
		if !ok {
			continue
		}
		out = append(out, candidatesForResource(obj, r, masked)...)
	}
	if len(list) == 0 {
		out = append(out, decoyPaths(instanceBase(0, 0), masked)...)
	}
	return dedupStrings(out)
}

// candidatesForResource collects physical, logical and decoy paths of one
// resource.
func candidatesForResource(obj map[string]any, resource int, masked []string) []string {
	raw, ok := obj["instances"].([]any)
	if !ok {
		return nil
	}
	var out []string
	position := 0
	for _, elem := range raw {
		instance, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		attrs, present := instance["attributes"]
		if !present {
			attrs = map[string]any{}
		}
		base := instanceBase(resource, position)
		physical, tokenSlices := physicalForInstance(attrs, base)
		out = append(out, physical...)
		for _, tokens := range tokenSlices {
			out = append(out, logicalVariants(base, attrs, tokens)...)
		}
		out = append(out, decoyPaths(base, masked)...)
		position++
	}
	return out
}

// agreementInput holds the inputs for one agreement check.
type agreementInput struct {
	label  string
	state  string
	path   string
	masked []string
	got    any
	err    error
	plain  any
	found  bool
}

// agreementError returns empty when the reveal result matches the masked
// list and the plain value, else a message that shows the mismatch.
func agreementError(in agreementInput) string {
	want := slices.Contains(in.masked, in.path) && in.found && !isMarkerShaped(in.plain)
	if !want {
		if in.err == nil {
			return fmt.Sprintf("%s: reveal of %q succeeded, want not-found; state %s",
				in.label, in.path, in.state)
		}
		if !errors.Is(in.err, workspace.ErrResourceNotFound) {
			return fmt.Sprintf("%s: reveal of %q error = %v, want not-found; state %s",
				in.label, in.path, in.err, in.state)
		}
		return ""
	}
	if in.err != nil {
		return fmt.Sprintf("%s: reveal of %q error = %v, want success; state %s",
			in.label, in.path, in.err, in.state)
	}
	wantRaw, okW := marshalExact(in.plain)
	gotRaw, okG := marshalExact(in.got)
	if !okW || !okG {
		return fmt.Sprintf("%s: compare of %q failed to marshal; state %s",
			in.label, in.path, in.state)
	}
	if wantRaw != gotRaw {
		return fmt.Sprintf("%s: reveal of %q = %s, want %s; state %s",
			in.label, in.path, gotRaw, wantRaw, in.state)
	}
	return ""
}

// agreementCount holds checked and revealed path counts.
type agreementCount struct {
	checked  int
	revealed int
}

// add adds other counts to the total.
func (c *agreementCount) add(other agreementCount) {
	c.checked += other.checked
	c.revealed += other.revealed
}

// conformanceCheck holds the inputs for one agreement check.
type conformanceCheck struct {
	label  string
	state  string
	marks  workspace.SensitiveMarks
	masked []string
	path   string
}

// checkOnePath checks the agreement property for one path P. It returns true
// when the reveal call succeeded.
func checkOnePath(t *testing.T, c conformanceCheck) bool {
	t.Helper()
	got, _, err := workspace.RevealValue([]byte(c.state), c.marks, c.path)
	plain, found := plainAt([]byte(c.state), c.path)
	in := agreementInput{
		label:  c.label,
		state:  c.state,
		path:   c.path,
		masked: c.masked,
		got:    got,
		err:    err,
		plain:  plain,
		found:  found,
	}
	if msg := agreementError(in); msg != "" {
		t.Error(msg)
	}
	return err == nil
}

// checkAgreement checks every candidate path of one state. It returns the
// counts of checked and revealed paths.
func checkAgreement(
	t *testing.T, label, state string, marks workspace.SensitiveMarks,
) agreementCount {
	t.Helper()
	masked, err := workspace.MaskedPaths([]byte(state), marks)
	if err != nil {
		t.Fatalf("%s: masked paths: %v; state %s", label, err, state)
	}
	candidates := candidatePaths([]byte(state), masked)
	var out agreementCount
	out.checked = len(candidates)
	for _, path := range candidates {
		if checkOnePath(t, conformanceCheck{
			label:  label,
			state:  state,
			marks:  marks,
			masked: masked,
			path:   path,
		}) {
			out.revealed++
		}
	}
	return out
}

// conformanceCase is one table row: a state with its marks.
type conformanceCase struct {
	name       string
	state      string
	marks      workspace.SensitiveMarks
	mustReveal bool
}

// largeConformanceAttributes builds 300 keys with 3 secrets and 5 copies.
func largeConformanceAttributes() string {
	var sb strings.Builder
	sb.WriteString("{")
	for i := range 300 {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(strconv.Quote("k" + strconv.Itoa(i)))
		sb.WriteString(":")
		switch i {
		case 10, 11, 12:
			sb.WriteString(strconv.Quote("secret-1"))
		case 100, 101, 102:
			sb.WriteString(strconv.Quote("secret-2"))
		case 200, 201:
			sb.WriteString(strconv.Quote("secret-3"))
		default:
			sb.WriteString(strconv.Quote("plain-" + strconv.Itoa(i)))
		}
	}
	sb.WriteString("}")
	return sb.String()
}

// multiConformanceState builds two resources, the second with two
// instances, all sharing one secret value.
func multiConformanceState() string {
	one := `{"mode":"managed","type":"terraform_data","name":"a",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"a","token":"shared-secret"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"token"}]]}]}`
	two := `{"mode":"managed","type":"terraform_data","name":"b",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"b0","token":"shared-secret"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"token"}]]},` +
		`{"schema_version":0,` +
		`"attributes":{"id":"b1","token":"shared-secret"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"token"}]]}]}`
	return `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` + one + `,` + two + `]}`
}

// missingConformanceState builds a resource whose instance holds no
// attributes key.
func missingConformanceState() string {
	return `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"x",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"sensitive_attributes":[]}]}]}`
}

// conformanceCases lists the fixed states.
func conformanceCases() []conformanceCase {
	wrapperAttrs := `{"input":{"value":{"k":"s"},"type":"dynamic"},` +
		`"output":{"value":{"k":"s"},"type":"dynamic"}}`
	return []conformanceCase{
		{
			name: "no marks nested arrays",
			state: conformanceState(
				`{"id":"x","a":{"b":["s1",{"c":"s2"}]},"list":[1,2,{"k":"v"}]}`,
				`[]`,
			),
			marks:      nil,
			mustReveal: true,
		},
		{
			name: "one sensitive scalar",
			state: conformanceState(
				`{"id":"a","token":"s3cr3t","other":"plain"}`,
				`[[{"type":"get_attr","value":"token"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "sensitive array element",
			state: conformanceState(
				`{"id":"a","list":["x","secret","y"]}`,
				`[[{"type":"get_attr","value":"list"},{"type":"index","value":1}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "duplicate string",
			state: conformanceState(
				`{"token":"s3cr3t","copy":"s3cr3t","nested":{"deep":"s3cr3t"},"other":"plain"}`,
				`[[{"type":"get_attr","value":"token"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "duplicate object and array",
			state: conformanceState(
				`{"obj":{"k1":"v","k2":1},"obj_copy":{"k2":1,"k1":"v"},`+
					`"arr":[1,2],"arr_copy":[1,2],"other":"x"}`,
				`[[{"type":"get_attr","value":"obj"}],[{"type":"get_attr","value":"arr"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "empty string not propagable",
			state: conformanceState(
				`{"token":"","copy":"","other":"x"}`,
				`[[{"type":"get_attr","value":"token"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name: "numbers not propagable",
			state: conformanceState(
				`{"n":1,"copy_float":1.0,"copy_int":1,"other":2}`,
				`[[{"type":"get_attr","value":"n"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name: "dynamic wrapper sensitive input",
			state: conformanceState(
				wrapperAttrs,
				`[[{"type":"get_attr","value":"input"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name:       "dynamic wrapper kept logical mark",
			state:      conformanceState(wrapperAttrs, `[]`),
			marks:      workspace.SensitiveMarks{"terraform_data.x": {"/input/k"}},
			mustReveal: false,
		},
		{
			name: "nested ancestor first",
			state: conformanceState(
				`{"a":{"b":"secret","c":"plain"},"other":"x"}`,
				`[[{"type":"get_attr","value":"a"}],`+
					`[{"type":"get_attr","value":"a"},{"type":"get_attr","value":"b"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name: "nested descendant first",
			state: conformanceState(
				`{"a":{"b":"secret","c":"plain"},"other":"x"}`,
				`[[{"type":"get_attr","value":"a"},{"type":"get_attr","value":"b"}],`+
					`[{"type":"get_attr","value":"a"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "two pointers one node via wrapper",
			state: conformanceState(
				`{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
				`[[{"type":"get_attr","value":"w"},{"type":"get_attr","value":"k"}],`+
					`[{"type":"get_attr","value":"w"},{"type":"get_attr","value":"value"},`+
					`{"type":"get_attr","value":"k"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name: "duplicate inside masked subtree",
			state: conformanceState(
				`{"a":"dup-secret","b":{"c":"dup-secret","d":"plain"}}`,
				`[[{"type":"get_attr","value":"a"}],[{"type":"get_attr","value":"b"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name: "marker shaped real value",
			state: conformanceState(
				`{"m":{"secret":true,"path":"x"},"u":{"secret":true,"path":"y"},"other":"z"}`,
				`[[{"type":"get_attr","value":"m"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name: "escaped keys",
			state: conformanceState(
				`{"a/b":"s1","c~d":"s2","e~1f":"s3","other":"x"}`,
				`[[{"type":"get_attr","value":"a/b"}],`+
					`[{"type":"get_attr","value":"c~d"}],[{"type":"get_attr","value":"e~1f"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "pointers match nothing",
			state: conformanceState(
				`{"a":null,"list":["x"]}`,
				`[[{"type":"get_attr","value":"missing"}],`+
					`[{"type":"get_attr","value":"list"},{"type":"index","value":5}],`+
					`[{"type":"get_attr","value":"a"},{"type":"get_attr","value":"b"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name: "secret key below masked parent",
			state: conformanceState(
				`{"p":{"secret":"s-sensitive","path":"p-path","other":"x"},"other":"y"}`,
				`[[{"type":"get_attr","value":"p"}],`+
					`[{"type":"get_attr","value":"p"},{"type":"get_attr","value":"secret"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name: "pointer through wrapper into list",
			state: conformanceState(
				`{"w":{"value":[10,20,30],"type":"dynamic"},"other":"x"}`,
				`[[{"type":"get_attr","value":"w"},{"type":"index","value":1}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name:       "multi instance shared secret",
			state:      multiConformanceState(),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "large map",
			state: conformanceState(
				largeConformanceAttributes(),
				`[[{"type":"get_attr","value":"k10"}],`+
					`[{"type":"get_attr","value":"k100"}],[{"type":"get_attr","value":"k200"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name:       "empty attributes",
			state:      conformanceState(`{}`, `[]`),
			marks:      nil,
			mustReveal: false,
		},
		{
			name:       "missing attributes",
			state:      missingConformanceState(),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name:       "null attributes",
			state:      conformanceState(`null`, `[]`),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name:       "kept mark for address",
			state:      conformanceState(`{"m":"kept-secret","other":"plain"}`, `[]`),
			marks:      workspace.SensitiveMarks{"terraform_data.x": {"/m"}},
			mustReveal: true,
		},
		{
			name: "number literals inside containers",
			state: conformanceState(
				`{"a":{"k":1,"s":"xA"},"b":{"k":1.0,"s":"xA"},`+
					`"c":{"k":1,"s":"xA"},"d":{"k":1e0,"s":"xA"}}`,
				`[[{"type":"get_attr","value":"a"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "key order and array order",
			state: conformanceState(
				`{"s":{"x":1,"y":[1,2]},"same":{"y":[1,2],"x":1},`+
					`"other":{"y":[2,1],"x":1}}`,
				`[[{"type":"get_attr","value":"s"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "deep duplicate container",
			state: conformanceState(
				`{"s":{"p":{"q":["v",{"r":"w"}]}},`+
					`"t":{"u":{"p":{"q":["v",{"r":"w"}]}}}}`,
				`[[{"type":"get_attr","value":"s"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "dup of pointer value deep in sibling",
			state: conformanceState(
				`{"token":"deep-secret","sib":{"l1":{"l2":"deep-secret"}},"other":"x"}`,
				`[[{"type":"get_attr","value":"token"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "dup ancestor blocks deeper",
			state: conformanceState(
				`{"a":"inner","o":{"k":"inner"},"t":{"k":"inner"},"other":"x"}`,
				`[[{"type":"get_attr","value":"a"}],[{"type":"get_attr","value":"o"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "dup inside applied location",
			state: conformanceState(
				`{"a":"s","p":{"q":"s","r":"x"},"other":"y"}`,
				`[[{"type":"get_attr","value":"a"}],[{"type":"get_attr","value":"p"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "pointer to marker shaped value",
			state: conformanceState(
				`{"m":{"secret":true},"other":"z"}`,
				`[[{"type":"get_attr","value":"m"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: false,
		},
		{
			name: "non-canonical index pointer",
			state: conformanceState(
				`{"l":["s","t"],"c":["s","t"],"d":"t"}`,
				`[[{"type":"get_attr","value":"l"},{"type":"index","value":"01"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "dirty node is not a duplicate",
			state: conformanceState(
				`{"s":{"k":"v"},"t":{"k":"v"}}`,
				`[[{"type":"get_attr","value":"s"}],`+
					`[{"type":"get_attr","value":"t"},{"type":"get_attr","value":"k"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "dirty ancestor does not block a duplicate",
			state: conformanceState(
				`{"s":{"k":"v","z":"v"},"t":{"k":"v","z":"v"}}`,
				`[[{"type":"get_attr","value":"s"}],`+
					`[{"type":"get_attr","value":"t"},{"type":"get_attr","value":"k"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
		{
			name: "clean ancestor blocks a duplicate",
			state: conformanceState(
				`{"s":{"k":"v"},"t":{"u":{"k":"v"}},"w":{"k":"v"}}`,
				`[[{"type":"get_attr","value":"s"}]]`,
			),
			marks:      conformanceOtherMarks(),
			mustReveal: true,
		},
	}
}

// TestRevealConformanceAgreement checks the fixed states.
func TestRevealConformanceAgreement(t *testing.T) {
	t.Parallel()
	var total agreementCount
	for _, tc := range conformanceCases() {
		got := checkAgreement(t, tc.name, tc.state, tc.marks)
		if tc.mustReveal && got.revealed == 0 {
			t.Errorf("%s: revealed 0 paths, want at least 1", tc.name)
		}
		total.add(got)
	}
	t.Logf("checked %d candidate paths, revealed %d", total.checked, total.revealed)
	if total.revealed < 40 {
		t.Errorf("revealed %d paths, want at least 40", total.revealed)
	}
}

// randomLeafValues holds the leaf pool of the generator.
func randomLeafValues() []any {
	return []any{"s1", "s2", "", 1, 1.5, true, false, nil}
}

// randomLeaf picks one leaf.
func randomLeaf(rng *rand.Rand) any {
	pool := randomLeafValues()
	return pool[rng.IntN(len(pool))]
}

// randomMapKeys holds the key alphabet of the generator.
func randomMapKeys() []string {
	return []string{"a", "b", "c", "value", "type", "secret", "path"}
}

// randomTree builds one value with depth at most 4.
func randomTree(rng *rand.Rand, depth int) any {
	if depth >= 4 {
		return randomLeaf(rng)
	}
	switch rng.IntN(10) {
	case 0, 1, 2, 3:
		return randomTreeMap(rng, depth)
	case 4, 5:
		return randomTreeList(rng, depth)
	default:
		return randomLeaf(rng)
	}
}

// randomTreeMap builds one map, sometimes a wrapper.
func randomTreeMap(rng *rand.Rand, depth int) any {
	if rng.IntN(10) == 0 {
		return map[string]any{"value": randomTree(rng, depth+1), "type": "dynamic"}
	}
	keys := randomMapKeys()
	count := rng.IntN(5)
	picked := map[string]bool{}
	out := map[string]any{}
	for range count {
		key := keys[rng.IntN(len(keys))]
		if picked[key] {
			continue
		}
		picked[key] = true
		out[key] = randomTree(rng, depth+1)
	}
	return out
}

// randomTreeList builds one list of 0 to 3 elements.
func randomTreeList(rng *rand.Rand, depth int) any {
	count := rng.IntN(4)
	out := make([]any, 0, count)
	for range count {
		out = append(out, randomTree(rng, depth+1))
	}
	return out
}

// randomAttributes builds the top attributes object.
func randomAttributes(rng *rand.Rand) map[string]any {
	impl := randomTreeMap(rng, 0)
	if obj, ok := impl.(map[string]any); ok {
		return obj
	}
	return map[string]any{"a": impl}
}

// collectRandomTokens lists every node token path below node.
func collectRandomTokens(node any, prefix []string, out *[][]string) {
	switch typed := node.(type) {
	case map[string]any:
		for key, child := range typed {
			next := append(append([]string(nil), prefix...), key)
			*out = append(*out, next)
			collectRandomTokens(child, next, out)
		}
	case []any:
		for i, child := range typed {
			next := append(append([]string(nil), prefix...), strconv.Itoa(i))
			*out = append(*out, next)
			collectRandomTokens(child, next, out)
		}
	default:
	}
}

// ctyStepOf renders one token as a cty path step object.
func ctyStepOf(parent any, token string) map[string]any {
	if _, ok := parent.([]any); ok {
		if n, err := strconv.Atoi(token); err == nil {
			return map[string]any{"type": "index", "value": n}
		}
		return map[string]any{"type": "get_attr", "value": token}
	}
	return map[string]any{"type": "get_attr", "value": token}
}

// ctyPathOf renders tokens as cty get_attr and index steps.
func ctyPathOf(attrs any, tokens []string) []any {
	steps := make([]any, 0, len(tokens))
	node := attrs
	for _, token := range tokens {
		steps = append(steps, ctyStepOf(node, token))
		next, ok := directChildAt(node, []string{token})
		if !ok {
			break
		}
		node = next
	}
	return steps
}

// randomSensitive picks 0 to 3 real node paths plus at times a missing one.
func randomSensitive(rng *rand.Rand, attrs map[string]any, tokens [][]string) []any {
	count := rng.IntN(4)
	var out []any
	for i := 0; i < count && len(tokens) > 0; i++ {
		pick := tokens[rng.IntN(len(tokens))]
		out = append(out, ctyPathOf(attrs, pick))
	}
	if rng.IntN(4) == 0 {
		out = append(out, []any{map[string]any{"type": "get_attr", "value": "missing"}})
	}
	return out
}

// pointerOf renders tokens as a kept-mark pointer.
func pointerOf(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, token := range tokens {
		sb.WriteString("/")
		sb.WriteString(escapeConformanceToken(token))
	}
	return sb.String()
}

// randomMarks picks nil, a non-matching entry, or a kept mark.
func randomMarks(rng *rand.Rand, tokens [][]string) workspace.SensitiveMarks {
	switch rng.IntN(4) {
	case 0:
		return nil
	case 1:
		return conformanceOtherMarks()
	default:
		if len(tokens) == 0 {
			return conformanceOtherMarks()
		}
		pick := tokens[rng.IntN(len(tokens))]
		return workspace.SensitiveMarks{"terraform_data.x": {pointerOf(pick)}}
	}
}

// randomState builds one state with its marks from rng.
func randomState(rng *rand.Rand) (string, workspace.SensitiveMarks, error) {
	attrs := randomAttributes(rng)
	var tokens [][]string
	collectRandomTokens(attrs, nil, &tokens)
	sensitive := randomSensitive(rng, attrs, tokens)
	marks := randomMarks(rng, tokens)
	attrsRaw, err := json.Marshal(attrs)
	if err != nil {
		return "", nil, fmt.Errorf("marshal attributes: %w", err)
	}
	sensitiveRaw, err := json.Marshal(sensitive)
	if err != nil {
		return "", nil, fmt.Errorf("marshal attributes: %w", err)
	}
	return conformanceState(string(attrsRaw), string(sensitiveRaw)), marks, nil
}

// TestRevealConformanceRandom checks 400 generated states with one shared
// source, so its loop holds no parallel subtests.
func TestRevealConformanceRandom(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // Seeded test data generator, not a security use.
	var total agreementCount
	for i := range 400 {
		state, marks, err := randomState(rng)
		if err != nil {
			t.Fatalf("random state: %v", err)
		}
		total.add(checkAgreement(t, "seed "+strconv.Itoa(i), state, marks))
	}
	t.Logf("checked %d candidate paths, revealed %d", total.checked, total.revealed)
	if total.revealed < 300 {
		t.Errorf("revealed %d paths, want at least 300", total.revealed)
	}
}

// disjointLeafValues holds the small leaf pool of the disjoint generator,
// so duplicates are frequent.
func disjointLeafValues() []any {
	return []any{"dup1", "dup2", "dup3", "", 1, true, false, nil}
}

// disjointLeaf picks one leaf.
func disjointLeaf(rng *rand.Rand) any {
	pool := disjointLeafValues()
	return pool[rng.IntN(len(pool))]
}

// disjointMapKeys holds the key alphabet of the disjoint generator.
func disjointMapKeys() []string {
	return []string{"a", "b", "c", "d", "value", "type"}
}

// disjointTree builds one value with depth at most 4 and no dynamic wrapper
// shaped map: a map with exactly the keys value and type gains a third key.
func disjointTree(rng *rand.Rand, depth int) any {
	if depth >= 4 {
		return disjointLeaf(rng)
	}

	switch rng.IntN(10) {
	case 0, 1, 2, 3:
		return disjointTreeMap(rng, depth)
	case 4, 5:
		return disjointTreeList(rng, depth)
	default:
		return disjointLeaf(rng)
	}
}

// disjointTreeMap builds one map without a wrapper shape.
func disjointTreeMap(rng *rand.Rand, depth int) any {
	keys := disjointMapKeys()
	count := rng.IntN(5)
	picked := map[string]bool{}
	out := map[string]any{}

	for range count {
		key := keys[rng.IntN(len(keys))]
		if picked[key] {
			continue
		}

		picked[key] = true
		out[key] = disjointTree(rng, depth+1)
	}

	if len(out) == 2 {
		_, hasValue := out["value"]
		_, hasType := out["type"]

		if hasValue && hasType {
			out["extra"] = disjointTree(rng, depth+1)
		}
	}

	return out
}

// disjointTreeList builds one list of 0 to 3 elements.
func disjointTreeList(rng *rand.Rand, depth int) any {
	count := rng.IntN(4)
	out := make([]any, 0, count)

	for range count {
		out = append(out, disjointTree(rng, depth+1))
	}

	return out
}

// disjointAttributes builds the top attributes object.
func disjointAttributes(rng *rand.Rand) map[string]any {
	impl := disjointTreeMap(rng, 0)
	if obj, ok := impl.(map[string]any); ok {
		return obj
	}

	return map[string]any{"a": impl}
}

// isTokenPrefix reports whether prefix is a prefix of tokens, equal length
// included.
func isTokenPrefix(prefix, tokens []string) bool {
	if len(prefix) > len(tokens) {
		return false
	}

	for i := range prefix {
		if prefix[i] != tokens[i] {
			return false
		}
	}

	return true
}

// disjointPicks picks up to 3 pairwise non-nested real node paths.
func disjointPicks(rng *rand.Rand, tokens [][]string) [][]string {
	order := rng.Perm(len(tokens))
	var out [][]string

	for _, index := range order {
		if len(out) >= 3 {
			break
		}

		candidate := tokens[index]
		if len(candidate) == 0 {
			continue
		}

		nested := false

		for _, kept := range out {
			if isTokenPrefix(kept, candidate) || isTokenPrefix(candidate, kept) {
				nested = true

				break
			}
		}

		if !nested {
			out = append(out, candidate)
		}
	}

	return out
}

// deepCopyConformance returns a deep copy of a generator value.
func deepCopyConformance(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = deepCopyConformance(child)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = deepCopyConformance(child)
		}

		return out
	default:
		return v
	}
}

// plantDisjointCopies adds duplicate values for the picked pointers: for each
// pointer, with probability 1/2 a deep copy of the pointed value under a new
// dup key, and with probability 1/3 a deep copy of its parent container under
// a new par key. The new keys are top level, so they hold no pointer.
func plantDisjointCopies(rng *rand.Rand, attrs map[string]any, picks [][]string) {
	for i, pick := range picks {
		plantDisjointCopyForPick(rng, attrs, pick, i)
	}
}

// plantDisjointCopyForPick plants the copies for one picked pointer.
func plantDisjointCopyForPick(rng *rand.Rand, attrs map[string]any, pick []string, i int) {
	if len(pick) == 0 {
		return
	}

	plantDupCopy(rng, attrs, pick, i)
	plantParCopy(rng, attrs, pick, i)
}

// plantDupCopy plants a copy of the pointed value under a new dup key.
func plantDupCopy(rng *rand.Rand, attrs map[string]any, pick []string, i int) {
	if rng.IntN(2) != 0 {
		return
	}

	value, ok := directChildAt(attrs, pick)
	if !ok {
		return
	}

	key := "dup" + strconv.Itoa(i)
	if _, exists := attrs[key]; exists {
		return
	}

	attrs[key] = deepCopyConformance(value)
}

// plantParCopy plants a copy of the parent container under a new par key.
func plantParCopy(rng *rand.Rand, attrs map[string]any, pick []string, i int) {
	roll := rng.IntN(3)
	if roll != 0 {
		return
	}

	if len(pick) < 2 {
		return
	}

	parent, ok := directChildAt(attrs, pick[:len(pick)-1])
	if !ok {
		return
	}

	key := "par" + strconv.Itoa(i)
	if _, exists := attrs[key]; exists {
		return
	}

	attrs[key] = deepCopyConformance(parent)
}

// disjointState builds one state with pairwise non-nested pointers from rng.
func disjointState(rng *rand.Rand) (string, workspace.SensitiveMarks, error) {
	attrs := disjointAttributes(rng)

	var tokens [][]string

	collectRandomTokens(attrs, nil, &tokens)

	picks := disjointPicks(rng, tokens)

	plantDisjointCopies(rng, attrs, picks)

	var sensitive []any

	var marks workspace.SensitiveMarks

	switch {
	case len(picks) == 0:
		marks = conformanceOtherMarks()
	case rng.IntN(4) != 0:
		for _, pick := range picks {
			sensitive = append(sensitive, ctyPathOf(attrs, pick))
		}

		marks = conformanceOtherMarks()
	default:
		kept := picks[rng.IntN(len(picks))]
		for _, pick := range picks {
			if slices.Equal(pick, kept) {
				continue
			}

			sensitive = append(sensitive, ctyPathOf(attrs, pick))
		}

		marks = workspace.SensitiveMarks{"terraform_data.x": {pointerOf(kept)}}
	}

	if sensitive == nil {
		sensitive = []any{}
	}

	attrsRaw, err := json.Marshal(attrs)
	if err != nil {
		return "", nil, fmt.Errorf("marshal attributes: %w", err)
	}

	sensitiveRaw, err := json.Marshal(sensitive)
	if err != nil {
		return "", nil, fmt.Errorf("marshal sensitive: %w", err)
	}

	return conformanceState(string(attrsRaw), string(sensitiveRaw)), marks, nil
}

// TestRevealConformanceRandomDisjoint checks 800 generated states without
// wrapper shapes and with pairwise non-nested pointers.
func TestRevealConformanceRandomDisjoint(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(7, 9)) //nolint:gosec // Seeded test data generator, not a security use.

	var total agreementCount

	for i := range 500 {
		state, marks, err := disjointState(rng)
		if err != nil {
			t.Fatalf("disjoint state: %v", err)
		}

		total.add(checkAgreement(t, "disjoint "+strconv.Itoa(i), state, marks))
	}

	t.Logf("checked %d candidate paths, revealed %d", total.checked, total.revealed)

	if total.revealed < 300 {
		t.Errorf("revealed %d paths, want at least 300", total.revealed)
	}
}

// TestRevealConformanceStreamingPath checks disjoint states with a pad
// attribute over the 4 MiB fallback limit: any lookup that uses the full
// decode fails with ErrTooLarge, so success proves the streaming path.
func TestRevealConformanceStreamingPath(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(11, 13)) //nolint:gosec // Seeded test data generator, not a security use.

	pad := strings.Repeat("p", 4194304+65536)
	cases := make([]streamingCase, 0, 8)

	// The random choices happen here, in order, so the run stays the same
	// whatever the order of the parallel subtests.
	for i := range 8 {
		state, marks, err := disjointState(rng)
		if err != nil {
			t.Fatalf("disjoint state: %v", err)
		}

		state = addPadAttribute(t, state, pad)
		cases = append(cases, prepareStreamingState(t, rng, "streaming "+strconv.Itoa(i), state, marks))
	}

	var (
		mu    sync.Mutex
		total agreementCount
	)

	// The totals are checked after every parallel subtest is done.
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()

		t.Logf("checked %d candidate paths, revealed %d", total.checked, total.revealed)

		if total.revealed == 0 {
			t.Error("revealed 0 paths, want at least 1")
		}
	})

	for _, item := range cases {
		t.Run(item.check.label, func(t *testing.T) {
			t.Parallel()

			got := checkStreamingCandidates(t, item.check, item.candidates)

			mu.Lock()
			total.add(got)
			mu.Unlock()
		})
	}
}

// addPadAttribute adds a pad string to the attributes of state. Pad is
// never a pointer.
func addPadAttribute(t *testing.T, state, pad string) string {
	t.Helper()

	doc, ok := decodeConformanceDoc([]byte(state))
	if !ok {
		t.Fatal("decode state for pad")
	}

	top, ok := doc.(map[string]any)
	if !ok {
		t.Fatal("state top is not an object")
	}

	list, ok := top["resources"].([]any)
	if !ok || len(list) == 0 {
		t.Fatal("state has no resources")
	}

	obj, ok := list[0].(map[string]any)
	if !ok {
		t.Fatal("resource is not an object")
	}

	raw, ok := obj["instances"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatal("resource has no instances")
	}

	instance, ok := raw[0].(map[string]any)
	if !ok {
		t.Fatal("instance is not an object")
	}

	attrs, ok := instance["attributes"].(map[string]any)
	if !ok {
		t.Fatal("attributes is not an object")
	}

	attrs["pad"] = pad

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal padded state: %v", err)
	}

	return string(out)
}

// streamingCheck holds the inputs for one padded-state check.
type streamingCheck struct {
	label  string
	state  string
	marks  workspace.SensitiveMarks
	masked []string
}

// streamingCase is one padded state with the paths to check.
type streamingCase struct {
	check      streamingCheck
	candidates []string
}

// prepareStreamingState picks the masked paths and a few other node paths of
// one padded state. It is the only step that uses the random source.
func prepareStreamingState(
	t *testing.T, rng *rand.Rand, label, state string, marks workspace.SensitiveMarks,
) streamingCase {
	t.Helper()

	masked := streamingMasked(t, label, state, marks)
	physical := streamingPhysical(t, label, state)

	candidates := dedupStrings(append(
		append([]string(nil), masked[:min(len(masked), streamingMaskedPerState)]...),
		pickStreamingOthers(rng, physical, masked)...,
	))

	return streamingCase{
		check: streamingCheck{
			label:  label,
			state:  state,
			marks:  marks,
			masked: masked,
		},
		candidates: candidates,
	}
}

// streamingMasked returns the masked paths of one padded state.
func streamingMasked(
	t *testing.T, label, state string, marks workspace.SensitiveMarks,
) []string {
	t.Helper()

	masked, err := workspace.MaskedPaths([]byte(state), marks)
	if err != nil {
		t.Fatalf("%s: masked paths: %v; state holds %d bytes", label, err, len(state))
	}

	return masked
}

// streamingPhysical returns the physical node paths of one padded state.
func streamingPhysical(t *testing.T, label, state string) []string {
	t.Helper()

	doc, ok := decodeConformanceDoc([]byte(state))
	if !ok {
		t.Fatalf("%s: decode state", label)
	}

	list := conformanceResourceList(doc)
	parts := make([][]string, 0, len(list))

	for r, elem := range list {
		parts = append(parts, streamingResourcePaths(elem, r))
	}

	return slices.Concat(parts...)
}

// streamingResourcePaths returns the physical node paths of one resource.
func streamingResourcePaths(elem any, resource int) []string {
	obj, ok := elem.(map[string]any)
	if !ok {
		return nil
	}

	raw, ok := obj["instances"].([]any)
	if !ok {
		return nil
	}

	var out []string

	position := 0

	for _, item := range raw {
		instance, ok := item.(map[string]any)
		if !ok {
			continue
		}

		attrs, present := instance["attributes"]
		if !present {
			attrs = map[string]any{}
		}

		paths, _ := physicalForInstance(attrs, instanceBase(resource, position))

		out = append(out, paths...)
		position++
	}

	return out
}

// checkStreamingCandidates checks one candidate list and counts reveals.
func checkStreamingCandidates(t *testing.T, check streamingCheck, candidates []string) agreementCount {
	t.Helper()

	var out agreementCount

	out.checked = len(candidates)

	for _, path := range candidates {
		if checkStreamingPath(t, check, path) {
			out.revealed++
		}
	}

	return out
}

// checkStreamingPath checks one path of a padded state. It reports whether
// the reveal succeeded.
func checkStreamingPath(t *testing.T, check streamingCheck, path string) bool {
	t.Helper()

	got, _, rerr := workspace.RevealValue([]byte(check.state), check.marks, path)
	if rerr != nil && errors.Is(rerr, workspace.ErrTooLarge) {
		t.Errorf("%s: reveal of %q used the full decode: %v", check.label, path, rerr)
	}

	plain, found := plainAt([]byte(check.state), path)
	in := agreementInput{
		label:  check.label,
		state:  "(padded state)",
		path:   path,
		masked: check.masked,
		got:    got,
		err:    rerr,
		plain:  plain,
		found:  found,
	}

	if msg := agreementError(in); msg != "" {
		t.Error(msg)
	}

	return rerr == nil
}

// pickStreamingOthers picks up to 20 physical paths outside the masked set.
// The padded states are large, so each reveal costs three passes over more
// than 4 MiB. The test checks a few masked paths and a few other paths of
// each state; the unpadded generator checks every path.
const (
	streamingMaskedPerState = 8
	streamingOthersPerState = 5
)

func pickStreamingOthers(rng *rand.Rand, physical, masked []string) []string {
	inMasked := map[string]bool{}
	for _, path := range masked {
		inMasked[path] = true
	}

	var others []string

	for _, path := range physical {
		if !inMasked[path] {
			others = append(others, path)
		}
	}

	order := rng.Perm(len(others))
	var out []string

	for _, index := range order {
		if len(out) >= streamingOthersPerState {
			break
		}

		out = append(out, others[index])
	}

	return out
}

// errDriftBoom is a distinct error for the drift test.
var errDriftBoom = errors.New("boom")

// TestRevealConformanceDetectsDrift checks that the agreement helper spots
// a drift between the masked list and the reveal result.
func TestRevealConformanceDetectsDrift(t *testing.T) {
	t.Parallel()
	state := conformanceState(
		`{"token":"s3cr3t","id":"a"}`,
		`[[{"type":"get_attr","value":"token"}]]`,
	)
	marks := conformanceOtherMarks()
	path := "/resources/0/instances/0/attributes/token"
	masked, err := workspace.MaskedPaths([]byte(state), marks)
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}
	got, _, revealErr := workspace.RevealValue([]byte(state), marks, path)
	plain, found := plainAt([]byte(state), path)
	base := agreementInput{
		label:  "drift",
		state:  state,
		path:   path,
		masked: masked,
		got:    got,
		err:    revealErr,
		plain:  plain,
		found:  found,
	}
	if msg := agreementError(base); msg != "" {
		t.Errorf("true input gave %q, want empty", msg)
	}
	noMask := base
	noMask.masked = nil
	if msg := agreementError(noMask); msg == "" {
		t.Error("unmasked success gave empty, want mismatch")
	}
	nopePath := "/resources/0/instances/0/attributes/nope"
	nopePlain, nopeFound := plainAt([]byte(state), nopePath)
	nopeIn := agreementInput{
		label:  "drift",
		state:  state,
		path:   nopePath,
		masked: []string{nopePath},
		got:    "x",
		err:    nil,
		plain:  nopePlain,
		found:  nopeFound,
	}
	if msg := agreementError(nopeIn); msg == "" {
		t.Error("missing value with success gave empty, want mismatch")
	}
	boom := base
	boom.err = errDriftBoom
	if msg := agreementError(boom); msg == "" {
		t.Error("masked path with error gave empty, want mismatch")
	}
}
