package pulumi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/requestid"
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
)

// secretBodyLimit bounds one secrets request body on the wire. Values travel
// base64-encoded in JSON, so the wire body runs about a third larger than the
// batch the domain accepts. The CLI gzips its batch calls, so the same bound
// caps the decompressed body (ADR 23).
func secretBodyLimit(secrets *secret.Service) int64 {
	return int64(secrets.MaxBatchBytes())*4/3 + secretBodySlack
}

// serveSecrets serves one encrypt or decrypt route: it resolves the scope of
// the stack in the path, streams the body into values, crypts them, and
// answers. The gate bounds concurrent large bodies; the decoder streams gzip
// into values so only one copy is held.
func (h *Handler) serveSecrets(
	w http.ResponseWriter,
	r *http.Request,
	crypt func(context.Context, secret.Scope, [][]byte) ([][]byte, error),
	decode func(*json.Decoder) ([][]byte, error),
	answer func(in, out [][]byte) any,
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

	values, ok := h.decodeSecrets(w, r, limit, decode)
	if !ok {
		return
	}

	out, err := crypt(ctx, scope, values)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, answer(values, out))
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
// body without the field answers 400 through writeInvalidBody.
func decodeBatch(field string) func(*json.Decoder) ([][]byte, error) {
	return func(dec *json.Decoder) ([][]byte, error) {
		var doc map[string][][]byte
		if err := dec.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode secrets body: %w", err)
		}

		values, ok := doc[field]
		if !ok || values == nil {
			return nil, fmt.Errorf("%w: missing %q", errInvalidBody, field)
		}

		return values, nil
	}
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
	h.serveSecrets(w, r, h.secrets.Encrypt, decodeSingles("plaintext"), answerSingle("ciphertext"))
}

// batchEncrypt encrypts values in one call under the stack's data key. The
// answer holds one ciphertext per plaintext, in order.
func (h *Handler) batchEncrypt(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	h.serveSecrets(w, r, h.secrets.Encrypt, decodeBatch("plaintexts"), answerBatch("ciphertexts"))
}

// decryptValue opens one ciphertext made by encryptValue for the same stack.
// Any failure is a 400 without detail.
func (h *Handler) decryptValue(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	h.serveSecrets(w, r, h.secrets.Decrypt, decodeSingles("ciphertext"), answerSingle("plaintext"))
}

// batchDecrypt opens each ciphertext made by batchEncrypt for the same stack.
// The server always serves it: the CLI has no fallback. Any failure fails the
// batch as a whole with a 400 without detail.
func (h *Handler) batchDecrypt(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	h.serveSecrets(w, r, h.secrets.Decrypt, decodeBatch("ciphertexts"), answerBatchDecrypt)
}

// logDecryption answers the audit route for one third-party decryption. It
// writes one audit entry of the kind reveal with the action unit:decrypt
// (ADR 41 decision 13) and still answers 204 when the append fails: the
// failure is a log line and a metric, never a failed CLI command.
func (h *Handler) logDecryption(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	h.appendRevealAudit(r, p, obj)
	w.WriteHeader(http.StatusNoContent)
}

// logBatchDecryption answers the audit route for a batch third-party
// decryption. It writes one audit entry of the kind reveal with the action
// unit:decrypt (ADR 41 decision 13) and still answers 204 when the append
// fails: the failure is a log line and a metric, never a failed CLI command.
func (h *Handler) logBatchDecryption(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	h.appendRevealAudit(r, p, obj)
	w.WriteHeader(http.StatusNoContent)
}

// appendRevealAudit writes one audit entry for a third-party decryption that
// the Pulumi CLI reports: the action unit:decrypt with the kind reveal, so
// a reader never confuses it with a tool operation. A failed append writes
// a log line with no secret and no value and counts the failure, and the
// route still answers 204.
func (h *Handler) appendRevealAudit(r *http.Request, p identity.Principal, obj identity.Object) {
	if h.audit == nil {
		return
	}

	ctx := r.Context()

	ns, err := h.namespaces.Get(ctx, obj.Namespace())
	if err != nil {
		h.failAudit(ctx)

		return
	}

	userAgent := r.UserAgent()
	if len(userAgent) > audit.MaxUserAgentLen {
		userAgent = userAgent[:audit.MaxUserAgentLen]
	}

	entry := audit.Entry{
		At:         time.Now().UTC(),
		Namespace:  obj.Namespace(),
		Subject:    p.Subject,
		IDPSubject: "",
		TokenID:    "",
		Action:     string(identity.ActionUnitDecrypt),
		Object:     obj.String(),
		Version:    "",
		ValuePath:  "",
		SourceIP:   sourceIP(r),
		UserAgent:  userAgent,
		Channel:    audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind:       audit.KindReveal,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  requestid.FromContext(ctx),
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}

	if _, err := h.audit.Append(ctx, ns.Fence(), entry); err != nil {
		h.failAudit(ctx)
	}
}

// failAudit records a lost audit entry: one log line with no secret and no
// value, and one count on the failure metric through the wired callback. It
// names no error text: the text may hold driver detail (ADR 32 decision 7).
func (h *Handler) failAudit(ctx context.Context) {
	h.logger.ErrorContext(ctx, "audit append failed")

	if h.onAuditFailure != nil {
		h.onAuditFailure()
	}
}

// sourceIP returns the remote address that the server sees, without the
// port. A value without a port reads back unchanged.
func sourceIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
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
