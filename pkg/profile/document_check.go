package profile

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// checkKeys refuses a key that is not text, a merge key and a duplicate key
// in the map n.
func checkKeys(n *yaml.Node) error {
	keys := map[string]bool{}

	for i := 0; i < len(n.Content); i += pairSize {
		k := n.Content[i]
		if k.Kind != yaml.ScalarNode {
			return fmt.Errorf("%w: line %d: a key must be text", ErrInvalid, k.Line)
		}

		if k.Tag == "!!merge" || k.Value == "<<" {
			return fmt.Errorf("%w: line %d: merge keys (<<) are not allowed", ErrInvalid, k.Line)
		}

		if k.Tag == "!!binary" {
			return fmt.Errorf("%w: line %d: binary keys are not allowed", ErrInvalid, k.Line)
		}

		if keys[k.Value] {
			return fmt.Errorf("%w: line %d: key %q is twice in one map", ErrInvalid, k.Line, k.Value)
		}

		keys[k.Value] = true
	}

	return nil
}

// checkPlain refuses an alias and a duplicate key, at any depth.
func checkPlain(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return fmt.Errorf("%w: line %d: anchors and aliases are not allowed", ErrInvalid, n.Line)
	}

	if n.Kind == yaml.MappingNode {
		if err := checkKeys(n); err != nil {
			return err
		}
	}

	for _, c := range n.Content {
		if err := checkPlain(c); err != nil {
			return err
		}
	}

	return nil
}

func checkSchemaVersion(n *yaml.Node) error {
	if n == nil {
		return fmt.Errorf("%w: schemaVersion is required: add \"schemaVersion: %d\"", ErrInvalid, SchemaVersion)
	}

	var v int
	if err := n.Decode(&v); err != nil || v < 1 {
		return fmt.Errorf("%w: schemaVersion must be a whole number of 1 or more", ErrInvalid)
	}

	if v > SchemaVersion {
		return fmt.Errorf("%w: schemaVersion %d, the server reads up to %d", ErrSchemaVersion, v, SchemaVersion)
	}

	return nil
}

func checkDescription(n *yaml.Node) error {
	if n == nil {
		return nil
	}

	if n.Kind != yaml.ScalarNode || utf8.RuneCountInString(n.Value) > MaxDescription {
		return fmt.Errorf("%w: description must be text of at most %d characters", ErrInvalid, MaxDescription)
	}

	return nil
}

func checkImports(n *yaml.Node) error {
	if n == nil {
		return nil
	}

	if n.Kind != yaml.SequenceNode || len(n.Content) > MaxImports {
		return fmt.Errorf("%w: imports must be a list of at most %d items", ErrInvalid, MaxImports)
	}

	for _, item := range n.Content {
		name := importName(item)

		base, _, _ := strings.Cut(name, "@")
		if _, err := ParseName(base); err != nil {
			return fmt.Errorf("%w: imports: %q is not a profile name", ErrInvalid, name)
		}
	}

	return nil
}

// importName returns the profile name, with its tag, of one import item.
func importName(item *yaml.Node) string {
	if item.Kind == yaml.ScalarNode {
		return item.Value
	}

	if item.Kind == yaml.MappingNode {
		if p := mapGet(item, "profile"); p != nil && p.Kind == yaml.ScalarNode {
			return p.Value
		}
	}

	return ""
}

func checkBlocks(n *yaml.Node, key string, limit int) error {
	if n == nil {
		return nil
	}

	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: %s must be a map of blocks", ErrInvalid, key)
	}

	if len(n.Content)/pairSize > limit {
		return fmt.Errorf("%w: %s holds more than %d blocks", ErrLimit, key, limit)
	}

	for i := 0; i < len(n.Content); i += pairSize {
		block := n.Content[i+1]

		if use := useOf(block); use == nil || use.Kind != yaml.ScalarNode {
			return fmt.Errorf("%w: %s.%s must be a map with a \"use\" key", ErrInvalid, key, n.Content[i].Value)
		}
	}

	return nil
}

// useOf returns the "use" key of a block, or nil.
func useOf(block *yaml.Node) *yaml.Node {
	if block.Kind != yaml.MappingNode {
		return nil
	}

	return mapGet(block, "use")
}

func checkChecks(n *yaml.Node) error {
	if n == nil {
		return nil
	}

	if n.Kind != yaml.SequenceNode || len(n.Content) > maxChecks {
		return fmt.Errorf("%w: checks must be a list of at most %d rules", ErrInvalid, maxChecks)
	}

	for _, c := range n.Content {
		if c.Kind != yaml.MappingNode || mapGet(c, "rule") == nil {
			return fmt.Errorf("%w: each item of checks needs a \"rule\"", ErrInvalid)
		}
	}

	return nil
}

// collectLeaves checks the values tree n and stores its leaves.
func (d *Document) collectLeaves(n *yaml.Node) error {
	d.leaves = nil

	if n == nil {
		return nil
	}

	count := 0

	return walkValues(n, "", 1, &count, func(e Entry, node *yaml.Node) {
		d.leaves = append(d.leaves, leafNode{entry: e, node: node})
	})
}

