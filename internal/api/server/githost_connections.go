package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// gitHostConnectionDocument is a connection on the wire. apiAddress, appID and
// hookID come from the registration of the operator and are empty when the
// installation has no registration. credentialSets is always empty: a
// connection of the installation-level app holds no credential of its own.
//
// Contract: never remove or rename these fields.
type gitHostConnectionDocument struct {
	ID             string   `json:"id"`
	Namespace      string   `json:"namespace"`
	Kind           string   `json:"kind"`
	Auth           string   `json:"auth"`
	AppScope       string   `json:"appScope"`
	InstallationID string   `json:"installationID"`
	AppID          string   `json:"appID"`
	APIAddress     string   `json:"apiAddress"`
	HookID         string   `json:"hookID"`
	CredentialSets []string `json:"credentialSets"`
	CreatedAt      string   `json:"createdAt"`
	UpdatedAt      string   `json:"updatedAt"`
	Version        int64    `json:"version"`
	Push           bool     `json:"push"`
	Default        bool     `json:"default"`
}

// gitHostConnectionsDocument is the list of the connections of a namespace,
// oldest first.
//
// Contract: never remove or rename the connections field.
type gitHostConnectionsDocument struct {
	Connections []gitHostConnectionDocument `json:"connections"`
}

// gitHostConnectionRequest is the body of a create. The fields that the
// operator owns are in the struct only so that a request that holds one fails
// with a sentence that says so, and not with the sentence of an unknown field.
// Each of them holds the JSON value as it came, because a field that the
// request held with the value null must fail like a field that holds a value.
type gitHostConnectionRequest struct {
	Kind           *string       `json:"kind"`
	Auth           *string       `json:"auth"`
	InstallationID *string       `json:"installationID"`
	Push           *bool         `json:"push"`
	APIAddress     operatorOwned `json:"apiAddress"`
	AppID          operatorOwned `json:"appID"`
	PrivateKey     operatorOwned `json:"privateKey"`
	EventSecret    operatorOwned `json:"eventSecret"`
	Credential     operatorOwned `json:"credential"`
	Credentials    operatorOwned `json:"credentials"`
}

// operatorOwned holds the JSON value of a field that only the operator sets.
// It has its own type so that the API description can say that a request that
// holds the field is refused.
type operatorOwned []byte

// UnmarshalJSON keeps a copy of the value as it came. The decoder calls it for
// the value null too, so a field that the request held is never nil.
func (o *operatorOwned) UnmarshalJSON(b []byte) error {
	if err := (*json.RawMessage)(o).UnmarshalJSON(b); err != nil {
		return fmt.Errorf("an operator field: %w", err)
	}

	return nil
}

// ownsFields reports whether the request holds a field that only the operator
// sets. The decoder leaves an absent field nil and keeps the four bytes of
// null for a field that the request held with the value null, so a field that
// the request held is never nil.
func (q *gitHostConnectionRequest) ownsFields() bool {
	return q.APIAddress != nil || q.AppID != nil || q.PrivateKey != nil || q.EventSecret != nil ||
		q.Credential != nil || q.Credentials != nil
}

// gitHostCheckDocument is the answer of a check: the account that the credential
// reaches, the capabilities of the connection, and the capabilities that each
// consumer needs and the connection does not have.
//
// Contract: never remove or rename these fields.
type gitHostCheckDocument struct {
	Account      gitHostAccountDocument `json:"account"`
	Capabilities []string               `json:"capabilities"`
	Missing      map[string][]string    `json:"missing"`
}

type gitHostAccountDocument struct {
	ID        string   `json:"id"`
	Login     string   `json:"login"`
	ExpiresAt *string  `json:"expiresAt"`
	Scopes    []string `json:"scopes"`
	NoExpiry  bool     `json:"noExpiry"`
}

