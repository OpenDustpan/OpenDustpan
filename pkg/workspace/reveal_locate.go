package workspace

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
)

// Pass 0 of the reveal lookup (ADR 41 design in #416): a streaming walk of
// the state to the target instance that collects only the identity fields of
// the resource, the index key, the sensitive list and the kind of the
// attributes value. It never decodes the attributes. Later passes answer the
// value: the no-marks pass walks the attributes on the stream, and the
// fallback decodes the instance in full.

const (
	// resourcesMemberName, instancesMemberName, attributesMemberName,
	// sensitiveMemberName and indexKeyMemberName name the members of the
	// streamed state the locate walk reads. They repeat the wire names, so
	// the lookup and the read agree.
	resourcesMemberName  = "resources"
	instancesMemberName  = "instances"
	attributesMemberName = "attributes"
	sensitiveMemberName  = "sensitive_attributes"
	indexKeyMemberName   = "index_key"
)

// instanceInfo is what pass 0 keeps of the target instance: the identity
// fields of the resource, the index key, the decoded sensitive list and the
// kind and raw size of the attributes value.
type instanceInfo struct {
	// fields holds the identity fields of the resource, as streamedResource
	// collects them.
	fields map[string]any
	// indexKey is the index key of the instance: a json.Number or a string,
	// else nil.
	indexKey any
	// sensitive holds the decoded sensitive_attributes (UseNumber), nil when
	// absent or not an array.
	sensitive []any
	// attributes is the kind of the "attributes" value, 0 (KindInvalid) when
	// absent.
	attributes jsontext.Kind
	// size counts the raw bytes of the instance object.
	size int64
}

// instanceVisitor reads one member value of the target instance object that
// dec streams. It reports whether it consumed the value; a member it does
// not consume is skipped. A visitor may return errStopWalk to end the walk
// early once a later pass already knows the state is well formed.
type instanceVisitor func(dec *jsontext.Decoder, name string) (consumed bool, err error)

// errStopWalk is the sentinel a visitor returns to end the walk early.
// locateInstance turns it into success.
var errStopWalk = errors.New("stop walk")

// walkResult accumulates one locate walk: the identity fields of the target
// resource, whether the target instance passed the identity check, and the
// raw byte range of the target instance object.
type walkResult struct {
	fields map[string]any
	found  bool
	start  int64
	end    int64
	ranged bool
}

// locateInstance walks the streamed state to the target instance, calling
// visit for each member of that instance object, and reports the identity
// fields of the resource. It walks the state exactly like revealStream does:
// every array and object runs to its end, so duplicate keys and bad JSON
// anywhere fail the lookup as they do today. found is true only when the
// instance exists and the resource has a non-empty string type and name, the
// same rule as revealFromResource. A visitor that returns errStopWalk ends
// the walk at once with found set: only passes that run after pass 0 has
// validated the whole state may return it.
//
//nolint:unparam // passes after pass 0 ignore the fields and found results; pass 0 validated them.
func locateInstance(
	dec *jsontext.Decoder, target revealTarget, visit instanceVisitor,
) (map[string]any, bool, error) {
	res := new(walkResult)
	res.fields = map[string]any{}

	if err := locateWalk(dec, target, visit, res); err != nil {
		return nil, false, err
	}

	return res.fields, res.found, nil
}

// locateWalk runs the locate walk and records the outcome in res, including
// the raw byte range of the target instance object. Pass 0 calls it directly
// so it can read that range into instanceInfo; locateInstance is the same
// walk without the range.
func locateWalk(
	dec *jsontext.Decoder, target revealTarget, visit instanceVisitor, res *walkResult,
) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return decodeStateFailed(err)
	}

	if tok.Kind() != jsontext.KindBeginObject {
		return nil
	}

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return err
		}

		if end {
			return nil
		}

		if err := locateTopMember(dec, target, name, visit, res); err != nil {
			if errors.Is(err, errStopWalk) {
				res.found = true

				return nil
			}

			return err
		}
	}
}

