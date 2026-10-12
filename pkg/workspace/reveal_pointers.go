package workspace

import (
	"encoding/json/jsontext"
	"errors"
	"strconv"
)

// Pass A of the reveal lookup for marks: resolution of the sensitive
// pointers on the stream, with digests of the propagable originals. A layout
// the stream cannot resolve exactly like maskTofuInstance reports entangled,
// and the caller uses the full decode instead.

// revealPointer is one sensitive pointer of the target instance.
type revealPointer struct {
	literal string   // the pointer as tofuSensitivePointers returns it, relative to the attributes
	tokens  []string // splitPointer(literal)
}

// instancePointers returns the pointers of the instance without a second
// copy of the same literal, in the order of tofuSensitivePointers.
func instancePointers(info instanceInfo, address string, marks SensitiveMarks) []revealPointer {
	instance := map[string]any{"sensitive_attributes": info.sensitive}
	literals := tofuSensitivePointers(instance, address, tofuInstanceBase(address), marks)

	seen := make(map[string]struct{}, len(literals))
	out := make([]revealPointer, 0, len(literals))

	for _, literal := range literals {
		if _, ok := seen[literal]; ok {
			continue
		}

		seen[literal] = struct{}{}
		out = append(out, revealPointer{literal: literal, tokens: splitPointer(literal)})
	}

	return out
}

// appliedPointer is a pointer that resolved on the stream.
type appliedPointer struct {
	literal string
	tokens  []string
	secret  *digest // digest of the original value when it is propagable, else nil
}

// pointerScan is the result of pass A.
type pointerScan struct {
	entangled bool             // the layout needs the full decode
	applied   []appliedPointer // in pointer order; only pointers that resolved
}

// errPointersEntangled reports a layout the stream cannot resolve exactly
// like maskTofuInstance.
var errPointersEntangled = errors.New("pointers entangled")

// pointerState is one pointer at the node being read: next is the index of
// the token to apply there, and next == len(tokens) means the node is the
// target of the pointer.
type pointerState struct {
	ptr  int
	next int
}

// pointerWalk collects pass A while the attributes value streams.
type pointerWalk struct {
	pointers []revealPointer
	have     []bool
	secrets  []*digest
}

// scanPointers walks the attributes value that dec streams and resolves the
// pointers on it. dec is positioned at the first token of the attributes
// value. It returns when the attributes value is consumed or when it finds
// the layout entangled.
func scanPointers(dec *jsontext.Decoder, pointers []revealPointer) (pointerScan, error) {
	if len(pointers) == 0 {
		return pointerScan{entangled: false, applied: nil}, nil
	}

	walk := &pointerWalk{
		pointers: pointers,
		have:     make([]bool, len(pointers)),
		secrets:  make([]*digest, len(pointers)),
	}

	states := make([]pointerState, len(pointers))
	for i := range pointers {
		states[i] = pointerState{ptr: i, next: 0}
	}

	if err := walk.node(dec, states); err != nil {
		if errors.Is(err, errPointersEntangled) {
			return pointerScan{entangled: true, applied: nil}, nil
		}

		return pointerScan{entangled: false, applied: nil}, err
	}

	return pointerScan{entangled: false, applied: walk.ordered()}, nil
}

// ordered returns the applied pointers in pointer order.
func (w *pointerWalk) ordered() []appliedPointer {
	var out []appliedPointer

	for i, ok := range w.have {
		if !ok {
			continue
		}

		out = append(out, appliedPointer{
			literal: w.pointers[i].literal,
			tokens:  w.pointers[i].tokens,
			secret:  w.secrets[i],
		})
	}

	return out
}

// node reads one JSON value with the states that arrive at it.
func (w *pointerWalk) node(dec *jsontext.Decoder, states []pointerState) error {
	split := w.split(states)
	hits := split.hits
	conts := split.conts

	if len(hits) > 1 || (len(hits) == 1 && len(conts) > 0) {
		return errPointersEntangled
	}

	if len(hits) == 1 {
		return w.finishHit(dec, hits[0].ptr)
	}

	if len(conts) == 0 {
		return skipStreamed(dec)
	}

	return w.walkValue(dec, conts)
}

// stateSplit holds the states at one node: hits on the node and states
// that go on below it.
type stateSplit struct {
	hits  []pointerState
	conts []pointerState
}

// split sorts states into hits on the node and states that go on below it.
func (w *pointerWalk) split(states []pointerState) stateSplit {
	split := stateSplit{hits: nil, conts: nil}

	for _, state := range states {
		if state.next == len(w.pointers[state.ptr].tokens) {
			split.hits = append(split.hits, state)
		} else {
			split.conts = append(split.conts, state)
		}
	}

	return split
}

// finishHit consumes the target of pointer ptr and records it.
func (w *pointerWalk) finishHit(dec *jsontext.Decoder, ptr int) error {
	switch dec.PeekKind() {
	case jsontext.KindString, jsontext.KindBeginObject, jsontext.KindBeginArray:
		sum, shape, err := digestStreamed(dec)
		if err != nil {
			return err
		}

		w.have[ptr] = true

		if shape.propagable() {
			kept := sum
			w.secrets[ptr] = &kept
		}

		return nil
	case jsontext.KindNumber,
		jsontext.KindTrue,
		jsontext.KindFalse,
		jsontext.KindNull,
		jsontext.KindInvalid,
		jsontext.KindEndObject,
		jsontext.KindEndArray:
		if err := skipStreamed(dec); err != nil {
			return err
		}

		w.have[ptr] = true

		return nil
	}

	return decodeStateFailed(errUnexpectedValue)
}

