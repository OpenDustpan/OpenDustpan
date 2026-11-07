package secret

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

// The vectors come from an independent Python run, not from this code.
const (
	katItemKeyAAD     = "32333a6f70656e6475737470616e2f6974656d2d6b65792f7631343a61636d65373a73636f70652d31343a706c616e373a706c6e2d6b6174363a696b2d6b6174"
	katItemContentAAD = "32373a6f70656e6475737470616e2f6974656d2d636f6e74656e742f7631343a61636d65373a73636f70652d31343a706c616e373a706c6e2d6b6174363a696b2d6b6174"
)

// katItemRef returns the item reference of the known-answer vectors.
func katItemRef(t *testing.T, item string) ItemRef {
	t.Helper()

	ref, err := NewItemRef(katScope(t), ItemKindPlan, item)
	if err != nil {
		t.Fatalf("item ref: %v", err)
	}

	return ref
}

// katWrapper returns a built-in wrapper over a fixed key.
func katWrapper(t *testing.T) *BuiltinWrapper {
	t.Helper()

	w, err := NewBuiltinWrapper(bytes.Repeat([]byte{0x5a}, builtinKeyBytes))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	return w
}

// TestItemAADKnownAnswer pins the associated data of a wrapped item key and
// of the content of an item. Each holds its own purpose label and the ID of
// the key record.
func TestItemAADKnownAnswer(t *testing.T) {
	t.Parallel()

	ref := katItemRef(t, "pln-kat")

	if got := itemKeyAAD(ref, "ik-kat"); !bytes.Equal(got, mustHex(t, katItemKeyAAD)) {
		t.Errorf("item key associated data = %x", got)
	}

	if got := (itemContent{ref: ref, id: "ik-kat"}).aad(); !bytes.Equal(got, mustHex(t, katItemContentAAD)) {
		t.Errorf("item content associated data = %x", got)
	}
}

// TestItemAADSplitFields sends hostile parts straight to the encoding, as if
// every check of the reference and of the record ID had failed: two inputs
// that move bytes from one part to the next must not give the same
// associated data.
func TestItemAADSplitFields(t *testing.T) {
	t.Parallel()

	scope := Scope{Namespace: "acme", ID: "scope-1"}

	cases := []struct {
		name       string
		ref1, ref2 ItemRef
		id1, id2   ItemKeyID
	}{
		{
			name: "item and record ID, zero byte",
			ref1: ItemRef{Scope: scope, Kind: ItemKindPlan, Item: "x\x00ik-1"}, id1: "ik-2",
			ref2: ItemRef{Scope: scope, Kind: ItemKindPlan, Item: "x"}, id2: "ik-1\x00ik-2",
		},
		{
			name: "item and record ID, colon and length",
			ref1: ItemRef{Scope: scope, Kind: ItemKindPlan, Item: "x4:ik-1"}, id1: "ik-2",
			ref2: ItemRef{Scope: scope, Kind: ItemKindPlan, Item: "x"}, id2: "4:ik-14:ik-2",
		},
		{
			name: "kind and item",
			ref1: ItemRef{Scope: scope, Kind: "plan\x00a", Item: "b"}, id1: "ik-1",
			ref2: ItemRef{Scope: scope, Kind: "plan", Item: "a\x00b"}, id2: "ik-1",
		},
		{
			name: "namespace and scope ID",
			ref1: ItemRef{Scope: Scope{Namespace: "acme\x00s", ID: "1"}, Kind: ItemKindPlan, Item: "p"}, id1: "ik-1",
			ref2: ItemRef{Scope: Scope{Namespace: "acme", ID: "s\x001"}, Kind: ItemKindPlan, Item: "p"}, id2: "ik-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if bytes.Equal(itemKeyAAD(tc.ref1, tc.id1), itemKeyAAD(tc.ref2, tc.id2)) {
				t.Error("two different item keys have the same associated data")
			}

			c1, c2 := itemContent{ref: tc.ref1, id: tc.id1}, itemContent{ref: tc.ref2, id: tc.id2}
			if bytes.Equal(c1.aad(), c2.aad()) {
				t.Error("two different item contents have the same associated data")
			}
		})
	}
}

