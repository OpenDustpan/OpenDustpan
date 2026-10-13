package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json/jsontext"
	"hash"
	"slices"
)

// Streaming Merkle digest of one JSON value. Later steps use it to compare
// a subtree with a kept secret without keeping the secret.

// digest is the hash of one JSON value.
type digest [sha256.Size]byte

// shape tells what the walk saw of one value.
type shape struct {
	kind jsontext.Kind
	size int
}

// propagable reports whether the value may carry a secret: a string, object
// or array whose size is above 0.
func (s shape) propagable() bool {
	switch s.kind {
	case jsontext.KindString, jsontext.KindBeginObject, jsontext.KindBeginArray:
		return s.size > 0
	case jsontext.KindInvalid, jsontext.KindNull, jsontext.KindFalse, jsontext.KindTrue,
		jsontext.KindNumber, jsontext.KindEndObject, jsontext.KindEndArray:
		return false
	}

	return false
}

// digestPair holds one object member: the hash of the key with the hash of
// the value.
type digestPair struct {
	key   digest
	value digest
}

// objectDigest collects one object member at a time, then sorts and hashes.
type objectDigest struct {
	pairs []digestPair
}

// add records one member with the unescaped key.
func (o *objectDigest) add(key string, value digest) {
	o.pairs = append(o.pairs, digestPair{key: keyDigest(key), value: value})
}

// sum hashes the collected members sorted by key hash.
func (o *objectDigest) sum() digest {
	slices.SortFunc(o.pairs, func(a, b digestPair) int {
		return bytes.Compare(a.key[:], b.key[:])
	})

	h := sha256.New()

	h.Write([]byte{'o'})

	var count [8]byte

	binary.BigEndian.PutUint64(count[:], uint64(len(o.pairs)))
	h.Write(count[:])

	for _, p := range o.pairs {
		h.Write(p.key[:])
		h.Write(p.value[:])
	}

	var out digest

	copy(out[:], h.Sum(nil))

	return out
}

// arrayDigest hashes elements as they stream, and keeps nothing else.
type arrayDigest struct {
	h hash.Hash
	n uint64
}

// newArrayDigest starts one array hash.
func newArrayDigest() *arrayDigest {
	h := sha256.New()
	h.Write([]byte{'a'})

	return &arrayDigest{h: h, n: 0}
}

// add feeds one element hash.
func (a *arrayDigest) add(value digest) {
	a.h.Write(value[:])
	a.n++
}

// sum closes the array hash with the element count.
func (a *arrayDigest) sum() digest {
	var count [8]byte

	binary.BigEndian.PutUint64(count[:], a.n)
	a.h.Write(count[:])

	var out digest

	copy(out[:], a.h.Sum(nil))

	return out
}

// keyDigest hashes one unescaped object key.
func keyDigest(key string) digest {
	return digestScalar('k', []byte(key))
}

// digestScalar hashes one scalar value with its tag and byte length.
func digestScalar(tag byte, data []byte) digest {
	h := sha256.New()
	h.Write([]byte{tag})

	var length [8]byte

	binary.BigEndian.PutUint64(length[:], uint64(len(data)))
	h.Write(length[:])
	h.Write(data)

	var out digest

	copy(out[:], h.Sum(nil))

	return out
}

// digestRaw hashes one JSON value in raw.
func digestRaw(raw []byte) (digest, shape, error) {
	return digestStreamed(jsontext.NewDecoder(bytes.NewReader(raw)))
}

// digestStreamed consumes exactly one value from dec and hashes it. Errors
// wrap as decode failures.
func digestStreamed(dec *jsontext.Decoder) (digest, shape, error) {
	switch dec.PeekKind() {
	case jsontext.KindString:
		return digestStreamedString(dec)
	case jsontext.KindNumber:
		return digestStreamedNumber(dec)
	case jsontext.KindTrue, jsontext.KindFalse, jsontext.KindNull:
		return digestStreamedLiteral(dec)
	case jsontext.KindBeginArray:
		return digestStreamedArray(dec)
	case jsontext.KindBeginObject:
		return digestStreamedObject(dec)
	case jsontext.KindInvalid, jsontext.KindEndObject, jsontext.KindEndArray:
		if _, err := dec.ReadToken(); err != nil {
			return digest{}, shape{}, decodeStateFailed(err)
		}

		return digest{}, shape{}, decodeStateFailed(errUnexpectedValue)
	}

	return digest{}, shape{}, decodeStateFailed(errUnexpectedValue)
}

