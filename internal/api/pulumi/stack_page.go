package pulumi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

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

// errStackTagFilter reports a tag filter of the stack list that is not valid.
// classify maps it to 400.
var errStackTagFilter = errors.New("the tag filter of the stack list is not valid")

// stackListQuery is one request of the stack list: the namespaces to read,
// the project filter, the tag filter, and the organization query value. The
// token fingerprint holds all but the namespaces.
type stackListQuery struct {
	organization string
	project      stack.ProjectName
	tag          *stack.TagFilter
	namespaces   []namespace.Name
}

// fingerprint returns a short digest of the query values that select the
// stacks, so a token works only for the query that made it. A query with no
// tag filter has the same digest as before the tag filter existed.
func (q stackListQuery) fingerprint() string {
	in := "organization=" + q.organization + "\nproject=" + q.project.String()

	if q.tag != nil {
		in += "\ntagName=" + strconv.Quote(q.tag.Name)

		if q.tag.HasValue {
			in += "\ntagValue=" + strconv.Quote(q.tag.Value)
		}
	}

	sum := sha256.Sum256([]byte(in))

	return hex.EncodeToString(sum[:8])
}

// stackTagFilter reads the tag filter of the stack list from query. The
// Pulumi CLI sends tagName alone for "--tag name" and tagName with tagValue
// for "--tag name=value". ok is false when query has no tagName: the list then
// keeps every stack. An empty tagName, or a tagValue with no tagName, gives
// errStackTagFilter.
func stackTagFilter(query url.Values) (stack.TagFilter, bool, error) {
	none := stack.TagFilter{Name: "", Value: "", HasValue: false}

	if !query.Has("tagName") {
		if query.Has("tagValue") {
			return none, false, fmt.Errorf(
				"%w: tagValue needs tagName: run pulumi stack ls --tag name=value", errStackTagFilter,
			)
		}

		return none, false, nil
	}

	name := query.Get("tagName")
	if name == "" {
		return none, false, fmt.Errorf(
			"%w: tagName is empty: run pulumi stack ls --tag name=value", errStackTagFilter,
		)
	}

	return stack.TagFilter{Name: name, Value: query.Get("tagValue"), HasValue: query.Has("tagValue")}, true, nil
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
// decode, that another query made, that holds a name that is not valid, or
// whose key lies outside the query gives errContinuationToken. The
// fingerprint is not keyed, so anyone can compute it; the cursor is bound to
// the query by its own names as well.
func decodeContinuation(token string, q stackListQuery) (stack.PageCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return stack.PageCursor{}, errContinuationToken
	}

	var w continuationWire
	if jsonErr := json.Unmarshal(raw, &w); jsonErr != nil || w.Fingerprint != q.fingerprint() {
		return stack.PageCursor{}, errContinuationToken
	}

	// The cursor goes to the store as SQL arguments only. The names must still
	// be valid names, so a tampered token cannot carry other text.
	ref, err := stack.ParseRef(w.Namespace, w.Project, w.Stack)
	if err != nil {
		return stack.PageCursor{}, errContinuationToken
	}

	// Defense in depth: the fingerprint check above and the readable
	// namespaces of the query do not bind the key of the cursor. This check
	// stays so that a token with a valid fingerprint cannot start the page
	// at a key outside the organization or the project of the query.
	if (q.organization != "" && ref.Namespace.String() != q.organization) ||
		(q.project != "" && ref.Project != q.project) {
		return stack.PageCursor{}, errContinuationToken
	}

	return stack.PageCursor{Namespace: ref.Namespace, Project: ref.Project, Name: ref.Stack}, nil
}

// serveStackPage answers one page of the stacks of q that pass the tag filter
// of r (see stackTagFilter), after the stack of the continuationToken of r.
// The answer has a continuationToken while a later page exists, and null on
// the last page.
func (h *Handler) serveStackPage(ctx context.Context, w http.ResponseWriter, r *http.Request, q stackListQuery) {
	tag, filtered, tagErr := stackTagFilter(r.URL.Query())
	if tagErr != nil {
		h.writeError(ctx, w, tagErr)

		return
	}

	if filtered {
		q.tag = &tag
	}

	page := stack.PageQuery{
		After:      nil,
		Project:    q.project,
		Tag:        q.tag,
		Namespaces: q.namespaces,
		Limit:      stackPageSize + 1,
	}

	if raw := r.URL.Query().Get("continuationToken"); raw != "" {
		after, err := decodeContinuation(raw, q)
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