// locateTopMember reads the streamed top-level member name of the state. A
// client-encrypted state fails with ErrClientEncrypted wherever the envelope
// sits, as revealStreamedMember does.
func locateTopMember(
	dec *jsontext.Decoder, target revealTarget, name string, visit instanceVisitor, res *walkResult,
) error {
	switch name {
	case encryptedStateKey:
		return fmt.Errorf("reveal of a client-encrypted state: %w", ErrClientEncrypted)
	case resourcesMemberName:
		return locateResources(dec, target, visit, res)
	default:
		return skipStreamed(dec)
	}
}

// locateResources walks the streamed resources array that dec streams to the
// target resource, then drains the rest of the array.
func locateResources(
	dec *jsontext.Decoder, target revealTarget, visit instanceVisitor, res *walkResult,
) error {
	open, err := openStreamedArray(dec)
	if err != nil {
		return err
	}

	if !open {
		return nil
	}

	for index := 0; ; index++ {
		end, err := endOfStreamedArray(dec)
		if err != nil {
			return err
		}

		if end {
			return nil
		}

		if index != target.resource {
			if err := skipStreamed(dec); err != nil {
				return err
			}

			continue
		}

		if err := locateResource(dec, target, visit, res); err != nil {
			return err
		}

		return drainStreamedArray(dec)
	}
}

// locateResource walks the streamed resource object that dec streams to its
// end, collecting the identity fields and the target instance. Entries that
// are not objects are skipped, as streamedResource does.
func locateResource(
	dec *jsontext.Decoder, target revealTarget, visit instanceVisitor, res *walkResult,
) error {
	if dec.PeekKind() != jsontext.KindBeginObject {
		return skipStreamed(dec)
	}

	if _, err := dec.ReadToken(); err != nil {
		return decodeStateFailed(err)
	}

	instanceFound := false

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return err
		}

		if end {
			break
		}

		found, err := locateResourceMember(dec, target, name, visit, res)
		if err != nil {
			return err
		}

		if found {
			instanceFound = true
		}
	}

	// Entries that are not objects with a string type and name have no masked
	// path in the read, so they never answer here either.
	if instanceFound && fieldText(res.fields, typeFieldName) != "" &&
		fieldText(res.fields, nameFieldName) != "" {
		res.found = true
	}

	return nil
}

// locateResourceMember reads the streamed value of one resource member named
// name: an identity field lands in the result fields, the wanted instance is
// walked, and every other value is skipped. It reports whether the member
// held the wanted instance.
func locateResourceMember(
	dec *jsontext.Decoder, target revealTarget, name string, visit instanceVisitor, res *walkResult,
) (bool, error) {
	switch name {
	case modeFieldName, typeFieldName, nameFieldName, moduleFieldName:
		value, ok, err := streamedString(dec)
		if err != nil {
			return false, err
		}

		if ok {
			res.fields[name] = value
		}

		return false, nil
	case instancesMemberName:
		return locateInstances(dec, target, visit, res)
	default:
		return false, skipStreamed(dec)
	}
}

// locateInstances walks the streamed instances array that dec streams to the
// target.position-th object element, then drains the rest of the array.
// Entries that are not objects do not count toward the position, as
// revealFromInstances does. It reports whether the array holds the target
// instance.
func locateInstances(
	dec *jsontext.Decoder, target revealTarget, visit instanceVisitor, res *walkResult,
) (bool, error) {
	open, err := openStreamedArray(dec)
	if err != nil {
		return false, err
	}

	if !open {
		return false, nil
	}

	count := 0

	for {
		end, err := endOfStreamedArray(dec)
		if err != nil {
			return false, err
		}

		if end {
			return false, nil
		}

		next, found, elemErr := locateInstancesElement(dec, target, count, visit, res)
		if elemErr != nil {
			return false, elemErr
		}

		count = next

		if found {
			if drainErr := drainStreamedArray(dec); drainErr != nil {
				return false, drainErr
			}

			return true, nil
		}
	}
}

