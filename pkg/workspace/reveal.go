package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
)

// Reveal of one secret value (ADR 41 decisions 4 and 5): the caller names a
// value path of a state file, the server answers the plain value only when
// the path is in the masked set of the masked state read. The masked set
// here is the union of the masked paths of every resource, built with the
// same maskTofuInstance as the read, so no second masking path exists. The
// reveal lookup streams the state to the path and keeps no more than one
// resource instance in memory, so no tree of the whole state is built.

// Memory bound: heap growth of the lookup does not depend on the size of the
// state. It grows with the largest single string token of the state, about
// four times that string, because jsontext buffers a string token whole while
// it skips it. A state with a 1 MiB string may cost about 4 MiB more per call.
//
// errUnexpectedValue reports streamed JSON of another shape than the lookup
// walks. The state machine of the decoder already rejects malformed JSON, so
// this only guards the Go switch. It carries no value.
var errUnexpectedValue = errors.New("unexpected value")

const (
	// resourceFieldNames name the identity fields of a streamed resource
	// that the instance address is built from. They repeat the wire names,
	// so the lookup and the read agree.
	modeFieldName   = "mode"
	typeFieldName   = "type"
	nameFieldName   = "name"
	moduleFieldName = "module"
)

// Limit: a reveal that needs the full instance decodes the one target
// instance in full, into a map that maskTofuInstance walks. It refuses an
// instance over 4 MiB, and a lookup whose sensitive_attributes run over
// 1 MiB fails too, so heap growth stays bounded. With no marks the lookup
// answers from the stream and its heap growth does not depend on the size of
// the state. With marks the lookup streams the answer too when the pointer
// layout is not entangled; only the entangled layout uses the full decode,
// so the 4 MiB limit applies only there. The streaming answer keeps the
// string token bound, plus 64 bytes for each member of each open object,
// plus the raw and decoded answer, plus the sensitive_attributes of the
// instance.

const (
	// revealFallbackLimit is the largest instance (raw bytes) that the full
	// decode accepts: 4 MiB.
	revealFallbackLimit = 4 << 20
	// revealMarksLimit is the largest sensitive_attributes (raw bytes) the
	// lookup reads: 1 MiB.
	revealMarksLimit = 1 << 20
)

// stateOpener opens the content of one finalized state version. The lookup calls it once for each pass, and each call
// must return the same bytes from the start. The caller does not close the result: the lookup does.
type stateOpener func() (io.ReadCloser, error)

// VersionReader reads finalized state versions. Service implements it.
type VersionReader interface {
	// ReadState returns the finalized state version with id, or
	// ErrStateVersionNotFound.
	ReadState(ctx context.Context, id StateVersionID) (StateVersion, error)
	// Open returns the content of sv. The caller closes it.
	Open(ctx context.Context, sv StateVersion) (io.ReadCloser, error)
}

// RevealVersion returns the plain value at path of the finalized state
// version id, which versions reads. The path must be in the masked set of the
// version, with the sensitive marks that the version keeps, else it fails
// with ErrResourceNotFound, so a caller cannot probe paths. A state the client
// encrypted fails with ErrClientEncrypted. It opens the content once for each
// pass of the lookup and decodes from the stream, so no reveal reads the
// whole state into memory. A failed close of the content does not fail the
// lookup: logger names it, with no value.
func RevealVersion(
	ctx context.Context, versions VersionReader, id StateVersionID, path string, logger *slog.Logger,
) (any, error) {
	sv, err := versions.ReadState(ctx, id)
	if err != nil {
		return nil, err
	}

	open := func() (io.ReadCloser, error) {
		content, openErr := versions.Open(ctx, sv)
		if openErr != nil {
			return nil, openErr
		}

		return &warnOnClose{ReadCloser: content, warn: func(closeErr error) {
			if logger != nil {
				logger.WarnContext(ctx, "close state content failed", slog.String("error", closeErr.Error()))
			}
		}}, nil
	}

	value, _, err := revealFromOpener(open, sv.SensitiveMarks, path)

	return value, err
}

