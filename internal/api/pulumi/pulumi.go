// Package pulumi serves the HTTP API that the Pulumi CLI speaks to its backend
// (the "service" or "httpstate" backend). The web console uses the same API.
//
// Besides login, stack management and the update cycle, it serves the masked
// state read of ADR 41: GET
// /api/stacks/{namespace}/{project}/{stack}/state/{version}/resources (a
// paged resource list with a changed marker per resource, where {version} is
// a stack version or "latest"), GET .../state/{version}/resources/{urn} (one
// resource with masked inputs and outputs), GET
// .../state/{version}/outputs (the masked stack outputs) and GET
// .../state/diff?from=<version>&to=<version> (the added, removed and changed
// resources of two versions with their changed value paths). Each secret
// value answers as a marker with its value path, never as plain or
// ciphertext; the diff answers no value at all, only URNs and value paths.
// POST .../state/{version}/reveal answers the plain value at one masked
// value path, with an audit entry of the kind reveal. Clients path-escape
// the URN in the one-resource route, so that "/" in a resource name becomes
// "%2F" and the URN stays one segment.
//
// Only the endpoints needed to log in and manage stacks exist so far. The CLI's
// API is not a published, versioned specification, so every endpoint here must
// be covered by the end-to-end suite that drives real CLI versions.
package pulumi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// ErrInvalidCutoff reports a Config with a negative checkpoint delta cutoff.
// Zero turns the delta capability off, so only negative values fail.
var ErrInvalidCutoff = errors.New("checkpoint delta cutoff must not be negative")

// errInvalidBody reports a request body that is not valid JSON. classify maps
// it to the 400 response the Pulumi CLI expects.
var errInvalidBody = errors.New(invalidBodyMessage)

const (
	maxBodyBytes = 1 << 20
	tokenScheme  = "token "
	// invalidBodyMessage is the response text for a request body that is not
	// valid JSON.
	invalidBodyMessage = "invalid JSON body"
	// decryptFailedMessage is the fixed response text for a decryption
	// failure. It carries no scope and no key-encryption key fingerprint:
	// the detail goes to the server log only.
	decryptFailedMessage = "Cannot decrypt the value. The ciphertext does not belong to this stack, or the key changed."
	// hasResourcesMessage is the response text when a delete without
	// force meets a stack that still manages resources. The Pulumi CLI
	// recognizes exactly this message and reports that removal is refused.
	hasResourcesMessage = "Bad Request: Stack still contains resources."
	// unitTool names this API's tool in unit references.
	unitTool = "pulumi"
)

// Config holds the dependencies of a Handler.
type Config struct {
	Stacks        *stack.Service
	Updates       *stack.Updates
	Secrets       *secret.Service
	Namespaces    *namespace.Service
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Logger        *slog.Logger
	// BaseURL reports the external base URL for a request, for absolute
	// URLs. The zero value derives it from each request.
	BaseURL baseurl.Resolver
	// MaxBodyBytesInFlight bounds concurrent large bodies by bytes held at
	// once. Zero means DefaultMaxBodyBytesInFlight. Ignored when Gate is set.
	MaxBodyBytesInFlight int64
	// Gate bounds concurrent large bodies by bytes held at once. Nil builds
	// one from MaxBodyBytesInFlight.
	Gate *bodygate.Gate
	// Audit is the audit store that the decrypt audit routes append to. Nil
	// means no audit store is configured: the routes still answer 204.
	Audit audit.Store
	// OnAuditFailure runs when an audit append fails. The server wires it
	// to the dustpan_audit_append_failures_total counter. Nil means no
	// counter: the routes still answer 204.
	OnAuditFailure func()
	// CheckpointDeltaCutoff is the checkpoint size in bytes from which the
	// CLI sends delta uploads. Zero turns the delta capability off.
	CheckpointDeltaCutoff int
	// DisablePulumiJournaling turns journaling off. ADR 43 item 2 reads it when
	// the version gate answers the journal version. Until that item lands,
	// every update answers journalVersion 0.
	DisablePulumiJournaling bool
}

