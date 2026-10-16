//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// maxRefuserBody bounds the request body the refuser reads. A journal batch
// over the checkpoint limit answers 413 without reaching the server, so a
// body over this bound passes through untouched.
const maxRefuserBody = 64 << 20

// JournalRefuser corrupts one journal batch on the way to the server, so the
// server refuses it and records the refusal. It rewrites the first batch that
// holds only outputs entries (kind 4), which the Pulumi CLI sends last and
// ignores the error of. Any other batch passes through untouched, and later
// batches pass once the fault fired. It is safe for concurrent use.
type JournalRefuser struct {
	mu        sync.Mutex
	corrupted int
}

// Wrap returns next with the outputs-only batch rewritten, once. The rewrite
// adds an unknown member to the batch body, which the server strict-rejects.
func (r *JournalRefuser) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.rewriteFirstOutputsBatch(req)
		next.ServeHTTP(w, req)
	})
}

// Corrupted reports how many batches the refuser rewrote.
func (r *JournalRefuser) Corrupted() int {
	if r == nil {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.corrupted
}

// rewriteFirstOutputsBatch rewrites req when it is the first journal batch
// that holds only outputs entries. It reports whether it rewrote the body.
// The check and the count hold the lock, so concurrent batches rewrite at
// most one. Bodies are small, so reading under the lock stays cheap.
func (r *JournalRefuser) rewriteFirstOutputsBatch(req *http.Request) bool {
	if r == nil {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.corrupted > 0 || req.Method != http.MethodPatch ||
		!strings.HasSuffix(req.URL.Path, "/journalentries") {
		return false
	}

	limited, ok := refuserRawBody(req)
	if !ok {
		return false
	}

	// The read consumed the body: restore it, so a batch that passes
	// through untouched reaches the server as sent.
	req.Body = io.NopCloser(bytes.NewReader(limited))

	raw, gzipped := refuserPlainBody(req, limited)
	if !outputsOnly(raw) {
		return false
	}

	faulted := faultyBatch(raw)

	var out []byte

	if gzipped {
		var buf bytes.Buffer

		w := gzip.NewWriter(&buf)
		if _, err := w.Write(faulted); err != nil {
			return false
		}

		if err := w.Close(); err != nil {
			return false
		}

		out = buf.Bytes()
	} else {
		out = faulted
	}

	req.Body = io.NopCloser(bytes.NewReader(out))
	req.ContentLength = int64(len(out))
	r.corrupted++

	return true
}

// refuserRawBody reads the body of req within the bound, for inspection. It
// reports false when the body cannot be read. The caller restores req.Body,
// which the read consumed.
func refuserRawBody(req *http.Request) ([]byte, bool) {
	limited, err := io.ReadAll(io.LimitReader(req.Body, maxRefuserBody+1))
	if err != nil || int64(len(limited)) > maxRefuserBody {
		return nil, false
	}

	return limited, true
}

// refuserPlainBody returns the batch body of req decompressed, and whether it
// was gzipped. A body that does not decompress returns as sent, so only
// outputs-shaped JSON matches.
func refuserPlainBody(req *http.Request, limited []byte) ([]byte, bool) {
	gzipped := req.Header.Get("Content-Encoding") == "gzip"

	if !gzipped && len(limited) > 2 && limited[0] == 0x1f && limited[1] == 0x8b {
		gzipped = true
	}

	if !gzipped {
		return limited, false
	}

	zr, err := gzip.NewReader(bytes.NewReader(limited))
	if err != nil {
		return limited, false
	}

	plain, err := io.ReadAll(io.LimitReader(zr, maxRefuserBody+1))
	closeErr := zr.Close()

	if err != nil || closeErr != nil || int64(len(plain)) > maxRefuserBody {
		return limited, false
	}

	return plain, true
}

// journalRefuserEntry is the part of a journal entry the refuser reads: its
// kind.
type journalRefuserEntry struct {
	Kind int `json:"kind"`
}

// journalRefuserBatch is the part of a journal batch body the refuser reads:
// its entries.
type journalRefuserBatch struct {
	Entries []journalRefuserEntry `json:"entries"`
}

// outputsKind is the journal entry kind of stack outputs. A batch that holds
// only such entries carries no resource change, and the Pulumi CLI ignores
// the error of a refused one.
const outputsKind = 4

// outputsOnly reports whether body is a journal batch whose entries are all
// outputs entries. Any other shape passes through untouched.
func outputsOnly(body []byte) bool {
	var batch journalRefuserBatch
	if err := json.Unmarshal(body, &batch); err != nil {
		return false
	}

	if len(batch.Entries) == 0 {
		return false
	}

	for _, e := range batch.Entries {
		if e.Kind != outputsKind {
			return false
		}
	}

	return true
}

// faultyBatch adds an unknown member to the batch body, which the server
// strict-rejects. The body starts with the opening brace of the batch object.
func faultyBatch(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	rest, ok := bytes.CutPrefix(trimmed, []byte("{"))
	if !ok {
		return body
	}

	return append([]byte(`{"refused":true,`), rest...)
}

// faultedHandler wraps next with the refuser, or returns next unchanged when
// the refuser is nil.
func faultedHandler(next http.Handler, refuser *JournalRefuser) http.Handler {
	if refuser == nil {
		return next
	}

	return refuser.Wrap(next)
}
