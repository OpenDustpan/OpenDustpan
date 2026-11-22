package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// newProfileTree returns an empty open tree.
func newProfileTree() *profileTree { return &profileTree{leaves: nil, byKey: map[string]int{}} }

// set stores leaf, replacing the leaf at its key.
func (t *profileTree) set(leaf mergeLeaf) {
	if n, ok := t.byKey[leaf.key]; ok {
		t.leaves[n] = leaf

		return
	}

	t.byKey[leaf.key] = len(t.leaves)
	t.leaves = append(t.leaves, leaf)
}

// remove deletes the leaf at key, if any.
func (t *profileTree) remove(key string) {
	n, ok := t.byKey[key]
	if !ok {
		return
	}

	t.leaves = append(t.leaves[:n], t.leaves[n+1:]...)
	delete(t.byKey, key)

	for i := n; i < len(t.leaves); i++ {
		t.byKey[t.leaves[i].key] = i
	}
}

// merge merges src into t: a later item wins, a map merges key by key, a
// list or a scalar replaces, and a null removes. Replacing or removing a
// final value of another profile fails with ErrFinal, which names the path
// and both profiles. current names the profile that merges, for the error.
func (t *profileTree) merge(src *profileTree, current Profile) error {
	for _, leaf := range src.leaves {
		if leaf.value == nil {
			if err := t.removeLeaf(leaf, current); err != nil {
				return err
			}

			continue
		}

		if err := t.replaceLeaf(leaf, current); err != nil {
			return err
		}
	}

	return nil
}

// replaceLeaf sets leaf, invalidating the subtree below it and the scalar or
// list above it. A final value in the way fails.
func (t *profileTree) replaceLeaf(leaf mergeLeaf, current Profile) error {
	for _, old := range t.leaves {
		if !coversLeaf(old.key, leaf.key) && !coversLeaf(leaf.key, old.key) {
			continue
		}

		if old.final && old.from != current.ID {
			return finalError(leaf.key, old, current)
		}
	}

	t.invalidateBelow(leaf.key)
	t.invalidateAbove(leaf.key)
	t.set(leaf)

	return nil
}

// removeLeaf removes the subtree at the key of leaf. A final value in the
// subtree fails.
func (t *profileTree) removeLeaf(leaf mergeLeaf, current Profile) error {
	for _, old := range t.leaves {
		if !coversLeaf(leaf.key, old.key) {
			continue
		}

		if old.final && old.from != current.ID {
			return finalError(leaf.key, old, current)
		}
	}

	t.invalidateBelow(leaf.key)

	return nil
}

// coversLeaf reports whether key names old itself or a leaf under it: equal,
// below with a dot, or old below key with a dot (an ancestor scalar).
func coversLeaf(key, old string) bool {
	return key == old || strings.HasPrefix(old, key+".") || strings.HasPrefix(key, old+".")
}

// invalidateBelow removes the leaf at key and every leaf under it.
func (t *profileTree) invalidateBelow(key string) {
	for _, old := range append([]mergeLeaf(nil), t.leaves...) {
		if old.key == key || strings.HasPrefix(old.key, key+".") {
			t.remove(old.key)
		}
	}
}

// invalidateAbove removes every ancestor scalar or list of key: an ancestor
// leaf is a scalar or a list, never a map, so a map below it replaces it.
func (t *profileTree) invalidateAbove(key string) {
	for _, old := range append([]mergeLeaf(nil), t.leaves...) {
		if old.key != key && strings.HasPrefix(key, old.key+".") {
			t.remove(old.key)
		}
	}
}

// finalError names the refusal of a final value.
func finalError(key string, old mergeLeaf, current Profile) error {
	return fmt.Errorf("%w: path %q is final in profile %q and profile %q replaces it",
		ErrFinal, key, old.fromName, current.Name)
}

// computeTemplates computes each template leaf of p against merged, in
// document order, and merges each result. merged holds the imports of p and
// its own literal values: the own tree that the expressions read.
func (r *openResolver) computeTemplates(
	ctx context.Context, p Profile, merged *profileTree, templates []templateLeaf,
) error {
	for _, tmpl := range templates {
		got, err := r.computeTemplate(ctx, p, merged, tmpl.source)
		if err != nil {
			return err
		}

		leaf := tmpl.leaf
		leaf.value, leaf.secret, leaf.never = got.value, leaf.secret || got.secret, leaf.never || got.never

		computed := &profileTree{leaves: []mergeLeaf{leaf}, byKey: map[string]int{leaf.key: 0}}
		if err := merged.merge(computed, p); err != nil {
			return err
		}
	}

	return nil
}

// templateResult is the computed value of one template with the secret and
// never-store marks of the paths that it read.
type templateResult struct {
	value  any
	secret bool
	never  bool
}