// walkValue reads one composite or scalar value with states that go on.
func (w *pointerWalk) walkValue(dec *jsontext.Decoder, conts []pointerState) error {
	switch dec.PeekKind() {
	case jsontext.KindBeginObject:
		return w.walkObject(dec, conts)
	case jsontext.KindBeginArray:
		return w.walkArray(dec, conts)
	case jsontext.KindString,
		jsontext.KindNumber,
		jsontext.KindTrue,
		jsontext.KindFalse,
		jsontext.KindNull,
		jsontext.KindInvalid,
		jsontext.KindEndObject,
		jsontext.KindEndArray:
		return skipStreamed(dec)
	}

	return decodeStateFailed(errUnexpectedValue)
}

// objectWalk counts the members of one streamed object.
type objectWalk struct {
	count    int
	hasValue bool
	hasType  bool
}

// note records one member name.
func (o *objectWalk) note(name string) {
	o.count++

	if name == "value" {
		o.hasValue = true
	}

	if name == "type" {
		o.hasType = true
	}
}

// wrapperEntangled reports whether the object is a dynamic wrapper in the
// path of a pointer.
func (o *objectWalk) wrapperEntangled(conts []pointerState) bool {
	return o.count == 2 && o.hasValue && o.hasType && hasDeepState(conts)
}

// groupContsByName sorts continuing states by the member name they step to.
func groupContsByName(pointers []revealPointer, conts []pointerState) map[string][]pointerState {
	byName := make(map[string][]pointerState, len(conts))

	for _, state := range conts {
		tokens := pointers[state.ptr].tokens
		if state.next >= len(tokens) {
			continue
		}

		key := tokens[state.next]
		byName[key] = append(byName[key], pointerState{ptr: state.ptr, next: state.next + 1})
	}

	return byName
}

// groupContsByIndex sorts continuing states by the element index they step
// to, parsed with strconv.Atoi exactly as maskListAt does.
func groupContsByIndex(pointers []revealPointer, conts []pointerState) map[int][]pointerState {
	byIndex := make(map[int][]pointerState, len(conts))

	for _, state := range conts {
		tokens := pointers[state.ptr].tokens
		if state.next >= len(tokens) {
			continue
		}

		got, err := strconv.Atoi(tokens[state.next])
		if err != nil || got < 0 {
			continue
		}

		byIndex[got] = append(byIndex[got], pointerState{ptr: state.ptr, next: state.next + 1})
	}

	return byIndex
}

// walkObject reads one object value with the states that arrive at it.
func (w *pointerWalk) walkObject(dec *jsontext.Decoder, conts []pointerState) error {
	if _, err := dec.ReadToken(); err != nil {
		return decodeStateFailed(err)
	}

	walk := &objectWalk{count: 0, hasValue: false, hasType: false}
	byName := groupContsByName(w.pointers, conts)

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return err
		}

		if end {
			break
		}

		walk.note(name)

		if err := w.walkObjectMember(dec, byName[name]); err != nil {
			return err
		}
	}

	// maskTokens may step through "value" of such an object, so a pointer
	// that passes this object cannot resolve on the stream.
	if walk.wrapperEntangled(conts) {
		return errPointersEntangled
	}

	return nil
}

// walkObjectMember reads the streamed value of one object member.
func (w *pointerWalk) walkObjectMember(dec *jsontext.Decoder, derived []pointerState) error {
	if len(derived) == 0 {
		return skipStreamed(dec)
	}

	return w.node(dec, derived)
}

// hasDeepState reports whether a state already stepped below the attributes.
func hasDeepState(conts []pointerState) bool {
	for _, state := range conts {
		if state.next >= 1 {
			return true
		}
	}

	return false
}

// walkArray reads one array value with the states that arrive at it.
func (w *pointerWalk) walkArray(dec *jsontext.Decoder, conts []pointerState) error {
	if _, err := dec.ReadToken(); err != nil {
		return decodeStateFailed(err)
	}

	byIndex := groupContsByIndex(w.pointers, conts)

	for index := 0; ; index++ {
		end, err := endOfStreamedArray(dec)
		if err != nil {
			return err
		}

		if end {
			return nil
		}

		if err := w.walkArrayElement(dec, byIndex[index]); err != nil {
			return err
		}
	}
}

// walkArrayElement reads one array element with the states that step to it.
func (w *pointerWalk) walkArrayElement(dec *jsontext.Decoder, derived []pointerState) error {
	if len(derived) == 0 {
		return skipStreamed(dec)
	}

	return w.node(dec, derived)
}

// pointerVisitor returns the instanceVisitor for pass A: for the attributes
// member it runs scanPointers into out, only when the value is an object or
// an array; then it returns errStopWalk. Every other member: not consumed.
func pointerVisitor(pointers []revealPointer, out *pointerScan) instanceVisitor {
	return func(dec *jsontext.Decoder, name string) (bool, error) {
		if name != attributesMemberName {
			return false, nil
		}

		return true, visitAttributes(dec, pointers, out)
	}
}

// visitAttributes scans the streamed attributes value into out.
func visitAttributes(dec *jsontext.Decoder, pointers []revealPointer, out *pointerScan) error {
	switch dec.PeekKind() {
	case jsontext.KindBeginObject, jsontext.KindBeginArray:
		scan, err := scanPointers(dec, pointers)
		if err != nil {
			return err
		}

		*out = scan

		return errStopWalk
	case jsontext.KindString,
		jsontext.KindNumber,
		jsontext.KindTrue,
		jsontext.KindFalse,
		jsontext.KindNull,
		jsontext.KindInvalid,
		jsontext.KindEndObject,
		jsontext.KindEndArray:
		if err := skipStreamed(dec); err != nil {
			return err
		}

		return errStopWalk
	}

	return errStopWalk
}
