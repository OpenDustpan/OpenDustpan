package server

import (
	"context"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
)

// lostMakerRevokeTimeout bounds the revoke of the service tokens whose maker
// lost the act-as path. The revoke runs after the commit of the change, so it
// must not stop with the request. It reads the active tokens in pages and
// writes one transaction for each revoked token, and a large installation may
// hold thousands of tokens. A limit of one minute covers that work with a wide
// margin, and it still ends a revoke that waits on a stuck store, so that no
// request holds a goroutine without end. A request, or a graceful shutdown
// that waits for the request, can wait up to this time after the commit of the
// change. When the limit passes, the revoke
// writes one log line, and the check at the use of each remaining token
// refuses it.
const lostMakerRevokeTimeout = time.Minute

// revokeLostMakers runs after a committed change of a grant, a role, a group,
// a membership or a principal: each such change can end the last act-as path
// of the maker of a service token. It revokes each active service token whose
// maker has no act-as path to the principal of the token any more. The audit
// entry of each revocation names the change: the action and the object of the
// change, and the subject of the caller.
//
// It runs in its own transactions after the commit of the change, before the
// answer, on a context that the cancel of the request does not end: a client
// that leaves after the commit must not stop the revoke, because the audit
// entry would then name the use of the token as the cause, not the change.
// The context keeps the values of the request and has the timeout
// lostMakerRevokeTimeout. A failure does not change the answer of the
// committed change: it writes one log line, and the check at the use of each
// such token refuses it.
//
// Rule of the paths with no sweep: a path that changes the act-as path of a
// maker with no call of this function does not leave the token active. These
// paths are the disable of a principal at sign-in, the group claims of the
// identity provider at sign-in (they can remove or suspend a membership), the
// revoke of the demo grant, a restore and a change on another instance. The check at the use of
// each service token (accesstoken.Service.checkMakerAtUse) refuses the token
// and revokes it, with the reason of the use. The test
// TestTokenIsRefusedAfterAPathWithNoSweep in pkg/accesstoken covers each path.
func (h *Handler) revokeLostMakers(r *http.Request, p identity.Principal, action identity.Action, object string) {
	if h.tokens == nil {
		return
	}

	cause := accesstoken.Cause{Subject: p.Subject, Action: string(action), Object: object}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), h.lostMakerTimeout)
	defer cancel()

	if _, err := h.tokens.RevokeLostMakers(ctx, cause); err != nil {
		faultlog.LogFault(ctx, h.logger,
			"revoke of the service tokens whose maker lost the act-as path failed",
			faultlog.CauseUnclassified, "accesstoken.revoke-lost-makers")
	}
}
