package secret

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

var errWrapperDown = errors.New("wrapper failed")

// recordingWrapper wraps by prefixing its ID and the AAD, and counts the
// calls to Unwrap.
type recordingWrapper struct {
	id      string
	fail    bool
	unwraps int
}

func (w *recordingWrapper) ID() string { return w.id }

func (w *recordingWrapper) Wrap(_ context.Context, key, aad []byte) ([]byte, error) {
	if w.fail {
		return nil, errWrapperDown
	}

	return append(append([]byte(w.id+"|"), aad...), append([]byte("|"), key...)...), nil
}

func (w *recordingWrapper) Unwrap(_ context.Context, ciphertext, aad []byte) ([]byte, error) {
	w.unwraps++

	prefix := append([]byte(w.id+"|"), append(aad, '|')...)
	if w.fail || !bytes.HasPrefix(ciphertext, prefix) {
		return nil, ErrDecrypt
	}

	return bytes.Clone(ciphertext[len(prefix):]), nil
}

func newRecordingWrapper(fail bool) *recordingWrapper {
	var w recordingWrapper

	w.id, w.fail = "kek-1", fail

	return &w
}

// TestEnvelopeRoundTrip checks that a new key unwraps to a key of the data
// key size under the same AAD.
func TestEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()

	w := newRecordingWrapper(false)

	wrapped, err := wrapNewKey(t.Context(), w, []byte("aad"), "key for test")
	if err != nil {
		t.Fatalf("wrapNewKey: %v", err)
	}

	key, err := unwrapKey(
		t.Context(),
		w,
		sealedKey{wrapperID: "kek-1", ciphertext: wrapped},
		[]byte("aad"),
		"key for test",
	)
	if err != nil {
		t.Fatalf("unwrapKey: %v", err)
	}

	if len(key) != dataKeyBytes {
		t.Fatalf("key has %d bytes, want %d", len(key), dataKeyBytes)
	}
}

// TestWrapNewKeyRefuses checks that wrapNewKey refuses a nil wrapper and
// returns the error of a failed wrap.
func TestWrapNewKeyRefuses(t *testing.T) {
	t.Parallel()

	aad := []byte("aad")

	_, err := wrapNewKey(t.Context(), nil, aad, "key for test")
	if !errors.Is(err, ErrKeyNotConfigured) {
		t.Fatalf("wrapNewKey with no wrapper = %v, want ErrKeyNotConfigured", err)
	}

	_, err = wrapNewKey(t.Context(), newRecordingWrapper(true), aad, "key for test")
	if !errors.Is(err, errWrapperDown) {
		t.Fatalf("wrapNewKey with a failing wrapper = %v, want its error", err)
	}
}

// TestUnwrapKeyRefuses sends each hostile stored key straight to unwrapKey,
// as if the caller had not checked it.
func TestUnwrapKeyRefuses(t *testing.T) {
	t.Parallel()

	aad := []byte("aad")
	good := newRecordingWrapper(false)

	wrapped, err := wrapNewKey(t.Context(), good, aad, "key for test")
	if err != nil {
		t.Fatalf("wrapNewKey: %v", err)
	}

	_, err = unwrapKey(t.Context(), nil, sealedKey{wrapperID: "kek-1", ciphertext: wrapped}, aad, "key")
	if !errors.Is(err, ErrKeyNotConfigured) || !errors.Is(err, ErrDecrypt) {
		t.Fatalf("unwrapKey with no wrapper = %v, want ErrDecrypt and ErrKeyNotConfigured", err)
	}

	other := newRecordingWrapper(false)

	_, err = unwrapKey(t.Context(), other, sealedKey{wrapperID: "kek-2", ciphertext: wrapped}, aad, "key")
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("unwrapKey of a key of another wrapper = %v, want ErrDecrypt", err)
	}

	if other.unwraps != 0 {
		t.Fatalf("unwrapKey called Unwrap %d times for a key of another wrapper, want 0", other.unwraps)
	}

	_, err = unwrapKey(
		t.Context(),
		good,
		sealedKey{wrapperID: "kek-1", ciphertext: wrapped},
		[]byte("other aad"),
		"key",
	)
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("unwrapKey under another AAD = %v, want ErrDecrypt", err)
	}
}
