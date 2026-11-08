package trustrules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// maxBodyBytes bounds the body of a create. A rule holds at most 64 KiB of
// static keys and 16 conditions of 32 values of 512 bytes, so a valid body
// is below it.
const maxBodyBytes = 512 << 10

// channelHeader names the request header that says where a request came
// from, for the audit entry.
const channelHeader = "Dustpan-Channel"

// conditionDocument is one condition on the wire.
//
// Contract: never remove or rename these fields.
type conditionDocument struct {
	Claim  string   `json:"claim"`
	Values []string `json:"values"`
}

// ruleDocument is one trust rule on the wire. StaticKeys is the JWKS of a
// rule with the key source static, as JSON text, and empty for the key source
// discovery. Principal is the ID of the principal that a match acts as.
//
// Contract: never remove or rename these fields.
type ruleDocument struct {
	CreatedAt  time.Time           `json:"createdAt"`
	ID         string              `json:"id"`
	Namespace  string              `json:"namespace"`
	Name       string              `json:"name"`
	Issuer     string              `json:"issuer"`
	Audience   string              `json:"audience"`
	KeySource  trustrule.KeySource `json:"keySource"`
	StaticKeys string              `json:"staticKeys"`
	Principal  string              `json:"principal"`
	CreatedBy  string              `json:"createdBy"`
	Conditions []conditionDocument `json:"conditions"`
}

// listDocument is the list of the rules of a namespace. Rules is never null.
type listDocument struct {
	Rules []ruleDocument `json:"rules"`
}

// createRequest is the body of a create. A pointer makes a missing value
// differ from an empty one. StaticKeys is optional: a rule with the key
// source discovery has none.
type createRequest struct {
	Name       *string              `dustpan:"required" json:"name"`
	Issuer     *string              `dustpan:"required" json:"issuer"`
	Audience   *string              `dustpan:"required" json:"audience"`
	KeySource  *trustrule.KeySource `dustpan:"required" json:"keySource"`
	StaticKeys *string              `dustpan:"optional" json:"staticKeys"`
	Principal  *string              `dustpan:"required" json:"principal"`
	Conditions *[]conditionDocument `dustpan:"required" json:"conditions"`
}

// list serves GET on RulesPath. It answers 200 with the rules of the
// namespace, ordered by name, then ID.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	rules, err := h.rules.List(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc := listDocument{Rules: make([]ruleDocument, 0, len(rules))}
	for i := range rules {
		doc.Rules = append(doc.Rules, documentOf(&rules[i]))
	}

	problem.WriteJSON(ctx, h.logger, w, http.StatusOK, doc)
}

// create serves POST on RulesPath. It answers 201 with the rule and its
// Location. A body that is not one object with the required fields, or a rule
// that fails the checks of a rule, answers 400 and changes nothing. The rule
// and its audit entry are one transaction in the store.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	in, err := readCreate(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	rule, err := h.rules.Create(ctx, p, h.entryOf(r, p), obj.Namespace(), in)
	if err != nil {
		h.appendCreateFailure(r, p, obj.Namespace(), in.PrincipalID, err)
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("Location", "/api/dustpan/v1/namespaces/"+url.PathEscape(rule.Namespace.String())+
		"/trust-rules/"+url.PathEscape(string(rule.ID)))
	problem.WriteJSON(ctx, h.logger, w, http.StatusCreated, documentOf(&rule))
}

// namespaceAppender appends the entries of a namespace. Both SQL adapters
// meet it with audit.Store.
type namespaceAppender interface {
	Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
}

// createFailureReason returns the audit reason of a create that the service
// refused for a missing action or for a check that could not run, and false
// for any other error. A reach refusal names its missing actions. A denial of
// principal:manage-tokens on the principal names that one action. An access
// check or a reach check that could not run names itself.
func createFailureReason(err error, principalID string) (string, bool) {
	if refused, ok := errors.AsType[access.ReachRefusal](err); ok {
		missing := refused.MissingActions()
		if len(missing) == 0 {
			return "", false
		}

		return access.MissingActionsReason(missing), true
	}

	switch {
	case errors.Is(err, trustrulesvc.ErrAccessUnchecked):
		return trustrulesvc.ErrAccessUnchecked.Error(), true
	case errors.Is(err, trustrulesvc.ErrReachUnchecked):
		return trustrulesvc.ErrReachUnchecked.Error(), true
	}

	// Only the denial of principal:manage-tokens carries this mark. Another
	// denial names no action.
	if !errors.Is(err, trustrulesvc.ErrManageTokensDenied) {
		return "", false
	}

	obj, parseErr := identity.ParsePrincipalObject(principalID)
	if parseErr != nil {
		return "", false
	}

	return access.MissingActionsReason([]access.HeldAction{{
		Action: identity.ActionPrincipalManageTokens,
		Object: obj,
	}}), true
}

