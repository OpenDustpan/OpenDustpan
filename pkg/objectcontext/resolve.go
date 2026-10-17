package objectcontext

import (
	"fmt"
	"slices"
)

// Layer is the context of one object from both sources. API and Output are
// nil when the source holds no entry for the object.
type Layer struct {
	API    *Entry
	Output *Entry
	Object Ref
}

// Provenance names where a resolved value comes from. It is the zero value
// for a value that no layer sets.
type Provenance struct {
	Source Source
	Object Ref
}

// Text is one resolved text field with its provenance.
type Text struct {
	Value string
	From  Provenance
}

// Owners is the resolved owners with their provenance.
type Owners struct {
	Value []string
	From  Provenance
}

// LinkValue is one resolved link with its provenance.
type LinkValue struct {
	Link

	From Provenance
}

// Shadow names an API value that the output of the same object hides. The
// console shows it, so that a person can delete the API value.
type Shadow struct {
	Object Ref
	Field  Field
}

// Warning is an output of a layer that did not parse.
type Warning struct {
	Object  Ref
	Message string
}

// Resolved is the effective context of one object, with the provenance of
// each value. Readme is the readme output of the unit of the chain.
type Resolved struct {
	Summary  Text
	Runbook  Text
	OnCall   Text
	Readme   string
	Owners   Owners
	Links    []LinkValue
	Shadowed []Shadow
	Warnings []Warning
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

		if i == 0 {
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
			return fmt.Errorf("%w: %s", ErrFieldFromCode, f)
		}
	}

	return nil
}
