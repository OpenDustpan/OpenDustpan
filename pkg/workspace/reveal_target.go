package workspace

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"strconv"
	"strings"
)

// Pass B of the reveal lookup for marks: a walk to the requested path that
// reports the raw value, its digest, its walked route and whether a proper
// ancestor below the attributes root matches a secret.

// targetResult is what pass B keeps of the requested path.
type targetResult struct {
	found        bool
	raw          []byte
	sum          digest
	route        []string
	maskedDepths []int
}

// errTargetDone ends pass B early once the value is read without hashing.
var errTargetDone = errors.New("target read")

// errTargetMissing aborts pass B once the walk knows the path is missing: a
// not-found answer needs no digest, and the value on the path is left
// unread, so the enclosing containers must stop too.
var errTargetMissing = errors.New("target missing")

// targetWalker walks the attributes value to the requested path.
type targetWalker struct {
	dec          *jsontext.Decoder
	secrets      map[digest]struct{}
	hashing      bool
	route        []string
	maskedDepths []int
	raw          []byte
	sum          digest
	found        bool
}

// scanTarget walks the attributes value that dec streams to the node at
// tokens, walked physically: an object key matches the token, an array
// element matches strconv.Atoi(token). It reports the raw value with its
// digest and walked route. With hashing it also digests every node it
// passes, so each proper ancestor digest is known; without hashing it stops
// as soon as the value is read.
//
//nolint:revive // hashing selects the digest mode of the walk; two walks would repeat it.
func scanTarget(
	dec *jsontext.Decoder, tokens []string, secrets map[digest]struct{}, hashing bool,
) (targetResult, error) {
	walker := &targetWalker{
		dec:          dec,
		secrets:      secrets,
		hashing:      hashing,
		route:        nil,
		maskedDepths: nil,
		raw:          nil,
		sum:          digest{},
		found:        false,
	}

	if err := walker.walkAt(tokens, 0); err != nil {
		if !hashing && errors.Is(err, errTargetDone) {
			return walker.result(), nil
		}

		if errors.Is(err, errTargetMissing) {
			var missing targetResult

			return missing, nil
		}

		var empty targetResult

		return empty, err
	}

	return walker.result(), nil
}

// result copies the walker outcome.
func (w *targetWalker) result() targetResult {
	return targetResult{
		found:        w.found,
		raw:          w.raw,
		sum:          w.sum,
		route:        append([]string(nil), w.route...),
		maskedDepths: append([]int(nil), w.maskedDepths...),
	}
}

// walkAt walks the value that dec streams with the tokens left.
func (w *targetWalker) walkAt(tokens []string, depth int) error {
	if len(tokens) == 0 {
		return w.readNode()
	}

	switch w.dec.PeekKind() {
	case jsontext.KindBeginObject:
		_, err := w.walkObject(tokens, depth)

		return err
	case jsontext.KindBeginArray:
		_, err := w.walkArray(tokens, depth)

		return err
	case jsontext.KindString,
		jsontext.KindNumber,
		jsontext.KindTrue,
		jsontext.KindFalse,
		jsontext.KindNull,
		jsontext.KindInvalid,
		jsontext.KindEndObject,
		jsontext.KindEndArray:
		return nil
	}

	return decodeStateFailed(errUnexpectedValue)
}

// readNode reads the node at the path into raw with its digest.
func (w *targetWalker) readNode() error {
	raw, err := w.dec.ReadValue()
	if err != nil {
		return decodeStateFailed(err)
	}

	kept := append([]byte(nil), raw...)

	w.raw = kept
	w.found = true

	if !w.hashing {
		return errTargetDone
	}

	sum, _, err := digestRaw(kept)
	if err != nil {
		return err
	}

	w.sum = sum

	return nil
}

// walkObject walks the object that dec streams with the tokens left.
func (w *targetWalker) walkObject(tokens []string, depth int) (digest, error) {
	if _, err := w.dec.ReadToken(); err != nil {
		return digest{}, decodeStateFailed(err)
	}

	scan := &targetObjectScan{
		walker:  w,
		want:    tokens[0],
		rest:    tokens[1:],
		depth:   depth,
		acc:     objectDigest{pairs: nil},
		matched: false,
		child:   digest{},
	}

	if err := scan.run(); err != nil {
		return digest{}, err
	}

	if !scan.matched || !w.found {
		return digest{}, nil
	}

	return scan.close(), nil
}

// walkArray walks the array that dec streams with the tokens left.
func (w *targetWalker) walkArray(tokens []string, depth int) (digest, error) {
	want, ok := targetIndex(tokens[0])
	if !ok {
		return digest{}, nil
	}

	if _, err := w.dec.ReadToken(); err != nil {
		return digest{}, decodeStateFailed(err)
	}

	scan := &targetArrayScan{
		walker:  w,
		want:    want,
		rest:    tokens[1:],
		depth:   depth,
		acc:     newArrayDigest(),
		matched: false,
		index:   0,
	}

	if err := scan.run(); err != nil {
		return digest{}, err
	}

	if !scan.matched || !w.found {
		return digest{}, nil
	}

	return scan.close(), nil
}

