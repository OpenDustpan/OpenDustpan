package pulumi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret"
)

// channelHeader is the request header that declares where a request comes
// from: "api" or "console". The audit entry records it as declared; unknown
// values are "api".
const channelHeader = "Dustpan-Channel"

const (
	// secretBodySlack covers the base64 and JSON frame around one secrets
	// batch on the wire, above the batch size the domain accepts.
	secretBodySlack = 1 << 20
	// auditBodyLimit bounds one decrypt audit report on the wire. The CLI
	// sends a small JSON document with the secret or command name, so 8 KiB
	// leaves ample room while a large body cannot grow memory without bound.
	auditBodyLimit = 8 << 10
)

// secretBodyLimit bounds one secrets request body on the wire. Values travel
// base64-encoded in JSON, so the wire body runs about a third larger than the
// batch the domain accepts. The CLI gzips its batch calls, so the same bound
// caps the decompressed body (ADR 23).
func secretBodyLimit(secrets *secret.Service) int64 {
	return int64(secrets.MaxBatchBytes())*4/3 + secretBodySlack
}

// errAuditAppend reports a failed audit append on a route that answers a
// secret value. classify maps it to 500 without detail, so the route sends
// no secret value when it cannot record the entry (ADR 41 decision 5).
var errAuditAppend = errors.New("audit append failed")

// secretsCall groups what one secrets route does with the request body:
// how to decode it into values, how to crypt the values and how to answer.
type secretsCall struct {
	crypt  func(context.Context, secret.Scope, [][]byte) ([][]byte, error)
	decode func(*json.Decoder) ([][]byte, error)
	answer func(in, out [][]byte) any
	// none is the answer of a body with no value, or nil when such a body
	// goes to crypt like any other body. A route with none answers it before
	// it reads a key, and writes no audit entry: it opens no value.
	none any
}

// serveSecrets serves one encrypt or decrypt route: it resolves the scope of
// the stack in the path, streams the body into values, crypts them, and
// answers. The gate bounds concurrent large bodies; the decoder streams gzip
// into values so only one copy is held. When auditDecrypt is set, the route
// answers a secret value, so it writes one tool-operation entry with the
// count of values before it answers: a failed append answers 500 with no
// secret value. Encrypt routes pass nil: they answer no secret value.
func (h *Handler) serveSecrets(
	w http.ResponseWriter, r *http.Request, op secretsCall, auditDecrypt func(count int) error,
) {
	ctx := r.Context()

	scope, ok := h.secretScope(w, r)
	if !ok {
		return
	}

	limit := secretBodyLimit(h.secrets)
	releaseHold, ok := h.hold(w, r, limit)
	if !ok {
		return
	}

	defer releaseHold()

	values, ok := h.decodeSecrets(w, r, limit, op.decode)
	if !ok {
		return
	}

	if op.none != nil && len(values) == 0 {
		h.writeJSON(ctx, w, http.StatusOK, op.none)

		return
	}

	out, err := op.crypt(ctx, scope, values)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if auditDecrypt != nil {
		if err := auditDecrypt(len(values)); err != nil {
			h.failAudit(ctx)
			h.writeError(ctx, w, errAuditAppend)

			return
		}
	}

	h.writeJSON(ctx, w, http.StatusOK, op.answer(values, out))
}

// decodeSecrets streams one secrets body into values. An oversized body
// answers 413, invalid JSON or a missing field answers 400.
func (h *Handler) decodeSecrets(
	w http.ResponseWriter, r *http.Request, limit int64, decode func(*json.Decoder) ([][]byte, error),
) ([][]byte, bool) {
	ctx := r.Context()

	counted, release, ok := h.largeReader(w, r, limit)
	if !ok {
		return nil, false
	}

	defer release()

	dec := json.NewDecoder(counted)

	values, err := decode(dec)
	if err != nil {
		switch {
		case counted.n > limit:
			h.writeTooLarge(w, r, limit)
		case isMaxBytes(err):
			h.writeTooLarge(w, r, limit)
		default:
			h.writeInvalidBody(ctx, w)
		}

		return nil, false
	}

	if counted.n > limit {
		h.writeTooLarge(w, r, limit)

		return nil, false
	}

	// The whole body must be one JSON value, as Unmarshal requires.
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		h.writeInvalidBody(ctx, w)

		return nil, false
	}

	return values, true
}

// isMaxBytes reports whether err wraps http.MaxBytesError.
func isMaxBytes(err error) bool {
	tooLarge, ok := errors.AsType[*http.MaxBytesError](err)

	return ok && tooLarge != nil
}

// decodeSingles returns a decoder for a body with one base64 value in field.
// A body without the field answers 400 through writeInvalidBody.
func decodeSingles(field string) func(*json.Decoder) ([][]byte, error) {
	return func(dec *json.Decoder) ([][]byte, error) {
		var doc map[string][]byte
		if err := dec.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode secrets body: %w", err)
		}

		value, ok := doc[field]
		if !ok || value == nil {
			return nil, fmt.Errorf("%w: missing %q", errInvalidBody, field)
		}

		return [][]byte{value}, nil
	}
}

