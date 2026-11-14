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
	"time"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/schema"
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

// AddressGate checks the client address against the IP allowlist of a
// namespace. Nil means no check.
type AddressGate interface {
	CheckAddress(ctx context.Context, ns namespace.Name) error
}

// Config holds the dependencies of a Handler.
type Config struct {
	Stacks        *stack.Service
	Updates       *stack.Updates
	Secrets       *secret.Service
	Namespaces    *namespace.Service
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Nil accepts access tokens only.
	Sessions *sessionauth.Checker
	// SessionPolicy gives the token freshness window of the session policy.
	// Nil means session.DefaultTokenFreshness.
	SessionPolicy SessionPolicy
	AddressGate   AddressGate
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
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter, so its bound on keys holds for all
	// of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	// CheckpointDeltaCutoff is the checkpoint size in bytes from which the
	// CLI sends delta uploads. Zero turns the delta capability off.
	CheckpointDeltaCutoff int
	// DisablePulumiJournaling turns journaling off. ADR 43 item 2 reads it when
	// the version gate answers the journal version. Until that item lands,
	// every update answers journalVersion 0.
	DisablePulumiJournaling bool
	// StateGuard checks each write of state at the decision point
	// state.write: an import, a restore, a rename, a delete and a cancel of a
	// stack. It is required. A server whose store keeps no gates gives the
	// guard of stategate.Off with its reason.
	StateGuard stategate.Guard
	// Placer chooses the collection of each new stack from the first placement
	// rule that matches it. Nil means the server keeps no placement rules: it
	// then refuses to make a stack.
	Placer *Placer
}

// Handler serves the Pulumi CLI API under /api/.
type Handler struct {
	stacks         *stack.Service
	updates        *stack.Updates
	secrets        *secret.Service
	namespaces     *namespace.Service
	authn          identity.Authenticator
	sessions       *sessionauth.Checker
	sessionPolicy  SessionPolicy
	authz          identity.Authorizer
	addressGate    AddressGate
	logger         *slog.Logger
	baseURL        baseurl.Resolver
	gate           *bodygate.Gate
	audit          audit.Store
	onAuditFailure func()
	// guard checks the declared actions of the routes and writes the entry of
	// each refusal.
	guard *refusal.Recorder
	// admit asks the state guard about each write of state and writes the
	// audit entry of each refusal.
	admit  *stategate.Admitter
	placer *Placer
	mux    *patternMux
	routes []Route
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

// validate returns ErrMissingDependency when cfg lacks a required dependency.
func (cfg Config) validate() error {
	switch {
	case cfg.Audit == nil:
		return fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Stacks == nil:
		return fmt.Errorf("%w: stack service", ErrMissingDependency)
	case cfg.Updates == nil:
		return fmt.Errorf("%w: update service", ErrMissingDependency)
	case cfg.Secrets == nil:
		return fmt.Errorf("%w: secret service", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.CheckpointDeltaCutoff < 0:
		return fmt.Errorf("%w: %d", ErrInvalidCutoff, cfg.CheckpointDeltaCutoff)
	}

	return nil
}

// NewHandler returns a Handler. Every Config field but Sessions is required.
// A nil StateGuard is ErrMissingDependency.
func NewHandler(cfg Config) (*Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	admit, err := stategate.New(cfg.StateGuard, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("%w: state guard: %w", ErrMissingDependency, err)
	}

	h := &Handler{
		stacks:         cfg.Stacks,
		updates:        cfg.Updates,
		secrets:        cfg.Secrets,
		namespaces:     cfg.Namespaces,
		authn:          cfg.Authenticator,
		sessions:       cfg.Sessions,
		sessionPolicy:  cfg.SessionPolicy,
		authz:          cfg.Authorizer,
		addressGate:    cfg.AddressGate,
		logger:         cfg.Logger,
		baseURL:        cfg.BaseURL,
		gate:           cfg.Gate,
		audit:          cfg.Audit,
		onAuditFailure: cfg.OnAuditFailure,
		guard:          recorder.WithLimiter(cfg.DenialLimiter).WithSurface("pulumi"),
		admit:          admit,
		placer:         cfg.Placer,
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
	// TemporaryAdministrator is true for a temporary administrator that
	// still works, so the console shows a banner for it. The Pulumi CLI
	// ignores it.
	TemporaryAdministrator bool `json:"temporaryAdministrator"`
	// ExpiresAt is the fixed end of a working temporary administrator in
	// UTC in RFC 3339 with nanoseconds, for the banner. It stays empty for
	// every other caller. The Pulumi CLI ignores it.
	ExpiresAt *string `json:"expiresAt"`
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
		GithubLogin:            p.Subject,
		Name:                   p.DisplayName,
		Organizations:          namespaces,
		TemporaryAdministrator: p.TemporaryAdministrator,
		ExpiresAt:              tempAdminExpiry(p),
	})
}

// tempAdminExpiry answers the fixed end of a working temporary
// administrator for the wire, or nil for every other caller.
func tempAdminExpiry(p identity.Principal) *string {
	if !p.TemporaryAdministrator || p.TemporaryExpiresAt.IsZero() {
		return nil
	}

	at := p.TemporaryExpiresAt.UTC().Format(time.RFC3339Nano)

	return &at
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
// namespace. The list is ordered by namespace, project, then stack name, in
// pages of stackPageSize stacks: an answer with a continuationToken has a
// next page.
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
		h.listStacksIn(w, r, p, obj.Namespace(), project)

		return
	}

	h.listStacksAll(ctx, w, r, p, project)
}

