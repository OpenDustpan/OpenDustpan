package profile

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Keys of an entry (a map whose keys start with "$").
const (
	keyValue       = "$value"
	keySecret      = "$secret"
	keySealed      = "$sealed"
	keyFinal       = "$final"
	keyType        = "$type"
	keyDescription = "$description"
	keyNeverStore  = "$neverStore"
)

// Top-level keys of a document.
const (
	topSchemaVersion = "schemaVersion"
	topDescription   = "description"
	topImports       = "imports"
	topProviders     = "providers"
	topRotators      = "rotators"
	topValues        = "values"
	topExports       = "exports"
	topChecks        = "checks"
)

// YAML tags of the nodes that this package makes.
const (
	tagStr  = "!!str"
	tagMap  = "!!map"
	tagBool = "!!bool"
)

const (
	maxChecks = 32
	pairSize  = 2
)

// Entry is one leaf of the values of a document: a plain value or an entry
// map. A secret entry holds no value here.
type Entry struct {
	// Path joins the keys with ".". A "." or "\" in a key is written with a
	// leading "\".
	Path        string
	Secret      bool
	Final       bool
	NeverStore  bool
	Description string
	// Kind is the kind of $type, or empty.
	Kind string
	// Value is the plain value of a non-secret entry.
	Value any
}

// Secret is one secret leaf of a document. Plain is set for $secret, and
// Sealed for $sealed.
type Secret struct {
	Path   string
	Plain  string
	Sealed []byte
}

// Document is a parsed and checked document.
type Document struct {
	root   *yaml.Node
	text   []byte
	leaves []leafNode
}

// leafNode is one leaf of values and the node that holds it.
type leafNode struct {
	entry Entry
	node  *yaml.Node
}

// ParseDocument parses text and checks the rules of the document format that
// need no provider and no other profile. It returns ErrInvalid for a bad
// document, ErrSchemaVersion for a higher schemaVersion, and ErrLimit for a
// document above a limit.
func ParseDocument(text []byte) (*Document, error) {
	if len(text) > MaxDocumentBytes {
		return nil, fmt.Errorf("%w: document has %d bytes, the limit is %d", ErrLimit, len(text), MaxDocumentBytes)
	}

	if !utf8.Valid(text) {
		return nil, fmt.Errorf("%w: document is not valid UTF-8", ErrInvalid)
	}

	root, err := decodeRoot(text)
	if err != nil {
		return nil, err
	}

	d := &Document{root: root, text: slices.Clone(text), leaves: nil}
	if checkErr := d.check(); checkErr != nil {
		return nil, checkErr
	}

	return d, nil
}

// decodeRoot reads the one YAML document of text and returns its map.
func decodeRoot(text []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(text))

	var doc yaml.Node

	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: document is empty", ErrInvalid)
		}

		return nil, fmt.Errorf("%w: document is not valid YAML: %w", ErrInvalid, err)
	}

	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: document holds more than one YAML document", ErrInvalid)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: document must be a map", ErrInvalid)
	}

	return doc.Content[0], nil
}

// Text returns the document text as parsed.
func (d *Document) Text() []byte { return d.text }

// Entries returns each leaf of values in document order. A secret entry
// holds no value. It never fails: ParseDocument checked the tree.
func (d *Document) Entries() []Entry {
	out := make([]Entry, 0, len(d.leaves))
	for _, l := range d.leaves {
		out = append(out, l.entry)
	}

	return out
}

// Secrets returns the secret leaves of values, in document order. It returns
// ErrInvalid for a $sealed value that is not base64.
func (d *Document) Secrets() ([]Secret, error) {
	var out []Secret

	var firstErr error

	for _, n := range d.secretNodes() {
		path, node := n.path, n.node

		if v := mapGet(node, keySecret); v != nil {
			out = append(out, Secret{Path: path, Plain: v.Value, Sealed: nil})

			continue
		}

		raw, err := decodeSealed(mapGet(node, keySealed))
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%w: %s: %s is not base64", ErrInvalid, path, keySealed)
		}

		out = append(out, Secret{Path: path, Plain: "", Sealed: raw})
	}

	return out, firstErr
}