// decodeBatch returns a decoder for a body with base64 values in field. A
// body without the field, or with null in it, answers 400 through
// writeInvalidBody.
func decodeBatch(field string) func(*json.Decoder) ([][]byte, error) {
	return func(dec *json.Decoder) ([][]byte, error) {
		values, err := readBatch(dec, field)
		if err != nil {
			return nil, err
		}

		if values == nil {
			return nil, fmt.Errorf("%w: missing %q", errInvalidBody, field)
		}

		return values, nil
	}
}

// decodeBatchOrNull returns a decoder like decodeBatch that reads null in
// field as a list with no value: some versions of the Pulumi CLI send null for
// a batch with no value. A body without the field answers 400.
func decodeBatchOrNull(field string) func(*json.Decoder) ([][]byte, error) {
	return func(dec *json.Decoder) ([][]byte, error) {
		values, err := readBatch(dec, field)
		if err != nil {
			return nil, err
		}

		if values == nil {
			return [][]byte{}, nil
		}

		return values, nil
	}
}

// readBatch decodes a body with base64 values in field. It returns nil values
// for null in field, and an error that wraps errInvalidBody for a body without
// the field.
func readBatch(dec *json.Decoder, field string) ([][]byte, error) {
	var doc map[string][][]byte
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode secrets body: %w", err)
	}

	values, ok := doc[field]
	if !ok {
		return nil, fmt.Errorf("%w: missing %q", errInvalidBody, field)
	}

	return values, nil
}

// answerSingle returns an answer with the single output value in field. An
// empty output answers no value instead of panicking.
func answerSingle(field string) func(_, out [][]byte) any {
	return func(_, out [][]byte) any {
		if len(out) == 0 {
			return map[string][]byte{}
		}

		return map[string][]byte{field: out[0]}
	}
}

// answerBatch returns an answer with the output values in order in field.
func answerBatch(field string) func(_, out [][]byte) any {
	return func(_, out [][]byte) any {
		return map[string][][]byte{field: out}
	}
}

// answerBatchDecrypt answers decrypted values keyed by the base64 of each
// ciphertext, as the CLI reads them.
func answerBatchDecrypt(in, out [][]byte) any {
	plain := make(map[string][]byte, len(in))
	for i, c := range in {
		plain[base64.StdEncoding.EncodeToString(c)] = out[i]
	}

	return map[string]map[string][]byte{"plaintexts": plain}
}

// encryptValue encrypts one value under the stack's data key, creating the
// key on first use.
func (h *Handler) encryptValue(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	h.serveSecrets(w, r, secretsCall{
		crypt:  h.secrets.Encrypt,
		decode: decodeSingles("plaintext"),
		answer: answerSingle("ciphertext"),
		none:   nil,
	}, nil)
}

// batchEncrypt encrypts values in one call under the stack's data key. The
// answer holds one ciphertext per plaintext, in order.
func (h *Handler) batchEncrypt(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	h.serveSecrets(w, r, secretsCall{
		crypt:  h.secrets.Encrypt,
		decode: decodeBatch("plaintexts"),
		answer: answerBatch("ciphertexts"),
		none:   nil,
	}, nil)
}

// decryptValue opens one ciphertext made by encryptValue for the same stack.
// It writes one audit entry of the kind tool-operation before it answers: a
// failed append answers 500 with no secret value. Any decryption failure is
// a 400 without detail.
func (h *Handler) decryptValue(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	h.serveSecrets(
		w,
		r,
		secretsCall{
			crypt:  h.secrets.Decrypt,
			decode: decodeSingles("ciphertext"),
			answer: answerSingle("plaintext"),
			none:   nil,
		},
		func(count int) error { return h.appendToolOperationAudit(r, p, obj, count) },
	)
}

// batchDecrypt opens each ciphertext made by batchEncrypt for the same stack.
// The server always serves it: the CLI has no fallback. A batch with no
// ciphertext (an empty list or null) answers no plaintext, before a key read
// and with no audit entry. Any other batch writes one audit entry of the kind
// tool-operation before it answers: a failed append answers 500 with no
// secret value. Any failure fails the batch as a whole with a 400 without
// detail.
func (h *Handler) batchDecrypt(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	h.serveSecrets(
		w,
		r,
		secretsCall{
			crypt:  h.secrets.Decrypt,
			decode: decodeBatchOrNull("ciphertexts"),
			answer: answerBatchDecrypt,
			none:   map[string]map[string][]byte{"plaintexts": {}},
		},
		func(count int) error { return h.appendToolOperationAudit(r, p, obj, count) },
	)
}

// logDecryption answers the audit route for one third-party decryption. It
// writes one audit entry of the kind reveal with the action unit:decrypt
// (ADR 41 decision 13) and still answers 204 when the append fails: the
// failure is a log line and a metric, never a failed CLI command. A body
// over auditBodyLimit answers 413 without an entry.
func (h *Handler) logDecryption(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	if !h.drainAuditBody(w, r) {
		return
	}

	h.appendRevealAudit(r, p, obj)
	w.WriteHeader(http.StatusNoContent)
}

