package pulumi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// stackPageSize is the number of stacks in one answer of GET
// /api/user/stacks. The Pulumi CLI reads the next page while the answer
// holds a continuationToken.
const stackPageSize = 500

// errContinuationToken reports a continuationToken that does not decode, or
// that another query made. classify maps it to 400.
var errContinuationToken = errors.New(
	"continuationToken does not belong to this query: start again without it",
)

// continuationWire is the opaque continuation token of the stack list, as
// base64url JSON. It holds the key of the last stack of the page and a
// fingerprint of the query, so a token of another query answers 400.
type continuationWire struct {
	Namespace   string `json:"n"`
	Project     string `json:"p"`
	Stack       string `json:"s"`
	Fingerprint string `json:"f"`
}

// stackListQuery is one request of the stack list: the namespaces to read,
// the project filter, and the organization query value, which the token
// fingerprint holds.
type stackListQuery struct {
	organization string
	project      stack.ProjectName
	namespaces   []namespace.Name
}

// fingerprint returns a short digest of the query values that select the
// stacks, so a token works only for the query that made it.
func (q stackListQuery) fingerprint() string {
	sum := sha256.Sum256([]byte("organization=" + q.organization + "\nproject=" + q.project.String()))

	return hex.EncodeToString(sum[:8])
}

// encodeContinuation returns the token of the page after the stack st.
func encodeContinuation(st stack.Stack, fingerprint string) string {
	raw, err := json.Marshal(continuationWire{
		Namespace:   st.Ref.Namespace.String(),
		Project:     st.Ref.Project.String(),
		Stack:       st.Ref.Stack.String(),
		Fingerprint: fingerprint,
	})
	if err != nil {
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeContinuation returns the cursor of the token. A token that does not
// decode, that another query made, or that holds a name that is not valid
// gives errContinuationToken.
func decodeContinuation(token, fingerprint string) (stack.PageCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return stack.PageCursor{}, errContinuationToken
	}

	var w continuationWire
	if jsonErr := json.Unmarshal(raw, &w); jsonErr != nil || w.Fingerprint != fingerprint {
		return stack.PageCursor{}, errContinuationToken
	}

	// The cursor goes to the store as SQL arguments only. The names must still
	// be valid names, so a tampered token cannot carry other text.
	ref, err := stack.ParseRef(w.Namespace, w.Project, w.Stack)
	if err != nil {
		return stack.PageCursor{}, errContinuationToken
	}

	return stack.PageCursor{Namespace: ref.Namespace, Project: ref.Project, Name: ref.Stack}, nil
}

// serveStackPage answers one page of the stacks of q, after the stack of the
// continuationToken of r. The answer has a continuationToken while a later
// page exists, and null on the last page.
func (h *Handler) serveStackPage(ctx context.Context, w http.ResponseWriter, r *http.Request, q stackListQuery) {
	page := stack.PageQuery{After: nil, Project: q.project, Namespaces: q.namespaces, Limit: stackPageSize + 1}

	if raw := r.URL.Query().Get("continuationToken"); raw != "" {
		after, err := decodeContinuation(raw, q.fingerprint())
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		page.After = &after
	}

	stacks, err := h.stacks.Page(ctx, page)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	resp := listStacksResponse{Stacks: nil, ContinuationToken: nil}

	if len(stacks) > stackPageSize {
		stacks = stacks[:stackPageSize]
		next := encodeContinuation(stacks[len(stacks)-1], q.fingerprint())
		resp.ContinuationToken = &next
	}

	resp.Stacks = summarize(stacks)

	h.writeJSON(ctx, w, http.StatusOK, resp)
}