// warnOnClose logs a failed close of the content of a state version. The
// lookup closes each pass; the log keeps the warning, and the error still
// reaches the lookup, which joins it.
type warnOnClose struct {
	io.ReadCloser

	warn func(closeErr error)
}

// Close closes the content, logs a failure, and returns it.
func (c *warnOnClose) Close() error {
	if err := c.ReadCloser.Close(); err != nil {
		c.warn(err)

		return fmt.Errorf("close state content: %w", err)
	}

	return nil
}

// revealFromOpener returns the plain value at path of the state that
// open serves, and the masked set holding the path. The path must be in the
// masked set, else it fails with ErrResourceNotFound, so a caller cannot
// probe paths. A state the client encrypted fails with ErrClientEncrypted.
// It decodes from the stream directly: values off the path are skipped
// without building them, and no more than one resource instance is kept in
// memory. The numbers of the answer keep their exact encoding. The lookup
// calls open once for each pass: two passes with no marks (locate, answer),
// and with marks three passes (locate, pointers, answer; the third is the
// full decode when the layout is entangled). A state version is
// write-once so the passes read the same bytes, and each pass decrypts the
// stream again (about 3x CPU). A failed open fails as an open state error. A
// close error does not fail the lookup. The opener logs it.
func revealFromOpener(
	open stateOpener, marks SensitiveMarks, path string,
) (any, []string, error) {
	target, shapeOK := parseRevealTarget(path, marks)
	if !shapeOK {
		var (
			value  any
			masked []string
		)

		err := withPass(open, func(dec *jsontext.Decoder) error {
			v, m, rerr := revealStream(dec, marks, path)
			value, masked = v, m

			return rerr
		})

		return value, masked, err
	}

	var info instanceInfo

	res := new(walkResult)
	res.fields = map[string]any{}

	err := withPass(open, func(dec *jsontext.Decoder) error {
		return locateWalk(dec, target, infoVisitor(&info), res)
	})
	if err != nil {
		return nil, nil, err
	}

	if !res.found {
		return nil, nil, notFoundState(path)
	}

	info.fields = res.fields

	if res.ranged {
		info.size = res.end - res.start
	}

	address := tofuInstanceAddress(res.fields, map[string]any{"index_key": info.indexKey})

	if len(marks) == 0 {
		return revealNoMarks(open, target, path)
	}

	return revealWithMarks(open, target, path, info, address)
}

// withPass opens the state, runs fn on its stream, and closes the stream
// before the next pass starts. A close error does not fail the pass. The
// opener logs it.
func withPass(open stateOpener, fn func(*jsontext.Decoder) error) error {
	stream, err := open()
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}

	defer func() {
		_ = stream.Close() //nolint:errcheck // the opener logs a close failure
	}()

	return fn(jsontext.NewDecoder(stream))
}

// revealNoMarks answers a reveal with no marks from the stream: with no kept
// marks every leaf of the attributes is masked, so the walk below the
// attributes is physical, with no dynamic wrapper rule. It answers the leaf
// at the path, or fails with ErrResourceNotFound.
func revealNoMarks(open stateOpener, target revealTarget, path string) (any, []string, error) {
	base := "/resources/" + strconv.Itoa(target.resource) +
		"/instances/" + strconv.Itoa(target.position) + "/attributes"

	var (
		answer    any
		answered  bool
		canonical string
	)

	err := withPass(open, func(dec *jsontext.Decoder) error {
		_, _, lerr := locateInstance(dec, target, func(dec *jsontext.Decoder, name string) (bool, error) {
			if name != attributesMemberName {
				return false, nil
			}

			value, canon, werr := revealNoMarksLeaf(dec, target.rest, base)
			if werr != nil {
				return true, werr
			}

			if canon != "" {
				answer, answered, canonical = value, true, canon
			}

			return true, errStopWalk
		})

		return lerr
	})
	if err != nil {
		return nil, nil, err
	}

	if !answered || canonical != path {
		return nil, nil, notFoundState(path)
	}

	return answer, []string{path}, nil
}