// listStacksIn lists the stacks in one namespace. The caller must hold
// unit:read and namespace:read on the namespace, as on a route that
// declares them.
func (h *Handler) listStacksIn(
	w http.ResponseWriter, r *http.Request, p identity.Principal, ns namespace.Name, project stack.ProjectName,
) {
	ctx := r.Context()

	for _, action := range []identity.Action{identity.ActionUnitRead, identity.ActionNamespaceRead} {
		// The guard writes the audit entry of a refusal.
		if err := h.guard.Authorize(ctx, r, p, action, identity.NamespaceObject(ns)); err != nil {
			h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", action, err))

			return
		}
	}

	if _, err := h.namespaces.Get(ctx, ns); err != nil {
		h.writeNamespaceNotFound(ctx, w, p, ns, err)

		return
	}

	h.serveStackPage(ctx, w, r, stackListQuery{
		organization: ns.String(), project: project, tag: nil, namespaces: []namespace.Name{ns},
	})
}

// listStacksAll lists the stacks in every namespace where p holds
// namespace:read and unit:read, the actions that listStacksIn checks for one
// namespace. So the list with no organization is the union of the lists of
// each organization. A denial skips the namespace; any other error of the
// authorizer fails the request.
func (h *Handler) listStacksAll(
	ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal, project stack.ProjectName,
) {
	all, err := h.namespaces.List(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	readable := make([]namespace.Name, 0, len(all))

	for i := range all {
		ok, err := h.mayListStacksIn(ctx, p, all[i].Name)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		if ok {
			readable = append(readable, all[i].Name)
		}
	}

	h.serveStackPage(ctx, w, r, stackListQuery{organization: "", project: project, tag: nil, namespaces: readable})
}

// mayListStacksIn reports whether p holds namespace:read and unit:read on the
// namespace ns. It returns false for a denial, and the error of the
// authorizer for any other failure.
func (h *Handler) mayListStacksIn(ctx context.Context, p identity.Principal, ns namespace.Name) (bool, error) {
	obj := identity.NamespaceObject(ns)

	for _, action := range []identity.Action{identity.ActionNamespaceRead, identity.ActionUnitRead} {
		err := h.authz.Authorize(ctx, p, action, obj)

		switch {
		case err == nil:
		case errors.Is(err, identity.ErrDenied):
			return false, nil
		default:
			return false, fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return true, nil
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
		h.writeFailure(ctx, w, coded(http.StatusNotFound, fmt.Sprintf(
			"Namespace %q does not exist. Run \"pulumi org set-default %s\" to set the default organization.",
			ns.String(),
			ns.String(),
		), errcode.NamespaceNotFound))

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
		h.writeInvalidBody(ctx, w)

		return
	}

	req, err := parseCreateStackRequest(body)
	if err != nil {
		h.writeInvalidBody(ctx, w)

		return
	}

	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), req.StackName)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	at, ok := h.choosePlacement(w, r, p, ref)
	if !ok {
		return
	}

	placed, err := stackPlacement(r, p, ref, at)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, err := h.stacks.CreatePlaced(ctx, ref, req.Tags, placed); err != nil {
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
		h.writeInvalidBody(ctx, w)

		return
	}

	// The Pulumi CLI replaces all tags with the map it sends
	// (pkg/backend/httpstate/client/client.go UpdateStackTags): a missing key
	// is a removed tag, and an empty object clears every tag.
	var tags map[string]string
	if err := json.Unmarshal(body, &tags); err != nil {
		h.writeInvalidBody(ctx, w)

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
// route answers 500 (ADR 60, fail closed). The delete meets the gates of
// state.write with the operation kind delete before its transaction.
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

	if !h.guardStateWrite(w, r, principal, obj, checkDelete()) {
		return
	}

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

// renameStack gives the stack a new name in the same project. The rename meets
// the gates of state.write of the source unit with the operation kind rename
// before its transaction.
func (h *Handler) renameStack(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	oldRef, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), r.PathValue("stack"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeInvalidBody(ctx, w)

		return
	}

	req, err := parseRenameStackRequest(body)
	if err != nil {
		h.writeInvalidBody(ctx, w)

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

	if err := h.authorizeRenameDestination(r, p, oldRef, name); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The gates of the source unit decide: the rename moves its state. The
	// destination unit has no state before the rename.
	if !h.guardStateWrite(w, r, p, obj, checkRename()) {
		return
	}

	entry := changeEntry(ctx, changeAudit{
		request:   r,
		principal: p,
		object:    obj,
		action:    identity.ActionUnitManage,
	})

	if _, err := h.stacks.Rename(ctx, oldRef, name, entry); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, renameStackResponse{})
}

// authorizeRenameDestination authorizes unit:manage on the destination unit
// of a rename: the same namespace and Pulumi project under the new stack
// name. The wrapper already authorized the source unit in the path.
func (h *Handler) authorizeRenameDestination(
	r *http.Request, p identity.Principal, oldRef stack.Ref, name stack.Name,
) error {
	dest := stack.Ref{Namespace: oldRef.Namespace, Project: oldRef.Project, Stack: name}

	destObj, err := unitObject(dest)
	if err != nil {
		return err
	}

	if err := h.guard.Authorize(r.Context(), r, p, identity.ActionUnitManage, destObj); err != nil {
		return fmt.Errorf("authorize %s: %w", identity.ActionUnitManage, err)
	}

	return nil
}

// unitObject maps a stack reference to its tool-neutral unit reference.
func unitObject(ref stack.Ref) (identity.Object, error) {
	return identity.UnitObject(unitTool, ref.Namespace, ref.Project.String()+"/"+ref.Stack.String())
}

// authenticate resolves the "Authorization: token <t>" header to a principal.
// A request with no Authorization header uses the console session cookie,
// and the bool reports that the cookie signed it in. A request with an
// Authorization header never uses the cookie.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, *sessionauth.Caller, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		p, caller, err := h.sessions.Authenticate(r)
		if err != nil {
			return p, nil, err
		}

		return p, &caller, nil
	}

	raw, ok := strings.CutPrefix(header, tokenScheme)
	if !ok || raw == "" {
		return identity.Principal{}, nil, identity.ErrUnauthenticated
	}

	p, err := h.authn.AuthenticateToken(r.Context(), raw)

	return p, nil, err
}