// appendCreateFailure writes one failure entry of a refused create: the
// caller, the action of a create and the principal of the rule, in the
// namespace of the path or in the installation scope when it cannot be read.
// A lost entry does not change the answer: it logs one line and calls the
// audit failure callback.
func (h *Handler) appendCreateFailure(
	r *http.Request, p identity.Principal, ns namespace.Name, principalID string, err error,
) {
	reason, ok := createFailureReason(err, principalID)
	if !ok {
		return
	}

	entry := h.entryOf(r, p)
	entry.Action = trustrulesvc.ActionCreate
	entry.Kind = audit.KindChange
	entry.Outcome = audit.OutcomeFailure
	entry.Object = principal.Ref(principalID)
	entry.Reason = refusal.Clean(reason, audit.MaxReasonLen)

	appender, ok := h.audit.(namespaceAppender)
	if !ok || ns == "" {
		h.appendServerFailure(r.Context(), entry)

		return
	}

	n, getErr := h.namespaces.Get(r.Context(), ns)
	if getErr != nil {
		h.appendServerFailure(r.Context(), entry)

		return
	}

	entry.Namespace = n.Name
	if _, appendErr := appender.Append(r.Context(), n.Fence(), entry); appendErr != nil {
		h.failAudit(r.Context())
	}
}

// appendServerFailure writes entry in the installation scope. A lost entry
// logs one line and calls the audit failure callback.
func (h *Handler) appendServerFailure(ctx context.Context, entry audit.Entry) {
	entry.Namespace = audit.ServerScope

	store, ok := h.audit.(audit.ServerStore)
	if !ok {
		h.failAudit(ctx)

		return
	}

	if _, err := store.AppendServer(ctx, entry); err != nil {
		h.failAudit(ctx)
	}
}

// failAudit records a lost audit entry: one log line and one count on the
// failure metric through the wired callback. The line never holds error text.
func (h *Handler) failAudit(ctx context.Context) {
	faultlog.LogFault(ctx, h.logger, "audit append failed", faultlog.CauseUnclassified, "audit.append")

	if h.onAuditFailure != nil {
		h.onAuditFailure()
	}
}

// get serves GET on RulePath. An ID that is not a rule ID, and a rule of
// another namespace, answer 404.
func (h *Handler) get(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	id := trustrule.ID(r.PathValue("rule_id"))
	if id.Validate() != nil {
		h.writeError(ctx, w, trustrule.ErrNotFound)

		return
	}

	rule, err := h.rules.Get(ctx, obj.Namespace(), id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	problem.WriteJSON(ctx, h.logger, w, http.StatusOK, documentOf(&rule))
}

// delete serves DELETE on RulePath. It answers 204. A workload that matched
// the rule keeps an access token that it got earlier until the token
// expires or a person revokes it.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	if err := h.rules.Delete(ctx, h.entryOf(r, p), obj.Namespace(), trustrule.ID(r.PathValue("rule_id"))); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// entryOf returns the template of the audit entry of a change by p: who
// acted, from where and through which channel. The service fills the time,
// the namespace, the action, the object and the changes.
func (h *Handler) entryOf(r *http.Request, p identity.Principal) audit.Entry {
	return audit.Entry{
		At: h.now().UTC(), Namespace: "", Subject: p.Subject, IDPSubject: "", TokenID: p.TokenID,
		Action: "", Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: clientip.String(r), UserAgent: audit.TruncateUserAgent(apidesc.Header(r, "User-Agent")),
		Channel: sessionauth.AuditChannel(r, channelHeader), Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: requestid.FromContext(r.Context()), ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: string(p.Kind), JobID: "", Reason: "",
	}
}

// readCreate decodes the body of a create. It refuses an unknown field, a
// body that is not one object, a required field that is missing or null, and
// a value of the wrong type. The checks of a rule run in the service and in
// the store.
func readCreate(w http.ResponseWriter, r *http.Request) (trustrulesvc.Input, error) {
	var in createRequest
	if err := route.DecodeBody(w, r, maxBodyBytes, &in); err != nil {
		return trustrulesvc.Input{}, fmt.Errorf("trust rule body: %w", err)
	}

	if in.Name == nil || in.Issuer == nil || in.Audience == nil || in.KeySource == nil || in.Principal == nil ||
		in.Conditions == nil {
		return trustrulesvc.Input{}, problem.BadRequest(errcode.RequestBodyInvalid,
			"the body must hold name, issuer, audience, keySource, principal and conditions")
	}

	out := trustrulesvc.Input{
		Name: *in.Name, Issuer: *in.Issuer, Audience: *in.Audience, Keys: *in.KeySource, StaticKeys: "",
		PrincipalID: *in.Principal, Conditions: make([]trustrule.Condition, 0, len(*in.Conditions)),
	}

	if in.StaticKeys != nil {
		out.StaticKeys = *in.StaticKeys
	}

	for _, c := range *in.Conditions {
		out.Conditions = append(out.Conditions, trustrule.Condition{Claim: c.Claim, Values: c.Values})
	}

	return out, nil
}

// documentOf returns the wire form of rule, with no null list.
func documentOf(rule *trustrule.Rule) ruleDocument {
	doc := ruleDocument{
		CreatedAt: rule.CreatedAt.UTC(), ID: string(rule.ID), Namespace: rule.Namespace.String(), Name: rule.Name,
		Issuer: rule.Issuer, Audience: rule.Audience, KeySource: rule.Keys, StaticKeys: rule.StaticKeys,
		Principal: rule.PrincipalID, CreatedBy: rule.CreatedBy,
		Conditions: make([]conditionDocument, 0, len(rule.Conditions)),
	}

	for _, c := range rule.Conditions {
		doc.Conditions = append(doc.Conditions, conditionDocument{
			Claim: c.Claim, Values: append(make([]string, 0, len(c.Values)), c.Values...),
		})
	}

	return doc
}

// writeError answers err through the shared error module of the tool-neutral
// APIs, with the classes of the trust rules.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: keepDenied, Classify: classify}.Write(ctx, w, err)
}