// revealNoMarksLeaf walks the attributes value that dec streams along rest
// and reports the leaf at the path with its canonical value path. At an
// object the token must be a key: members are read, the others skipped, the
// first match taken. At an array the token is an index parsed with
// strconv.Atoi exactly as maskListAt does. Anything else with tokens left is
// not found, as is an object or an array where the tokens run out: only a
// leaf answers. The canonical path is rebuilt from the walked route, so a
// path the masked read never produces (such as an index with a leading zero)
// answers not found. A not found path reports an empty canonical path,
// never an error: the canonical path of an answer is never empty.
func revealNoMarksLeaf(
	dec *jsontext.Decoder, rest []string, base string,
) (any, string, error) {
	var canonical strings.Builder

	canonical.WriteString(base)

	for _, token := range rest {
		step, ok, err := revealNoMarksStep(dec, token)
		if err != nil {
			return nil, "", err
		}

		if !ok {
			return nil, "", nil
		}

		canonical.WriteString(step)
	}

	value, ok, err := revealNoMarksValue(dec)
	if err != nil {
		return nil, "", err
	}

	if !ok {
		return nil, "", nil
	}

	return value, canonical.String(), nil
}

// revealNoMarksStep walks one token of rest below the attributes value that
// dec streams and reports its canonical step. Anything else with a token
// left reads as not found.
func revealNoMarksStep(dec *jsontext.Decoder, token string) (string, bool, error) {
	kind := dec.PeekKind()
	if kind != jsontext.KindBeginObject && kind != jsontext.KindBeginArray {
		return "", false, nil
	}

	if kind == jsontext.KindBeginObject {
		key, ok, err := revealNoMarksKey(dec, token)
		if err != nil {
			return "", false, err
		}

		if !ok {
			return "", false, nil
		}

		return "/" + escapeTofuPointerToken(key), true, nil
	}

	index, ok, err := revealNoMarksArray(dec, token)
	if err != nil {
		return "", false, err
	}

	if !ok {
		return "", false, nil
	}

	return "/" + strconv.Itoa(index), true, nil
}

// revealNoMarksKey walks the streamed object value that dec streams to the
// value of token and reports its key. Members off the path are skipped.
func revealNoMarksKey(dec *jsontext.Decoder, token string) (string, bool, error) {
	if _, err := dec.ReadToken(); err != nil {
		return "", false, decodeStateFailed(err)
	}

	for {
		if dec.PeekKind() == jsontext.KindEndObject {
			return "", false, nil
		}

		tok, err := dec.ReadToken()
		if err != nil {
			return "", false, decodeStateFailed(err)
		}

		if tok.Kind() != jsontext.KindString {
			return "", false, decodeStateFailed(errUnexpectedValue)
		}

		if tok.String() == token {
			return tok.String(), true, nil
		}

		if err := skipStreamed(dec); err != nil {
			return "", false, err
		}
	}
}

// revealNoMarksArray walks the streamed array value that dec streams to the
// element of token and reports its index. A token that is not an index, or
// an array that does not hold the element, reads as not found.
func revealNoMarksArray(dec *jsontext.Decoder, token string) (int, bool, error) {
	index, ok := revealNoMarksIndex(token)
	if !ok {
		return 0, false, nil
	}

	if _, err := dec.ReadToken(); err != nil {
		return 0, false, decodeStateFailed(err)
	}

	elemOK, err := revealNoMarksElement(dec, index)
	if err != nil {
		return 0, false, err
	}

	if !elemOK {
		return 0, false, nil
	}

	return index, true, nil
}

// revealNoMarksIndex parses token as an array index for the no-marks walk,
// exactly as maskListAt does. A token that is not an index reads as not
// found, never as a failure.
func revealNoMarksIndex(token string) (int, bool) {
	index, err := strconv.Atoi(token)
	if err != nil || index < 0 {
		return 0, false
	}

	return index, true
}