// errorResponse is the error body the Pulumi CLI expects.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// codedErrorResponse is errorResponse with the stable machine code of the
// error and its arguments. The fields of errorResponse keep their names and
// their meaning. An answer without a code has no errorCode and no errorArgs.
type codedErrorResponse struct {
	Message   string         `json:"message"`
	Code      int            `json:"code"`
	ErrorCode string         `json:"errorCode,omitempty"`
	ErrorArgs map[string]any `json:"errorArgs,omitempty"`
}

// failure is the answer of an error: the HTTP status, the message, and the
// code of the catalog with its answer. err is the error of errcode.New, or
// nil.
type failure struct {
	err     error
	answer  errcode.Answer
	code    string
	message string
	status  int
}

// coded returns the failure of status and message with code, a code of the
// catalog, and its arguments args.
func coded(status int, message, code string, args ...errcode.Value) failure {
	answer, err := errcode.New(code, args...)

	return failure{err: err, answer: answer, code: code, message: message, status: status}
}

// unmatched returns the failure of a classify function that does not know
// the error. Its caller gets false with it and tries the next classify
// function.
func unmatched() failure {
	return failure{err: nil, answer: errcode.Answer{}, code: "", message: "", status: 0}
}

// writeFailure writes the error body of f. When errcode.New refused the code
// of f or its arguments, which is a bug in this adapter, it answers 500 with
// server.internal and logs the code.
func (h *Handler) writeFailure(ctx context.Context, w http.ResponseWriter, f failure) {
	if f.err != nil {
		faultlog.LogInternal(ctx, h.logger, "error answer is not valid", faultlog.CauseUnclassified, f.code)
		h.writeAnswer(ctx, w, http.StatusInternalServerError, "internal error", errcode.Internal())

		return
	}

	h.writeAnswer(ctx, w, f.status, f.message, f.answer)
}

