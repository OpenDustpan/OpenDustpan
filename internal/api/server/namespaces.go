package server

// Namespace route of the installation API (ADR 39): create a namespace.
//
// POST /api/server/namespaces creates a namespace homed in the region of
// the server. The bootstrap admin token calls it on an empty installation
// for the first namespace. It needs the action server:create-namespace on
// the installation (ADR 52). A creator with a stored principal holds the
// namespace-admin role on the new namespace: the grant and the namespace
// commit in one transaction, and the grant writes an audit entry.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errInvalidNamespaceBody reports a namespace create body that is not valid JSON
// with a name. classify maps it to 400.
var errInvalidNamespaceBody = errors.New(`invalid namespace body: provide {"name":"..."}`)

// maxNamespaceBodyBytes bounds a namespace create body the resolver reads
// before the handler. A name holds at most 100 characters, so this is ample.
const maxNamespaceBodyBytes = 8192

// createNamespaceRequest is the create body: the name of the namespace.
type createNamespaceRequest struct {
	Name string `json:"name"`
}

// createNamespaceResponse answers a created namespace: its name, home region,
// failover version and creation time. Times are UTC in RFC 3339 with
// nanoseconds, the wire form of backup times.
//
// Contract: never remove or rename these fields. The command and the console
// read only this API, and they update on their own schedule.
type createNamespaceResponse struct {
	Name            string `json:"name"`
	HomeRegion      string `json:"homeRegion"`
	FailoverVersion int64  `json:"failoverVersion"`
	CreatedAt       string `json:"createdAt"`
}

// decodeNamespaceName reads body as a create request and answers the parsed
// namespace name.
func decodeNamespaceName(body []byte) (namespace.Name, error) {
	var req createNamespaceRequest

	if err := json.Unmarshal(body, &req); err != nil {
		return "", errInvalidNamespaceBody
	}

	name, err := namespace.ParseName(req.Name)
	if err != nil {
		return "", err
	}

	return name, nil
}

// resolveCreateNamespace returns the object the create acts on: the
// installation. It reads the body with http.MaxBytesReader and restores
// r.Body before returning, so the handler can read it again. A missing or
// bad name fails before authorization runs.
func resolveCreateNamespace(w http.ResponseWriter, r *http.Request) (identity.Object, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxNamespaceBodyBytes))
	if err != nil {
		return identity.Object{}, errInvalidNamespaceBody
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	if _, err := decodeNamespaceName(body); err != nil {
		return identity.Object{}, err
	}

	return identity.ServerObject(), nil
}

// createNamespace serves POST /api/server/namespaces: it creates a namespace
// homed in the region of the server and answers 201 with it. A duplicate
// answers 409. A bad name answers 400.
func (h *Handler) createNamespace(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxNamespaceBodyBytes))
	if err != nil {
		h.writeError(ctx, w, errInvalidNamespaceBody)

		return
	}

	name, err := decodeNamespaceName(body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ns, err := h.storeNamespace(r, p, name)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, createNamespaceResponse{
		Name:            ns.Name.String(),
		HomeRegion:      ns.HomeRegion.String(),
		FailoverVersion: ns.FailoverVersion,
		CreatedAt:       ns.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// storeNamespace stores the namespace name. With an access service, the
// namespace, the namespace-admin grant of the creator and the audit entry
// commit together.
func (h *Handler) storeNamespace(
	r *http.Request,
	p identity.Principal,
	name namespace.Name,
) (namespace.Namespace, error) {
	if h.access == nil {
		ns, err := h.namespaces.Create(r.Context(), name)
		if err != nil {
			return namespace.Namespace{}, fmt.Errorf("create namespace: %w", err)
		}

		return ns, nil
	}

	ns := h.namespaces.New(name)

	e := callerEntry(r, p, identity.ActionServerCreateNamespace)
	if _, err := h.access.CreateNamespace(r.Context(), e, p, ns); err != nil {
		return namespace.Namespace{}, fmt.Errorf("create namespace: %w", err)
	}

	return ns, nil
}