// revealNoMarksValue reads the streamed value that dec streams and reports
// it when it is a leaf: a JSON string, number, true, false or null. An
// object or an array reads as not found.
func revealNoMarksValue(dec *jsontext.Decoder) (any, bool, error) {
	kind := dec.PeekKind()
	if kind == jsontext.KindBeginObject || kind == jsontext.KindBeginArray {
		return nil, false, nil
	}

	raw, err := dec.ReadValue()
	if err != nil {
		return nil, false, decodeStateFailed(err)
	}

	outDec := json.NewDecoder(bytes.NewReader(raw))
	outDec.UseNumber()

	var out any

	if err := outDec.Decode(&out); err != nil {
		return nil, false, decodeStateFailed(err)
	}

	return out, true, nil
}

// revealNoMarksElement walks the streamed array that dec streams to element
// index, counting elements while skipping. It reports whether the array
// holds the element. The element stays for the caller.
func revealNoMarksElement(dec *jsontext.Decoder, index int) (bool, error) {
	for at := 0; ; at++ {
		if dec.PeekKind() == jsontext.KindEndArray {
			return false, nil
		}

		if at == index {
			return true, nil
		}

		if err := skipStreamed(dec); err != nil {
			return false, err
		}
	}
}

// revealWithMarks answers a reveal with marks from the stream when the
// pointer layout is not entangled, else with the full decode.
func revealWithMarks(
	open stateOpener, target revealTarget, path string, info instanceInfo, address string,
) (any, []string, error) {
	pointers := instancePointers(info, address, target.marks)
	if len(pointers) == 0 || !isAttributesContainer(info) {
		return nil, nil, notFoundState(path)
	}

	scan, err := revealPointerScan(open, target, pointers)
	if err != nil {
		return nil, nil, err
	}

	if scan.entangled {
		return revealFullDecode(open, target.marks, path, info)
	}

	return revealStreamedAnswer(open, target, path, scan)
}

// isAttributesContainer reports whether the attributes value can hold a
// masked path: an object or an array.
func isAttributesContainer(info instanceInfo) bool {
	return info.attributes == jsontext.KindBeginObject || info.attributes == jsontext.KindBeginArray
}

// revealPointerScan runs pass A of the marks lookup.
func revealPointerScan(
	open stateOpener, target revealTarget, pointers []revealPointer,
) (pointerScan, error) {
	var scan pointerScan

	err := withPass(open, func(dec *jsontext.Decoder) error {
		_, _, lerr := locateInstance(dec, target, pointerVisitor(pointers, &scan))

		return lerr
	})
	if err != nil {
		return pointerScan{}, err
	}

	return scan, nil
}

// revealStreamedAnswer runs pass B of the marks lookup and answers it.
func revealStreamedAnswer(
	open stateOpener, target revealTarget, path string, scan pointerScan,
) (any, []string, error) {
	base := revealBase(target)
	secrets := revealSecrets(scan.applied)

	if !literalMasked(path, base, scan.applied) && len(secrets) == 0 {
		return nil, nil, notFoundState(path)
	}

	res, err := revealTargetScan(open, target, secrets)
	if err != nil {
		return nil, nil, err
	}

	if !decideMasked(path, base, scan.applied, secrets, res) {
		return nil, nil, notFoundState(path)
	}

	value, ok, err := decodeTargetValue(res.raw)
	if err != nil {
		return nil, nil, err
	}

	if !ok {
		return nil, nil, notFoundState(path)
	}

	return value, []string{path}, nil
}

// revealBase is the attributes base path of the target instance.
func revealBase(target revealTarget) string {
	return "/resources/" + strconv.Itoa(target.resource) +
		"/instances/" + strconv.Itoa(target.position) + "/attributes"
}

// revealSecrets collects the secret digests of the applied pointers.
func revealSecrets(applied []appliedPointer) map[digest]struct{} {
	secrets := map[digest]struct{}{}

	for _, item := range applied {
		if item.secret != nil {
			secrets[*item.secret] = struct{}{}
		}
	}

	return secrets
}