// Handler serves the Pulumi CLI API under /api/.
type Handler struct {
	stacks         *stack.Service
	updates        *stack.Updates
	secrets        *secret.Service
	namespaces     *namespace.Service
	authn          identity.Authenticator
	authz          identity.Authorizer
	logger         *slog.Logger
	baseURL        baseurl.Resolver
	gate           *bodygate.Gate
	audit          audit.Store
	onAuditFailure func()
	mux            *patternMux
	routes         []Route
	// deltaCutoff is the advertised delta checkpoint cutoff in bytes. Zero
	// means the server hides the delta capability.
	deltaCutoff int
	// disablePulumiJournaling turns journaling off for every start (ADR 43,
	// decision 1.2).
	disablePulumiJournaling bool
	// supportedJournal is the highest journal version that a start may answer.
	// It is maxSupportedJournalVersion in production.
	supportedJournal int
}

// NewHandler returns a Handler. Every Config field is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Stacks == nil:
		return nil, fmt.Errorf("%w: stack service", ErrMissingDependency)
	case cfg.Updates == nil:
		return nil, fmt.Errorf("%w: update service", ErrMissingDependency)
	case cfg.Secrets == nil:
		return nil, fmt.Errorf("%w: secret service", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.CheckpointDeltaCutoff < 0:
		return nil, fmt.Errorf("%w: %d", ErrInvalidCutoff, cfg.CheckpointDeltaCutoff)
	}

	h := &Handler{
		stacks:         cfg.Stacks,
		updates:        cfg.Updates,
		secrets:        cfg.Secrets,
		namespaces:     cfg.Namespaces,
		authn:          cfg.Authenticator,
		authz:          cfg.Authorizer,
		logger:         cfg.Logger,
		baseURL:        cfg.BaseURL,
		gate:           cfg.Gate,
		audit:          cfg.Audit,
		onAuditFailure: cfg.OnAuditFailure,
		mux:            newPatternMux(),
		routes:         nil,
		deltaCutoff:    cfg.CheckpointDeltaCutoff,

		disablePulumiJournaling: cfg.DisablePulumiJournaling,
		supportedJournal:        maxSupportedJournalVersion,
	}

	if h.gate == nil {
		h.gate = bodygate.New(cfg.MaxBodyBytesInFlight)
	}

	h.routes = h.buildRoutes()
	for _, route := range h.routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// ExternalBaseURL returns the external base URL for r: the --public-url
// value when one is set, else the scheme and host of the request.
func (h *Handler) ExternalBaseURL(r *http.Request) string {
	return h.baseURL.BaseURL(r)
}

type apiCapabilityConfig struct {
	Capability string `json:"capability"`
	// Version names the capability version the CLI reads. The delta
	// checkpoint capability needs version 2; empty means the capability
	// needs none.
	Version int `json:"version,omitempty"`
	// Configuration holds the capability settings the CLI reads, such as
	// the delta checkpoint cutoff. Empty means the capability needs none.
	Configuration json.RawMessage `json:"configuration,omitempty"`
}

type capabilitiesResponse struct {
	Capabilities []apiCapabilityConfig `json:"capabilities"`
}

// batchEncryptionCapability advertises that the server encrypts and decrypts
// secrets in batches. The CLI reads it as batch-encrypt and batches its
// secrets calls when it sees it, falling back to one value per call when it
// does not.
const batchEncryptionCapability = "batch-encrypt"

// deltaCheckpointCapability advertises that the server takes verbatim and
// delta checkpoint uploads. The CLI reads it as delta-checkpoint-uploads-v2
// and sends deltas once both the last saved and the new deployment reach
// the cutoff.
const deltaCheckpointCapability = "delta-checkpoint-uploads-v2"

// deltaCheckpointVersion is the capability version the CLI reads for delta
// checkpoint uploads. The CLI ignores the capability without it.
const deltaCheckpointVersion = 2

// DefaultCheckpointDeltaCutoff is the checkpoint size in bytes from which
// the CLI sends delta uploads unless configured otherwise. Zero turns the
// delta capability off.
const DefaultCheckpointDeltaCutoff = 1 << 20

// deltaCheckpointConfig is the configuration of the delta checkpoint
// capability as the CLI reads it.
type deltaCheckpointConfig struct {
	CheckpointCutoffSizeBytes int `json:"checkpointCutoffSizeBytes"`
}

func (h *Handler) capabilities(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	caps := []apiCapabilityConfig{{Capability: batchEncryptionCapability, Version: 0, Configuration: nil}}

	if h.deltaCutoff > 0 {
		cfg, err := json.Marshal(deltaCheckpointConfig{CheckpointCutoffSizeBytes: h.deltaCutoff})
		if err == nil {
			caps = append(
				caps,
				apiCapabilityConfig{
					Capability:    deltaCheckpointCapability,
					Version:       deltaCheckpointVersion,
					Configuration: cfg,
				},
			)
		}
	}

	h.writeJSON(r.Context(), w, http.StatusOK, capabilitiesResponse{Capabilities: caps})
}