// writeAnswer writes the error body of status and message with the code and
// the arguments of answer.
func (h *Handler) writeAnswer(
	ctx context.Context, w http.ResponseWriter, status int, message string, answer errcode.Answer,
) {
	h.writeJSON(ctx, w, status, codedErrorResponse{
		Message: message, Code: status, ErrorCode: answer.Code(), ErrorArgs: answer.Args(),
	})
}

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	f := classify(err)

	if f.status == http.StatusInternalServerError {
		// The log names the status, a cause from the closed list, the request
		// ID and the code, never the error text: the text may hold driver
		// detail, a token, or state.
		faultlog.LogInternal(ctx, h.logger, "request failed", faultlog.Cause(err), f.code)
	} else {
		h.logRefusal(ctx, err)
	}

	if f.status == http.StatusServiceUnavailable && errors.Is(err, namespace.ErrUnavailable) {
		problem.SetStoreRetryAfter(w)
	}

	h.writeFailure(ctx, w, f)
}

// logRefusal logs a refusal that the operator may need to see: a value that
// does not decrypt, and a write that a newer failover version fenced.
func (h *Handler) logRefusal(ctx context.Context, err error) {
	switch {
	case errors.Is(err, secret.ErrDecrypt):
		h.logger.WarnContext(ctx, "decrypt failed", slog.String("error", err.Error()))
	case errors.Is(err, namespace.ErrFenced):
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	default:
	}
}

// classifyAuth maps the errors of the authentication and the
// authorization of a request. ok is false for any other error.
func classifyAuth(err error) (failure, bool) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return coded(http.StatusTooManyRequests, "too many failed sign-ins from this address: try again later",
			errcode.AuthRateLimited), true
	case errors.Is(err, identity.ErrUnauthenticated), errors.Is(err, token.ErrInvalid),
		errors.Is(err, stack.ErrAccessTokenUnchecked):
		return coded(http.StatusUnauthorized, "unauthenticated", errcode.AuthUnauthenticated), true
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return coded(http.StatusForbidden, err.Error(), errcode.Forbidden(err)), true
	case errors.Is(err, errSessionUpdate):
		return coded(http.StatusForbidden, err.Error(), errcode.UpdateConsoleSession), true
	case errors.Is(err, errSessionSecrets):
		return coded(http.StatusForbidden, err.Error(), errcode.SecretConsoleSession), true
	case errors.Is(err, identity.ErrDenied):
		return coded(http.StatusForbidden, "forbidden", errcode.Forbidden(err)), true
	}

	return unmatched(), false
}

// classify maps domain errors to an HTTP status and a message safe to show callers.
// Today only the secret package produces key-encryption key fingerprints
// (builtin- IDs in its wrapper-mismatch detail), and that detail leaves
// through ErrDecrypt, which classifySecret answers with the fixed message
// below; the leak tests in secrets_test.go guard this. The remaining
// err.Error() branches echo request names, counts, or backend detail that
// holds no fingerprint; scopes there are the request's own names, not key
// material.
func classify(err error) failure {
	for _, try := range []func(error) (failure, bool){
		classifyAuth, classifyNamespace, classifyRequest, classifyMinimum, classifyStack, classifyPlacement,
	} {
		if f, ok := try(err); ok {
			return f
		}
	}

	switch {
	case errors.Is(err, stack.ErrInvalidUpdate):
		return coded(http.StatusBadRequest, err.Error(), errcode.UpdateInvalid)
	case errors.Is(err, stack.ErrUpdateNotFound):
		return coded(http.StatusNotFound, err.Error(), errcode.UpdateNotFound)
	case errors.Is(err, secret.ErrTooLarge),
		errors.Is(err, secret.ErrDecrypt),
		errors.Is(err, secret.ErrKeyNotConfigured):
		return classifySecret(err)
	default:
		return classifyUpdate(err)
	}
}