// keepDenied maps the classes of a denial that keep their own text, as
// problem.Writer.KeepDenied does. A create of a rule runs the reach compare of
// a token, so a refusal of that compare answers the same coded 403 with the
// same text as the token route.
func keepDenied(err error) (problem.Problem, bool) {
	if errors.Is(err, accesstoken.ErrReachExceeded) {
		// The trust rule service returns the refusal of the reach compare
		// unwrapped, so this text is the text of the token route. It names
		// the principal and the actions of the principal that the caller
		// does not hold, and no object that the caller cannot read.
		return problem.Built(http.StatusForbidden, err.Error(), errcode.Forbidden(err), errcode.Answer{}, nil), true
	}

	return problem.Internal(), false
}

// classify maps the errors of the trust rule routes to an answer that is safe
// to show callers. The shared module answers the rest, among them a denial,
// the unknown namespace, a fenced write and the maintenance mode.
func classify(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, trustrulesvc.ErrNoMatch):
		return problem.New(http.StatusUnauthorized, errcode.TrustRuleNoMatch, trustrulesvc.ErrNoMatch.Error()), true
	case errors.Is(err, trustrulesvc.ErrAmbiguous):
		return problem.New(http.StatusConflict, errcode.TrustRuleAmbiguous, trustrulesvc.ErrAmbiguous.Error()), true
	case errors.Is(err, trustrulesvc.ErrSignInRequired):
		return problem.New(http.StatusForbidden, errcode.TrustRuleSignInRequired,
			trustrulesvc.ErrSignInRequired.Error()), true
	case errors.Is(err, trustrule.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.TrustRuleNotFound, trustrule.ErrNotFound.Error()), true
	case errors.Is(err, trustrule.ErrExists):
		return problem.New(http.StatusConflict, errcode.TrustRuleAlreadyExists,
			"the namespace already has a trust rule with this name"), true
	case errors.Is(err, trustrule.ErrLimit):
		return problem.New(http.StatusConflict, errcode.TrustRuleLimit,
			fmt.Sprintf("the namespace holds %d trust rules: delete one first", trustrule.MaxPerNamespace)), true
	case errors.Is(err, trustrule.ErrIssuerRefused):
		return problem.RefusedText(
			problem.Detail(err, trustrule.ErrIssuerRefused), http.StatusBadRequest, errcode.TrustRuleIssuerRefused,
		), true
	case errors.Is(err, trustrule.ErrPrincipal):
		return problem.RefusedText(
			problem.Detail(err, trustrule.ErrPrincipal), http.StatusBadRequest, errcode.TrustRulePrincipalRefused,
		), true
	case errors.Is(err, trustrule.ErrInvalid):
		return problem.RefusedText(
			problem.Detail(err, trustrule.ErrInvalid), http.StatusBadRequest, errcode.TrustRuleInvalid,
		), true
	}

	// Not an error of the trust rules: package problem answers it.
	return problem.Pass()
}