// organizationSummary is one namespace as the Pulumi CLI reads it: an
// organization with the namespace name in both name fields.
type organizationSummary struct {
	GithubLogin string `json:"githubLogin"`
	Name        string `json:"name"`
	AvatarURL   string `json:"avatarUrl"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
}

type userResponse struct {
	GithubLogin   string                `json:"githubLogin"`
	Name          string                `json:"name"`
	Organizations []organizationSummary `json:"organizations"`
}

// currentUser answers GET /api/user. Organizations are the namespaces the
// principal may read, filtered with the Authorizer.
func (h *Handler) currentUser(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	namespaces, err := h.readableNamespaces(ctx, p)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(r.Context(), w, http.StatusOK, userResponse{
		GithubLogin:   p.Subject,
		Name:          p.DisplayName,
		Organizations: namespaces,
	})
}

// defaultOrganizationResponse is the server opinion the CLI reads for the
// default organization. Messages stays empty: the server sends no notices.
type defaultOrganizationResponse struct {
	GithubLogin string `json:"GitHubLogin"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
	Messages    []any  `json:"Messages"`    //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
}

// defaultOrganization answers GET /api/user/organizations/default. It names
// the only readable namespace, or answers 404 when there is not exactly one,
// so the CLI chooses.
func (h *Handler) defaultOrganization(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	namespaces, err := h.readableNamespaces(ctx, p)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if len(namespaces) != 1 {
		h.writeError(ctx, w, fmt.Errorf("default organization: %w", namespace.ErrNotFound))

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, defaultOrganizationResponse{
		GithubLogin: namespaces[0].Name,
		Messages:    []any{},
	})
}

// readableNamespaces returns the namespaces the principal may read, ordered
// by name, as organization summaries.
func (h *Handler) readableNamespaces(
	ctx context.Context, p identity.Principal,
) ([]organizationSummary, error) {
	all, err := h.namespaces.List(ctx)
	if err != nil {
		return nil, err
	}

	out := []organizationSummary{}

	for i := range all {
		obj := identity.NamespaceObject(all[i].Name)

		if err := h.authz.Authorize(ctx, p, identity.ActionNamespaceRead, obj); err != nil {
			if errors.Is(err, identity.ErrDenied) {
				continue
			}

			return nil, err
		}

		name := all[i].Name.String()
		out = append(out, organizationSummary{GithubLogin: name, Name: name, AvatarURL: ""})
	}

	return out, nil
}

type stackSummary struct {
	OrgName       string `json:"orgName"`
	ProjectName   string `json:"projectName"`
	StackName     string `json:"stackName"`
	LastUpdate    int64  `json:"lastUpdate,omitempty"`
	ResourceCount int    `json:"resourceCount,omitempty"`
}

type listStacksResponse struct {
	Stacks            []stackSummary `json:"stacks"`
	ContinuationToken *string        `json:"continuationToken"`
}

// listStacks answers GET /api/user/stacks. With an organization it lists the
// stacks in that namespace. Without one it lists the stacks in every readable
// namespace, ordered by namespace, project, then stack name. Paging stays
// trivial: every answer holds all stacks and no continuation token.
func (h *Handler) listStacks(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	var project stack.ProjectName
	if raw := r.URL.Query().Get("project"); raw != "" {
		var err error
		if project, err = stack.ParseProjectName(raw); err != nil {
			h.writeError(ctx, w, err)

			return
		}
	}

	if r.URL.Query().Get("organization") != "" {
		h.listStacksIn(ctx, w, p, obj.Namespace(), project)

		return
	}

	h.listStacksAll(ctx, w, p, project)
}

// listStacksIn lists the stacks in one namespace. The caller must hold
// unit:read and namespace:read on the namespace, as on a route that
// declares them.
func (h *Handler) listStacksIn(
	ctx context.Context, w http.ResponseWriter, p identity.Principal, ns namespace.Name, project stack.ProjectName,
) {
	for _, action := range []identity.Action{identity.ActionUnitRead, identity.ActionNamespaceRead} {
		if err := h.authz.Authorize(ctx, p, action, identity.NamespaceObject(ns)); err != nil {
			h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", action, err))

			return
		}
	}

	if _, err := h.namespaces.Get(ctx, ns); err != nil {
		h.writeNamespaceNotFound(ctx, w, p, ns, err)

		return
	}

	stacks, err := h.stacks.List(ctx, stack.Filter{Namespace: ns, Project: project})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, listStacksResponse{
		Stacks:            summarize(stacks),
		ContinuationToken: nil,
	})
}