// classifyMinimum maps a write that this binary is too old to make: 503 with
// the text that names both versions, and the versions as the arguments of the
// code. ok is false for any other error.
func classifyMinimum(err error) (failure, bool) {
	if errors.Is(err, schema.ErrBinaryBelowMinimum) {
		text, args := belowMinimumText(err)

		return coded(http.StatusServiceUnavailable, text, errcode.SchemaBinaryBelowMinimum, args...), true
	}

	return unmatched(), false
}

// belowMinimumText returns the message of the answer to a write that this
// binary is too old to make, and the versions as the arguments of the code.
// The message names both versions.
func belowMinimumText(err error) (string, []errcode.Value) {
	below, ok := errors.AsType[*schema.MinimumError](err)
	if !ok {
		return schema.ErrBinaryBelowMinimum.Error(), nil
	}

	return below.WriteText(), []errcode.Value{
		errcode.Identifier("binaryVersion", below.BinaryVersion),
		errcode.Identifier("minimumVersion", below.SchemaMinimum),
	}
}

// classifyNamespace maps the errors of the namespaces. ok is false for any
// other error.
func classifyNamespace(err error) (failure, bool) {
	switch {
	case errors.Is(err, namespace.ErrInvalidName):
		return coded(http.StatusBadRequest, err.Error(), errcode.NamespaceInvalidName), true
	case errors.Is(err, namespace.ErrNotFound):
		return coded(http.StatusNotFound, err.Error(), errcode.NamespaceNotFound), true
	case errors.Is(err, namespace.ErrFenced):
		// The copy of the sentinel only: a wrap adds the name of the
		// operation, which the log holds.
		return coded(http.StatusConflict, namespace.ErrFenced.Error(), errcode.NamespaceFenced), true
	case errors.Is(err, namespace.ErrNotHome):
		return coded(http.StatusMisdirectedRequest, err.Error(), errcode.NamespaceNotHome), true
	}

	return unmatched(), false
}

// classifyStack maps the errors of the stacks. ok is false for any other
// error.
func classifyStack(err error) (failure, bool) {
	switch {
	case errors.Is(err, stack.ErrInvalidName):
		return coded(http.StatusBadRequest, err.Error(), errcode.StackInvalidName), true
	case errors.Is(err, stack.ErrNotFound):
		return coded(http.StatusNotFound, err.Error(), errcode.StackNotFound), true
	case errors.Is(err, stack.ErrNoState):
		return coded(http.StatusNotFound, err.Error(), errcode.StackNoState), true
	case errors.Is(err, stack.ErrNoStateAt):
		// A stack with a first state names its time. A stack with no
		// checkpoint yet has no state at all.
		if at, ok := errors.AsType[*stack.NoStateAtError](err); ok && !at.First.IsZero() {
			return coded(http.StatusNotFound, err.Error(), errcode.StackNoStateAtTime,
				errcode.Time("firstAt", at.First)), true
		}

		return coded(http.StatusNotFound, err.Error(), errcode.StackNoState), true
	case errors.Is(err, stack.ErrResourceNotFound):
		return coded(http.StatusNotFound, err.Error(), errcode.StackResourceNotFound), true
	case errors.Is(err, stack.ErrHasResources):
		return coded(http.StatusBadRequest, hasResourcesMessage, errcode.StackHasResources), true
	case errors.Is(err, stack.ErrAlreadyExists):
		return coded(http.StatusConflict, err.Error(), errcode.StackAlreadyExists), true
	case errors.Is(err, stack.ErrOtherSecretsProvider):
		return coded(http.StatusConflict, err.Error(), errcode.StackOtherSecretsProvider), true
	}

	return unmatched(), false
}

