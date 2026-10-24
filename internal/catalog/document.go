package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// DocumentSchemaVersion is the newest schema version of a request document
// that this server reads.
const DocumentSchemaVersion = 1

// Limits of a request document.
const (
	MaxDocumentBytes  = 64 << 10
	MaxAnswers        = 64
	MaxStringAnswer   = 4 << 10
	MaxListAnswerSize = 100
)

// Document is a Catalog request: the desired state of one unit from one
// blueprint. The fields are in the sorted order of their JSON names, so
// that Canonical gives sorted keys. Encode writes the order of the request
// file in git.
type Document struct {
	Blueprint     BlueprintPin               `json:"blueprint"`
	Ensure        Ensure                     `json:"ensure,omitempty"`
	Home          *Home                      `json:"home,omitempty"`
	Inputs        map[string]json.RawMessage `json:"inputs,omitempty"`
	Name          string                     `json:"name"`
	Namespace     namespace.Name             `json:"namespace"`
	SchemaVersion int                        `json:"schemaVersion"`
}

// BlueprintPin names the blueprint of a request and the published version
// that it pins.
type BlueprintPin struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// Home is where the document of a request lives. The first submit fixes it.
// Connection, Repository, Ref and Delivery are for HomeGit. An empty
// Connection takes the forge connection of the namespace. An empty Delivery
// takes job.DeliveryReviewRequest.
type Home struct {
	Kind       HomeKind     `json:"kind"`
	Connection string       `json:"connection,omitempty"`
	Repository string       `json:"repository,omitempty"`
	Ref        string       `json:"ref,omitempty"`
	Delivery   job.Delivery `json:"delivery,omitempty"`
}

// Validate reports whether h names a home that a request can use. It
// returns ErrInvalid otherwise.
func (h *Home) Validate() error {
	if !h.Kind.Valid() {
		return fmt.Errorf("%w: home %q", ErrInvalid, h.Kind)
	}

	if h.Kind == HomeOpenDustpan {
		if *h != (Home{Kind: HomeOpenDustpan, Connection: "", Repository: "", Ref: "", Delivery: ""}) {
			return fmt.Errorf("%w: a home in OpenDustpan names no repository", ErrInvalid)
		}

		return nil
	}

	switch {
	case h.Repository == "" || h.Ref == "":
		return fmt.Errorf("%w: a home in git needs a repository and a ref", ErrInvalid)
	case h.Delivery != "" && h.Delivery != job.DeliveryReviewRequest && h.Delivery != job.DeliveryDirectPush:
		return fmt.Errorf("%w: delivery %q", ErrInvalid, h.Delivery)
	default:
		return checkRepository("a home in git", h.Repository)
	}
}

// ParseDocument reads a request document. It refuses a document over
// MaxDocumentBytes, a duplicate key, an unknown field, a value of the wrong
// JSON type and data after the document with ErrInvalid or ErrLimit, and a
// schema version above DocumentSchemaVersion with ErrSchemaVersion. It then
// applies Document.Validate.
func ParseDocument(data []byte) (Document, error) {
	var d Document

	if err := decodeStrict(data, MaxDocumentBytes, "document", &d); err != nil {
		return Document{}, err
	}

	if err := d.Validate(); err != nil {
		return Document{}, err
	}

	return d, nil
}

// decodeStrict decodes data into v: at most limit bytes, no duplicate key,
// no unknown field and no data after the value.
func decodeStrict(data []byte, limit int, what string, v any) error {
	if len(data) > limit {
		return fmt.Errorf("%w: the %s has %d bytes, the limit is %d", ErrLimit, what, len(data), limit)
	}

	if err := checkDuplicateKeys(data); err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: data after the %s", ErrInvalid, what)
	}

	return nil
}

// Validate checks the fields of d that need no blueprint: the schema
// version, the namespace, the names, the pin, ensure, the home, and the
// shape and the size of each answer. Blueprint.CheckAnswers checks the
// answers against the inputs.
func (d *Document) Validate() error {
	if err := d.validateHeader(); err != nil {
		return err
	}

	if d.Home != nil {
		if err := d.Home.Validate(); err != nil {
			return err
		}
	}

	return validateAnswers(d.Inputs)
}

