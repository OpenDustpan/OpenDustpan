package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
)

// limitStore serves one manifest object through the reader that the test
// made. The test reads the unread bytes of that reader after the call, so it
// can prove that the manifest read stopped at its bound.
type limitStore struct {
	*mapStore

	reader *bytes.Reader
}

func (s *limitStore) Get(context.Context, blob.Key) (io.ReadCloser, error) {
	return io.NopCloser(s.reader), nil
}

// manifestReadCase is one case of TestReadManifestLimitBoundsTheRead.
type manifestReadCase struct {
	name     string
	limit    int64
	object   []byte
	wantErr  bool
	wantRead int
}

// TestReadManifestLimitBoundsTheRead proves that the manifest reader accepts
// an object of exactly the limit, refuses an object of one byte more with
// ErrCorrupt, and reads at most limit+1 bytes of a much larger object.
func TestReadManifestLimitBoundsTheRead(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(testManifest(t, nil, nil))
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}

	larger := append(bytes.Clone(data), bytes.Repeat([]byte(" "), 4096)...)

	for _, tc := range []manifestReadCase{
		{
			name:     "exactly at the limit",
			limit:    int64(len(data)),
			object:   data,
			wantErr:  false,
			wantRead: len(data),
		},
		{
			name:     "one byte over the limit",
			limit:    int64(len(data)) - 1,
			object:   data,
			wantErr:  true,
			wantRead: len(data),
		},
		{
			name:     "far over the limit",
			limit:    int64(len(data)) / 2,
			object:   larger,
			wantErr:  true,
			wantRead: len(data)/2 + 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkManifestReadLimit(t, tc)
		})
	}
}

// checkManifestReadLimit runs one case against readManifestLimit and checks
// the answer and the bytes that the read took.
func checkManifestReadLimit(t *testing.T, tc manifestReadCase) {
	t.Helper()

	reader := bytes.NewReader(tc.object)
	target := &limitStore{mapStore: newMapStore(false, nil), reader: reader}

	manifest, err := readManifestLimit(t.Context(), target, preBackupID, tc.limit)

	if tc.wantErr && !errors.Is(err, ErrCorrupt) {
		t.Fatalf("readManifestLimit = %v, want ErrCorrupt", err)
	}

	if !tc.wantErr && err != nil {
		t.Fatalf("readManifestLimit = %v, want nil", err)
	}

	if !tc.wantErr && manifest.ID != preBackupID {
		t.Errorf("manifest ID = %q, want %q", manifest.ID, preBackupID)
	}

	if read := len(tc.object) - reader.Len(); read != tc.wantRead {
		t.Errorf("the reader handed out %d bytes, want %d", read, tc.wantRead)
	}
}
