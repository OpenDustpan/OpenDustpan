package server

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/jobproxysvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The routes of the proxy rule sets of a namespace and of a unit.
const (
	namespaceProxyRulesPath = "/api/dustpan/v1/namespaces/{namespace}/proxy-rules"
	unitProxyRulesPath      = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/proxy-rules"
	renderedProxyRulesPath  = unitProxyRulesPath + "/rendered"
	tagProxyRules           = "proxy-rules"
)

// maxProxyRulesBodyBytes bounds the body of a save of a proxy rule set.
const maxProxyRulesBodyBytes = 64 * 1024

// proxyRulesJSON is the one content type of a save of a proxy rule set.
const proxyRulesJSON = "application/json"

// errStoredProxyRuleSet is the answer for a stored set that no longer parses.
var errStoredProxyRuleSet = errors.New("the stored proxy rule set is not valid")

// digestPattern is the shape of the digest of a stored proxy rule set: the
// lowercase hex SHA-256 of its document.
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// The errors of the proxy rule routes that no domain sentinel carries.
var (
	errProxyRulesMediaType = &codedError{
		status: http.StatusUnsupportedMediaType, code: errcode.RequestContentTypeUnsupported, args: nil,
		message: "content type must be application/json",
	}
	errProxyRulesTooLarge = &codedError{
		status: http.StatusRequestEntityTooLarge, code: errcode.RequestBodyTooLarge,
		args:    []errcode.Value{errcode.Quantity("maxBytes", maxProxyRulesBodyBytes)},
		message: "the proxy rule set is larger than 64 KB",
	}
	errProxyRulesIfMatch = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestIfMatchInvalid, args: nil,
		message: `If-Match must be the digest of the stored proxy rule set in quotes, as the ETag of a read gives it`,
	}
	errProxyRulesUnitPath = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestInvalidPath, args: nil,
		message: "unit_id must be from 1 to 64 letters, digits, _ or -",
	}
	errProxyRulesOperation = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestInvalidQuery, args: nil,
		message: "operation must be plan, apply, destroy or refresh",
	}
)

// proxyRuleBody is one proxy rule on the wire. Port and serverTrust hold their
// effective values in an answer.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type proxyRuleBody struct {
	ID                string   `json:"id"`
	Operations        []string `json:"operations"`
	Host              string   `json:"host"`
	Port              int      `json:"port"`
	Mode              string   `json:"mode"`
	Methods           []string `json:"methods,omitempty"`
	Paths             []string `json:"paths,omitempty"`
	Credential        string   `json:"credential,omitempty"`
	ClientCertificate string   `json:"clientCertificate,omitempty"`
	ServerTrust       string   `json:"serverTrust"`
}

// proxyRuleSetRequest is the body of a save: the proxy rules of one scope.
type proxyRuleSetRequest struct {
	Rules []proxyRuleBody `json:"rules"`
}

// proxyRuleSetBody is the stored proxy rule set of one scope. A scope with no
// set has no rules, an empty digest and version 0.
type proxyRuleSetBody struct {
	Rules     []proxyRuleBody `json:"rules"`
	Digest    string          `json:"digest"`
	Version   int64           `json:"version"`
	UpdatedAt int64           `json:"updatedAt"`
}

// joinedProxyRuleBody is one proxy rule of a joined set with its scope.
type joinedProxyRuleBody struct {
	// Scope is namespace or unit.
	Scope string        `json:"scope"`
	Rule  proxyRuleBody `json:"rule"`
}

// unitProxyRulesBody is the answer of the read of a unit: the set of the unit
// and the joined set of the namespace and the unit, in this order.
type unitProxyRulesBody struct {
	Rules     []proxyRuleBody       `json:"rules"`
	Digest    string                `json:"digest"`
	Version   int64                 `json:"version"`
	UpdatedAt int64                 `json:"updatedAt"`
	Joined    []joinedProxyRuleBody `json:"joined"`
}

// renderedProxyRulesBody is the debug view of the joined set for one
// operation.
type renderedProxyRulesBody struct {
	Operation string                `json:"operation"`
	Rules     []joinedProxyRuleBody `json:"rules"`
}

