package workspace_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestOpenStateBlobRefusesSwappedVersionBlob proves the sealed bytes of one
// state version of a workspace do not open as another version of the same
// workspace. The bytes of version A are planted under a key that names
// version B and another attempt: the key passes the segment check, so the
// open reaches the stream, which is bound to version B and fails there.
func TestOpenStateBlobRefusesSwappedVersionBlob(t *testing.T) {
	t.Parallel()

	f := newSealedFixture(t)
	ctx := t.Context()

	contentA := `{"version":4,"outputs":{"secret":{"sensitive":true,"value":"state-a-value"}}}`
	contentB := `{"version":4,"outputs":{"secret":{"sensitive":true,"value":"state-b-value"}}}`

	svA := uploadSealedStateAtSerial(t, f, contentA, "out-a", 1)
	svB := uploadSealedStateAtSerial(t, f, contentB, "out-b", 2)

	sealedA, readErr := readSealedBlobBytes(t, f, svA)
	if readErr != nil {
		t.Fatalf("read sealed blob of version a: %v", readErr)
	}

	parts := strings.Split(svB.Content.String(), "/")
	if len(parts) != 4 {
		t.Fatalf("content key = %q, want tofu/<workspace>/<version>/<attempt>", svB.Content)
	}

	planted, keyErr := blob.ParseKey("tofu/" + parts[1] + "/" + parts[2] + "/swapped")
	if keyErr != nil {
		t.Fatalf("planted key: %v", keyErr)
	}

	if putErr := f.blobs.Put(ctx, planted, bytes.NewReader(sealedA)); putErr != nil {
		t.Fatalf("put planted blob: %v", putErr)
	}

	swapped, openErr := f.svc.OpenStateBlob(ctx, planted)
	if openErr != nil {
		t.Fatalf("open planted key: %v", openErr)
	}

	got, readSwapErr := io.ReadAll(swapped)
	closeErr := swapped.Close()

	if !errors.Is(readSwapErr, workspace.ErrSealedContent) {
		t.Fatalf("swapped blob read err = %v, want ErrSealedContent", readSwapErr)
	}

	if closeErr != nil {
		t.Errorf("close swapped reader: %v", closeErr)
	}

	if bytes.Contains(got, []byte("state-a-value")) {
		t.Fatal("swapped blob released the plain content of version a")
	}

	assertVersionKeyOpens(t, f, svB, contentB)
}

// uploadSealedStateAtSerial uploads a sealed state of the fixture workspace at
// serial, after the lock of an earlier upload is released. The workspace
// takes one serial per state, so a second version needs a newer serial.
func uploadSealedStateAtSerial(
	t *testing.T, f fixture, content, outputValue string, serial int64,
) workspace.StateVersion {
	t.Helper()

	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", workspace.NewState{
		Lineage: "l1",
		MD5:     sum(content),
		Outputs: sealedOutputsDoc(outputValue),
		Serial:  serial,
		Force:   false,
	}, authority(forcedOnly(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	sv, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if _, unlockErr := f.svc.Unlock(ctx, f.ws.ID, "alice"); unlockErr != nil {
		t.Fatalf("unlock: %v", unlockErr)
	}

	return sv
}