// walkValues checks the values tree and calls visit for each leaf.
func walkValues(n *yaml.Node, path string, depth int, count *int, visit func(Entry, *yaml.Node)) error {
	if depth > MaxDepth {
		return fmt.Errorf("%w: values are deeper than %d levels", ErrLimit, MaxDepth)
	}

	if isEntryMap(n) {
		e, err := readEntry(n, path)
		if err != nil {
			return err
		}

		return leaf(e, n, count, visit)
	}

	if n.Kind == yaml.MappingNode {
		return walkChildren(n, path, depth, count, visit)
	}

	if err := noEntryKeys(n, joinPath(topValues, path)); err != nil {
		return err
	}

	e := newEntry(path)
	if err := n.Decode(&e.Value); err != nil {
		return fmt.Errorf("%w: value at %s: %w", ErrInvalid, path, err)
	}

	return leaf(e, n, count, visit)
}

// walkChildren walks the values of the map n.
func walkChildren(n *yaml.Node, path string, depth int, count *int, visit func(Entry, *yaml.Node)) error {
	for i := 0; i < len(n.Content); i += pairSize {
		child := joinPath(path, n.Content[i].Value)

		err := walkValues(n.Content[i+1], child, depth+1, count, visit)
		if err != nil {
			return err
		}
	}

	return nil
}

func leaf(e Entry, n *yaml.Node, count *int, visit func(Entry, *yaml.Node)) error {
	*count++
	if *count > MaxLeaves {
		return fmt.Errorf("%w: values hold more than %d leaves", ErrLimit, MaxLeaves)
	}

	if visit != nil {
		visit(e, n)
	}

	return nil
}

// readEntry reads the entry map n at path.
func readEntry(n *yaml.Node, path string) (Entry, error) {
	e := newEntry(path)
	forms := 0

	for i := 0; i < len(n.Content); i += pairSize {
		form, err := e.setField(n.Content[i].Value, n.Content[i+1])
		if err != nil {
			return Entry{}, fmt.Errorf("%s: %w", path, err)
		}

		if form {
			forms++
		}
	}

	if forms != 1 {
		return Entry{}, fmt.Errorf("%w: %s: an entry holds exactly one of %s, %s and %s",
			ErrInvalid, path, keyValue, keySecret, keySealed)
	}

	if e.NeverStore && !e.Secret {
		return Entry{}, fmt.Errorf("%w: %s: %s needs %s, because a %s is stored in the document",
			ErrInvalid, path, keyNeverStore, keySecret, keyValue)
	}

	return e, nil
}

// setField reads one key of an entry map into e. It reports whether the key
// holds the value of the entry.
func (e *Entry) setField(key string, val *yaml.Node) (bool, error) {
	switch key {
	case keyValue:
		if err := noEntryKeys(val, keyValue); err != nil {
			return false, err
		}

		if err := val.Decode(&e.Value); err != nil {
			return false, fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		return true, nil
	case keySecret, keySealed:
		e.Secret = true

		if val.Kind != yaml.ScalarNode || val.Value == "" && key == keySealed {
			return false, fmt.Errorf("%w: %s must be text", ErrInvalid, key)
		}

		return true, nil
	default:
		return false, e.setMeta(key, val)
	}
}

// setMeta reads one key of an entry map that does not hold the value.
func (e *Entry) setMeta(key string, val *yaml.Node) error {
	switch key {
	case keyFinal:
		return setFlag(&e.Final, key, val)
	case keyNeverStore:
		return setFlag(&e.NeverStore, key, val)
	case keyDescription:
		if val.Kind != yaml.ScalarNode {
			return fmt.Errorf("%w: %s must be text", ErrInvalid, key)
		}

		e.Description = val.Value

		return nil
	case keyType:
		kind, err := typeKind(val)
		e.Kind = kind

		return err
	default:
		return fmt.Errorf("%w: %q is not an entry key, and an entry map holds only keys that start with \"$\"",
			ErrInvalid, key)
	}
}

func setFlag(dst *bool, key string, val *yaml.Node) error {
	if val.Kind != yaml.ScalarNode || val.Tag != tagBool {
		return fmt.Errorf("%w: %s must be true or false", ErrInvalid, key)
	}

	if err := val.Decode(dst); err != nil {
		return fmt.Errorf("%w: %s must be true or false", ErrInvalid, key)
	}

	return nil
}

func typeKind(n *yaml.Node) (string, error) {
	if n.Kind != yaml.MappingNode {
		return "", fmt.Errorf("%w: %s must be a map with a \"kind\"", ErrInvalid, keyType)
	}

	for i := 0; i < len(n.Content); i += pairSize {
		if n.Content[i].Value != "kind" {
			return "", fmt.Errorf("%w: %s holds only \"kind\"", ErrInvalid, keyType)
		}
	}

	k := mapGet(n, "kind")
	if k == nil {
		return "", fmt.Errorf("%w: %s needs a \"kind\"", ErrInvalid, keyType)
	}

	switch k.Value {
	case "string", "number", "boolean", "list", "map", "any":
		return k.Value, nil
	default:
		return "", fmt.Errorf("%w: %s kind %q is not one of string, number, boolean, list, map and any",
			ErrInvalid, keyType, k.Value)
	}
}
