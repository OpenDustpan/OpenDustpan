// Package pulumi serves the HTTP API that the Pulumi CLI speaks to its backend
// (the "service" or "httpstate" backend). The web console uses the same API.
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

	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// errInvalidBody reports a request body that is not valid JSON. classify maps
// it to the 400 response the Pulumi CLI expects.
var errInvalidBody = errors.New(invalidBodyMessage)

const (
	maxBodyBytes = 1 << 20
	tokenScheme  = "token "
	// invalidBodyMessage is the response text for a request body that is not
	// valid JSON.
	invalidBodyMessage = "invalid JSON body"
	// unitTool names this API's tool in unit references.
	unitTool = "pulumi"
)

// Config holds the dependencies of a Handler.
type Config struct {
	Stacks        *stack.Service
	Updates       *stack.Updates
	Namespaces    *namespace.Service
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Logger        *slog.Logger
}

// Handler serves the Pulumi CLI API under /api/.
type Handler struct {
	stacks     *stack.Service
	updates    *stack.Updates
	namespaces *namespace.Service
	authn      identity.Authenticator
	authz      identity.Authorizer
	logger     *slog.Logger
	mux        *patternMux
	routes     []Route
}

// NewHandler returns a Handler. Every Config field is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Stacks == nil:
		return nil, fmt.Errorf("%w: stack service", ErrMissingDependency)
	case cfg.Updates == nil:
		return nil, fmt.Errorf("%w: update service", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	h := &Handler{
		stacks:     cfg.Stacks,
		updates:    cfg.Updates,
		namespaces: cfg.Namespaces,
		authn:      cfg.Authenticator,
		authz:      cfg.Authorizer,
		logger:     cfg.Logger,
		mux:        newPatternMux(),
		routes:     nil,
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

type capabilitiesResponse struct {
	Capabilities []string `json:"capabilities"`
}

func (h *Handler) capabilities(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	h.writeJSON(r.Context(), w, http.StatusOK, capabilitiesResponse{Capabilities: []string{}})
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

// listStacksIn lists the stacks in one namespace.
func (h *Handler) listStacksIn(
	ctx context.Context, w http.ResponseWriter, p identity.Principal, ns namespace.Name, project stack.ProjectName,
) {
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

	h.writeJSON(ctx, w, http.StatusOK, stackResponse{
		Tags:         st.Tags,
		OrgName:      st.Ref.Namespace.String(),
		ProjectName:  st.Ref.Project.String(),
		StackName:    st.Ref.Stack.String(),
		ID:           st.ID.String(),
		ActiveUpdate: "",
		Version:      st.Version,
	})
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

func (h *Handler) renameStack(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
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

	if _, err := h.stacks.Rename(ctx, oldRef, name); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, renameStackResponse{})
}

// unitObject maps a stack reference to its tool-neutral unit reference.
func unitObject(ref stack.Ref) (identity.Object, error) {
	return identity.UnitObject(unitTool, ref.Namespace, ref.Project.String()+"/"+ref.Stack.String())
}

// authenticate resolves the "Authorization: token <t>" header to a principal.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), tokenScheme)
	if !ok || token == "" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return h.authn.AuthenticateToken(r.Context(), token)
}

// errorResponse is the error body the Pulumi CLI expects.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	status, message := classify(err)
	if status == http.StatusInternalServerError {
		h.logger.ErrorContext(ctx, "request failed", slog.String("error", err.Error()))
	}

	h.writeJSON(ctx, w, status, errorResponse{Code: status, Message: message})
}

// classify maps domain errors to an HTTP status and a message safe to show callers.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, identity.ErrDenied):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, errInvalidBody):
		return http.StatusBadRequest, invalidBodyMessage
	case errors.Is(err, namespace.ErrInvalidName), errors.Is(err, stack.ErrInvalidName),
		errors.Is(err, identity.ErrInvalidObject):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, namespace.ErrNotFound), errors.Is(err, stack.ErrNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, stack.ErrAlreadyExists), errors.Is(err, namespace.ErrFenced):
		return http.StatusConflict, err.Error()
	case errors.Is(err, namespace.ErrNotHome):
		return http.StatusMisdirectedRequest, err.Error()
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
