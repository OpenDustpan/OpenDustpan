package objectcontext

import (
	"encoding/json"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/graph"
)

// Layer is the context of one object from both sources. API and Output are
// nil when the source holds no entry for the object. Hidden is true when the
// caller does not hold the read action of the object (ReadAction): Resolve
// then takes only the owners and the on-call of the layer.
type Layer struct {
	API    *Entry
	Output *Entry
	Object Ref
	Hidden bool
}

// Provenance names where a resolved value comes from. It is the zero value
// for a value that no layer sets.
type Provenance struct {
	Source Source
	Object Ref
}

// provenanceJSON is the wire form of Provenance: the kind of the object, its
// source, and the object itself unless the layer is hidden and has no name.
type provenanceJSON struct {
	Ref    *ObjectID `json:"ref,omitempty"`
	Object Kind      `json:"object"`
	Source Source    `json:"source"`
}

// MarshalJSON implements json.Marshaler. A value that no layer sets writes
// null. A layer that the caller cannot read has no namespace in its object,
// so the answer names the kind and the source only.
func (p Provenance) MarshalJSON() ([]byte, error) {
	if p == (Provenance{Source: "", Object: Ref{Namespace: "", Kind: "", Name: "", Resource: "", Unit: graph.Unit{Namespace: "", Tool: "", ID: ""}}}) {
		return []byte("null"), nil
	}

	out := provenanceJSON{Object: p.Object.Kind, Source: p.Source, Ref: nil}

	if p.Object.Namespace != "" {
		id := IDOf(p.Object)
		out.Ref = &id
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode provenance: %w", err)
	}

	return raw, nil
}

// Text is one resolved text field with its provenance.
type Text struct {
	Value string     `json:"value"`
	From  Provenance `json:"from"`
}

// Owners is the resolved owners with their provenance.
type Owners struct {
	Value []string   `json:"value"`
	From  Provenance `json:"from"`
}

// LinkValue is one resolved link with its provenance.
type LinkValue struct {
	Link

	From Provenance `json:"from"`
}

// Shadow names an API value that the output of the same object hides. The
// console shows it, so that a person can delete the API value.
type Shadow struct {
	Object Ref   `json:"-"`
	Field  Field `json:"field"`
}

// MarshalJSON implements json.Marshaler.
func (s Shadow) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(struct {
		Object ObjectID `json:"object"`
		Field  Field    `json:"field"`
	}{Object: IDOf(s.Object), Field: s.Field})
	if err != nil {
		return nil, fmt.Errorf("encode shadow: %w", err)
	}

	return raw, nil
}

// Warning is an output of a layer that did not parse.
type Warning struct {
	Object  Ref    `json:"-"`
	Message string `json:"message"`
}

// MarshalJSON implements json.Marshaler.
func (w Warning) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(struct {
		Object  ObjectID `json:"object"`
		Message string   `json:"message"`
	}{Object: IDOf(w.Object), Message: w.Message})
	if err != nil {
		return nil, fmt.Errorf("encode warning: %w", err)
	}

	return raw, nil
}

// Resolved is the effective context of one object, with the provenance of
// each value. Readme is the readme output of the unit of the chain.
type Resolved struct {
	Summary  Text        `json:"summary"`
	Runbook  Text        `json:"runbook"`
	OnCall   Text        `json:"onCall"`
	Readme   string      `json:"readme,omitempty"`
	Owners   Owners      `json:"owners"`
	Links    []LinkValue `json:"links"`
	Shadowed []Shadow    `json:"shadowed,omitempty"`
	Warnings []Warning   `json:"warnings,omitempty"`
}

// chainOrder returns the order of the kinds in a chain, the most specific
// first.
func chainOrder() []Kind {
	return []Kind{KindResource, KindUnit, KindBlueprint, KindCollection, KindNamespace}
}