func gitHostConnectionOf(v *forge.ConnectionView) gitHostConnectionDocument {
	rec := &v.Record

	return gitHostConnectionDocument{
		ID: rec.ID, Namespace: rec.Namespace.String(), Kind: string(rec.Kind), Auth: string(rec.Auth),
		AppScope: string(rec.Scope), InstallationID: rec.InstallationID, AppID: v.AppID, APIAddress: v.BaseURL,
		HookID: v.HookID, CredentialSets: []string{}, CreatedAt: wireTime(rec.CreatedAt),
		UpdatedAt: wireTime(rec.UpdatedAt), Version: rec.Version, Push: rec.Push, Default: rec.Default,
	}
}

func gitHostConnectionLocation(rec *forge.ConnectionRecord) string {
	return "/api/dustpan/v1/namespaces/" + url.PathEscape(rec.Namespace.String()) + "/git-host-connections/" +
		url.PathEscape(rec.ID)
}

// gitHostCheckOf returns the wire form of a check. Lists are never null.
func gitHostCheckOf(res *forge.CheckResult) gitHostCheckDocument {
	acc := res.Account

	doc := gitHostCheckDocument{
		Account: gitHostAccountDocument{
			ID: acc.ID, Login: acc.Login, ExpiresAt: optionalWireTime(acc.ExpiresAt),
			Scopes: append([]string{}, acc.Scopes...), NoExpiry: acc.NoExpiry,
		},
		Capabilities: make([]string, 0, len(res.Capabilities)),
		Missing:      make(map[string][]string, len(res.Needs)),
	}

	for _, c := range res.Capabilities {
		doc.Capabilities = append(doc.Capabilities, string(c))
	}

	for _, need := range res.Needs {
		missing := make([]string, 0, len(need.Missing))
		for _, c := range need.Missing {
			missing = append(missing, string(c))
		}

		doc.Missing[string(need.Consumer)] = missing
	}

	return doc
}

// connectionAnswerOf returns the function that makes the answer of a write of
// a connection. The answer holds the values that the registration gave when the
// request began, so the stored answer and the sent answer are one. status is
// the status of the answer.
func connectionAnswerOf(reg *forge.RegistrationView, status int) func(any) (idempotency.Answer, error) {
	return func(result any) (idempotency.Answer, error) {
		rec, ok := result.(forge.ConnectionRecord)
		if !ok {
			return idempotency.Answer{}, errGitHostResult
		}

		view := forge.ConnectionView{Record: rec, AppID: "", BaseURL: "", HookID: ""}
		if reg != nil && reg.Kind == rec.Kind {
			view.AppID, view.BaseURL, view.HookID = reg.AppID, reg.BaseURL, reg.HookID
		}

		location := ""
		if status == http.StatusCreated {
			location = gitHostConnectionLocation(&rec)
		}

		return jsonAnswer(status, rec.Version, gitHostConnectionOf(&view), location)
	}
}

// registrationForAnswer reads the registration that a connection answer shows.
// A missing registration shows empty values: the create fails then.
func (h *Handler) registrationForAnswer(r *http.Request) *forge.RegistrationView {
	reg, err := h.gitHost.GetRegistration(r.Context(), forge.KindGitHub)
	if err != nil {
		return nil
	}

	return &reg
}