// proxyRuleOperationLabels names the proxy rule routes in the description.
func proxyRuleOperationLabels() map[string]apidesc.Label {
	rule := apidesc.Named[proxyRuleBody]("ProxyRule")
	joined := apidesc.Named[joinedProxyRuleBody]("JoinedProxyRule")
	proxyRuleSetSchema := apidesc.Typed[proxyRuleSetBody]("ProxyRuleSet", rule)
	proxyRulesRequest := apidesc.Typed[proxyRuleSetRequest]("SaveProxyRuleSetRequest", rule)
	unitProxyRulesSchema := apidesc.Typed[unitProxyRulesBody]("UnitProxyRules", rule, joined)
	renderedProxyRulesSchema := apidesc.Typed[renderedProxyRulesBody]("RenderedProxyRules", rule, joined)

	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, namespaceProxyRulesPath): apidesc.Name("getNamespaceProxyRules", tagProxyRules).
			WithETag().
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, proxyRuleSetSchema),
		apidesc.Pattern(http.MethodPut, namespaceProxyRulesPath): apidesc.Name("saveNamespaceProxyRules", tagProxyRules).
			WithIfMatch().
			WithETag().
			On(apidesc.KindNamespace).
			WithBodies(proxyRulesRequest, proxyRuleSetSchema),
		apidesc.Pattern(http.MethodGet, unitProxyRulesPath): apidesc.Name("getUnitProxyRules", tagProxyRules).
			WithETag().
			On(apidesc.KindUnit).
			WithBodies(apidesc.NoBody, unitProxyRulesSchema),
		apidesc.Pattern(http.MethodPut, unitProxyRulesPath): apidesc.Name("saveUnitProxyRules", tagProxyRules).
			WithIfMatch().
			WithETag().
			On(apidesc.KindUnit).
			WithBodies(proxyRulesRequest, proxyRuleSetSchema),
		apidesc.Pattern(http.MethodGet, renderedProxyRulesPath): apidesc.Name("getRenderedProxyRules", tagProxyRules).
			On(apidesc.KindUnit).
			WithQuery(apidesc.StringParam("operation", "The operation to render: plan, apply, destroy or refresh.")).
			WithBodies(apidesc.NoBody, renderedProxyRulesSchema),
	}
}

// proxyRuleRoutes returns the rows of the proxy rule sets. Nil ProxyRules
// means the store keeps none: no route is mounted. A read needs a read action
// and a save needs the manage action of its scope, and a fresh sign-in.
func (h *Handler) proxyRuleRoutes() []Route {
	if h.proxyRules == nil {
		return nil
	}

	nsRead := []identity.Action{identity.ActionNamespaceRead}
	nsManage := []identity.Action{identity.ActionNamespaceManageProxyRules}
	unitRead := []identity.Action{identity.ActionUnitRead}
	unitManage := []identity.Action{identity.ActionUnitManageProxyRules}

	unitRow := func(method, path string, actions []identity.Action, serve RouteHandler) Route {
		return Route{
			Method: method, Path: path, Scope: ScopeAuthorized, Actions: actions,
			Resolve: h.resolveProxyUnit, ActionsFor: nil, OpaqueDenial: false, Serve: serve,
		}
	}

	return []Route{
		authorizedRoute(http.MethodGet, namespaceProxyRulesPath, nsRead, resolveNamespacePath,
			h.readNamespaceProxyRules),
		authorizedRoute(http.MethodPut, namespaceProxyRulesPath, nsManage, resolveNamespacePath,
			h.fresh(identity.ActionNamespaceManageProxyRules, h.saveNamespaceProxyRules)),
		unitRow(http.MethodGet, unitProxyRulesPath, unitRead, h.readUnitProxyRules),
		unitRow(http.MethodPut, unitProxyRulesPath, unitManage,
			h.fresh(identity.ActionUnitManageProxyRules, h.saveUnitProxyRules)),
		unitRow(http.MethodGet, renderedProxyRulesPath, unitRead, h.readRenderedProxyRules),
	}
}

// resolveProxyUnit returns the unit that the unit ID of the path names, as the
// object of the route. It checks the read of the namespace first, so a caller
// that cannot read the namespace learns nothing about its units.
func (h *Handler) resolveProxyUnit(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, err
	}

	unitID := r.PathValue("unit_id")
	scopeErr := rulestore.ValidScope(rulestore.Scope{Namespace: ns, UnitID: unitID})
	if scopeErr != nil || unitID == "" {
		return identity.Object{}, errProxyRulesUnitPath
	}

	p, ok := principalFromRequest(r)
	if !ok {
		return identity.Object{}, identity.ErrUnauthenticated
	}

	ctx := r.Context()
	authErr := h.guard.Authorize(ctx, r, p, identity.ActionNamespaceRead, identity.NamespaceObject(ns))
	if authErr != nil {
		return identity.Object{}, authErr
	}

	entry, err := h.proxyRules.Unit(ctx, ns, unitID)
	if err != nil {
		return identity.Object{}, err
	}

	return identity.UnitObject(entry.Tool, ns, entry.Name)
}