// logBatchDecryption answers the audit route for a batch third-party
// decryption. It writes one audit entry of the kind reveal with the action
// unit:decrypt (ADR 41 decision 13) and still answers 204 when the append
// fails: the failure is a log line and a metric, never a failed CLI command.
// A body over auditBodyLimit answers 413 without an entry.
func (h *Handler) logBatchDecryption(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	if !h.drainAuditBody(w, r) {
		return
	}

	h.appendRevealAudit(r, p, obj)
	w.WriteHeader(http.StatusNoContent)
}

// drainAuditBody bounds and drains one decrypt audit report. The body is
// advisory: any document within the limit is discarded and the route writes
// its entry. An oversized body answers 413 and writes no entry, so a large
// body cannot grow memory without bound.
func (h *Handler) drainAuditBody(w http.ResponseWriter, r *http.Request) bool {
	releaseHold, ok := h.hold(w, r, auditBodyLimit)
	if !ok {
		return false
	}

	defer releaseHold()

	counted, release, ok := h.largeReader(w, r, auditBodyLimit)
	if !ok {
		return false
	}

	defer release()

	if _, err := io.Copy(io.Discard, counted); err != nil {
		if isMaxBytes(err) {
			h.writeTooLarge(w, r, auditBodyLimit)
		} else {
			h.writeInvalidBody(r.Context(), w)
		}

		return false
	}

	if counted.n > auditBodyLimit {
		h.writeTooLarge(w, r, auditBodyLimit)

		return false
	}

	return true
}

// appendRevealAudit writes one audit entry for a third-party decryption that
// the Pulumi CLI reports: the action unit:decrypt with the kind reveal, so
// a reader never confuses it with a tool operation. A failed append writes
// a log line with no secret and no value and counts the failure, and the
// route still answers 204.
func (h *Handler) appendRevealAudit(r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	entry := requestEntry(ctx, r, p, obj, auditDetail{
		at:         time.Now().UTC(),
		action:     string(identity.ActionUnitDecrypt),
		version:    "",
		valuePath:  "",
		valueCount: nil,
		kind:       audit.KindReveal,
		outcome:    audit.OutcomeSuccess,
	})

	if err := h.appendEntry(ctx, entry); err != nil {
		h.failAudit(ctx)
	}
}

// appendToolOperationAudit writes one audit entry for a decrypt call that the
// server answers itself: the action unit:decrypt with the kind tool-operation
// and the count of values in the value count field, so a reader never confuses it
// with a person who asked to see a secret value. Version stays empty: it
// names a state version. It returns the append error,
// so the route answers 500 with no secret value when it cannot record the
// entry.
func (h *Handler) appendToolOperationAudit(
	r *http.Request, p identity.Principal, obj identity.Object, count int,
) error {
	valueCount := int64(count)

	entry := requestEntry(r.Context(), r, p, obj, auditDetail{
		at:         time.Now().UTC(),
		action:     string(identity.ActionUnitDecrypt),
		version:    "",
		valuePath:  "",
		valueCount: &valueCount,
		kind:       audit.KindToolOperation,
		outcome:    audit.OutcomeSuccess,
	})

	return h.appendEntry(r.Context(), entry)
}

// appendEntry writes e in its namespace with the fence check of the
// namespace, so the appends of one namespace form one sequence.
func (h *Handler) appendEntry(ctx context.Context, e audit.Entry) error {
	ns, err := h.namespaces.Get(ctx, e.Namespace)
	if err != nil {
		return err
	}

	_, err = h.audit.Append(ctx, ns.Fence(), e)

	return err
}

// failAudit records a lost audit entry: one log line with no secret and no
// value, and one count on the failure metric through the wired callback. It
// names no error text: the text may hold driver detail (ADR 32 decision 7).
func (h *Handler) failAudit(ctx context.Context) {
	faultlog.LogFault(ctx, h.logger, "audit append failed", faultlog.CauseUnclassified, "audit.append")

	if h.onAuditFailure != nil {
		h.onAuditFailure()
	}
}

// secretScope resolves the stack in the path to the scope its values are
// encrypted under: the namespace and the stack ID. A rename keeps the ID, so
// ciphertexts survive it, and one stack's ciphertext never opens as another's.
func (h *Handler) secretScope(w http.ResponseWriter, r *http.Request) (secret.Scope, bool) {
	ctx := r.Context()

	ref, ok := h.updateRef(w, r)
	if !ok {
		var zero secret.Scope

		return zero, false
	}

	st, err := h.stacks.Get(ctx, ref)
	if err != nil {
		h.writeError(ctx, w, err)

		var zero secret.Scope

		return zero, false
	}

	scope, err := secret.NewScope(ref.Namespace, st.ID.String())
	if err != nil {
		h.writeError(ctx, w, err)

		var zero secret.Scope

		return zero, false
	}

	return scope, true
}
