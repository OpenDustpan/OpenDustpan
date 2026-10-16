package pulumi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// journalEntries stores one journal batch of a running update (ADR 43, decision
// 6). The lease token authorizes the write. The server answers 2xx only after
// the batch is committed (decision 6.2). The body is the batch as the CLI sent
// it, gzipped or not. A batch over the journal batch bound answers 413, and the
// CLI splits it.
func (h *Handler) journalEntries(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	tok, _, ok := h.leaseToken(w, r)
	if !ok {
		return
	}

	ref, id, ok := h.updateTarget(w, r)
	if !ok {
		return
	}

	limit := int64(h.updates.MaxJournalBatchBytes())

	releaseHold, ok := h.hold(w, r, limit)
	if !ok {
		return
	}

	defer releaseHold()

	body, ok := h.readJournalBody(w, r, limit)
	if !ok {
		return
	}

	if err := h.updates.AppendJournal(ctx, ref, id, tok, body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, struct{}{})
}

// readJournalBody reads one journal batch as the decompressed JSON the CLI
// sent. It bounds the decompressed size by limit, so a gzip bomb cannot exhaust
// memory, and maps an oversized body to 413.
func (h *Handler) readJournalBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	counted, release, ok := h.largeReader(w, r, limit)
	if !ok {
		return nil, false
	}

	defer release()

	body, err := io.ReadAll(counted)
	if counted.n > limit {
		h.writeTooLarge(w, r, limit)

		return nil, false
	}

	if err != nil {
		if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok && tooLarge != nil {
			h.writeTooLarge(w, r, limit)
		} else {
			h.writeInvalidBody(r.Context(), w)
		}

		return nil, false
	}

	if !json.Valid(body) {
		h.writeInvalidBody(r.Context(), w)

		return nil, false
	}

	return body, true
}