// classifyPlacement maps the errors of the placement of a new stack. ok is
// false for any other error.
func classifyPlacement(err error) (failure, bool) {
	switch {
	case errors.Is(err, collection.ErrNoPlacement):
		return coded(http.StatusUnprocessableEntity, err.Error(), errcode.PlacementNoMatch), true
	case errors.Is(err, errPlacementOff), errors.Is(err, stack.ErrPlacementUnsupported):
		return coded(http.StatusNotImplemented, err.Error(), errcode.PlacementUnavailable), true
	case errors.Is(err, collection.ErrPlacementCollectionMissing):
		// The rule names a collection that does not exist (any more).
		return coded(http.StatusConflict, err.Error(), errcode.CollectionNotFound), true
	}

	return unmatched(), false
}

// classifyRequest maps the errors of a request that the adapter refuses
// before a domain package reads it: the body, the reveal path, the query of
// the stack list and the object reference. ok is false for any other error.
func classifyRequest(err error) (failure, bool) {
	switch {
	case errors.Is(err, errInvalidBody):
		return coded(http.StatusBadRequest, invalidBodyMessage, errcode.RequestBodyInvalid), true
	case errors.Is(err, errRevealPathTooLong):
		return coded(http.StatusBadRequest, err.Error(), errcode.StackRevealPathTooLong,
			errcode.Quantity("maxBytes", maxRevealPathLen)), true
	case errors.Is(err, errContinuationToken):
		return coded(http.StatusBadRequest, err.Error(), errcode.RequestPageTokenInvalid), true
	case errors.Is(err, errStackTagFilter):
		return coded(http.StatusBadRequest, err.Error(), errcode.RequestInvalidQuery), true
	case errors.Is(err, identity.ErrInvalidObject):
		return coded(http.StatusBadRequest, err.Error(), errcode.RequestInvalidObject), true
	}

	return unmatched(), false
}

// classifySecret maps the secrets errors to an HTTP status and a message
// safe to show callers: a missing key-encryption key is a 500 that names the
// setting only, an oversized batch is a 413, a decryption failure a 400 with
// a fixed message and no detail (ADR 23). A missing key wraps both ErrDecrypt
// and ErrKeyNotConfigured, so the key check runs before the decrypt check.
func classifySecret(err error) failure {
	switch {
	case errors.Is(err, secret.ErrKeyNotConfigured):
		return coded(http.StatusInternalServerError, secret.ErrKeyNotConfigured.Error(), errcode.SecretKeyNotConfigured)
	case errors.Is(err, secret.ErrTooLarge):
		return coded(http.StatusRequestEntityTooLarge, err.Error(), errcode.SecretTooLarge)
	case errors.Is(err, secret.ErrDecrypt):
		return coded(http.StatusBadRequest, decryptFailedMessage, errcode.SecretNotDecrypted)
	default:
		return coded(http.StatusInternalServerError, "internal error", errcode.ServerInternal)
	}
}

// updateConflictHint is the command that ends a stuck update. Each answer of
// stack.ErrUpdateConflict names it.
const updateConflictHint = "wait for it to end, or run pulumi cancel"

// updateConflictText answers a call that another update of the stack blocks:
// the sentence of stack.ErrUpdateConflict, the ID of the update that holds the
// stack when the store names it, and the command that ends a stuck update.
// The rest of the wrap names the refused update, so it is not sent to the
// caller.
func updateConflictText(err error) string {
	if conflict, ok := errors.AsType[*stack.UpdateConflictError](err); ok {
		return conflict.Error() + ": " + updateConflictHint
	}

	return stack.ErrUpdateConflict.Error() + ": " + updateConflictHint
}