// proxyRuleBodyOf converts a proxy rule of the domain.
func proxyRuleBodyOf(r jobproxy.Rule) proxyRuleBody {
	ops := make([]string, len(r.Operations))
	for i, op := range r.Operations {
		ops[i] = string(op)
	}

	return proxyRuleBody{
		ID: r.ID, Operations: ops, Host: r.Host, Port: r.Port, Mode: string(r.Mode), Methods: r.Methods,
		Paths: r.Paths, Credential: r.Credential, ClientCertificate: r.ClientCertificate, ServerTrust: r.ServerTrust,
	}
}

// proxyRuleSetOf converts a stored set. The stored document passed Parse on
// the save, and the service checks it again, so a document that fails here
// is a stored bad value: fail closed.
func proxyRuleSetOf(set jobproxysvc.Set) (proxyRuleSetBody, error) {
	body := proxyRuleSetBody{
		Rules: []proxyRuleBody{}, Digest: set.Digest, Version: set.Version, UpdatedAt: set.UpdatedAt,
	}

	if set.Version == 0 {
		return body, nil
	}

	parsed, err := jobproxy.Parse(set.Document)
	if err != nil {
		// The cause names a field of a stored document, not of this request, so
		// it must not answer as a 422 of the caller: the answer is a 500.
		return proxyRuleSetBody{}, errStoredProxyRuleSet
	}

	for i := range parsed.Rules {
		body.Rules = append(body.Rules, proxyRuleBodyOf(parsed.Rules[i]))
	}

	return body, nil
}

// joinedBodies converts joined entries.
func joinedBodies(entries []jobproxysvc.Entry) []joinedProxyRuleBody {
	out := make([]joinedProxyRuleBody, 0, len(entries))
	for i := range entries {
		out = append(out, joinedProxyRuleBody{Scope: entries[i].Scope, Rule: proxyRuleBodyOf(entries[i].Rule)})
	}

	return out
}

// setETag sets the ETag header: the quoted digest, when the scope has a set.
func setETag(w http.ResponseWriter, digest string) {
	if digest != "" {
		w.Header().Set("ETag", `"`+digest+`"`)
	}
}

// readNamespaceProxyRules serves GET /api/dustpan/v1/namespaces/{namespace}/proxy-rules.
func (h *Handler) readNamespaceProxyRules(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	set, err := h.proxyRules.Get(ctx, rulestore.Scope{Namespace: obj.Namespace(), UnitID: ""})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeProxyRuleSet(ctx, w, set)
}

// writeProxyRuleSet writes the answer of a read or a save of one scope.
func (h *Handler) writeProxyRuleSet(ctx context.Context, w http.ResponseWriter, set jobproxysvc.Set) {
	body, err := proxyRuleSetOf(set)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	setETag(w, set.Digest)
	h.writeJSON(ctx, w, http.StatusOK, body)
}

// unitScope returns the scope of the unit of the path.
func unitScope(r *http.Request, obj identity.Object) rulestore.Scope {
	return rulestore.Scope{Namespace: obj.Namespace(), UnitID: r.PathValue("unit_id")}
}

// readUnitProxyRules serves GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/proxy-rules.
// It answers the set of the unit and the joined set.
func (h *Handler) readUnitProxyRules(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	scope := unitScope(r, obj)

	set, err := h.proxyRules.Get(ctx, scope)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	joined, err := h.proxyRules.Joined(ctx, scope.Namespace, scope.UnitID)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	own, err := proxyRuleSetOf(set)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	setETag(w, set.Digest)
	h.writeJSON(ctx, w, http.StatusOK, unitProxyRulesBody{
		Rules: own.Rules, Digest: own.Digest, Version: own.Version, UpdatedAt: own.UpdatedAt,
		Joined: joinedBodies(joined),
	})
}

