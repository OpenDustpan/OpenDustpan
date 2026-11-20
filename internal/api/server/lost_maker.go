package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
)

// revokeLostMakers runs after a committed change of a grant, a role, a group,
// a membership or a principal: each such change can end the last act-as path
// of the maker of a service token. It revokes each active service token whose
// maker has no act-as path to the principal of the token any more. The audit
// entry of each revocation names the change: the action and the object of the
// change, and the subject of the caller.
//
// It runs in its own transactions after the commit of the change, before the
// answer. A failure does not change the answer of the committed change: it
// writes one log line, and the check at the use of each such token refuses
// it.
func (h *Handler) revokeLostMakers(r *http.Request, p identity.Principal, action identity.Action, object string) {
	if h.tokens == nil {
		return
	}

	cause := accesstoken.Cause{Subject: p.Subject, Action: string(action), Object: object}

	if _, err := h.tokens.RevokeLostMakers(r.Context(), cause); err != nil {
		faultlog.LogFault(r.Context(), h.logger,
			"revoke of the service tokens whose maker lost the act-as path failed",
			faultlog.CauseUnclassified, "accesstoken.revoke-lost-makers")
	}
}