// digestStreamedString hashes one string value with its unescaped text.
func digestStreamedString(dec *jsontext.Decoder) (digest, shape, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return digest{}, shape{}, decodeStateFailed(err)
	}

	if tok.Kind() != jsontext.KindString {
		return digest{}, shape{}, decodeStateFailed(errUnexpectedValue)
	}

	text := tok.String()

	return digestScalar('s', []byte(text)), shape{kind: jsontext.KindString, size: len(text)}, nil
}

// digestStreamedNumber hashes one number value with its literal text exactly
// as written.
func digestStreamedNumber(dec *jsontext.Decoder) (digest, shape, error) {
	raw, err := dec.ReadValue()
	if err != nil {
		return digest{}, shape{}, decodeStateFailed(err)
	}

	literal := bytes.TrimSpace([]byte(raw))

	return digestScalar('n', literal), shape{kind: jsontext.KindNumber, size: 0}, nil
}

// digestStreamedLiteral hashes one true, false or null value.
func digestStreamedLiteral(dec *jsontext.Decoder) (digest, shape, error) {
	kind := dec.PeekKind()

	if _, err := dec.ReadToken(); err != nil {
		return digest{}, shape{}, decodeStateFailed(err)
	}

	if kind == jsontext.KindTrue {
		return digestScalar('t', nil), shape{kind: jsontext.KindTrue, size: 0}, nil
	}

	if kind == jsontext.KindFalse {
		return digestScalar('f', nil), shape{kind: jsontext.KindFalse, size: 0}, nil
	}

	if kind == jsontext.KindNull {
		return digestScalar('z', nil), shape{kind: jsontext.KindNull, size: 0}, nil
	}

	return digest{}, shape{}, decodeStateFailed(errUnexpectedValue)
}

// digestStreamedArray hashes one array value as its elements stream.
func digestStreamedArray(dec *jsontext.Decoder) (digest, shape, error) {
	if _, err := dec.ReadToken(); err != nil {
		return digest{}, shape{}, decodeStateFailed(err)
	}

	acc := newArrayDigest()

	size := 0

	for {
		if dec.PeekKind() == jsontext.KindEndArray {
			if _, err := dec.ReadToken(); err != nil {
				return digest{}, shape{}, decodeStateFailed(err)
			}

			break
		}

		elem, _, err := digestStreamed(dec)
		if err != nil {
			return digest{}, shape{}, err
		}

		acc.add(elem)

		size++
	}

	return acc.sum(), shape{kind: jsontext.KindBeginArray, size: size}, nil
}

// digestStreamedObject hashes one object value, and keeps one hash pair for
// each member of the open object only.
func digestStreamedObject(dec *jsontext.Decoder) (digest, shape, error) {
	if _, err := dec.ReadToken(); err != nil {
		return digest{}, shape{}, decodeStateFailed(err)
	}

	var acc objectDigest

	for {
		if dec.PeekKind() == jsontext.KindEndObject {
			if _, err := dec.ReadToken(); err != nil {
				return digest{}, shape{}, decodeStateFailed(err)
			}

			break
		}

		tok, err := dec.ReadToken()
		if err != nil {
			return digest{}, shape{}, decodeStateFailed(err)
		}

		if tok.Kind() != jsontext.KindString {
			return digest{}, shape{}, decodeStateFailed(errUnexpectedValue)
		}

		key := tok.String()

		value, _, err := digestStreamed(dec)
		if err != nil {
			return digest{}, shape{}, err
		}

		acc.add(key, value)
	}

	return acc.sum(), shape{kind: jsontext.KindBeginObject, size: len(acc.pairs)}, nil
}