// computeTemplate computes one template against tree and answers the value
// with the secret and never-store marks of the paths that it reads.
func (r *openResolver) computeTemplate(
	ctx context.Context, p Profile, tree *profileTree, source string,
) (templateResult, error) {
	values := tree.toMap()

	var (
		secret bool
		never  bool
	)

	eval := func(inner string) (any, error) {
		// The checks read the expression with its quoted strings blanked:
		// text in quotes names no path. refsInText reads template text, so
		// the inner expression goes back in its delimiters.
		for _, ref := range refsInText("${" + blankQuoted(inner) + "}") {
			if !pathExists(values, ref) {
				return nil, fmt.Errorf(
					"%w: profile %q reads a path that is not in values: %s",
					ErrReference, p.Name, joinRef(ref),
				)
			}

			marks := tree.marksOf(ref)
			secret, never = secret || marks.secret, never || marks.never
		}

		out, err := r.svc.evaluator.Eval(ctx, inner, values)
		if err != nil {
			// The error of the engine may quote a value of the environment,
			// so the open never carries it: it names the profile and the
			// path only.
			return nil, fmt.Errorf("%w: profile %q has an expression that does not compute",
				ErrReference, p.Name)
		}

		return out.Value, nil
	}

	value, err := renderTemplate(source, eval)
	if err != nil {
		return templateResult{}, err
	}

	return templateResult{value: value, secret: secret, never: never}, nil
}

// joinRef joins path segments for an error.
func joinRef(ref []string) string {
	if len(ref) == 0 {
		return "values"
	}

	return "values." + strings.Join(ref, ".")
}

// pathExists reports whether ref resolves in values.
func pathExists(values map[string]any, ref []string) bool {
	var current any = values

	for _, seg := range ref {
		next, ok := current.(map[string]any)
		if !ok {
			return false
		}

		current, ok = next[seg]
		if !ok {
			return false
		}
	}

	return true
}

// leafMarks are the marks of the leaves under a path.
type leafMarks struct {
	secret bool
	never  bool
}

// marksOf answers the secret and never-store marks of the leaves at or under
// ref: an expression that reads a secret feeds a secret result.
func (t *profileTree) marksOf(ref []string) leafMarks {
	var marks leafMarks

	prefix := strings.Join(ref, "\x00")

	for _, leaf := range t.leaves {
		joined := strings.Join(leaf.segments, "\x00")
		if joined == prefix || strings.HasPrefix(joined, prefix+"\x00") {
			marks.secret, marks.never = marks.secret || leaf.secret, marks.never || leaf.never
		}
	}

	return marks
}

// toMap rebuilds the nested values of the tree.
func (t *profileTree) toMap() map[string]any {
	out := map[string]any{}

	for _, leaf := range t.leaves {
		setNested(out, leaf.segments, leaf.value)
	}

	return out
}

// setNested sets segments to value in root, making maps on the way. A
// conflict keeps the last write: the merge already resolved conflicts.
func setNested(root map[string]any, segments []string, value any) {
	current := root

	for _, seg := range segments[:len(segments)-1] {
		next, ok := current[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[seg] = next
		}

		current = next
	}

	current[segments[len(segments)-1]] = value
}

// renderTemplate computes the template source. A string that is exactly one
// placeholder answers the value of the expression, of any type. A
// placeholder inside a longer string answers the string form of the value.
// "$${" writes a literal "${". eval computes one inner expression.
func renderTemplate(source string, eval func(inner string) (any, error)) (any, error) {
	spans, err := splitTemplate(source)
	if err != nil {
		return nil, err
	}

	if len(spans) == 1 && spans[0].expr && spans[0].whole {
		return eval(spans[0].text)
	}

	var sb strings.Builder

	for _, span := range spans {
		if !span.expr {
			sb.WriteString(span.text)

			continue
		}

		value, err := eval(span.text)
		if err != nil {
			return nil, err
		}

		text, err := stringForm(value)
		if err != nil {
			return nil, err
		}

		sb.WriteString(text)
	}

	return sb.String(), nil
}

// stringForm answers the string form of a computed value inside a longer
// string: a string as itself, any other JSON value as its encoding.
func stringForm(value any) (string, error) {
	if s, ok := value.(string); ok {
		return s, nil
	}

	text, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: an expression gave a value that is not JSON", ErrReference)
	}

	return string(text), nil
}

// templateSpan is one span of a template: literal text or one expression.
type templateSpan struct {
	// text is the literal text or the inner expression.
	text string
	// expr reports an expression span.
	expr bool
	// whole reports the only span of a source that is exactly one
	// placeholder.
	whole bool
}

