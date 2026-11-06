package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
)

// MaskText is the text that replaces a secret value in the log of a step.
const MaskText = "[masked]"

// MinMaskLen is the length of the shortest value that a masker hides. A
// shorter value would hide common text.
const MinMaskLen = 4

// ErrMaskerClosed reports a write to a masker after Close.
var ErrMaskerClosed = errors.New("the log masker is closed")

// StreamMasker is the Masker of the launcher: it hides each secret value in
// a stream of bytes, also when the value arrives in two writes. It holds
// back the end of a write that could start a secret value, and writes it on
// a later write or on Close. It is safe for concurrent use.
type StreamMasker struct {
	mu      sync.Mutex
	out     io.Writer
	secrets [][]byte
	pending []byte
	closed  bool
}

var _ Masker = (*StreamMasker)(nil)

// NewMasker returns a masker that writes the masked stream to out.
func NewMasker(out io.Writer) *StreamMasker {
	return &StreamMasker{mu: sync.Mutex{}, out: out, secrets: nil, pending: nil, closed: false}
}

// Add adds a secret value. A value shorter than MinMaskLen is not masked.
func (m *StreamMasker) Add(value []byte) {
	if len(value) < MinMaskLen {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if slices.ContainsFunc(m.secrets, func(s []byte) bool { return bytes.Equal(s, value) }) {
		return
	}

	m.secrets = append(m.secrets, bytes.Clone(value))
	// The longest value is tried first, so a value that holds a shorter
	// value is hidden as one.
	slices.SortFunc(m.secrets, func(a, b []byte) int { return len(b) - len(a) })
}

// Write masks p and writes it, holding back the end of p that could start a
// secret value. It returns len(p) when the write succeeds.
func (m *StreamMasker) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, ErrMaskerClosed
	}

	buf := make([]byte, 0, len(m.pending)+len(p))
	buf = append(buf, m.pending...)
	buf = append(buf, p...)

	out, keep := m.mask(buf)
	m.pending = bytes.Clone(buf[keep:])

	if len(out) > 0 {
		if _, err := m.out.Write(out); err != nil {
			return 0, fmt.Errorf("write the masked log: %w", err)
		}
	}

	return len(p), nil
}

// Close masks and writes what Write held back. A second Close does
// nothing.
func (m *StreamMasker) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}

	m.closed = true
	out := m.maskAll(m.pending)
	m.pending = nil

	if len(out) == 0 {
		return nil
	}

	if _, err := m.out.Write(out); err != nil {
		return fmt.Errorf("write the masked log: %w", err)
	}

	return nil
}

// mask returns the masked text of buf and the index from which it held
// back the end of buf that could start a secret value.
func (m *StreamMasker) mask(buf []byte) ([]byte, int) {
	out := make([]byte, 0, len(buf))

	for i := 0; i < len(buf); {
		if n := m.matchAt(buf[i:]); n > 0 {
			out = append(out, MaskText...)
			i += n

			continue
		}

		if m.couldStart(buf[i:]) {
			return out, i
		}

		out = append(out, buf[i])
		i++
	}

	return out, len(buf)
}

// maskAll returns the masked text of buf, and holds back nothing: no later
// write can complete a value.
func (m *StreamMasker) maskAll(buf []byte) []byte {
	out := make([]byte, 0, len(buf))

	for i := 0; i < len(buf); {
		if n := m.matchAt(buf[i:]); n > 0 {
			out = append(out, MaskText...)
			i += n

			continue
		}

		out = append(out, buf[i])
		i++
	}

	return out
}

// matchAt returns the length of the secret value at the start of b, or 0.
func (m *StreamMasker) matchAt(b []byte) int {
	for _, s := range m.secrets {
		if bytes.HasPrefix(b, s) {
			return len(s)
		}
	}

	return 0
}

// couldStart reports whether b, all of the rest of a write, is the start of
// a secret value that a later write could complete.
func (m *StreamMasker) couldStart(b []byte) bool {
	for _, s := range m.secrets {
		if len(b) < len(s) && bytes.HasPrefix(s, b) {
			return true
		}
	}

	return false
}