// listGitHostConnections serves GET .../git-host-connections.
func (h *Handler) listGitHostConnections(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	if _, err := h.namespaces.Get(ctx, obj.Namespace()); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	list, err := h.gitHost.ListConnections(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := gitHostConnectionsDocument{Connections: make([]gitHostConnectionDocument, 0, len(list))}

	for i := range list {
		// Defense in depth: the store lists the connections of the namespace
		// only. This check stays so that a store that lists another one cannot
		// show it to this namespace.
		if list[i].Record.Namespace != obj.Namespace() {
			continue
		}

		out.Connections = append(out.Connections, gitHostConnectionOf(&list[i]))
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, out)
}

// readGitHostConnection serves GET .../git-host-connections/{id}.
func (h *Handler) readGitHostConnection(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	if _, err := h.namespaces.Get(ctx, obj.Namespace()); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	view, err := h.gitHost.GetConnection(ctx, obj.Namespace(), r.PathValue("id"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: see listGitHostConnections.
	if view.Record.Namespace != obj.Namespace() || view.Record.ID != r.PathValue("id") {
		h.writeError(ctx, w, forge.ErrConnectionNotFound)

		return
	}

	w.Header().Set("ETag", etag(view.Record.Version))
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, gitHostConnectionOf(&view))
}

// createGitHostConnection serves POST .../git-host-connections. It answers 201
// with the connection. The first connection of a namespace is the default.
func (h *Handler) createGitHostConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	reg := h.registrationForAnswer(r)
	build := connectionAnswerOf(reg, http.StatusCreated)

	h.keyed(w, r, p, keyedCall{
		build: build,
		serve: func(w http.ResponseWriter, r *http.Request) { h.makeGitHostConnection(w, r, p, obj.Namespace(), build) },
	})
}

// makeGitHostConnection makes the connection and writes the answer that build
// makes, which is the answer that the store keeps for a keyed request.
func (h *Handler) makeGitHostConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, ns namespace.Name,
	build func(any) (idempotency.Answer, error),
) {
	view, err := h.createConnection(w, r, p, ns)
	if err != nil {
		// The create names no connection: the route made none.
		h.failGitHostConnection(w, r, p, connectionChange{ns: ns, verb: "create", id: ""}, err)

		return
	}

	h.writeBuilt(w, r, build, view.Record)
}

// writeBuilt writes the answer that build makes for result.
func (h *Handler) writeBuilt(
	w http.ResponseWriter, r *http.Request, build func(any) (idempotency.Answer, error), result any,
) {
	answer, err := build(result)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	writeAnswer(w, answer)
}

// createConnection reads the body and makes the connection. A field that only
// the operator sets fails with 400, and so does an auth kind that is not the
// app. The errors of the registration and of the map get the status that the
// namespace administrator can act on.
func (h *Handler) createConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, ns namespace.Name,
) (forge.ConnectionView, error) {
	var none forge.ConnectionView

	var req gitHostConnectionRequest
	if err := readGitHostBody(w, r, &req, errGitHostConnectionBody); err != nil {
		return none, err
	}

	if req.ownsFields() {
		return none, errGitHostOperatorOwns
	}

	if req.Kind == nil || req.Auth == nil || req.InstallationID == nil {
		return none, errGitHostConnectionBody
	}

	in := forge.ConnectionInput{
		Kind: forge.Kind(*req.Kind), Auth: forge.AuthKind(*req.Auth), InstallationID: *req.InstallationID,
		Push: valueOr(req.Push, false),
	}

	view, err := h.gitHost.CreateConnection(
		r.Context(), callerEntry(r, p, identity.ActionNamespaceManageGitHostConnections), ns, in,
	)
	switch {
	case errors.Is(err, forge.ErrNoRegistration):
		return none, errGitHostNoRegistration
	case errors.Is(err, forge.ErrInstallationNotAssigned):
		return none, errGitHostNotAssigned
	case err != nil:
		return none, err
	default:
		return view, nil
	}
}

// deleteGitHostConnection serves DELETE .../git-host-connections/{id}. It
// answers 204. The default connection answers 409 while another one exists.
func (h *Handler) deleteGitHostConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	h.keyed(w, r, p, keyedCall{
		build: noContentAnswer,
		serve: func(w http.ResponseWriter, r *http.Request) { h.removeGitHostConnection(w, r, p, obj.Namespace()) },
	})
}

func (h *Handler) removeGitHostConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, ns namespace.Name,
) {
	id := r.PathValue("id")

	version, _, err := ifMatch(r)
	if err == nil {
		err = h.gitHost.DeleteConnection(
			r.Context(), callerEntry(r, p, identity.ActionNamespaceManageGitHostConnections), ns, id, version,
		)
	}

	if err != nil {
		h.failGitHostConnection(w, r, p, connectionChange{ns: ns, verb: "delete", id: id}, err)

		return
	}

	writeAnswer(w, idempotency.Answer{Status: http.StatusNoContent, Headers: map[string]string{}, Body: nil})
}