// decodeSealed decodes the base64 text of a $sealed node.
func decodeSealed(n *yaml.Node) ([]byte, error) {
	if n == nil {
		return nil, fmt.Errorf("%w: %s is missing", ErrInvalid, keySealed)
	}

	raw, err := base64.StdEncoding.DecodeString(n.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not base64", ErrInvalid, keySealed)
	}

	return raw, nil
}

// Seal replaces each $secret by $sealed. seal turns the plain texts into
// ciphertexts, one for each text, in order. It returns the text to store.
// A document with no $secret is returned as written. Otherwise the document
// is written again from its tree: comments and the order of keys stay, and
// spacing may change.
func (d *Document) Seal(seal func(plain [][]byte) ([][]byte, error)) ([]byte, error) {
	var nodes []*yaml.Node

	for _, n := range d.secretNodes() {
		if mapGet(n.node, keySecret) != nil {
			nodes = append(nodes, n.node)
		}
	}

	if len(nodes) == 0 {
		return d.text, nil
	}

	plain := make([][]byte, len(nodes))
	for i, n := range nodes {
		v := mapGet(n, keySecret)
		if v == nil {
			return nil, fmt.Errorf("%w: %s is missing", ErrInvalid, keySecret)
		}

		plain[i] = []byte(v.Value)
	}

	sealed, err := seal(plain)
	if err != nil {
		return nil, err
	}

	if len(sealed) != len(nodes) {
		return nil, fmt.Errorf("%w: sealer returned %d values for %d secrets", ErrInvalid, len(sealed), len(nodes))
	}

	for i, n := range nodes {
		for j := 0; j < len(n.Content); j += pairSize {
			if n.Content[j].Value == keySecret {
				n.Content[j].Value = keySealed
				n.Content[j+1] = newNode(yaml.ScalarNode, tagStr, base64.StdEncoding.EncodeToString(sealed[i]))
			}
		}
	}

	return d.encode()
}

// RoutingChanges returns what a change from before to after changes in where
// a block sends secrets: a difference in a block of providers or rotators,
// in imports, in exports, or in a path that a block reads. It works on the
// syntax of the two documents. It errs on the side of reporting too much: a
// computed index counts as a read of the whole subtree. A nil before is an
// empty document. Each item names the block, the import or the path.
func RoutingChanges(before, after *Document) []string {
	b, a := before.orEmpty(), after.orEmpty()

	var out []string

	for _, key := range []string{topImports, topExports} {
		if nodeText(mapGet(b.root, key)) != nodeText(mapGet(a.root, key)) {
			out = append(out, key)
		}
	}

	tainted := map[string][]string{}

	for _, group := range []string{topProviders, topRotators} {
		changed, reads := blockChanges(b, a, group)
		out = append(out, changed...)

		maps.Copy(tainted, reads)
	}

	// A value that an export reads goes to the job proxy, to a file, to an
	// environment variable or to the configuration of a tool. A change of
	// such a value changes where a secret goes.
	for _, doc := range []*Document{b, a} {
		for _, n := range routedExports(doc) {
			maps.Copy(tainted, doc.taint(n))
		}
	}

	for _, k := range slices.Sorted(maps.Keys(tainted)) {
		p := tainted[k]
		if nodeText(b.nodeAt(p)) != nodeText(a.nodeAt(p)) {
			out = append(out, topValues+joinSegments(p))
		}
	}

	return out
}