// listStacksAll lists the stacks in every readable namespace.
func (h *Handler) listStacksAll(
	ctx context.Context, w http.ResponseWriter, p identity.Principal, project stack.ProjectName,
) {
	all, err := h.namespaces.List(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := []stackSummary{}

	for i := range all {
		obj := identity.NamespaceObject(all[i].Name)

		if err := h.authz.Authorize(ctx, p, identity.ActionNamespaceRead, obj); err != nil {
			if errors.Is(err, identity.ErrDenied) {
				continue
			}

			h.writeError(ctx, w, err)

			return
		}

		stacks, err := h.stacks.List(ctx, stack.Filter{Namespace: all[i].Name, Project: project})
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		out = append(out, summarize(stacks)...)
	}

	h.writeJSON(ctx, w, http.StatusOK, listStacksResponse{Stacks: out, ContinuationToken: nil})
}

// summarize converts stacks to the wire shape, never returning nil.
func summarize(stacks []stack.Stack) []stackSummary {
	out := make([]stackSummary, 0, len(stacks))

	for i := range stacks {
		out = append(out, stackSummary{
			OrgName:       stacks[i].Ref.Namespace.String(),
			ProjectName:   stacks[i].Ref.Project.String(),
			StackName:     stacks[i].Ref.Stack.String(),
			LastUpdate:    stacks[i].UpdatedAt.Unix(),
			ResourceCount: 0,
		})
	}

	return out
}

// writeNamespaceNotFound answers a request that names a missing namespace.
// When the name equals the subject of the principal, the CLI likely used its
// fallback, so the error names the command that sets the default
// organization.
func (h *Handler) writeNamespaceNotFound(
	ctx context.Context, w http.ResponseWriter, p identity.Principal, ns namespace.Name, err error,
) {
	if errors.Is(err, namespace.ErrNotFound) && ns.String() == p.Subject {
		h.writeJSON(ctx, w, http.StatusNotFound, errorResponse{
			Code: http.StatusNotFound,
			Message: fmt.Sprintf(
				"Namespace %q does not exist. Run \"pulumi org set-default %s\" to set the default organization.",
				ns.String(),
				ns.String(),
			),
		})

		return
	}

	h.writeError(ctx, w, err)
}

type createStackRequest struct {
	Tags      map[string]string `json:"tags"`
	StackName string            `json:"stackName"`
}

type createStackResponse struct{}

func (h *Handler) createStack(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeJSON(
			ctx,
			w,
			http.StatusBadRequest,
			errorResponse{Code: http.StatusBadRequest, Message: invalidBodyMessage},
		)

		return
	}

	req, err := parseCreateStackRequest(body)
	if err != nil {
		h.writeJSON(
			ctx,
			w,
			http.StatusBadRequest,
			errorResponse{Code: http.StatusBadRequest, Message: invalidBodyMessage},
		)

		return
	}

	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), req.StackName)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, err := h.stacks.Create(ctx, ref, req.Tags); err != nil {
		h.writeNamespaceNotFound(ctx, w, p, ref.Namespace, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, createStackResponse{})
}

type updateStackTagsResponse struct{}

func (h *Handler) updateStackTags(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), r.PathValue("stack"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeJSON(
			ctx,
			w,
			http.StatusBadRequest,
			errorResponse{Code: http.StatusBadRequest, Message: invalidBodyMessage},
		)

		return
	}

	// The Pulumi CLI replaces all tags with the map it sends
	// (pkg/backend/httpstate/client/client.go UpdateStackTags): a missing key
	// is a removed tag, and an empty object clears every tag.
	var tags map[string]string
	if err := json.Unmarshal(body, &tags); err != nil {
		h.writeJSON(
			ctx,
			w,
			http.StatusBadRequest,
			errorResponse{Code: http.StatusBadRequest, Message: invalidBodyMessage},
		)

		return
	}

	if _, err := h.stacks.UpdateTags(ctx, ref, tags); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, updateStackTagsResponse{})
}

type stackResponse struct {
	Tags         map[string]string `json:"tags"`
	OrgName      string            `json:"orgName"`
	ProjectName  string            `json:"projectName"`
	StackName    string            `json:"stackName"`
	ID           string            `json:"id"`
	ActiveUpdate string            `json:"activeUpdate"`
	Version      int64             `json:"version"`
}

