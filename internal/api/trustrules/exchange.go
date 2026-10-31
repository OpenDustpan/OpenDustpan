package trustrules

import (
	"fmt"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// ExchangePath is the route of the token exchange of a namespace. It needs
// no sign-in: the token of the workload is the credential.
const ExchangePath = "/api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange"

// maxExchangeBytes bounds the body of an exchange: a token of at most
// trustrule.MaxTokenBytes and a rule ID.
const maxExchangeBytes = trustrule.MaxTokenBytes + 1024

// exchangeRequest is the body of an exchange. Rule is optional: it names the
// rule when more than one rule accepts the token.
type exchangeRequest struct {
	Token *string `dustpan:"required" json:"token"`
	Rule  *string `dustpan:"optional" json:"rule"`
}

// exchangeDocument is the answer of an exchange. Token is the secret of the
// access token: the server shows it once.
//
// Contract: never remove or rename these fields.
type exchangeDocument struct {
	ExpiresAt time.Time `json:"expiresAt"`
	Token     string    `json:"token"`
	TokenID   string    `json:"id"`
	Principal string    `json:"principal"`
	Rule      string    `json:"rule"`
}

// exchange serves POST on ExchangePath. It answers 201 with the access token.
// A token that no rule accepts answers 401 with trust-rule.no-match, and the
// answer never names the check that failed: the audit entry of the namespace
// does. A token that two rules accept, with no rule in the request, answers
// 409 with trust-rule.ambiguous.
func (h *Handler) exchange(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		h.writeError(ctx, w, fmt.Errorf("namespace in the path: %w", err))

		return
	}

	var in exchangeRequest
	if decodeErr := route.DecodeBody(w, r, maxExchangeBytes, &in); decodeErr != nil {
		h.writeError(ctx, w, fmt.Errorf("exchange body: %w", decodeErr))

		return
	}

	if in.Token == nil {
		h.writeError(ctx, w, problem.BadRequest(errcode.RequestBodyInvalid, "the body must hold token"))

		return
	}

	rule := trustrule.ID("")
	if in.Rule != nil {
		rule = trustrule.ID(*in.Rule)

		// The rule ID goes to the audit log: refuse each value that is not a
		// rule ID before any use.
		if ruleErr := rule.Validate(); ruleErr != nil {
			h.writeError(ctx, w, problem.BadRequest(errcode.RequestBodyInvalid, "rule is not a trust rule ID"))

			return
		}
	}

	res, err := h.exchanger.Exchange(ctx, trustrulesvc.ExchangeInput{
		Namespace: name, Token: *in.Token, Rule: rule, Request: h.requestEntry(r),
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The answer holds a secret: no cache may keep it.
	w.Header().Set("Cache-Control", "no-store")
	problem.WriteJSON(ctx, h.logger, w, http.StatusCreated, exchangeDocument{
		ExpiresAt: res.Token.ExpiresAt.UTC(), Token: res.Secret.Reveal(), TokenID: res.Token.ID,
		Principal: res.Token.PrincipalID, Rule: string(res.Rule.ID),
	})
}

// requestEntry returns the template of the audit entries of an exchange:
// where the request came from. The service fills the rest.
func (h *Handler) requestEntry(r *http.Request) audit.Entry {
	return audit.Entry{
		At: h.now().UTC(), Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: clientip.String(r),
		UserAgent: audit.TruncateUserAgent(apidesc.Header(r, "User-Agent")), Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: requestid.FromContext(r.Context()),
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "",
		Reason: "",
	}
}
