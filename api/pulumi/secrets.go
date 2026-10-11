package pulumi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/secret"
)

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
// the stack in the path, reads the body, decodes it into values, crypts them,
// and answers.
func (h *Handler) serveSecrets(
	w http.ResponseWriter,
	r *http.Request,
	crypt func(context.Context, secret.Scope, [][]byte) ([][]byte, error),
	decode func([]byte) ([][]byte, error),
	answer func(in, out [][]byte) any,
) {
	ctx := r.Context()

	scope, ok := h.secretScope(w, r)
	if !ok {
		return
	}

	body, ok := h.secretBody(w, r)
	if !ok {
		return
	}

	values, err := decode(body)
	if err != nil {
		h.writeInvalidBody(ctx, w)

		return
	}

	out, err := crypt(ctx, scope, values)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, answer(values, out))
}

// decodeSingles returns a decoder for a body with one base64 value in field.
// A body without the field answers 400 through writeInvalidBody.
func decodeSingles(field string) func([]byte) ([][]byte, error) {
	return func(body []byte) ([][]byte, error) {
		var doc map[string][]byte
		if err := json.Unmarshal(body, &doc); err != nil {
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
func decodeBatch(field string) func([]byte) ([][]byte, error) {
	return func(body []byte) ([][]byte, error) {
		var doc map[string][][]byte
		if err := json.Unmarshal(body, &doc); err != nil {
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
// stores nothing until the audit log exists.
func (h *Handler) logDecryption(
	w http.ResponseWriter, _ *http.Request, _ identity.Principal, _ identity.Object,
) {
	w.WriteHeader(http.StatusNoContent)
}

// logBatchDecryption answers the audit route for a batch third-party
// decryption. It stores nothing until the audit log exists.
func (h *Handler) logBatchDecryption(
	w http.ResponseWriter, _ *http.Request, _ identity.Principal, _ identity.Object,
) {
	w.WriteHeader(http.StatusNoContent)
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

// secretBody reads one secrets request body, gunzipping it when the CLI sent
// it compressed. A body over the wire limit answers 413, and a body that
// decompresses over it answers 413 too, so a gzip bomb cannot exhaust memory.
func (h *Handler) secretBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	limit := secretBodyLimit(h.secrets)

	body, ok := h.readBody(w, r, limit)
	if !ok {
		return nil, false
	}

	if strings.Contains(r.Header.Get("Content-Encoding"), "gzip") {
		return h.gunzipBody(w, r, body, limit)
	}

	return body, true
}