// revealTargetScan runs pass B of the marks lookup.
func revealTargetScan(
	open stateOpener, target revealTarget, secrets map[digest]struct{},
) (targetResult, error) {
	var res targetResult

	err := withPass(open, func(dec *jsontext.Decoder) error {
		_, _, lerr := locateInstance(dec, target, targetVisitor(target.rest, secrets, len(secrets) > 0, &res))

		return lerr
	})
	if err != nil {
		return targetResult{}, err
	}

	return res, nil
}

// revealFullDecode answers a reveal that needs the full instance by decoding
// the target instance in full, as before. It refuses an instance over the
// fallback limit.
func revealFullDecode(
	open stateOpener, marks SensitiveMarks, path string, info instanceInfo,
) (any, []string, error) {
	if info.size > revealFallbackLimit {
		return nil, nil, fmt.Errorf(
			"instance holds %d bytes, over the limit of %d bytes for a reveal that needs the full instance: %w",
			info.size,
			revealFallbackLimit,
			ErrTooLarge,
		)
	}

	var (
		value  any
		masked []string
	)

	err := withPass(open, func(dec *jsontext.Decoder) error {
		v, m, rerr := revealStream(dec, marks, path)
		value, masked = v, m

		return rerr
	})

	return value, masked, err
}

// revealTarget names the streamed value a reveal lookup walks to: the
// resource and instance positions, the attribute path below them, the full
// value path for the masked set check, and the kept marks of the version.
type revealTarget struct {
	resource int
	position int
	rest     []string
	path     string
	marks    SensitiveMarks
}

// parseRevealTarget reads path as a reveal value path: an absolute pointer
// below the attributes of one instance. A path of another shape still
// streams the state, so a client-encrypted state answers 409 whatever the
// path is, as the read does.
func parseRevealTarget(path string, marks SensitiveMarks) (revealTarget, bool) {
	var noTarget revealTarget

	if path == "" || path[0] != '/' {
		return noTarget, false
	}

	tokens := splitPointer(path)
	if len(tokens) < 6 || tokens[0] != "resources" || tokens[2] != "instances" ||
		tokens[4] != "attributes" {
		return noTarget, false
	}

	resource, resourceErr := strconv.Atoi(tokens[1])
	position, positionErr := strconv.Atoi(tokens[3])

	if resourceErr != nil || resource < 0 || positionErr != nil || position < 0 {
		return noTarget, false
	}

	return revealTarget{
		resource: resource,
		position: position,
		rest:     tokens[5:],
		path:     path,
		marks:    marks,
	}, true
}

// decodeStateFailed wraps a decoder failure for a state that does not
// decode. It carries no value.
func decodeStateFailed(err error) error {
	return fmt.Errorf("decode state: %w", err)
}

// notFoundState reports a path that names no masked value, quoted for the
// caller but with no value. It wraps only the not-found sentinel.
func notFoundState(path string) error {
	return fmt.Errorf("%w: %q", ErrResourceNotFound, path)
}

// endOfStreamedArray reports whether the streamed array that dec streams
// whether it opens an array. Any other value is consumed.
func openStreamedArray(dec *jsontext.Decoder) (bool, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return false, decodeStateFailed(err)
	}

	return tok.Kind() == jsontext.KindBeginArray, nil
}

// streamedMember reads the next member name of the streamed object that dec
// streams. end reports the closing delimiter.
func streamedMember(dec *jsontext.Decoder) (string, bool, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return "", false, decodeStateFailed(err)
	}

	if tok.Kind() == jsontext.KindEndObject {
		return "", true, nil
	}

	if tok.Kind() != jsontext.KindString {
		return "", false, decodeStateFailed(errUnexpectedValue)
	}

	return tok.String(), false, nil
}

