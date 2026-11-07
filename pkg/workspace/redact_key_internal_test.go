package workspace

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
)

// errStoreDown is a store failure that names no key.
var errStoreDown = errors.New("store down")

// TestRedactKeyWithTheFilesystemStore proves that the text of a missing-blob
// error of the real fs store does not carry the blob key once redactKey runs,
// and that blob.ErrNotFound still matches. The test first checks that the fs
// store does name the key in its text, so the redaction is tested on a real
// case.
func TestRedactKeyWithTheFilesystemStore(t *testing.T) {
	t.Parallel()

	store, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open the fs store: %v", err)
	}

	key := blob.Key("namespaces/ns-acmeacmeacmeacmeacme/tofu/ws-1/sv-1/a1")

	_, getErr := store.Get(t.Context(), key)
	if !errors.Is(getErr, blob.ErrNotFound) {
		t.Fatalf("get of a missing blob: err = %v, want blob.ErrNotFound", getErr)
	}

	if !strings.Contains(getErr.Error(), key.String()) {
		t.Fatalf("the fs store error %q does not name the key, so the test proves nothing", getErr)
	}

	got := redactKey(getErr, key)

	if strings.Contains(got.Error(), key.String()) {
		t.Errorf("the redacted error %q still names the key", got)
	}

	if !errors.Is(got, blob.ErrNotFound) {
		t.Errorf("the redacted error %q does not match blob.ErrNotFound", got)
	}
}

// TestRedactKeyKeepsErrorsThatNameNoKey proves that redactKey returns an error
// unchanged when it has no key, when the key is empty, and when the error is nil.
func TestRedactKeyKeepsErrorsThatNameNoKey(t *testing.T) {
	t.Parallel()

	plain := errStoreDown

	tests := []struct {
		name string
		err  error
		key  blob.Key
	}{
		{"nil error", nil, "namespaces/ns-x/tofu/ws-1/sv-1/a1"},
		{"empty key", plain, ""},
		{"error without the key", plain, "namespaces/ns-x/tofu/ws-1/sv-1/a1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := redactKey(tt.err, tt.key); !errors.Is(got, tt.err) {
				t.Fatalf("redactKey() = %v, want the error unchanged", got)
			}
		})
	}
}