// TestItemKeyWrapBindsRecord sends hostile associated data straight to the
// unwrap of an item key: the wrapped key of record ik-a must open only for
// the purpose, the item and the record ID that wrapped it.
func TestItemKeyWrapBindsRecord(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	w := katWrapper(t)
	ref := katItemRef(t, "pln-1")

	wrapped, err := wrapNewKey(ctx, w, itemKeyAAD(ref, "ik-a"), "item key")
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}

	cases := []struct {
		name    string
		aad     []byte
		wantErr error
	}{
		{name: "the record that wrapped it", aad: itemKeyAAD(ref, "ik-a"), wantErr: nil},
		{name: "another record ID", aad: itemKeyAAD(ref, "ik-b"), wantErr: ErrDecrypt},
		{name: "no record ID", aad: itemKeyAAD(ref, ""), wantErr: ErrDecrypt},
		{name: "another item", aad: itemKeyAAD(katItemRef(t, "pln-2"), "ik-a"), wantErr: ErrDecrypt},
		{name: "the content purpose", aad: itemContent{ref: ref, id: "ik-a"}.aad(), wantErr: ErrDecrypt},
		{name: "the stream key purpose", aad: []byte(streamKeyInfo), wantErr: ErrDecrypt},
		{name: "tampered", aad: append(itemKeyAAD(ref, "ik-a"), 'x'), wantErr: ErrDecrypt},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, err := unwrapKey(ctx, w, sealedKey{wrapperID: w.ID(), ciphertext: wrapped}, tc.aad, "item key")
			clear(key)

			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("unwrap err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// sealItemContent seals plain with key for the content source src.
func sealItemContent(t *testing.T, key []byte, src aadSource, plain []byte) []byte {
	t.Helper()

	r, err := sealStreamWith(key, src, []byte("bind-1"), bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	sealed, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read sealed: %v", err)
	}

	return sealed
}

// TestItemContentBindsRecord sends hostile associated data straight to the
// open of item content, with the right item key: content sealed under record
// ik-a must open only for the item and the record ID that sealed it.
func TestItemContentBindsRecord(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x11}, dataKeyBytes)
	ref := katItemRef(t, "pln-1")
	plain := bytes.Repeat([]byte("item content "), 9000)
	sealed := sealItemContent(t, key, itemContent{ref: ref, id: "ik-a"}, plain)

	cases := []struct {
		src     aadSource
		name    string
		wantErr error
	}{
		{name: "the record that sealed it", src: itemContent{ref: ref, id: "ik-a"}, wantErr: nil},
		{name: "another record ID", src: itemContent{ref: ref, id: "ik-b"}, wantErr: ErrDecrypt},
		{name: "no record ID", src: itemContent{ref: ref, id: ""}, wantErr: ErrDecrypt},
		{name: "another item", src: itemContent{ref: katItemRef(t, "pln-2"), id: "ik-a"}, wantErr: ErrDecrypt},
		{name: "the scope of the item", src: ref.Scope, wantErr: ErrDecrypt},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rd := openStreamWith(bytes.Clone(key), tc.src, []byte("bind-1"), bytes.NewReader(sealed), true)
			got, err := io.ReadAll(rd)

			if closeErr := rd.Close(); closeErr != nil {
				t.Fatalf("close: %v", closeErr)
			}

			if tc.wantErr == nil {
				if err != nil || !bytes.Equal(got, plain) {
					t.Fatalf("open = %d bytes, err %v; want the plaintext", len(got), err)
				}

				return
			}

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("open err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// closeCase is one case of TestOpenReaderClose.
type closeCase struct {
	name string
	// readFirst reads the stream to its end before Close.
	readFirst bool
	// closeTwice calls Close a second time.
	closeTwice bool
}

// TestOpenReaderClose proves that Close clears the raw key of a reader that
// was never read, and that a Read after Close fails with ErrStreamClosed.
func TestOpenReaderClose(t *testing.T) {
	t.Parallel()

	src := itemContent{ref: katItemRef(t, "pln-1"), id: "ik-a"}
	sealed := sealItemContent(t, bytes.Repeat([]byte{0x22}, dataKeyBytes), src, []byte("content"))

	cases := []closeCase{
		{name: "never read", readFirst: false, closeTwice: false},
		{name: "never read, closed twice", readFirst: false, closeTwice: true},
		{name: "read to the end", readFirst: true, closeTwice: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkClose(t, tc, src, sealed)
		})
	}
}

// checkClose runs one case of TestOpenReaderClose.
func checkClose(t *testing.T, tc closeCase, src itemContent, sealed []byte) {
	t.Helper()

	dek := bytes.Repeat([]byte{0x22}, dataKeyBytes)
	rd := openStreamWith(dek, src, []byte("bind-1"), bytes.NewReader(sealed), true)

	if tc.readFirst {
		if _, err := io.ReadAll(rd); err != nil {
			t.Fatalf("read: %v", err)
		}
	}

	closes := 1
	if tc.closeTwice {
		closes = 2
	}

	for range closes {
		if err := rd.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	if !bytes.Equal(dek, make([]byte, dataKeyBytes)) {
		t.Errorf("key after Close = %x, want zeros", dek)
	}

	if n, err := rd.Read(make([]byte, 8)); n != 0 || !errors.Is(err, ErrStreamClosed) {
		t.Errorf("read after Close = %d, %v; want 0, ErrStreamClosed", n, err)
	}

	checkReleased(t, rd)
}

// checkReleased fails when a closed reader still holds its stream cipher or a
// buffer of sealed or plain bytes.
func checkReleased(t *testing.T, rd *openReader) {
	t.Helper()

	if rd.aead != nil || rd.pending != nil || rd.plainBuf != nil || rd.sealedBuf != nil {
		t.Errorf("closed reader holds aead %t, pending %d, plain %d, sealed %d bytes",
			rd.aead != nil, len(rd.pending), cap(rd.plainBuf), cap(rd.sealedBuf))
	}
}

// TestOpenReaderTakeKeyAfterClose sends a reader that is marked closed, but
// that still holds its key, straight to the read of the header, as if the
// check of Read and the clear of Close had failed: the header read must not
// use the key.
func TestOpenReaderTakeKeyAfterClose(t *testing.T) {
	t.Parallel()

	src := itemContent{ref: katItemRef(t, "pln-1"), id: "ik-a"}
	sealed := sealItemContent(t, bytes.Repeat([]byte{0x33}, dataKeyBytes), src, []byte("content"))

	rd := openStreamWith(bytes.Repeat([]byte{0x33}, dataKeyBytes), src, []byte("bind-1"), bytes.NewReader(sealed), true)
	rd.closed = true

	if err := rd.readHeader(); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("read header after Close err = %v, want ErrStreamClosed", err)
	}

	if rd.aead != nil {
		t.Fatal("read header after Close set up a stream key")
	}
}

// TestOpenReaderHeaderFailureClearsKey proves that a header that does not
// read clears the raw key at once, with no Close.
func TestOpenReaderHeaderFailureClearsKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		stream []byte
	}{
		{name: "empty", stream: nil},
		{name: "format byte only", stream: []byte{streamVersion}},
		{name: "cut salt", stream: append([]byte{streamVersion}, make([]byte, streamSaltSize-1)...)},
		{name: "unknown format", stream: append([]byte{0x7f}, make([]byte, streamSaltSize)...)},
		{name: "legacy format", stream: append([]byte{streamLegacyVersion}, make([]byte, streamLegacyNonceSize)...)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dek := bytes.Repeat([]byte{0x55}, dataKeyBytes)
			rd := openStreamWith(dek, katScope(t), []byte("bind-1"), bytes.NewReader(tc.stream), true)

			if _, err := io.ReadAll(rd); !errors.Is(err, ErrDecrypt) {
				t.Fatalf("read err = %v, want ErrDecrypt", err)
			}

			if !bytes.Equal(dek, make([]byte, dataKeyBytes)) {
				t.Errorf("key after a header failure = %x, want zeros", dek)
			}
		})
	}
}

