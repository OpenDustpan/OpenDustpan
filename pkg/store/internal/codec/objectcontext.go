package codec

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// ContextKey is the key of one context entry below the stable ID of its
// namespace: the kind, the ID of the object (empty for a namespace), the tool
// of a unit or a resource, and the key of a resource.
type ContextKey struct {
	Kind        string
	ObjectID    string
	Tool        string
	ResourceKey string
}

// ContextKeyOf returns the key of the object ref. collectionID is the stable
// ID of the collection that ref names, set for a collection only. It returns
// objectcontext.ErrInvalidObject for a kind that has no key. Blueprints have
// no object yet.
func ContextKeyOf(ref objectcontext.Ref, collectionID string) (ContextKey, error) {
	switch ref.Kind {
	case objectcontext.KindNamespace:
		return ContextKey{Kind: string(ref.Kind), ObjectID: "", Tool: "", ResourceKey: ""}, nil
	case objectcontext.KindCollection:
		return ContextKey{Kind: string(ref.Kind), ObjectID: collectionID, Tool: "", ResourceKey: ""}, nil
	case objectcontext.KindUnit:
		return ContextKey{Kind: string(ref.Kind), ObjectID: ref.Unit.ID, Tool: ref.Unit.Tool, ResourceKey: ""}, nil
	case objectcontext.KindResource:
		return ContextKey{
			Kind: string(ref.Kind), ObjectID: ref.Unit.ID, Tool: ref.Unit.Tool, ResourceKey: ref.Resource,
		}, nil
	case objectcontext.KindBlueprint:
		return ContextKey{}, fmt.Errorf("%w: a blueprint has no stored context yet", objectcontext.ErrInvalidObject)
	default:
		return ContextKey{}, fmt.Errorf("%w: kind %q", objectcontext.ErrInvalidObject, ref.Kind)
	}
}

// ContextRow is the column representation of one context entry, without its
// key.
type ContextRow struct {
	Tool        string
	Source      string
	Fields      string
	Warning     string
	ItemID      string
	UpdatedBy   string
	ItemSeq     int64
	UpdatedAtMs int64
}

// storedContext is the JSON of the column fields: the five fields and, for a
// unit output entry, the readme.
type storedContext struct {
	objectcontext.Fields

	Readme string `json:"readme,omitempty"`
}

// EncodeContextFields returns the JSON text of the fields and the readme of
// e.
func EncodeContextFields(e objectcontext.Entry) (string, error) {
	raw, err := json.Marshal(storedContext{Fields: e.Fields, Readme: e.Readme})
	if err != nil {
		return "", fmt.Errorf("encode context fields: %w", err)
	}

	return string(raw), nil
}

// ContextEntry converts a row of the object ref into an entry. hidden is true
// for a row that holds no context: the order marker that ReplaceOutput keeps
// for a unit whose output is empty, so a late run cannot bring back an older
// output. A reader never sees such a row.
func ContextEntry(ref objectcontext.Ref, row ContextRow) (objectcontext.Entry, bool, error) {
	var stored storedContext
	if err := json.Unmarshal([]byte(row.Fields), &stored); err != nil {
		return objectcontext.Entry{}, false, fmt.Errorf("decode context fields: %w", err)
	}

	src := objectcontext.Source(row.Source)
	hidden := src == objectcontext.SourceOutput && len(stored.Set()) == 0 && stored.Readme == "" && row.Warning == ""

	return objectcontext.Entry{
		UpdatedAt: FromMillis(row.UpdatedAtMs),
		Object:    ref,
		Source:    src,
		Readme:    stored.Readme,
		Item:      row.ItemID,
		Warning:   row.Warning,
		UpdatedBy: row.UpdatedBy,
		Fields:    stored.Fields,
	}, hidden, nil
}

// ContextRef rebuilds the reference of an object from its key. ns is the
// current name of the namespace, and collection the current name of the
// collection when the kind is a collection.
func ContextRef(ns namespace.Name, k ContextKey, collection string) (objectcontext.Ref, error) {
	unit := graph.Unit{Namespace: ns, Tool: k.Tool, ID: k.ObjectID}

	switch objectcontext.Kind(k.Kind) {
	case objectcontext.KindNamespace:
		return objectcontext.NamespaceRef(ns), nil
	case objectcontext.KindCollection:
		return objectcontext.CollectionRef(ns, collection), nil
	case objectcontext.KindUnit:
		return objectcontext.UnitRef(unit), nil
	case objectcontext.KindResource:
		return objectcontext.ResourceRef(unit, k.ResourceKey), nil
	case objectcontext.KindBlueprint:
		return objectcontext.Ref{}, fmt.Errorf(
			"%w: a blueprint has no stored context yet",
			objectcontext.ErrInvalidObject,
		)
	default:
		return objectcontext.Ref{}, fmt.Errorf("%w: stored kind %q", objectcontext.ErrInvalidObject, k.Kind)
	}
}

// ContextCursor is the position after which a page of entries by owner
// starts: the key of the last entry of the page before, and its source.
type ContextCursor struct {
	Kind        string `json:"k"`
	ObjectID    string `json:"i"`
	ResourceKey string `json:"r"`
	Source      string `json:"s"`
}

// maxContextCursorBytes bounds a cursor text: four short IDs and a resource
// key of at most 1 KiB.
const maxContextCursorBytes = 4 << 10

// EncodeContextCursor returns the opaque text of c.
func EncodeContextCursor(c ContextCursor) string {
	raw, err := json.Marshal(c)
	if err != nil {
		// A struct of four strings always encodes.
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeContextCursor returns the cursor that text names. The empty text is
// the start of the list. It returns objectcontext.ErrInvalidContext for any
// other text that EncodeContextCursor did not make.
func DecodeContextCursor(text string) (ContextCursor, error) {
	if text == "" {
		return ContextCursor{Kind: "", ObjectID: "", ResourceKey: "", Source: ""}, nil
	}

	bad := fmt.Errorf("%w: cursor", objectcontext.ErrInvalidContext)

	if len(text) > maxContextCursorBytes {
		return ContextCursor{}, bad
	}

	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return ContextCursor{}, bad
	}

	var c ContextCursor
	if err := json.Unmarshal(raw, &c); err != nil || c.Kind == "" || c.Source == "" {
		return ContextCursor{}, bad
	}

	return c, nil
}