func (h *Handler) getStack(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), r.PathValue("stack"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	st, err := h.stacks.Get(ctx, ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The CLI finds the running update here: `pulumi cancel` reads
	// activeUpdate with GetStack and stops with an error if it is empty.
	// Only a running mutating update shows; otherwise the answer is "".
	active := ""

	if u, err := h.updates.Active(ctx, ref); err == nil {
		active = u.ID.String()
	} else if !errors.Is(err, stack.ErrUpdateNotFound) {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, stackResponse{
		Tags:         st.Tags,
		OrgName:      st.Ref.Namespace.String(),
		ProjectName:  st.Ref.Project.String(),
		StackName:    st.Ref.Stack.String(),
		ID:           st.ID.String(),
		ActiveUpdate: active,
		Version:      st.Version,
	})
}

// deleteStack soft-deletes the stack (ADR 47): the stack leaves every tool
// route and list at once, and its name is free, but its history, state and
// data key stay until the retention period ends. Without force=true the store
// refuses when the stack still manages resources, as the Pulumi CLI expects.
// The check runs inside the delete transaction, so an update that finishes
// between the check and the delete cannot drop resources. A delete while a
// mutating update runs answers 409. The checkpoint blobs stay, because a
// restore reads them. Each delete writes an audit entry of the kind change in
// the delete transaction: if the append fails, the delete rolls back and the
// route answers 500 (ADR 60, fail closed).
func (h *Handler) deleteStack(
	w http.ResponseWriter, r *http.Request, principal identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), r.PathValue("stack"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	entry := changeEntry(ctx, changeAudit{
		request:   r,
		principal: principal,
		object:    obj,
		action:    identity.ActionUnitManage,
	})

	if deleteForce(r) {
		err = h.stacks.ForceDelete(ctx, ref, entry)
	} else {
		err = h.stacks.Delete(ctx, ref, entry)
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// deleteForce reports whether the request asks to delete a stack that still
// manages resources, as the Pulumi CLI sends it: force=true.
func deleteForce(r *http.Request) bool {
	v := r.URL.Query().Get("force")

	return strings.EqualFold(v, "true") || v == "1"
}

// renameStackRequest is the body the Pulumi CLI sends to rename a stack: the
// new stack name and the new project name. An empty value keeps the current
// one.
type renameStackRequest struct {
	NewName    string `json:"newName"`
	NewProject string `json:"newProject"`
}

// parseRenameStackRequest decodes a stack rename body: the whole body must be
// one JSON value.
func parseRenameStackRequest(body []byte) (renameStackRequest, error) {
	var req renameStackRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return renameStackRequest{}, errInvalidBody
	}

	return req, nil
}

type renameStackResponse struct{}

func (h *Handler) renameStack(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	oldRef, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), r.PathValue("stack"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeJSON(
			ctx,
			w,
			http.StatusBadRequest,
			errorResponse{Code: http.StatusBadRequest, Message: invalidBodyMessage},
		)

		return
	}

	req, err := parseRenameStackRequest(body)
	if err != nil {
		h.writeJSON(
			ctx,
			w,
			http.StatusBadRequest,
			errorResponse{Code: http.StatusBadRequest, Message: invalidBodyMessage},
		)

		return
	}

	if req.NewProject != "" && req.NewProject != oldRef.Project.String() {
		h.writeError(
			ctx,
			w,
			fmt.Errorf("%w: renaming the project to %q", stack.ErrInvalidName, req.NewProject),
		)

		return
	}

	newName := req.NewName
	if newName == "" {
		newName = oldRef.Stack.String()
	}

	name, err := stack.ParseName(newName)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if err := h.authorizeRenameDestination(ctx, p, oldRef, name); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, err := h.stacks.Rename(ctx, oldRef, name); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, renameStackResponse{})
}

// authorizeRenameDestination authorizes unit:manage on the destination unit
// of a rename: the same namespace and Pulumi project under the new stack
// name. The wrapper already authorized the source unit in the path.
func (h *Handler) authorizeRenameDestination(
	ctx context.Context, p identity.Principal, oldRef stack.Ref, name stack.Name,
) error {
	dest := stack.Ref{Namespace: oldRef.Namespace, Project: oldRef.Project, Stack: name}

	destObj, err := unitObject(dest)
	if err != nil {
		return err
	}

	if err := h.authz.Authorize(ctx, p, identity.ActionUnitManage, destObj); err != nil {
		return fmt.Errorf("authorize %s: %w", identity.ActionUnitManage, err)
	}

	return nil
}