// endOfStreamedArray reports whether the streamed array that dec streams
// closes here, and consumes the delimiter when it does.
func endOfStreamedArray(dec *jsontext.Decoder) (bool, error) {
	if dec.PeekKind() != jsontext.KindEndArray {
		return false, nil
	}

	if _, err := dec.ReadToken(); err != nil {
		return false, decodeStateFailed(err)
	}

	return true, nil
}

// skipStreamed skips the streamed value that dec streams without building
// it. A decoder failure wraps as a decode failure.
func skipStreamed(dec *jsontext.Decoder) error {
	if err := dec.SkipValue(); err != nil {
		return decodeStateFailed(err)
	}

	return nil
}

// drainStreamedArray skips the rest of the streamed array that dec streams,
// so the caller goes on with the value after it.
func drainStreamedArray(dec *jsontext.Decoder) error {
	for {
		end, err := endOfStreamedArray(dec)
		if err != nil {
			return err
		}

		if end {
			return nil
		}

		if skipErr := skipStreamed(dec); skipErr != nil {
			return skipErr
		}
	}
}

// revealStream walks the streamed state to the value at the path of target
// and returns it. A state the client encrypted fails with ErrClientEncrypted
// wherever the envelope sits. A path outside the masked set fails with
// ErrResourceNotFound.
func revealStream(
	dec *jsontext.Decoder, marks SensitiveMarks, path string,
) (any, []string, error) {
	target, shapeOK := parseRevealTarget(path, marks)

	var want *revealTarget
	if shapeOK {
		want = &target
	}

	tok, err := dec.ReadToken()
	if err != nil {
		return nil, nil, decodeStateFailed(err)
	}

	if tok.Kind() != jsontext.KindBeginObject {
		return nil, nil, notFoundState(path)
	}

	var (
		found     any
		haveFound bool
	)

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return nil, nil, err
		}

		if end {
			break
		}

		value, ok, err := revealStreamedMember(dec, want, name)
		if err != nil {
			return nil, nil, err
		}

		if ok {
			found = value
			haveFound = true
		}
	}

	if haveFound {
		return found, []string{path}, nil
	}

	return nil, nil, notFoundState(path)
}

// revealStreamedMember reads the streamed top-level member name of the state
// and returns the reveal value when the member holds it. A nil target means
// the path has another shape than a reveal path: the resources array is then
// skipped like every other member, so a client-encrypted state still answers
// 409 whatever the path is. The encrypted envelope fails the lookup, so no
// value of a client-encrypted state ever answers.
func revealStreamedMember(
	dec *jsontext.Decoder, target *revealTarget, name string,
) (any, bool, error) {
	switch name {
	case encryptedStateKey:
		return nil, false, fmt.Errorf("reveal of a client-encrypted state: %w", ErrClientEncrypted)
	case "resources":
		if target == nil {
			return nil, false, skipStreamed(dec)
		}

		return revealFromResources(dec, *target)
	default:
		return nil, false, skipStreamed(dec)
	}
}

// streamArrayAt skips the streamed array that dec streams to element index,
// and reports whether the array holds one. The element stays for the caller.
func streamArrayAt(dec *jsontext.Decoder, index int) (bool, error) {
	for i := 0; ; i++ {
		end, err := endOfStreamedArray(dec)
		if err != nil {
			return false, err
		}

		if end {
			return false, nil
		}

		if i == index {
			return true, nil
		}

		if skipErr := skipStreamed(dec); skipErr != nil {
			return false, skipErr
		}
	}
}

// revealFromResources walks the streamed resources array that dec streams to
// the value of target. It reports whether the walk found the value. Values
// off the path are skipped without building them.
func revealFromResources(dec *jsontext.Decoder, target revealTarget) (any, bool, error) {
	open, err := openStreamedArray(dec)
	if err != nil {
		return nil, false, err
	}

	if !open {
		return nil, false, nil
	}

	at, err := streamArrayAt(dec, target.resource)
	if err != nil {
		return nil, false, err
	}

	if !at {
		return nil, false, nil
	}

	value, ok, err := revealFromResource(dec, target)
	if err != nil {
		return nil, false, err
	}

	// Drain the rest of the array, so the caller goes on with the members
	// after it.
	if drainErr := drainStreamedArray(dec); drainErr != nil {
		return nil, false, drainErr
	}

	return value, ok, nil
}

