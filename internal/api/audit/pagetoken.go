package audit

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// pageTokenWire is the wire shape of a page token: the sequence that the
// next page starts below, and the fingerprint of the scope of the list.
type pageTokenWire struct {
	Fingerprint string `json:"f"`
	Before      int64  `json:"b"`
}

// pageCursor is a page token that passed every check: its sequence is 1 or
// more, and it belongs to the scope of the request. The type has no exported
// constructor, so the handler never passes an unchecked sequence to the
// store.
type pageCursor struct {
	before int64
}

// errPageToken answers a page token that the server does not accept. It
// holds no detail of the reason, so a caller learns nothing about the token.
var errPageToken = problem.BadRequest(errcode.RequestPageTokenInvalid,
	"pageToken is not valid for this scope: send the request again without pageToken")

// errTokenSequence reports a sequence that no page token can hold.
var errTokenSequence = errors.New("a page token needs a sequence of 1 or more")

// scopeFingerprint returns the fingerprint of a scope. The fingerprint is a
// consistency check and not a signature: a caller who may read the scope can
// make a token for it, and the token gives no reach that the caller does not
// have.
func scopeFingerprint(scope namespace.Name) string {
	return pagetoken.Fingerprint("audit\x00" + scope.String())
}

// decodePageCursor reads token for scope. It returns errPageToken for a token
// that does not decode, that is too long, that holds a sequence below 1, or
// that holds the fingerprint of another scope. Fail closed: any other value
// is refused too.
func decodePageCursor(token string, scope namespace.Name) (pageCursor, error) {
	var wire pageTokenWire

	if err := pagetoken.Decode(token, &wire); err != nil {
		return pageCursor{}, errPageToken
	}

	if wire.Before < 1 || !pagetoken.SameFingerprint(wire.Fingerprint, scopeFingerprint(scope)) {
		return pageCursor{}, errPageToken
	}

	return pageCursor{before: wire.Before}, nil
}

// encodePageToken returns the token of the page that starts below before.
func encodePageToken(scope namespace.Name, before int64) (string, error) {
	if before < 1 {
		return "", errTokenSequence
	}

	token, err := pagetoken.Encode(pageTokenWire{Fingerprint: scopeFingerprint(scope), Before: before})
	if err != nil {
		return "", fmt.Errorf("make the next page token: %w", err)
	}

	return token, nil
}