// blockingReader blocks each Read until release is closed, then reads data.
type blockingReader struct {
	release chan struct{}
	data    []byte
}

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.release

	if len(b.data) == 0 {
		return 0, io.EOF
	}

	n := copy(p, b.data)
	b.data = b.data[n:]

	return n, nil
}

// gatedReader reads data, and stops before the byte at gate: it closes
// entered and waits for release.
type gatedReader struct {
	entered chan struct{}
	release chan struct{}
	data    []byte
	off     int
	gate    int
}

func (g *gatedReader) Read(p []byte) (int, error) {
	if g.off == g.gate {
		close(g.entered)
		<-g.release
	}

	if g.off >= len(g.data) {
		return 0, io.EOF
	}

	end := len(g.data)
	if g.off < g.gate {
		end = g.gate
	}

	n := copy(p, g.data[g.off:end])
	g.off += n

	return n, nil
}

// TestOpenReaderCloseDuringRead closes a reader while its Read waits for a
// frame, after the header set up the stream cipher. The Read that runs must
// release the cipher and the buffers when it returns.
func TestOpenReaderCloseDuringRead(t *testing.T) {
	t.Parallel()

	src := itemContent{ref: katItemRef(t, "pln-1"), id: "ik-a"}
	sealed := sealItemContent(t, bytes.Repeat([]byte{0x66}, dataKeyBytes), src, []byte("content"))

	gated := &gatedReader{
		entered: make(chan struct{}), release: make(chan struct{}),
		data: sealed, off: 0, gate: 1 + streamSaltSize,
	}
	rd := openStreamWith(bytes.Repeat([]byte{0x66}, dataKeyBytes), src, []byte("bind-1"), gated, true)

	done := make(chan error, 1)

	// One Read only: a later Read would release the state itself.
	go func() {
		_, err := rd.Read(make([]byte, 64))
		done <- err
	}()

	<-gated.entered

	if err := rd.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	close(gated.release)

	// The Read that ran finishes its chunk.
	if err := <-done; err != nil {
		t.Fatalf("read err = %v, want nil", err)
	}

	checkReleased(t, rd)

	if _, err := rd.Read(make([]byte, 64)); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("read after Close err = %v, want ErrStreamClosed", err)
	}
}