// targetIndex parses one array token exactly as maskListAt does.
func targetIndex(token string) (int, bool) {
	index, err := strconv.Atoi(token)
	if err != nil || index < 0 {
		return 0, false
	}

	return index, true
}

// targetObjectScan reads one object on the path.
type targetObjectScan struct {
	walker  *targetWalker
	want    string
	rest    []string
	depth   int
	acc     objectDigest
	matched bool
	child   digest
}

// run reads the members of the open object.
func (s *targetObjectScan) run() error {
	for {
		name, end, err := streamedMember(s.walker.dec)
		if err != nil {
			return err
		}

		if end {
			return nil
		}

		if err := s.member(name); err != nil {
			return err
		}
	}
}

// member reads one member value.
func (s *targetObjectScan) member(name string) error {
	if name == s.want && !s.matched {
		return s.follow(name)
	}

	return s.offPath(name)
}

// follow walks the member on the path.
func (s *targetObjectScan) follow(name string) error {
	s.walker.route = append(s.walker.route, name)

	child, err := s.walker.followRest(s.rest, s.depth)
	if err != nil {
		return err
	}

	if !s.walker.found {
		s.walker.route = s.walker.route[:len(s.walker.route)-1]

		return errTargetMissing
	}

	s.matched = true
	s.child = child
	s.acc.add(name, child)

	return nil
}

// offPath digests or skips one member off the path. Below the attributes
// root the digest feeds the ancestor hash; at the root (depth 0) the digest
// is never compared, so the value is skipped.
func (s *targetObjectScan) offPath(name string) error {
	if s.depth == 0 {
		return skipStreamed(s.walker.dec)
	}

	if !s.walker.hashing {
		return skipStreamed(s.walker.dec)
	}

	value, _, err := digestStreamed(s.walker.dec)
	if err != nil {
		return err
	}

	s.acc.add(name, value)

	return nil
}

// close sums the object and notes a masked ancestor. At the attributes
// root the sum is never compared, so it returns zero.
func (s *targetObjectScan) close() digest {
	if s.depth == 0 {
		return digest{}
	}

	sum := s.acc.sum()

	if _, ok := s.walker.secrets[sum]; ok {
		s.walker.maskedDepths = append(s.walker.maskedDepths, s.depth)
	}

	return sum
}

// followRest walks the value below one ancestor.
func (w *targetWalker) followRest(rest []string, depth int) (digest, error) {
	if len(rest) == 0 {
		if err := w.readNode(); err != nil {
			return digest{}, err
		}

		if !w.found {
			return digest{}, nil
		}

		return w.sum, nil
	}

	switch w.dec.PeekKind() {
	case jsontext.KindBeginObject:
		return w.walkObject(rest, depth+1)
	case jsontext.KindBeginArray:
		return w.walkArray(rest, depth+1)
	case jsontext.KindString,
		jsontext.KindNumber,
		jsontext.KindTrue,
		jsontext.KindFalse,
		jsontext.KindNull,
		jsontext.KindInvalid,
		jsontext.KindEndObject,
		jsontext.KindEndArray:
		if err := skipStreamed(w.dec); err != nil {
			return digest{}, err
		}

		return digest{}, nil
	}

	return digest{}, decodeStateFailed(errUnexpectedValue)
}

// targetArrayScan reads one array on the path.
type targetArrayScan struct {
	walker  *targetWalker
	want    int
	rest    []string
	depth   int
	acc     *arrayDigest
	matched bool
	index   int
}

// run reads the elements of the open array.
func (s *targetArrayScan) run() error {
	for {
		end, err := endOfStreamedArray(s.walker.dec)
		if err != nil {
			return err
		}

		if end {
			return nil
		}

		if err := s.element(); err != nil {
			return err
		}

		s.index++
	}
}

// element reads one array element.
func (s *targetArrayScan) element() error {
	if s.index == s.want && !s.matched {
		return s.follow()
	}

	return s.offPath()
}

// follow walks the element on the path.
func (s *targetArrayScan) follow() error {
	s.walker.route = append(s.walker.route, strconv.Itoa(s.index))

	child, err := s.walker.followRest(s.rest, s.depth)
	if err != nil {
		return err
	}

	if !s.walker.found {
		s.walker.route = s.walker.route[:len(s.walker.route)-1]

		return errTargetMissing
	}

	s.matched = true
	s.acc.add(child)

	return nil
}

