package dustpan

import (
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errDatabaseDown is a read error that is not damage.
var errDatabaseDown = errors.New("database is down")

// TestStateDamageMapsOnlyContentErrors proves that the state opener of the
// backup marks as damage only sealed content that does not open and plain
// content that does not match its row. A missing blob, a row that cannot be
// read and any other error keep their class, so the backup before a restore
// stops at them.
func TestStateDamageMapsOnlyContentErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err    error
		damage bool
	}{
		{err: workspace.ErrSealedContent, damage: true},
		{err: workspace.ErrStoredContentMismatch, damage: true},
		{err: blob.ErrNotFound, damage: false},
		{err: workspace.ErrNotFound, damage: false},
		{err: blob.ErrOutOfScope, damage: false},
		{err: errDatabaseDown, damage: false},
	} {
		got := stateDamage(tc.err)
		if errors.Is(got, backup.ErrLiveBlobDamaged) != tc.damage || !errors.Is(got, tc.err) {
			t.Errorf("stateDamage(%v) = %v, want damage %v and the first error kept", tc.err, got, tc.damage)
		}
	}
}

// failingReader returns its bytes, then err.
type failingReader struct {
	data string
	err  error
	done bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}

	r.done = true

	return copy(p, r.data), nil
}

func (*failingReader) Close() error { return nil }

// TestDamageReaderMapsReadErrors proves that a read error of opened state is
// mapped as at the open, and that io.EOF stays io.EOF.
func TestDamageReaderMapsReadErrors(t *testing.T) {
	t.Parallel()

	_, err := io.ReadAll(
		damageReader{ReadCloser: &failingReader{data: "x", err: workspace.ErrSealedContent, done: false}},
	)
	if !errors.Is(err, backup.ErrLiveBlobDamaged) {
		t.Fatalf("read of sealed content that does not open = %v, want ErrLiveBlobDamaged", err)
	}

	data, err := io.ReadAll(damageReader{ReadCloser: &failingReader{data: "state", err: io.EOF, done: false}})
	if err != nil || !strings.EqualFold(string(data), "state") {
		t.Fatalf("read of whole state = %q, %v; want the bytes and no error", data, err)
	}

	_, err = io.ReadAll(damageReader{ReadCloser: &failingReader{data: "", err: blob.ErrNotFound, done: false}})
	if errors.Is(err, backup.ErrLiveBlobDamaged) {
		t.Fatalf("read of a missing blob = %v, want no damage", err)
	}
}
