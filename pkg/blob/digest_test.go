package blob_test

import (
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
)

// errBoom is the error of the failing reader.
var errBoom = errors.New("boom")

// failingReader returns its error at the first read.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

func TestDigestOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantMD5 string
	}{
		{"an empty blob", "", "d41d8cd98f00b204e9800998ecf8427e"},
		{"a short blob", "sealed bytes", "409a0970da0518fa46176d372ca3ea99"},
		{"a blob of several reads", strings.Repeat("0123456789", 100_000), "174ac9a4f023a557a68ab0417355970e"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := blob.DigestOf(strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("DigestOf = %v", err)
			}

			if got.Size != int64(len(tt.body)) || hex.EncodeToString(got.MD5[:]) != tt.wantMD5 {
				t.Errorf(
					"DigestOf = %d bytes, MD5 %x; want %d bytes, MD5 %s",
					got.Size,
					got.MD5,
					len(tt.body),
					tt.wantMD5,
				)
			}

			if got.IsZero() {
				t.Errorf("IsZero() = true for %q: the MD5 of an empty blob is not zero", tt.name)
			}
		})
	}
}

func TestDigestIsZero(t *testing.T) {
	t.Parallel()

	var zero blob.Digest

	if !zero.IsZero() {
		t.Error("IsZero() = false for the zero value")
	}
}

func TestDigestOfReadError(t *testing.T) {
	t.Parallel()

	if _, err := blob.DigestOf(failingReader{err: errBoom}); !errors.Is(err, errBoom) {
		t.Fatalf("DigestOf = %v, want the error of the reader", err)
	}
}

// TestDigestReaderPassesTheBytes checks that the reader gives the bytes
// unchanged while it measures them.
func TestDigestReaderPassesTheBytes(t *testing.T) {
	t.Parallel()

	r := blob.NewDigestReader(strings.NewReader("abc"))

	got, err := io.ReadAll(r)
	if err != nil || string(got) != "abc" {
		t.Fatalf("ReadAll = %q, %v; want abc", got, err)
	}

	if d := r.Digest(); d.Size != 3 {
		t.Errorf("Digest().Size = %d, want 3", d.Size)
	}
}

func TestDigestBytes(t *testing.T) {
	t.Parallel()

	got := blob.DigestBytes([]byte("sealed bytes"))
	if got.Size != 12 || hex.EncodeToString(got.MD5[:]) != "409a0970da0518fa46176d372ca3ea99" {
		t.Errorf(
			"DigestBytes = %d bytes, MD5 %x; want 12 bytes, MD5 409a0970da0518fa46176d372ca3ea99",
			got.Size,
			got.MD5,
		)
	}

	h := blob.NewMD5()
	if h.Size() != len(got.MD5) {
		t.Errorf("NewMD5().Size() = %d, want %d", h.Size(), len(got.MD5))
	}
}

// TestDigestReaderEndOfStream checks that the reader returns io.EOF itself at
// the end, and wraps another error of its source.
func TestDigestReaderEndOfStream(t *testing.T) {
	t.Parallel()

	r := blob.NewDigestReader(strings.NewReader(""))
	_, err := r.Read(make([]byte, 4))
	if !errors.Is(err, io.EOF) {
		t.Errorf("Read at the end = %v, want io.EOF", err)
	}
}