// revealFromResource walks the streamed resource object that dec streams to
// the value of target. The resource fields may sit before or after the
// instances array. It reports whether the walk found a masked value at the
// path.
func revealFromResource(dec *jsontext.Decoder, target revealTarget) (any, bool, error) {
	side, err := streamedResource(dec, target.position)
	if err != nil {
		return nil, false, err
	}

	if side.instance == nil {
		return nil, false, nil
	}

	// Entries that are not objects with a string type and name have no
	// masked path in the read, so they never answer here either.
	if fieldText(side.fields, typeFieldName) == "" || fieldText(side.fields, nameFieldName) == "" {
		return nil, false, nil
	}

	address := tofuInstanceAddress(side.fields, side.instance)

	attributes, _ := side.instance["attributes"]

	plain, ok := lookupTokens(attributes, target.rest)
	if !ok {
		return nil, false, nil
	}

	// A marker never answers: the plain state holds the plain value at a
	// masked path. A marker-shaped real value is still a real value.
	if isStateMarker(plain) {
		return nil, false, nil
	}

	// The value travels through JSON, so numbers keep their exact encoding.
	raw, err := json.Marshal(plain)
	if err != nil {
		return nil, false, fmt.Errorf("encode revealed value: %w", err)
	}

	outDec := json.NewDecoder(bytes.NewReader(raw))
	outDec.UseNumber()

	var out any

	if err := outDec.Decode(&out); err != nil {
		return nil, false, fmt.Errorf("decode revealed value: %w", err)
	}

	_, masked := maskTofuInstance(side.instance, target.resource, target.position, address, target.marks)
	if !slices.Contains(masked, target.path) {
		return nil, false, nil
	}

	return out, true, nil
}

// fieldText reads one identity field of a streamed resource, or "" when the
// field is missing. Only strings land in fields, so this matches the read,
// which skips entries without a string type and name.
func fieldText(fields map[string]any, key string) string {
	text, ok := fields[key].(string)
	if !ok {
		return ""
	}

	return text
}

// resourceSide is one streamed resource: its identity fields with the
// decoded instance at the wanted position. A missing instance reads as no
// instance.
type resourceSide struct {
	fields   map[string]any
	instance map[string]any
}

// streamedResource walks the streamed resource object that dec streams and
// returns its identity fields with the instance at position. Only that
// instance is decoded; every other value is skipped.
func streamedResource(
	dec *jsontext.Decoder, position int,
) (resourceSide, error) {
	if dec.PeekKind() != jsontext.KindBeginObject {
		if skipErr := skipStreamed(dec); skipErr != nil {
			return resourceSide{fields: map[string]any{}, instance: nil}, skipErr
		}

		return resourceSide{fields: map[string]any{}, instance: nil}, nil
	}

	if _, err := dec.ReadToken(); err != nil {
		return resourceSide{fields: map[string]any{}, instance: nil}, decodeStateFailed(err)
	}

	side := resourceSide{fields: map[string]any{}, instance: nil}

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return resourceSide{fields: map[string]any{}, instance: nil}, err
		}

		if end {
			return side, nil
		}

		instance, ok, err := streamedResourceValue(dec, side.fields, name, position)
		if err != nil {
			return resourceSide{fields: map[string]any{}, instance: nil}, err
		}

		if ok {
			side.instance = instance
		}
	}
}

// streamedResourceValue reads the streamed value of one resource member
// named name: an identity field lands in fields, the wanted instance
// decodes, and every other value is skipped. It reports whether the member
// held the wanted instance.
func streamedResourceValue(
	dec *jsontext.Decoder, fields map[string]any, name string, position int,
) (map[string]any, bool, error) {
	switch name {
	case modeFieldName, typeFieldName, nameFieldName, moduleFieldName:
		value, ok, err := streamedString(dec)
		if err != nil {
			return nil, false, err
		}

		if ok {
			fields[name] = value
		}

		return nil, false, nil
	case "instances":
		return revealFromInstances(dec, position)
	default:
		return nil, false, skipStreamed(dec)
	}
}