// unitObject maps a stack reference to its tool-neutral unit reference.
func unitObject(ref stack.Ref) (identity.Object, error) {
	return identity.UnitObject(unitTool, ref.Namespace, ref.Project.String()+"/"+ref.Stack.String())
}

// authenticate resolves the "Authorization: token <t>" header to a principal.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), tokenScheme)
	if !ok || raw == "" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return h.authn.AuthenticateToken(r.Context(), raw)
}

// errorResponse is the error body the Pulumi CLI expects.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	status, message := classify(err)

	if status == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text
		// may hold driver detail, a token, or state (ADR 32 decision 7).
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", status))
	} else if errors.Is(err, secret.ErrDecrypt) {
		h.logger.WarnContext(ctx, "decrypt failed", slog.String("error", err.Error()))
	}

	h.writeJSON(ctx, w, status, errorResponse{Code: status, Message: message})
}

// classify maps domain errors to an HTTP status and a message safe to show callers.
// Today only the secret package produces key-encryption key fingerprints
// (builtin- IDs in its wrapper-mismatch detail), and that detail leaves
// through ErrDecrypt, which classifySecret answers with the fixed message
// below; the leak tests in secrets_test.go guard this. The remaining
// err.Error() branches echo request names, counts, or backend detail that
// holds no fingerprint; scopes there are the request's own names, not key
// material.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated), errors.Is(err, token.ErrInvalid):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, identity.ErrDenied):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, errInvalidBody):
		return http.StatusBadRequest, invalidBodyMessage
	case errors.Is(err, namespace.ErrInvalidName), errors.Is(err, stack.ErrInvalidName),
		errors.Is(err, identity.ErrInvalidObject), errors.Is(err, stack.ErrInvalidUpdate):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, namespace.ErrNotFound), errors.Is(err, stack.ErrNotFound),
		errors.Is(err, stack.ErrUpdateNotFound), errors.Is(err, stack.ErrNoState),
		errors.Is(err, stack.ErrResourceNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, stack.ErrHasResources):
		return http.StatusBadRequest, hasResourcesMessage
	case errors.Is(err, stack.ErrAlreadyExists), errors.Is(err, namespace.ErrFenced),
		errors.Is(err, stack.ErrOtherSecretsProvider):
		return http.StatusConflict, err.Error()
	case errors.Is(err, namespace.ErrNotHome):
		return http.StatusMisdirectedRequest, err.Error()
	case errors.Is(err, secret.ErrTooLarge),
		errors.Is(err, secret.ErrDecrypt),
		errors.Is(err, secret.ErrKeyNotConfigured):
		return classifySecret(err)
	default:
		return classifyUpdate(err)
	}
}

// classifySecret maps the secrets errors to an HTTP status and a message
// safe to show callers: a missing key-encryption key is a 500 that names the
// setting only, an oversized batch is a 413, a decryption failure a 400 with
// a fixed message and no detail (ADR 23). A missing key wraps both ErrDecrypt
// and ErrKeyNotConfigured, so the key check runs before the decrypt check.
func classifySecret(err error) (int, string) {
	switch {
	case errors.Is(err, secret.ErrKeyNotConfigured):
		return http.StatusInternalServerError, secret.ErrKeyNotConfigured.Error()
	case errors.Is(err, secret.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, err.Error()
	case errors.Is(err, secret.ErrDecrypt):
		return http.StatusBadRequest, decryptFailedMessage
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

// classifyUpdate maps the errors of the update cycle to an HTTP status and a
// message safe to show callers.
func classifyUpdate(err error) (int, string) {
	switch {
	case errors.Is(err, stack.ErrUpdateConflict), errors.Is(err, stack.ErrWrongUpdateState),
		errors.Is(err, stack.ErrConcurrentModification), errors.Is(err, stack.ErrInvalidDelta),
		errors.Is(err, stack.ErrCheckpointConflict), errors.Is(err, stack.ErrStaleCheckpoint),
		errors.Is(err, stack.ErrJournalConflict):
		return http.StatusConflict, err.Error()
	case errors.Is(err, stack.ErrJournalBatch):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, stack.ErrLeaseInvalid), errors.Is(err, stack.ErrLeaseExpired):
		return http.StatusUnauthorized, err.Error()
	case errors.Is(err, stack.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, err.Error()
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