// makeGitHostDefault serves POST .../git-host-connections/{id}/make-default. It
// answers 200 with the connection.
func (h *Handler) makeGitHostDefault(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	reg := h.registrationForAnswer(r)
	build := connectionAnswerOf(reg, http.StatusOK)

	h.keyed(w, r, p, keyedCall{
		build: build,
		serve: func(w http.ResponseWriter, r *http.Request) { h.setGitHostDefault(w, r, p, obj.Namespace(), build) },
	})
}

func (h *Handler) setGitHostDefault(
	w http.ResponseWriter, r *http.Request, p identity.Principal, ns namespace.Name,
	build func(any) (idempotency.Answer, error),
) {
	version, _, err := ifMatch(r)
	if err != nil {
		h.failGitHostConnection(w, r, p, connectionChange{ns: ns, verb: "make-default", id: r.PathValue("id")}, err)

		return
	}

	view, err := h.gitHost.MakeDefault(
		r.Context(), callerEntry(r, p, identity.ActionNamespaceManageGitHostConnections), ns, r.PathValue("id"),
		version,
	)
	if err != nil {
		h.failGitHostConnection(w, r, p, connectionChange{ns: ns, verb: "make-default", id: r.PathValue("id")}, err)

		return
	}

	h.writeBuilt(w, r, build, view.Record)
}

// checkGitHostConnection serves POST .../git-host-connections/{id}/check. It
// calls GitHub with the credential of the installation and answers the account,
// the capabilities and what each consumer misses. The route changes no stored
// value, so it takes no Idempotency-Key: a retry is safe.
func (h *Handler) checkGitHostConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	change := connectionChange{ns: obj.Namespace(), verb: "check", id: ""}

	if idempotent.Present(r) {
		h.failGitHostConnection(w, r, p, change, errGitHostIdempotencyKey)

		return
	}

	if _, err := h.namespaces.Get(ctx, obj.Namespace()); err != nil {
		h.failGitHostConnection(w, r, p, change, err)

		return
	}

	entry := callerEntry(r, p, identity.ActionNamespaceManageGitHostConnections)

	res, err := h.gitHost.Check(ctx, entry, obj.Namespace(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, forge.ErrInstallationNotAssigned) {
			// The same code and status as the create route.
			err = errGitHostNotAssigned
		}

		// The service audits a refused open and the outcome of a call to GitHub.
		// The refusals that it does not audit are written here, so that a caller
		// who hits the limit or asks for IDs that do not exist leaves a trace.
		if errors.Is(err, forge.ErrRateLimited) || errors.Is(err, forge.ErrConnectionNotFound) {
			h.failGitHostConnection(w, r, p, change, err)

			return
		}

		h.writeGitHostError(r, w, err)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, gitHostCheckOf(&res))
}

// connectionChange names a change of a connection that a failure entry records:
// the namespace, the verb, and the ID of the connection that the change holds,
// which is empty when the change holds none.
type connectionChange struct {
	ns   namespace.Name
	verb string
	id   string
}

// failGitHostConnection writes the failure entry of a refused change of a
// connection, and answers err. The entry names the connection by a fixed
// object of the namespace, and by the ID of the change when it holds one that
// the server could have made. A create holds none.
func (h *Handler) failGitHostConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, c connectionChange, err error,
) {
	object := "git-host-connection:" + c.verb

	// Defense in depth: the ID comes from the path, which is not trusted. Only
	// an ID of the shape that the server makes reaches the audit log, so the
	// log never holds other text of a request.
	if forge.ValidConnectionID(c.id) {
		object += ":" + c.id
	}

	h.appendObjectChange(r, p, identity.NamespaceObject(c.ns), serverChange{
		at: h.now(), action: identity.ActionNamespaceManageGitHostConnections,
		object: object, outcome: audit.OutcomeFailure,
	})
	h.writeGitHostError(r, w, err)
}