// readRenderedProxyRules serves GET .../units/{unit_id}/proxy-rules/rendered.
// It is a debug view of the joined proxy rules that allow one operation.
func (h *Handler) readRenderedProxyRules(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	op := jobproxy.Operation(queryOf(r).Get("operation"))
	if !op.Valid() {
		h.writeError(ctx, w, errProxyRulesOperation)

		return
	}

	scope := unitScope(r, obj)

	rules, err := h.proxyRules.Rendered(ctx, scope.Namespace, scope.UnitID, op)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, renderedProxyRulesBody{Operation: string(op), Rules: joinedBodies(rules)})
}

// saveNamespaceProxyRules serves PUT /api/dustpan/v1/namespaces/{namespace}/proxy-rules.
func (h *Handler) saveNamespaceProxyRules(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	h.saveProxyRules(w, r, p, obj, proxyRuleSave{
		scope:  rulestore.Scope{Namespace: obj.Namespace(), UnitID: ""},
		action: identity.ActionNamespaceManageProxyRules,
	})
}

// saveUnitProxyRules serves PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/proxy-rules.
func (h *Handler) saveUnitProxyRules(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	h.saveProxyRules(w, r, p, obj, proxyRuleSave{scope: unitScope(r, obj), action: identity.ActionUnitManageProxyRules})
}

// proxyRuleSave names the scope of a save and the action that the route declared.
type proxyRuleSave struct {
	scope  rulestore.Scope
	action identity.Action
}

// saveProxyRules reads the request, saves the document and answers the stored
// set. The digest of the If-Match header, when it is sent, must be the stored
// digest. Without it, the last write wins.
func (h *Handler) saveProxyRules(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, save proxyRuleSave,
) {
	ctx := r.Context()
	scope, action := save.scope, save.action

	doc, err := readProxyRulesBody(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ifMatch, err := proxyRulesIfMatch(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	entry := callerEntry(r, p, action)
	entry.Kind = audit.KindChange
	entry.Outcome = audit.OutcomeSuccess
	entry.Object = obj.String()

	set, err := h.proxyRules.Put(ctx, jobproxysvc.PutRequest{
		Scope: scope, Document: doc, IfMatch: ifMatch, Entry: entry,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeProxyRuleSet(ctx, w, set)
}

// readProxyRulesBody reads the document of a save: the content type is JSON
// and the size is at most maxProxyRulesBodyBytes.
func readProxyRulesBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	mediaType, _, err := mime.ParseMediaType(apidesc.Header(r, "Content-Type"))
	if err != nil || mediaType != proxyRulesJSON {
		return nil, errProxyRulesMediaType
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProxyRulesBodyBytes))
	if tooLarge, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit && tooLarge.Limit > 0 {
		return nil, errProxyRulesTooLarge
	}

	if err != nil {
		return nil, errInvalidBody
	}

	return body, nil
}

// proxyRulesIfMatch returns the digest of the If-Match header, or an empty
// string when the request sent none. The header holds one quoted digest, as
// the ETag of a read gives it. Any other value is refused.
func proxyRulesIfMatch(r *http.Request) (string, error) {
	raw := apidesc.Header(r, "If-Match")
	if raw == "" {
		return "", nil
	}

	digest := strings.Trim(raw, `"`)
	if !digestPattern.MatchString(digest) {
		return "", errProxyRulesIfMatch
	}

	return digest, nil
}

// classifyProxyRules maps the errors of the proxy rule routes to their status
// and message. The text of an invalid document names the proxy rule and the
// field, and holds no secret value.
func classifyProxyRules(err error) (failure, bool) {
	switch {
	case errors.Is(err, inventory.ErrUnitNotFound):
		return failCoded(http.StatusNotFound, "no unit has this ID in the namespace", errcode.UnitNotFound), true
	case errors.Is(err, rulestore.ErrConflict):
		return failCoded(http.StatusPreconditionFailed,
			"the proxy rule set changed since the digest of If-Match: read it again and send the save again",
			errcode.RequestPreconditionFailed), true
	case errors.Is(err, jobproxysvc.ErrOperation):
		return failCoded(http.StatusBadRequest, jobproxysvc.ErrOperation.Error(), errcode.RequestInvalidQuery), true
	case errors.Is(err, jobproxy.ErrInvalid):
		return failCoded(http.StatusUnprocessableEntity, err.Error(), errcode.RequestBodyInvalid), true
	}

	return fail(0, ""), false
}