// streamedString reads the streamed value that dec streams and returns its
// text when it is a string. Any other value is skipped.
func streamedString(dec *jsontext.Decoder) (string, bool, error) {
	if dec.PeekKind() != jsontext.KindString {
		if skipErr := skipStreamed(dec); skipErr != nil {
			return "", false, skipErr
		}

		return "", false, nil
	}

	tok, err := dec.ReadToken()
	if err != nil {
		return "", false, decodeStateFailed(err)
	}

	return tok.String(), true, nil
}

// revealFromInstances walks the streamed instances array that dec streams to
// the instance at position and decodes only that instance. Entries that are
// not objects do not count toward the position, as in the masked read. It
// reports whether the array holds such an instance.
func revealFromInstances(
	dec *jsontext.Decoder, position int,
) (map[string]any, bool, error) {
	open, err := openStreamedArray(dec)
	if err != nil {
		return nil, false, err
	}

	if !open {
		return nil, false, nil
	}

	count := 0

	for {
		end, err := endOfStreamedArray(dec)
		if err != nil {
			return nil, false, err
		}

		if end {
			return nil, false, nil
		}

		step, err := streamedInstanceAt(dec, position, count)
		if err != nil {
			return nil, false, err
		}

		count = step.next

		if !step.found {
			continue
		}

		if drainErr := drainStreamedArray(dec); drainErr != nil {
			return nil, false, drainErr
		}

		if step.instance == nil {
			return nil, false, nil
		}

		return step.instance, true, nil
	}
}

// instanceStep is the result of reading one element of the streamed
// instances array: the decoded instance with the next object count and
// whether the element is the wanted one.
type instanceStep struct {
	instance map[string]any
	next     int
	found    bool
}

// streamedInstanceAt reads one element of the streamed instances array that
// dec streams. Non-object entries never count toward position, as in the
// masked read. Every other element is skipped.
func streamedInstanceAt(
	dec *jsontext.Decoder, position, count int,
) (instanceStep, error) {
	if dec.PeekKind() != jsontext.KindBeginObject {
		if skipErr := skipStreamed(dec); skipErr != nil {
			return instanceStep{instance: nil, next: count, found: false}, skipErr
		}

		return instanceStep{instance: nil, next: count, found: false}, nil
	}

	if count != position {
		if skipErr := skipStreamed(dec); skipErr != nil {
			return instanceStep{instance: nil, next: count, found: false}, skipErr
		}

		return instanceStep{instance: nil, next: count + 1, found: false}, nil
	}

	instance, err := readStreamedInstance(dec)
	if err != nil {
		return instanceStep{instance: nil, next: count, found: false}, err
	}

	return instanceStep{instance: instance, next: count, found: true}, nil
}

// readStreamedInstance reads the streamed instance object that dec streams
// and decodes it with numbers kept exact. Only this instance is kept.
func readStreamedInstance(dec *jsontext.Decoder) (map[string]any, error) {
	raw, err := dec.ReadValue()
	if err != nil {
		return nil, decodeStateFailed(err)
	}

	instanceDec := json.NewDecoder(bytes.NewReader(raw))
	instanceDec.UseNumber()

	var instance map[string]any

	if err := instanceDec.Decode(&instance); err != nil {
		return nil, decodeStateFailed(err)
	}

	return instance, nil
}

// isStateMarker reports whether v is a secret marker of the masked read: an
// object with the secret flag. The plain state never holds one at a masked
// path; a real value with the same shape but no entry in the masked set is a
// real value and never reaches this check.
func isStateMarker(v any) bool {
	obj, ok := v.(map[string]any)
	if !ok {
		return false
	}

	flag, ok := obj[markerSecretKey].(bool)

	return ok && flag
}