func validateAnswers(inputs map[string]json.RawMessage) error {
	if len(inputs) > MaxAnswers {
		return fmt.Errorf("%w: %d answers, the limit is %d", ErrLimit, len(inputs), MaxAnswers)
	}

	for name, raw := range inputs {
		if !ValidInputName(name) {
			return fmt.Errorf("%w: input name %q", ErrInvalid, name)
		}

		if _, err := answerShape(raw); err != nil {
			return fmt.Errorf("input %s: %w", name, err)
		}
	}

	return nil
}

func validNamespace(ns namespace.Name) bool {
	_, err := namespace.ParseName(ns.String())

	return err == nil
}

// Ref returns the reference of the request of d.
func (d *Document) Ref() RequestRef {
	return RequestRef{Namespace: d.Namespace, Blueprint: d.Blueprint.Name, Name: d.Name}
}

// Canonical returns the canonical bytes of d with the defaults of b filled
// in: ensure set, each input with a default that d leaves out set to its
// default, and no home, in the canonical JSON of CanonicalJSON. So keys are
// sorted, there is no white space, and numbers and strings have one form:
// an answer of 20, 20.0 or 2e1 gives the same bytes. Two documents with
// equal canonical bytes ask for the same state, so a submit equal to the
// last applied revision makes no job.
func (d *Document) Canonical(b *Blueprint) ([]byte, error) {
	c := *d
	c.Home = nil
	c.Inputs = maps.Clone(d.Inputs)

	if c.Ensure == "" {
		c.Ensure = EnsurePresent
	}

	if c.Inputs == nil {
		c.Inputs = map[string]json.RawMessage{}
	}

	for i := range b.Inputs {
		in := &b.Inputs[i]
		if _, ok := c.Inputs[in.Name]; !ok && len(in.Default) > 0 {
			c.Inputs[in.Name] = in.Default
		}
	}

	for name, raw := range c.Inputs {
		if _, err := compactJSON(raw); err != nil {
			return nil, fmt.Errorf("input %s: %w", name, err)
		}
	}

	out, err := json.Marshal(&c)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return CanonicalJSON(out)
}

// Encode returns the bytes of the request file in git: the fields in the
// order of the document table, two spaces of indent, and a final new line.
// The console shows the same bytes as request.json.
func (d *Document) Encode() ([]byte, error) {
	w := documentFile{
		SchemaVersion: d.SchemaVersion,
		Namespace:     d.Namespace,
		Name:          d.Name,
		Blueprint:     d.Blueprint,
		Ensure:        d.Ensure,
		Inputs:        d.Inputs,
		Home:          d.Home,
	}

	out, err := json.MarshalIndent(&w, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return append(out, '\n'), nil
}

func (d *Document) validateHeader() error {
	switch {
	case d.SchemaVersion < 1:
		return fmt.Errorf("%w: schemaVersion is missing", ErrInvalid)
	case d.SchemaVersion > DocumentSchemaVersion:
		return fmt.Errorf("%w: version %d, this server reads up to version %d",
			ErrSchemaVersion, d.SchemaVersion, DocumentSchemaVersion)
	case !validNamespace(d.Namespace):
		return fmt.Errorf("%w: namespace %q", ErrInvalid, d.Namespace)
	case !ValidName(d.Name):
		return fmt.Errorf("%w: name %q", ErrInvalid, d.Name)
	case !ValidName(d.Blueprint.Name) || d.Blueprint.Version < 1:
		return fmt.Errorf("%w: blueprint %q version %d", ErrInvalid, d.Blueprint.Name, d.Blueprint.Version)
	case d.Ensure != "" && d.Ensure != EnsurePresent && d.Ensure != EnsureAbsent:
		return fmt.Errorf("%w: ensure %q", ErrInvalid, d.Ensure)
	default:
		return nil
	}
}

// documentFile is Document with the fields in the order of the request
// file.
type documentFile struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Namespace     namespace.Name             `json:"namespace"`
	Name          string                     `json:"name"`
	Blueprint     BlueprintPin               `json:"blueprint"`
	Ensure        Ensure                     `json:"ensure,omitempty"`
	Inputs        map[string]json.RawMessage `json:"inputs,omitempty"`
	Home          *Home                      `json:"home,omitempty"`
}