// TestOpenReaderConcurrentClose runs Close while a Read is in progress. The
// race detector checks the access to the key, and the key is cleared at the
// end whichever ran first.
func TestOpenReaderConcurrentClose(t *testing.T) {
	t.Parallel()

	src := itemContent{ref: katItemRef(t, "pln-1"), id: "ik-a"}
	sealed := sealItemContent(t, bytes.Repeat([]byte{0x44}, dataKeyBytes), src, []byte("content"))

	for range 20 {
		dek := bytes.Repeat([]byte{0x44}, dataKeyBytes)
		release := make(chan struct{})
		blocked := &blockingReader{release: release, data: sealed}
		rd := openStreamWith(dek, src, []byte("bind-1"), blocked, true)

		var (
			wg       sync.WaitGroup
			readErr  error
			closeErr error
		)

		wg.Go(func() {
			_, readErr = io.ReadAll(rd)
		})
		wg.Go(func() {
			closeErr = rd.Close()
		})

		close(release)
		wg.Wait()

		if closeErr != nil {
			t.Fatalf("close: %v", closeErr)
		}

		// The read ends with the plaintext when it ran first, and with
		// ErrStreamClosed when Close ran first.
		if readErr != nil && !errors.Is(readErr, ErrStreamClosed) {
			t.Fatalf("read err = %v, want nil or ErrStreamClosed", readErr)
		}

		if !bytes.Equal(dek, make([]byte, dataKeyBytes)) {
			t.Fatalf("key after a concurrent Close = %x, want zeros", dek)
		}

		checkReleased(t, rd)
	}
}
