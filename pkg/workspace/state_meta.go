package workspace

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
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

// readStateMeta reads the top-level members of the state in r, one value at a
// time, so it never holds the whole document. It skips every member that it does
// not need. An outputs value above maxOutputsBytes fails with ErrTooLarge. A
// document that is not a JSON object fails with ErrInvalidState.
func readStateMeta(r io.Reader) (streamedMeta, error) {
	var meta streamedMeta

	dec := jsontext.NewDecoder(r)

	if err := expectObjectStart(dec); err != nil {
		return streamedMeta{}, err
	}

	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return streamedMeta{}, fmt.Errorf("%w: read member of state: %w", ErrInvalidState, err)
		}

		if tok.Kind() == '}' {
			return meta, nil
		}

		if tok.Kind() != '"' {
			return streamedMeta{}, fmt.Errorf("%w: state member name", ErrInvalidState)
		}

		if err := readMember(dec, tok.String(), &meta); err != nil {
			return streamedMeta{}, err
		}
	}
}

// expectObjectStart reads the opening brace of the state document.
func expectObjectStart(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil || tok.Kind() != '{' {
		return fmt.Errorf("%w: state is not a JSON object", ErrInvalidState)
	}

	return nil
}

// readMember reads the value of one top-level member name into meta, or skips
// it. The client-encryption marker sets meta and skips its value like any other
// member.
func readMember(dec *jsontext.Decoder, name string, meta *streamedMeta) error {
	if name == encryptedStateKey {
		meta.ClientEncrypted = true
	}

	switch name {
	case "serial":
		return readSerial(dec, meta)
	case "lineage":
		return readLineage(dec, meta)
	case "outputs":
		return readOutputs(dec, meta)
	default:
		return skipMember(dec, name)
	}
}

// readSerial reads the serial member into meta.
func readSerial(dec *jsontext.Decoder, meta *streamedMeta) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return fmt.Errorf("%w: serial: %w", ErrInvalidState, err)
	}

	if err := json.Unmarshal(raw, &meta.Serial); err != nil {
		return fmt.Errorf("%w: serial: %w", ErrInvalidState, err)
	}

	meta.HasSerial = true

	return nil
}

// readLineage reads the lineage member into meta.
func readLineage(dec *jsontext.Decoder, meta *streamedMeta) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return fmt.Errorf("%w: lineage: %w", ErrInvalidState, err)
	}

	if err := json.Unmarshal(raw, &meta.Lineage); err != nil {
		return fmt.Errorf("%w: lineage: %w", ErrInvalidState, err)
	}

	meta.HasLineage = true

	return nil
}

// readOutputs reads the outputs member into meta. A null value leaves no
// outputs. An outputs value above maxOutputsBytes fails with ErrTooLarge.
func readOutputs(dec *jsontext.Decoder, meta *streamedMeta) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return fmt.Errorf("%w: outputs: %w", ErrInvalidState, err)
	}

	if len(raw) > maxOutputsBytes {
		return fmt.Errorf("%w: outputs of %d bytes, limit %d", ErrTooLarge, len(raw), maxOutputsBytes)
	}

	if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		meta.Outputs = bytes.Clone(raw)
	}

	return nil
}

// skipMember skips the value of a member that the domain does not need.
func skipMember(dec *jsontext.Decoder, name string) error {
	if err := dec.SkipValue(); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalidState, name, err)
	}

	return nil
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
		meta, err := readStateMeta(pr)
		if err != nil {
			_ = pr.CloseWithError(err)
		} else if _, err = io.Copy(io.Discard, pr); err != nil {
			_ = pr.CloseWithError(err)
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
