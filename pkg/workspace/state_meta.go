package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// streamedMeta holds what the domain reads from a state while it streams in:
// the serial, the lineage, the top-level outputs and the client-encryption
// marker (ADR 49, decision 8). Each member is read wherever it sits in the
// document, so the key order of the client does not matter.
type streamedMeta struct {
	Lineage         string
	Outputs         []byte
	Serial          int64
	HasSerial       bool
	HasLineage      bool
	ClientEncrypted bool
}

// maxMemberNameBytes bounds a top-level member name. A longer name is refused.
const maxMemberNameBytes = 256

// maxLineageBytes bounds the raw bytes of the lineage value. The domain then
// checks the decoded length against maxLineageLength.
const maxLineageBytes = 1024

// maxSerialBytes bounds the raw bytes of the serial value.
const maxSerialBytes = 32

// readStateMeta reads the top-level members of the state in r. It checks the
// JSON grammar of the whole document, and it refuses data after the closing
// brace, so the state it accepts is the state OpenTofu reads. It buffers only the values
// the domain keeps, each within a limit. A skipped value is consumed byte by byte
// and never buffered, so a large string costs no memory. An outputs value above
// maxOutputsBytes fails with ErrTooLarge. A document that is not a JSON object
// fails with ErrInvalidState.
func readStateMeta(r io.Reader) (streamedMeta, error) {
	var meta streamedMeta

	s := newJSONScanner(r)

	if err := s.skipSpace(); err != nil {
		return streamedMeta{}, err
	}

	if b, err := s.peek(); err != nil {
		return streamedMeta{}, err
	} else if b != '{' {
		return streamedMeta{}, syntaxError("state is not a JSON object")
	}

	if err := s.topObject(&meta); err != nil {
		return streamedMeta{}, err
	}

	if err := s.end(); err != nil {
		return streamedMeta{}, err
	}

	return meta, nil
}

// topObject walks the members of the top-level object, and keeps the members the
// domain reads.
func (s *jsonScanner) topObject(meta *streamedMeta) error {
	// The top-level object is one level of nesting, as in encoding/json.
	if err := s.enter(); err != nil {
		return err
	}

	defer func() { s.depth-- }()

	if err := s.expect('{'); err != nil {
		return err
	}

	if err := s.skipSpace(); err != nil {
		return err
	}

	if b, err := s.peek(); err != nil {
		return err
	} else if b == '}' {
		_, err := s.read()

		return err
	}

	seen := make(map[string]bool, topLevelKeyCount)

	for {
		name, err := s.memberName()
		if err != nil {
			return err
		}

		if keyErr := checkTopLevelKey(name, seen); keyErr != nil {
			return keyErr
		}

		if topErr := s.topMember(name, meta); topErr != nil {
			return topErr
		}

		done, sepErr := s.separator('}')
		if sepErr != nil || done {
			return sepErr
		}
	}
}

// topLevelKeys are the top-level members the domain reads. encoding/json (v1)
// matches the struct fields of a state case-insensitively, so OpenTofu reads a
// key that folds to one of these as that member (ADR 49, decision 8).
// topLevelKeyCount is the number of keys in topLevelKeys.
const topLevelKeyCount = 4

func topLevelKeys() [topLevelKeyCount]string {
	return [topLevelKeyCount]string{"serial", "lineage", "outputs", encryptedStateKey}
}

// checkTopLevelKey refuses a key that folds to one of topLevelKeys without being
// it, and a second key that is one of them. Either would make this reader and
// OpenTofu disagree about the metadata of the state.
func checkTopLevelKey(name string, seen map[string]bool) error {
	for _, key := range topLevelKeys() {
		if name == key {
			if seen[key] {
				return syntaxError("duplicate member " + key)
			}

			seen[key] = true

			return nil
		}

		if strings.EqualFold(name, key) {
			return syntaxError("member " + name + " folds to " + key)
		}
	}

	return nil
}

// memberName reads one member name, bounded by maxMemberNameBytes.
func (s *jsonScanner) memberName() (string, error) {
	raw, err := s.keepValue(maxMemberNameBytes)
	if err != nil {
		if errors.Is(err, errCaptureFull) {
			return "", syntaxError("member name is too long")
		}

		return "", err
	}

	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return "", syntaxError("member name is not a string")
	}

	return name, nil
}