// blockChanges compares the blocks of one group in two documents. It returns
// the names of the blocks that differ, and the paths that the blocks of
// either document read.
func blockChanges(b, a *Document, group string) ([]string, map[string][]string) {
	names := map[string]bool{}

	for _, doc := range []*Document{b, a} {
		if blocks := mapGet(doc.root, group); blocks != nil {
			for i := 0; i < len(blocks.Content); i += pairSize {
				names[blocks.Content[i].Value] = true
			}
		}
	}

	var changed []string

	reads := map[string][]string{}

	for _, name := range slices.Sorted(maps.Keys(names)) {
		bn, an := blockOf(b, group, name), blockOf(a, group, name)
		if nodeText(bn) != nodeText(an) {
			changed = append(changed, group+"."+name)
		}

		for _, side := range []struct {
			doc  *Document
			node *yaml.Node
		}{{b, bn}, {a, an}} {
			if side.node != nil {
				maps.Copy(reads, side.doc.taint(side.node))
			}
		}
	}

	return changed, reads
}

func (d *Document) check() error {
	seen := map[string]*yaml.Node{}

	if err := checkPlain(d.root); err != nil {
		return err
	}

	for i := 0; i < len(d.root.Content); i += pairSize {
		key := d.root.Content[i].Value

		switch key {
		case topSchemaVersion, topDescription, topImports, topProviders, topRotators, topValues, topExports,
			topChecks:
			seen[key] = d.root.Content[i+1]
		default:
			return fmt.Errorf("%w: unknown top-level key %q", ErrInvalid, key)
		}
	}

	checks := []func() error{
		func() error { return checkSchemaVersion(seen[topSchemaVersion]) },
		func() error { return checkDescription(seen[topDescription]) },
		func() error { return checkImports(seen[topImports]) },
		func() error { return checkBlocks(seen[topProviders], topProviders, MaxProviderBlocks) },
		func() error { return checkBlocks(seen[topRotators], topRotators, MaxRotatorBlocks) },
		func() error { return d.checkExports(seen[topExports]) },
		func() error { return checkChecks(seen[topChecks]) },
		func() error { return checkNoEntryKeys(seen) },
		func() error { return d.collectLeaves(seen[topValues]) },
	}

	for _, c := range checks {
		if err := c(); err != nil {
			return err
		}
	}

	return nil
}

// checkNoEntryKeys fails for a key that starts with "$" in any map outside
// values. Only values holds entries, so a $secret elsewhere would stay in
// plain text.
func checkNoEntryKeys(top map[string]*yaml.Node) error {
	for _, key := range []string{topImports, topProviders, topRotators, topExports, topChecks} {
		if n := top[key]; n != nil {
			if err := noEntryKeys(n, key); err != nil {
				return err
			}
		}
	}

	return nil
}

func noEntryKeys(n *yaml.Node, path string) error {
	if n.Kind == yaml.MappingNode {
		return noEntryKeysInMap(n, path)
	}

	if n.Kind != yaml.SequenceNode {
		return nil
	}

	for i, c := range n.Content {
		if err := noEntryKeys(c, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}

	return nil
}

func noEntryKeysInMap(n *yaml.Node, path string) error {
	for i := 0; i+1 < len(n.Content); i += pairSize {
		k := n.Content[i].Value
		if strings.HasPrefix(k, "$") {
			return fmt.Errorf("%w: %s.%s: entry keys such as $secret are allowed only under values: "+
				"put the secret in values and read it with ${ values.<path> }", ErrInvalid, path, k)
		}

		if err := noEntryKeys(n.Content[i+1], path+"."+k); err != nil {
			return err
		}
	}

	return nil
}

func (d *Document) encode() ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(pairSize)

	if err := enc.Encode(d.root); err != nil {
		return nil, fmt.Errorf("%w: write document: %w", ErrInvalid, err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("%w: write document: %w", ErrInvalid, err)
	}

	return buf.Bytes(), nil
}

func (d *Document) orEmpty() *Document {
	if d != nil {
		return d
	}

	return &Document{root: newNode(yaml.MappingNode, tagMap, ""), text: nil, leaves: nil}
}

type secretNode struct {
	path string
	node *yaml.Node
}

// secretNodes returns the entry maps of the secret leaves of values.
func (d *Document) secretNodes() []secretNode {
	var out []secretNode

	for _, l := range d.leaves {
		if l.entry.Secret && isEntryMap(l.node) {
			out = append(out, secretNode{path: l.entry.Path, node: l.node})
		}
	}

	return out
}

// nodeAt returns the node at path under the values of d, or the nearest
// node on the way when the path ends inside an entry or a plain value.
func (d *Document) nodeAt(path []string) *yaml.Node {
	cur := mapGet(d.root, topValues)

	for _, seg := range path {
		if cur == nil || cur.Kind != yaml.MappingNode || isEntryMap(cur) {
			return cur
		}

		next := mapGet(cur, seg)
		if next == nil {
			return nil
		}

		cur = next
	}

	return cur
}

// taint returns the paths under values that feed the block n, directly or
// through other expressions, keyed by their joined form.
func (d *Document) taint(n *yaml.Node) map[string][]string {
	out := map[string][]string{}
	queue := refsIn(n)

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		key := strings.Join(p, "\x00")
		if _, ok := out[key]; ok {
			continue
		}

		out[key] = p

		if target := d.nodeAt(p); target != nil {
			queue = append(queue, refsIn(target)...)
		}
	}

	return out
}

