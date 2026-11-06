package job

import (
	"crypto/sha256"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
)

// TestLogReaderKeepsTheHashError proves that a closed log whose content does
// not match its hash fails each read, not only the first one, so a caller
// that reads again never sees a clean end of the log.
func TestLogReaderKeepsTheHashError(t *testing.T) {
	t.Parallel()

	// A closed log with no chunk and a wrong hash: the first read reaches
	// the hash check at the end of the log, and no blob or sealer is used.
	log := new(LogRef)
	log.ID, log.Chunks, log.SHA256 = "jlv-test", 0, "00"

	var ref secret.ItemRef

	r := &logReader{
		ctx:    t.Context(),
		blobs:  nil,
		sealer: nil,
		ns:     "",
		job:    "",
		ref:    ref,
		log:    log,
		keys:   nil,
		cur:    nil,
		closer: nil,
		next:   0,
		sum:    sha256.New(),
		done:   false,
		check:  true,
		end:    nil,
	}

	buf := make([]byte, 8)

	for i := range 3 {
		if _, err := r.Read(buf); !errors.Is(err, secret.ErrDecrypt) {
			t.Fatalf("read %d = %v, want secret.ErrDecrypt", i+1, err)
		}
	}
}