// topMember reads the colon and the value of one top-level member. The members
// the domain reads are kept, each within its limit. Every other member is
// skipped.
func (s *jsonScanner) topMember(name string, meta *streamedMeta) error {
	if err := s.skipSpace(); err != nil {
		return err
	}

	if err := s.expect(':'); err != nil {
		return err
	}

	if err := s.skipSpace(); err != nil {
		return err
	}

	switch name {
	case "serial":
		return keepSerial(s, meta)
	case "lineage":
		return keepLineage(s, meta)
	case "outputs":
		return keepOutputs(s, meta)
	case encryptedStateKey:
		meta.ClientEncrypted = true

		return s.value()
	default:
		return s.value()
	}
}

// keepSerial reads the serial value into meta.
func keepSerial(s *jsonScanner, meta *streamedMeta) error {
	raw, err := s.keepValue(maxSerialBytes)
	if err != nil {
		return boundedError(err, "serial")
	}

	if err := json.Unmarshal(raw, &meta.Serial); err != nil {
		return syntaxError("serial is not a number")
	}

	meta.HasSerial = true

	return nil
}

// keepLineage reads the lineage value into meta.
func keepLineage(s *jsonScanner, meta *streamedMeta) error {
	raw, err := s.keepValue(maxLineageBytes)
	if err != nil {
		return boundedError(err, "lineage")
	}

	if err := json.Unmarshal(raw, &meta.Lineage); err != nil {
		return syntaxError("lineage is not a string")
	}

	meta.HasLineage = true

	return nil
}

// keepOutputs reads the outputs value into meta. A null value leaves no outputs.
// An outputs value over maxOutputsBytes fails with ErrTooLarge, and the walk
// stops at that byte, so no more than the limit is ever held.
func keepOutputs(s *jsonScanner, meta *streamedMeta) error {
	raw, err := s.keepValue(maxOutputsBytes)
	if err != nil {
		if errors.Is(err, errCaptureFull) {
			return fmt.Errorf("%w: outputs of more than %d bytes", ErrTooLarge, maxOutputsBytes)
		}

		return err
	}

	if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		meta.Outputs = raw
	}

	return nil
}

// keepValue reads one value and returns its bytes. The value must fit in limit
// bytes: a longer value fails with errCaptureFull, and only limit bytes are held.
func (s *jsonScanner) keepValue(limit int) ([]byte, error) {
	keep := &capture{buf: nil, limit: limit}

	s.keep = keep
	err := s.value()
	s.keep = nil

	if err != nil {
		return nil, err
	}

	return keep.buf, nil
}

// boundedError maps a kept value that is too long to a grammar error of its
// member. Any other error passes through.
func boundedError(err error, member string) error {
	if errors.Is(err, errCaptureFull) {
		return syntaxError(member + " is too long")
	}

	return err
}

// metaResult is what the reader of a state stream returns.
type metaResult struct {
	err  error
	meta streamedMeta
}

// metaStream reads the meta of one state while its bytes pass through it. The
// bytes go to the store, and a copy goes to a decoder that runs beside it, so
// the document is read once. A failed store write or a failed decode closes
// the pipe with the error, so neither side waits for the other.
type metaStream struct {
	pipe *io.PipeWriter
	done chan metaResult
}

// newMetaStream returns the reader the store reads, and the stream that
// finishes the meta read. The caller must call finish once the store is done.
func newMetaStream(content io.Reader) (io.Reader, *metaStream) {
	pr, pw := io.Pipe()
	ms := &metaStream{pipe: pw, done: make(chan metaResult, 1)}

	go func() {
		// A document the decoder refuses still streams to the end, so the store
		// keeps its bytes. The caller decides whether the refusal counts.
		meta, err := readStateMeta(pr)
		if _, drainErr := io.Copy(io.Discard, pr); err == nil && drainErr != nil {
			err = drainErr
		}

		ms.done <- metaResult{err: err, meta: meta}
	}()

	return io.TeeReader(content, pw), ms
}

// finish ends the stream after the store is done. storeErr is the store's
// result: a store failure closes the pipe with it. It returns the meta, or the
// first error of the store or the decoder.
func (ms *metaStream) finish(storeErr error) (streamedMeta, error) {
	if storeErr != nil {
		_ = ms.pipe.CloseWithError(storeErr)
	} else {
		_ = ms.pipe.Close() //nolint:errcheck // io.PipeWriter.Close never fails.
	}

	res := <-ms.done

	if storeErr != nil {
		return streamedMeta{}, storeErr
	}

	if res.err != nil {
		return streamedMeta{}, res.err
	}

	return res.meta, nil
}