// classifyUpdate maps the errors of the update cycle to an HTTP status, a
// message safe to show callers and a code. A manual lock comes first: the
// refusal of a manual lock also wraps stack.ErrUpdateConflict.
func classifyUpdate(err error) failure {
	if manual, ok := errors.AsType[*manuallock.LockedError](err); ok {
		answer, aerr := refusal.Locked(manual)

		return failure{
			err: aerr, answer: answer, code: errcode.LockLocked, message: refusal.LockedText(manual),
			status: http.StatusConflict,
		}
	}

	for _, try := range []func(error) (failure, bool){classifyUpdateState, classifyJournal, classifyAttempt, classifyItemKey} {
		if f, ok := try(err); ok {
			return f
		}
	}

	switch {
	case errors.Is(err, stack.ErrLeaseInvalid):
		return coded(http.StatusUnauthorized, err.Error(), errcode.UpdateLeaseInvalid)
	case errors.Is(err, stack.ErrLeaseExpired):
		return coded(http.StatusUnauthorized, err.Error(), errcode.UpdateLeaseExpired)
	case errors.Is(err, stack.ErrTooLarge):
		// A replay bound refusal answers its one-sentence message, which
		// names the bound, its limit and the one command that helps,
		// without the update wrapper prefixes. Any other oversized
		// request answers the error text itself.
		if msg, ok := stack.TooLargeMessage(err); ok {
			return coded(http.StatusRequestEntityTooLarge, msg, errcode.UpdateTooLarge)
		}

		return coded(http.StatusRequestEntityTooLarge, err.Error(), errcode.UpdateTooLarge)
	case errors.Is(err, stack.ErrJournalBuildTimeout):
		return coded(http.StatusServiceUnavailable, err.Error(), errcode.JournalBuildTimeout)
	case errors.Is(err, maintenance.ErrInMaintenance):
		return coded(http.StatusServiceUnavailable, maintenance.ErrInMaintenance.Error()+": try again later",
			errcode.MaintenanceInMaintenance)
	case errors.Is(err, namespace.ErrUnavailable):
		return coded(http.StatusServiceUnavailable, namespace.ErrUnavailable.Error()+": try again",
			errcode.NamespaceUnavailable)
	default:
		return coded(http.StatusInternalServerError, "internal error", errcode.ServerInternal)
	}
}

// itemKeyUnavailableText is the answer to a request of an update whose item
// key is shredded or missing. It names no key and no value.
const itemKeyUnavailableText = "The item key of this update is shredded or missing, so the server cannot " +
	"write or read its events. Run the update again."

// classifyItemKey maps the refusal of an update whose item key is shredded
// or missing. ok is false for any other error.
func classifyItemKey(err error) (failure, bool) {
	if errors.Is(err, stack.ErrItemKeyUnavailable) {
		return coded(http.StatusConflict, itemKeyUnavailableText, errcode.UpdateItemKeyUnavailable), true
	}

	return unmatched(), false
}

// classifyUpdateState maps the errors of an update that another update
// blocks, or that changed or holds another state than the request needs. ok is
// false for any other error.
func classifyUpdateState(err error) (failure, bool) {
	switch {
	case errors.Is(err, stack.ErrUpdateConflict):
		return coded(http.StatusConflict, updateConflictText(err), errcode.UpdateConflict), true
	case errors.Is(err, stack.ErrWrongUpdateState):
		return coded(http.StatusConflict, err.Error(), errcode.UpdateWrongState), true
	case errors.Is(err, stack.ErrConcurrentModification):
		return coded(http.StatusConflict, err.Error(), errcode.UpdateConcurrentModification), true
	case errors.Is(err, stack.ErrInvalidDelta):
		return coded(http.StatusConflict, err.Error(), errcode.UpdateDeltaInvalid), true
	case errors.Is(err, stack.ErrCheckpointConflict):
		return coded(http.StatusConflict, err.Error(), errcode.UpdateCheckpointConflict), true
	case errors.Is(err, stack.ErrStaleCheckpoint):
		return coded(http.StatusConflict, err.Error(), errcode.UpdateCheckpointStale), true
	}

	return unmatched(), false
}

// classifyAttempt maps the refusal of a write attempt that ran past its
// deadline. The CLI sends the request again, and the new attempt has a new
// start time. ok is false for any other error.
func classifyAttempt(err error) (failure, bool) {
	if errors.Is(err, blob.ErrAttemptExpired) {
		return coded(http.StatusServiceUnavailable, blob.ErrAttemptExpired.Error()+": try again",
			errcode.UpdateAttemptExpired), true
	}

	return unmatched(), false
}

// classifyJournal maps the errors of the update journal. ok is false for any
// other error.
func classifyJournal(err error) (failure, bool) {
	switch {
	case errors.Is(err, stack.ErrJournalConflict):
		return coded(http.StatusConflict, err.Error(), errcode.JournalConflict), true
	case errors.Is(err, stack.ErrJournalIncomplete):
		return coded(http.StatusConflict, err.Error(), errcode.JournalIncomplete), true
	case errors.Is(err, stack.ErrJournalBatch):
		return coded(http.StatusBadRequest, err.Error(), errcode.JournalBatchInvalid), true
	}

	return unmatched(), false
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