// Shape is the JSON shape of an answer.
type Shape string

// The shapes of an answer.
const (
	ShapeString  Shape = "string"
	ShapeNumber  Shape = "number"
	ShapeBoolean Shape = "boolean"
	ShapeList    Shape = "string-list"
)

// answerShape returns the shape of an answer: a string of at most
// MaxStringAnswer bytes, a number, a boolean, or a list of at most
// MaxListAnswerSize strings. It returns ErrInvalid for null, an object, a
// list of other values, and ErrLimit above a limit.
func answerShape(raw json.RawMessage) (Shape, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	switch x := v.(type) {
	case string:
		if len(x) > MaxStringAnswer {
			return "", fmt.Errorf("%w: a string of %d bytes, the limit is %d", ErrLimit, len(x), MaxStringAnswer)
		}

		return ShapeString, nil
	case float64:
		return ShapeNumber, nil
	case bool:
		return ShapeBoolean, nil
	case []any:
		return listShape(x)
	default:
		return "", fmt.Errorf("%w: an answer is a string, a number, a boolean or a list of strings", ErrInvalid)
	}
}

func listShape(items []any) (Shape, error) {
	if len(items) > MaxListAnswerSize {
		return "", fmt.Errorf("%w: a list of %d items, the limit is %d", ErrLimit, len(items), MaxListAnswerSize)
	}

	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return "", fmt.Errorf("%w: a list answer holds strings only", ErrInvalid)
		}

		if len(s) > MaxStringAnswer {
			return "", fmt.Errorf("%w: a string of %d bytes, the limit is %d", ErrLimit, len(s), MaxStringAnswer)
		}
	}

	return ShapeList, nil
}

func compactJSON(raw json.RawMessage) (json.RawMessage, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return buf.Bytes(), nil
}

// checkDuplicateKeys returns ErrInvalid when an object in data has a key
// twice. The standard decoder keeps the last value of a duplicate key, so
// two readers of one file could see two documents.
func checkDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	open := []*keySet{}

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		var top *keySet
		if n := len(open); n > 0 {
			top = open[n-1]
		}

		switch t := tok.(type) {
		case json.Delim:
			open = applyDelim(open, t)
		case string:
			if err := top.see(t); err != nil {
				return err
			}
		default:
			top.value()
		}
	}
}

// keySet is an open array, or an open object with its keys. A nil keySet
// is the top level.
type keySet struct {
	keys      map[string]bool
	isObject  bool
	expectKey bool
}

// see takes a string token: a key when the object expects one, else a
// value.
func (k *keySet) see(s string) error {
	if k == nil || !k.isObject || !k.expectKey {
		k.value()

		return nil
	}

	if k.keys[s] {
		return fmt.Errorf("%w: key %q appears twice", ErrInvalid, s)
	}

	k.keys[s] = true
	k.expectKey = false

	return nil
}

// value takes a value token: an object then expects its next key.
func (k *keySet) value() {
	if k != nil && k.isObject {
		k.expectKey = true
	}
}

func applyDelim(open []*keySet, d json.Delim) []*keySet {
	switch d {
	case '{':
		return append(open, &keySet{keys: map[string]bool{}, isObject: true, expectKey: true})
	case '[':
		return append(open, &keySet{keys: nil, isObject: false, expectKey: false})
	default:
		if len(open) == 0 {
			return open
		}

		open = open[:len(open)-1]
		if n := len(open); n > 0 {
			open[n-1].value()
		}

		return open
	}
}