// Resolve returns the effective context of the first layer of layers. layers
// is the chain of the object, the most specific first: the resource, its unit,
// the blueprint of the unit, the collection of the unit and the namespace.
// Each kind is optional and comes at most once. The rules:
//
//   - In one layer, a field that the output sets wins over the same field
//     from the API, and the API value is in Shadowed.
//   - The summary and the runbook come from the first layer only. A
//     collection does not describe each of its units.
//   - The owners and the on-call come from the first layer that sets them.
//     A blueprint is skipped: the team that offers a blueprint does not own
//     the units made from it.
//   - The links come from every layer, the most specific first, with no
//     second link of the same URL, up to MaxEffectiveLinks.
//   - A hidden layer gives its owners and its on-call only. Its summary,
//     runbook, links, readme, warnings and shadowed values stay out of the
//     answer. Owners and on-call are not sensitive, and a person who reads a
//     unit during an incident must get the right people to call, also when
//     a role does not hold the read action of the collection or of the
//     namespace. Their provenance keeps the source and the kind of the
//     layer only: the name of the object is blank, so the caller does not
//     learn, for example, the name of a collection that it cannot read.
//
// It returns ErrInvalidObject for a chain out of order, an empty chain, or a
// layer whose entries name another object.
func Resolve(layers []Layer) (Resolved, error) {
	var r Resolved

	if err := checkChain(layers); err != nil {
		return r, err
	}

	seenURL := map[string]bool{}

	for i := range layers {
		eff, from := r.add(&layers[i], seenURL)

		if i == 0 && !layers[i].Hidden {
			r.Summary = Text{Value: eff.Summary, From: from[FieldSummary]}
			r.Runbook = Text{Value: eff.Runbook, From: from[FieldRunbook]}
		}
	}

	return r, nil
}

// add adds the context of layer l to r, except the summary and the runbook,
// and returns the effective fields of l and the provenance of each field that
// l sets. seenURL holds the URLs of the links that r holds.
func (r *Resolved) add(l *Layer, seenURL map[string]bool) (Fields, map[Field]Provenance) {
	eff, from, shadows := l.merge()

	if l.Hidden {
		blank := Ref{
			Kind: l.Object.Kind, Namespace: "", Name: "", Resource: "",
			Unit: graph.Unit{Namespace: "", Tool: "", ID: ""},
		}
		for f := range from {
			from[f] = Provenance{Source: from[f].Source, Object: blank}
		}

		if l.Object.Kind != KindBlueprint {
			r.addPeople(&eff, from)
		}

		return eff, from
	}

	r.Shadowed = append(r.Shadowed, shadows...)

	if l.Output != nil && l.Output.Warning != "" {
		r.Warnings = append(r.Warnings, Warning{Object: l.Object, Message: l.Output.Warning})
	}

	if l.Object.Kind == KindUnit && l.Output != nil {
		r.Readme = l.Output.Readme
	}

	if l.Object.Kind != KindBlueprint {
		r.addPeople(&eff, from)
	}

	for _, link := range eff.Links {
		if seenURL[link.URL] || len(r.Links) >= MaxEffectiveLinks {
			continue
		}

		seenURL[link.URL] = true
		r.Links = append(r.Links, LinkValue{Link: link, From: from[FieldLinks]})
	}

	return eff, from
}

// addPeople sets the owners and the on-call of r from eff, when no earlier
// layer set them.
func (r *Resolved) addPeople(eff *Fields, from map[Field]Provenance) {
	if r.Owners.Value == nil && len(eff.Owners) > 0 {
		r.Owners = Owners{Value: slices.Clone(eff.Owners), From: from[FieldOwners]}
	}

	if r.OnCall.Value == "" && eff.OnCall != "" {
		r.OnCall = Text{Value: eff.OnCall, From: from[FieldOnCall]}
	}
}

// merge returns the effective fields of one layer, the provenance of each
// field that it sets, and the API values that the output hides.
func (l Layer) merge() (Fields, map[Field]Provenance, []Shadow) {
	var api, out Fields
	if l.API != nil {
		api = l.API.Fields
	}

	if l.Output != nil {
		out = l.Output.Fields
	}

	from := map[Field]Provenance{}
	fromOut := Provenance{Object: l.Object, Source: SourceOutput}
	fromAPI := Provenance{Object: l.Object, Source: SourceAPI}

	var shadows []Shadow

	for _, f := range api.Set() {
		from[f] = fromAPI
	}

	outSet := out.Set()
	for _, f := range outSet {
		if _, ok := from[f]; ok {
			shadows = append(shadows, Shadow{Object: l.Object, Field: f})
		}

		from[f] = fromOut
	}

	eff := api

	for _, f := range outSet {
		takeField(&eff, &out, f)
	}

	return eff, from, shadows
}