// splitTemplate splits source into literal and expression spans. "$${"
// writes a literal "${".
func splitTemplate(source string) ([]templateSpan, error) {
	var spans []templateSpan

	var literal strings.Builder

	flush := func() {
		if literal.Len() > 0 {
			spans = append(spans, templateSpan{text: literal.String(), expr: false, whole: false})
			literal.Reset()
		}
	}

	i := 0

	for i < len(source) {
		switch {
		case strings.HasPrefix(source[i:], "$${"):
			literal.WriteString("${")
			i += len("$${")
		case strings.HasPrefix(source[i:], "${"):
			inner, next, err := splitPlaceholder(source, i+len("${"))
			if err != nil {
				return nil, err
			}

			flush()
			spans = append(spans, templateSpan{text: inner, expr: true, whole: false})
			i = next
		default:
			literal.WriteByte(source[i])
			i++
		}
	}

	flush()

	for n := range spans {
		spans[n].whole = len(spans) == 1 && spans[n].expr
	}

	if len(spans) == 0 {
		spans = append(spans, templateSpan{text: "", expr: false, whole: false})
	}

	return spans, nil
}

// splitPlaceholder reads the inner expression after "${" at next, balancing
// braces and skipping quoted strings. It answers the inner text and the
// index after the closing "}".
func splitPlaceholder(source string, next int) (string, int, error) {
	depth := 1

	var (
		inner strings.Builder
		quote byte
	)

	for i := next; i < len(source); i++ {
		c := source[i]

		switch {
		case quote != 0:
			inner.WriteByte(c)

			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
			inner.WriteByte(c)
		case c == '{':
			depth++
			inner.WriteByte(c)
		case c == '}':
			depth--

			if depth == 0 {
				return inner.String(), i + 1, nil
			}

			inner.WriteByte(c)
		default:
			inner.WriteByte(c)
		}
	}

	return "", 0, fmt.Errorf("%w: an expression never ends: %q", ErrReference, source)
}

// blankQuoted blanks the quoted strings of an expression with spaces: text
// in quotes names no path and no name outside values. A quoted span that is
// an index key (after "["), such as values["db-host"], stays: it names the
// key that the expression reads.
func blankQuoted(source string) string {
	var sb strings.Builder

	sb.Grow(len(source))

	scan := quoteScan{quote: 0, escaped: false, index: false}

	for i := range len(source) {
		scan.step(source[i], &sb)
	}

	return sb.String()
}

// quoteScan holds the quoted-string state of blankQuoted.
type quoteScan struct {
	quote   byte
	escaped bool
	// index reports a "[" as the last byte outside a string: the next quoted
	// span is an index key.
	index bool
}

// step writes one byte of source to sb, blanking quoted text that is not an
// index key.
func (q *quoteScan) step(c byte, sb *strings.Builder) {
	if q.quote != 0 {
		q.stepInside(c, sb)

		return
	}

	q.stepOutside(c, sb)
}

// stepInside writes one byte inside a quoted span.
func (q *quoteScan) stepInside(c byte, sb *strings.Builder) {
	if q.index {
		sb.WriteByte(c)
	} else {
		sb.WriteByte(' ')
	}

	q.quote, q.escaped = advanceQuote(q.quote, q.escaped, c)
}

// stepOutside writes one byte outside a quoted span.
func (q *quoteScan) stepOutside(c byte, sb *strings.Builder) {
	switch c {
	case '"', '\'', '`':
		q.quote, q.escaped = c, false

		if q.index {
			sb.WriteByte(c)
		} else {
			sb.WriteByte(' ')
		}
	default:
		sb.WriteByte(c)

		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			q.index = c == '['
		}
	}
}

// advanceQuote moves the quoted-string state past c: it answers the quote
// and escaped values after c.
func advanceQuote(quote byte, escaped bool, c byte) (byte, bool) {
	switch {
	case escaped:
		return quote, false
	case c == '\\':
		return quote, true
	case c == quote:
		return 0, false
	default:
		return quote, false
	}
}

// selectPaths answers the session leaves of tree: the asked paths, or every
// path but the never-store ones. An asked never-store path refuses the open
// with ErrNeverStore, which names the path. A never-store value never reaches
// a session.
func (r *openResolver) selectPaths(tree *profileTree, root Profile) (map[string]sessionLeaf, error) {
	out := map[string]sessionLeaf{}

	if r.asked == nil {
		for _, leaf := range tree.leaves {
			if leaf.never {
				continue
			}

			out[leaf.key] = sessionLeaf{Value: leaf.value, Secret: leaf.secret}
		}

		return out, nil
	}

	for path := range r.asked {
		n, ok := tree.byKey[path]
		if !ok {
			return nil, fmt.Errorf("%w: open of %s asks a path that is not in values: %q",
				ErrInvalid, root.Name, path)
		}

		leaf := tree.leaves[n]
		if leaf.never {
			return nil, fmt.Errorf("%w: open of %s asks the never-store path %q",
				ErrNeverStore, root.Name, path)
		}

		out[path] = sessionLeaf{Value: leaf.value, Secret: leaf.secret}
	}

	return out, nil
}
