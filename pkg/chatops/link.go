package chatops

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Bounds of a link request.
const (
	// LinkRequestLifetime is how long a person has to accept a link
	// request in the console.
	LinkRequestLifetime = 5 * time.Minute
	// MinLinkTokenLen is the minimum length of the one-time token of a
	// link request. The server makes it from at least 32 random bytes.
	MinLinkTokenLen = 43
	// maxLinkTokenLen caps the token, so that a hash input stays small.
	maxLinkTokenLen = 512
)

// LinkRequest is a one-time request to link a chat user to the principal
// that accepts it. The chat tool shows its token once, to the chat user
// only. The store keeps only TokenSHA256, the SHA-256 of the token, so a
// copy of the database gives no token. The request is bound to the chat
// user: the link that it makes takes Platform, Workspace and User from
// the request, never from the browser request that accepts it. It is
// good for one use, until ExpiresAt. UsedBy is the principal that
// accepted it.
type LinkRequest struct {
	TokenSHA256 string
	Platform    string
	Workspace   string
	User        string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	UsedAt      time.Time
	UsedBy      string
}

// HashLinkToken returns the SHA-256 of token, in lowercase hexadecimal,
// as LinkRequest.TokenSHA256 holds it.
func HashLinkToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

// NewLinkRequest returns the link request of token for the chat user u at
// now, with the allowlist allow. It
// returns ErrInvalid for a token shorter than MinLinkTokenLen or longer
// than the maximum, or for an ID that is empty or too long, and
// ErrWorkspace for a workspace that allow does not have.
func NewLinkRequest(token string, u ChatUser, allow Workspaces, now time.Time) (LinkRequest, error) {
	if len(token) < MinLinkTokenLen || len(token) > maxLinkTokenLen {
		return LinkRequest{}, fmt.Errorf("%w: a link token has %d to %d characters", ErrInvalid, MinLinkTokenLen,
			maxLinkTokenLen)
	}

	if !allow.Allows(u.Platform, u.Workspace) {
		return LinkRequest{}, fmt.Errorf("%w: %s %s", ErrWorkspace, u.Platform, u.Workspace)
	}

	r := LinkRequest{
		TokenSHA256: HashLinkToken(token), Platform: u.Platform, Workspace: u.Workspace, User: u.User,
		CreatedAt: now, ExpiresAt: now.Add(LinkRequestLifetime), UsedAt: time.Time{}, UsedBy: "",
	}

	return r, r.Validate()
}

// Validate returns ErrInvalid for a link request with a hash that is not
// a SHA-256 in lowercase hexadecimal, an ID that is empty or too long, or
// times that do not match LinkRequestLifetime.
func (r LinkRequest) Validate() error {
	b, err := hex.DecodeString(r.TokenSHA256)

	switch {
	case err != nil || len(b) != sha256.Size || hex.EncodeToString(b) != r.TokenSHA256:
		return fmt.Errorf("%w: a link request holds the SHA-256 of its token", ErrInvalid)
	case !validIDs(r.Platform, r.Workspace, r.User):
		return fmt.Errorf("%w: a link request names a platform, a workspace and a user", ErrInvalid)
	case r.CreatedAt.IsZero() || !r.ExpiresAt.Equal(r.CreatedAt.Add(LinkRequestLifetime)):
		return fmt.Errorf("%w: a link request expires %s after its creation", ErrInvalid, LinkRequestLifetime)
	default:
		return nil
	}
}

// Check returns nil when a person can accept r at now, with the
// allowlist allow. It returns ErrUsed for a request with a use,
// ErrExpired after its expiry, and ErrWorkspace for a workspace that
// allow no longer has. The console page and the accept route both call
// it.
func (r LinkRequest) Check(allow Workspaces, now time.Time) error {
	switch {
	case !r.UsedAt.IsZero():
		return ErrUsed
	case !now.Before(r.ExpiresAt):
		return ErrExpired
	case !allow.Allows(r.Platform, r.Workspace):
		return fmt.Errorf("%w: %s %s", ErrWorkspace, r.Platform, r.Workspace)
	default:
		return nil
	}
}

// Link returns the link of the chat user of r to principal at now.
func (r LinkRequest) Link(principal string, now time.Time) Link {
	return Link{Platform: r.Platform, Workspace: r.Workspace, User: r.User, Principal: principal, LinkedAt: now}
}