// locateInstancesElement reads one element of the streamed instances array
// that dec streams: the target.position-th object element is walked with the
// visitor, every other element is skipped. Entries that are not objects
// never count toward the position, as streamedInstanceAt does. It reports
// the next object count and whether the element held the target instance,
// whose raw byte range lands in res.
func locateInstancesElement(
	dec *jsontext.Decoder, target revealTarget, count int, visit instanceVisitor, res *walkResult,
) (int, bool, error) {
	if dec.PeekKind() != jsontext.KindBeginObject {
		if err := skipStreamed(dec); err != nil {
			return count, false, err
		}

		return count, false, nil
	}

	if count != target.position {
		if err := skipStreamed(dec); err != nil {
			return count, false, err
		}

		return count + 1, false, nil
	}

	res.start = dec.InputOffset()

	if _, err := dec.ReadToken(); err != nil {
		return count, false, decodeStateFailed(err)
	}

	if err := locateInstanceMembers(dec, visit); err != nil {
		return count, false, err
	}

	res.end = dec.InputOffset()
	res.ranged = true

	return count, true, nil
}

// locateInstanceMembers reads the members of the target instance object that
// dec streams, calling visit for each. A member the visitor does not consume
// is skipped.
func locateInstanceMembers(dec *jsontext.Decoder, visit instanceVisitor) error {
	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return err
		}

		if end {
			return nil
		}

		consumed, err := visit(dec, name)
		if err != nil {
			return err
		}

		if !consumed {
			if err := skipStreamed(dec); err != nil {
				return err
			}
		}
	}
}

// infoVisitor returns the pass 0 visitor, which collects the index key, the
// sensitive list and the kind of the attributes value into info. It never
// returns errStopWalk: pass 0 drains the whole state.
func infoVisitor(info *instanceInfo) instanceVisitor {
	return func(dec *jsontext.Decoder, name string) (bool, error) {
		switch name {
		case sensitiveMemberName:
			return true, infoSensitive(info, dec)
		case indexKeyMemberName:
			return true, infoIndexKey(info, dec)
		case attributesMemberName:
			return true, infoAttributes(info, dec)
		default:
			return false, nil
		}
	}
}

// infoSensitive reads the streamed sensitive_attributes value that dec
// streams into info, failing when it runs over the marks limit. A value that
// is not an array leaves no sensitive list.
func infoSensitive(info *instanceInfo, dec *jsontext.Decoder) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return decodeStateFailed(err)
	}

	if len(raw) > revealMarksLimit {
		return fmt.Errorf(
			"sensitive_attributes holds %d bytes, over the limit of %d bytes: %w",
			len(raw),
			revealMarksLimit,
			ErrTooLarge,
		)
	}

	sensitiveDec := json.NewDecoder(bytes.NewReader(raw))
	sensitiveDec.UseNumber()

	var sensitive []any

	if err := sensitiveDec.Decode(&sensitive); err != nil {
		sensitive = nil
	}

	info.sensitive = sensitive

	return nil
}

// infoIndexKey reads the streamed index_key value that dec streams into
// info: a string lands as a string, a number lands as the json.Number of its
// literal text, and any other value is skipped.
func infoIndexKey(info *instanceInfo, dec *jsontext.Decoder) error {
	kind := dec.PeekKind()
	if kind == jsontext.KindString {
		tok, err := dec.ReadToken()
		if err != nil {
			return decodeStateFailed(err)
		}

		info.indexKey = tok.String()

		return nil
	}

	if kind == jsontext.KindNumber {
		raw, err := dec.ReadValue()
		if err != nil {
			return decodeStateFailed(err)
		}

		info.indexKey = json.Number(string(raw))

		return nil
	}

	return skipStreamed(dec)
}

// infoAttributes records the kind of the streamed attributes value that dec
// streams into info, then skips the value.
func infoAttributes(info *instanceInfo, dec *jsontext.Decoder) error {
	info.attributes = dec.PeekKind()

	return skipStreamed(dec)
}
