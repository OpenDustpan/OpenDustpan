package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sync"
)

// MaskText is the text that replaces a secret value in the log of a step.
const MaskText = "[masked]"

// MinMaskLen is the length of the shortest value that a masker hides. A
// shorter value would hide common text.
const MinMaskLen = 4

// Limits of a masker. The bounds keep each buffer of the masker small and
// its size computable without overflow.
const (
	// MaxMaskValues is the most secret values that one masker holds.
	MaxMaskValues = 256
	// MaxMaskLen is the longest secret value that a masker holds.
	MaxMaskLen = 64 << 10
	// maskPiece is the most bytes of a write that the masker handles at a
	// time.
	maskPiece = 64 << 10
)

var (
	// ErrMaskerClosed reports a write to a masker after Close.
	ErrMaskerClosed = errors.New("the log masker is closed")
	// ErrMaskerFull reports a secret value that a masker cannot hold: too
	// long, or one value too many. The step fails before it starts.
	ErrMaskerFull = errors.New("the log masker cannot hold the secret value")
)

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
// It returns an error that wraps ErrMaskerFull, and adds nothing, for a
// value longer than MaxMaskLen or a value above MaxMaskValues.
func (m *StreamMasker) Add(value []byte) error {
	if len(value) < MinMaskLen {
		return nil
	}

	if len(value) > MaxMaskLen {
		return fmt.Errorf("%w: a value has more than %d bytes", ErrMaskerFull, MaxMaskLen)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if slices.ContainsFunc(m.secrets, func(s []byte) bool { return bytes.Equal(s, value) }) {
		return nil
	}

	if len(m.secrets) >= MaxMaskValues {
		return fmt.Errorf("%w: more than %d values", ErrMaskerFull, MaxMaskValues)
	}

	m.secrets = append(m.secrets, bytes.Clone(value))
	// The longest value is tried first, so a value that holds a shorter
	// value is hidden as one.
	slices.SortFunc(m.secrets, func(a, b []byte) int { return len(b) - len(a) })

	return nil
}

// Write masks p and writes it, holding back the end of p that could start a
// secret value. It returns len(p) when the write succeeds.
func (m *StreamMasker) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, ErrMaskerClosed
	}

	for done := 0; done < len(p); {
		piece := p[done:min(len(p), done+maskPiece)]
		if err := m.writePiece(piece); err != nil {
			return done, err
		}

		done += len(piece)
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

// errMaskSize reports a buffer whose size does not fit an int.
var errMaskSize = errors.New("the log masker buffer is too large")

// writePiece masks one piece of at most maskPiece bytes. The caller holds
// mu.
func (m *StreamMasker) writePiece(piece []byte) error {
	// The held-back part is shorter than the longest value (MaxMaskLen),
	// and a piece has at most maskPiece bytes, so the sum fits. The check
	// stays so that the size never overflows.
	if len(piece) > maskPiece || len(m.pending) > math.MaxInt-maskPiece {
		return errMaskSize
	}

	buf := make([]byte, 0, len(m.pending)+len(piece))
	buf = append(buf, m.pending...)
	buf = append(buf, piece...)

	out, keep := m.mask(buf)
	m.pending = bytes.Clone(buf[keep:])

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