// newNode returns a node with the fields that this package sets.
func newNode(kind yaml.Kind, tag, value string) *yaml.Node {
	return &yaml.Node{
		Kind: kind, Style: 0, Tag: tag, Value: value, Anchor: "", Alias: nil, Content: nil,
		HeadComment: "", LineComment: "", FootComment: "", Line: 0, Column: 0,
	}
}

func newEntry(path string) Entry {
	return Entry{
		Path: path, Secret: false, Final: false, NeverStore: false, Description: "", Kind: "", Value: nil,
	}
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += pairSize {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}

	return nil
}

// routedExportKeys returns the keys of exports whose reads RoutingChanges
// follows. Each one hands a value to a run: exports.proxy to the job proxy,
// exports.files to a file, exports.variables to an environment variable, and
// exports.pulumi and exports.opentofu to the configuration of the tool.
func routedExportKeys() []string {
	return []string{exportProxy, exportFiles, exportVariables, exportPulumi, exportOpenTofu}
}

// routedExports returns the nodes of the keys of routedExportKeys that d has.
func routedExports(d *Document) []*yaml.Node {
	exports := mapGet(d.root, topExports)
	if exports == nil || exports.Kind != yaml.MappingNode {
		return nil
	}

	var out []*yaml.Node

	for _, key := range routedExportKeys() {
		if n := mapGet(exports, key); n != nil {
			out = append(out, n)
		}
	}

	return out
}

func blockOf(d *Document, group, name string) *yaml.Node {
	if blocks := mapGet(d.root, group); blocks != nil {
		return mapGet(blocks, name)
	}

	return nil
}

func isEntryMap(n *yaml.Node) bool {
	if n.Kind != yaml.MappingNode {
		return false
	}

	for i := 0; i < len(n.Content); i += pairSize {
		if strings.HasPrefix(n.Content[i].Value, "$") {
			return true
		}
	}

	return false
}

func escapeKey(k string) string {
	k = strings.ReplaceAll(k, `\`, `\\`)

	return strings.ReplaceAll(k, ".", `\.`)
}

func joinPath(parent, key string) string {
	if parent == "" {
		return escapeKey(key)
	}

	return parent + "." + escapeKey(key)
}

func joinSegments(p []string) string {
	var sb strings.Builder

	for _, s := range p {
		sb.WriteString("." + escapeKey(s))
	}

	return sb.String()
}

func nodeText(n *yaml.Node) string {
	if n == nil {
		return ""
	}

	b, err := yaml.Marshal(n)
	if err != nil {
		return "\x00error"
	}

	return string(b)
}