// offPath digests or skips one element off the path. Below the attributes
// root the digest feeds the ancestor hash; at the root (depth 0) the digest
// is never compared, so the value is skipped.
func (s *targetArrayScan) offPath() error {
	if s.depth == 0 {
		return skipStreamed(s.walker.dec)
	}

	if !s.walker.hashing {
		return skipStreamed(s.walker.dec)
	}

	value, _, err := digestStreamed(s.walker.dec)
	if err != nil {
		return err
	}

	s.acc.add(value)

	return nil
}

// close sums the array and notes a masked ancestor. At the attributes root
// the sum is never compared, so it returns zero.
func (s *targetArrayScan) close() digest {
	if s.depth == 0 {
		return digest{}
	}

	sum := s.acc.sum()

	if _, ok := s.walker.secrets[sum]; ok {
		s.walker.maskedDepths = append(s.walker.maskedDepths, s.depth)
	}

	return sum
}

// targetVisitor returns the instanceVisitor for pass B: for the attributes
// member it runs scanTarget into out, only when the value is an object or an
// array; then it returns errStopWalk. Every other member: not consumed.
func targetVisitor(
	tokens []string, secrets map[digest]struct{}, hashing bool, out *targetResult,
) instanceVisitor {
	return func(dec *jsontext.Decoder, name string) (bool, error) {
		if name != attributesMemberName {
			return false, nil
		}

		return true, visitTarget(dec, tokens, secrets, hashing, out)
	}
}

// visitTarget scans the streamed attributes value into out.
func visitTarget(
	dec *jsontext.Decoder, tokens []string, secrets map[digest]struct{}, hashing bool, out *targetResult,
) error {
	switch dec.PeekKind() {
	case jsontext.KindBeginObject, jsontext.KindBeginArray:
		res, err := scanTarget(dec, tokens, secrets, hashing)
		if err != nil {
			return err
		}

		*out = res

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

// decideMasked reports whether the requested path is masked under the rule
// that maskTofuInstance computes when pointers are disjoint: a literal match
// answers, else a duplicate of a secret answers when no ancestor is masked,
// the node sits outside every applied location, and the canonical path
// equals the requested path.
func decideMasked(
	path, base string, applied []appliedPointer, secrets map[digest]struct{}, res targetResult,
) bool {
	if !res.found {
		return false
	}

	if literalMasked(path, base, applied) {
		return true
	}

	if len(secrets) == 0 {
		return false
	}

	if !inSecrets(res.sum, secrets) {
		return false
	}

	if belowApplied(applied, res.route) {
		return false
	}

	if dirty(applied, res.route) {
		return false
	}

	if canonicalPath(base, res.route) != path {
		return false
	}

	for _, depth := range res.maskedDepths {
		if depth < 1 || depth > len(res.route) {
			continue
		}

		if !dirty(applied, res.route[:depth]) {
			return false
		}
	}

	return true
}

// literalMasked reports whether an applied pointer names the path.
func literalMasked(path, base string, applied []appliedPointer) bool {
	for _, item := range applied {
		if base+item.literal == path {
			return true
		}
	}

	return false
}

// inSecrets reports whether sum is a secret digest.
func inSecrets(sum digest, secrets map[digest]struct{}) bool {
	_, ok := secrets[sum]

	return ok
}

// belowApplied reports whether the route sits at or below an applied
// location: the applied physical route is a prefix of the route.
func belowApplied(applied []appliedPointer, route []string) bool {
	for _, item := range applied {
		if isTokenPrefix(item.route, route) {
			return true
		}
	}

	return false
}

// dirty reports whether prefix is a proper prefix of an applied physical
// route: the node at prefix contains an applied location strictly below it.
func dirty(applied []appliedPointer, prefix []string) bool {
	for _, item := range applied {
		if len(item.route) > len(prefix) && isTokenPrefix(prefix, item.route) {
			return true
		}
	}

	return false
}

// isTokenPrefix reports whether prefix is a prefix of route, equal length
// included.
func isTokenPrefix(prefix, route []string) bool {
	if len(prefix) > len(route) {
		return false
	}

	for i := range prefix {
		if prefix[i] != route[i] {
			return false
		}
	}

	return true
}

// canonicalPath builds the value path from the walked route.
func canonicalPath(base string, route []string) string {
	var out strings.Builder

	out.WriteString(base)

	for _, token := range route {
		out.WriteString("/")
		out.WriteString(escapeTofuPointerToken(token))
	}

	return out.String()
}

// decodeTargetValue decodes the raw value at the path with numbers kept
// exact. A marker shaped value answers not found, as the old code does.
func decodeTargetValue(raw []byte) (any, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var out any

	if err := dec.Decode(&out); err != nil {
		return nil, false, decodeStateFailed(err)
	}

	if isStateMarker(out) {
		return nil, false, nil
	}

	return out, true, nil
}