// takeField sets field f of dst to its value in src.
func takeField(dst, src *Fields, f Field) {
	switch f {
	case FieldSummary:
		dst.Summary = src.Summary
	case FieldRunbook:
		dst.Runbook = src.Runbook
	case FieldOwners:
		dst.Owners = src.Owners
	case FieldOnCall:
		dst.OnCall = src.OnCall
	case FieldLinks:
		dst.Links = src.Links
	default:
		// Set returns only the fields above.
	}
}

func checkChain(layers []Layer) error {
	if len(layers) == 0 {
		return fmt.Errorf("%w: empty context chain", ErrInvalidObject)
	}

	last := -1

	order := chainOrder()

	for i := range layers {
		l := &layers[i]

		pos := slices.Index(order, l.Object.Kind)
		if pos <= last {
			return fmt.Errorf("%w: context chain out of order at %s", ErrInvalidObject, l.Object.Kind)
		}

		last = pos

		for _, e := range []*Entry{l.API, l.Output} {
			if e != nil && e.Object != l.Object {
				return fmt.Errorf("%w: an entry of another object in the layer of %s", ErrInvalidObject, l.Object.Key())
			}
		}
	}

	return nil
}

// Patch is a change of the API entry of one object. A nil field stays as it
// is. A field that points to the zero value clears the field.
type Patch struct {
	Summary *string
	Runbook *string
	OnCall  *string
	Owners  *[]string
	Links   *[]Link
}

// Fields returns the fields that p changes, in a fixed order.
func (p Patch) Fields() []Field {
	var out []Field

	for _, c := range []struct {
		field Field
		set   bool
	}{
		{FieldSummary, p.Summary != nil},
		{FieldRunbook, p.Runbook != nil},
		{FieldOwners, p.Owners != nil},
		{FieldOnCall, p.OnCall != nil},
		{FieldLinks, p.Links != nil},
	} {
		if c.set {
			out = append(out, c.field)
		}
	}

	return out
}

// Apply returns f with p applied, and checks the result. It returns
// ErrInvalidContext.
func (p Patch) Apply(f Fields) (Fields, error) {
	if p.Summary != nil {
		f.Summary = *p.Summary
	}

	if p.Runbook != nil {
		f.Runbook = *p.Runbook
	}

	if p.OnCall != nil {
		f.OnCall = *p.OnCall
	}

	if p.Owners != nil {
		f.Owners = slices.Clone(*p.Owners)
	}

	if p.Links != nil {
		f.Links = slices.Clone(*p.Links)
	}

	return f, f.Validate()
}

// CheckPatch reports whether p may change the API entry of an object whose
// output entry is output. Each value of p must be valid, else it returns
// ErrInvalidContext. A patch that sets a field to a value, where the output
// sets the same field, fails with ErrFieldFromCode. A patch that clears such
// a field is allowed, so that a person can delete a shadowed API value.
// output is nil when the object has no output entry.
func CheckPatch(output *Entry, p Patch) error {
	values, err := p.Apply(Fields{Summary: "", Runbook: "", Owners: nil, OnCall: "", Links: nil})
	if err != nil {
		return err
	}

	if output == nil {
		return nil
	}

	fromCode := output.Fields.Set()

	for _, f := range values.Set() {
		if slices.Contains(fromCode, f) {
			return &FieldFromCodeError{Field: f}
		}
	}

	return nil
}

// FieldFromCodeError reports an API write of Field, which the output of the
// same object sets. It matches ErrFieldFromCode with errors.Is.
type FieldFromCodeError struct {
	Field Field
}

// Error returns the text of ErrFieldFromCode and the field.
func (e *FieldFromCodeError) Error() string {
	return ErrFieldFromCode.Error() + ": " + string(e.Field)
}

// Is reports whether target is ErrFieldFromCode.
func (e *FieldFromCodeError) Is(target error) bool {
	return target == ErrFieldFromCode
}
