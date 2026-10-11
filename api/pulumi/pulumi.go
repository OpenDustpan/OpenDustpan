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
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Logger        *slog.Logger
}

// Handler serves the Pulumi CLI API under /api/.
type Handler struct {
	stacks  *stack.Service
	updates *stack.Updates
	authn   identity.Authenticator
	authz   identity.Authorizer
	logger  *slog.Logger
	mux     *patternMux
	routes  []Route
}

// NewHandler returns a Handler. Every Config field is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Stacks == nil:
		return nil, fmt.Errorf("%w: stack service", ErrMissingDependency)
	case cfg.Updates == nil:
		return nil, fmt.Errorf("%w: update service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	h := &Handler{
		stacks:  cfg.Stacks,
		updates: cfg.Updates,
		authn:   cfg.Authenticator,
		authz:   cfg.Authorizer,
		logger:  cfg.Logger,
		mux:     newPatternMux(),
		routes:  nil,
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

type userResponse struct {
	GithubLogin   string   `json:"githubLogin"`
	Name          string   `json:"name"`
	Organizations []string `json:"organizations"`
}

func (h *Handler) currentUser(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	h.writeJSON(r.Context(), w, http.StatusOK, userResponse{
		GithubLogin:   p.Subject,
		Name:          p.DisplayName,
		Organizations: []string{},
	})
}

type stackSummary struct {
	OrgName       string `json:"orgName"`
	ProjectName   string `json:"projectName"`
	StackName     string `json:"stackName"`
	LastUpdate    int64  `json:"lastUpdate,omitempty"`
	ResourceCount int    `json:"resourceCount,omitempty"`
}

type listStacksResponse struct {
	Stacks []stackSummary `json:"stacks"`
}

func (h *Handler) listStacks(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	ns := obj.Namespace()

	var project stack.ProjectName
	if raw := r.URL.Query().Get("project"); raw != "" {
		var err error
		if project, err = stack.ParseProjectName(raw); err != nil {
			h.writeError(ctx, w, err)

			return
		}
	}

	stacks, err := h.stacks.List(ctx, stack.Filter{Namespace: ns, Project: project})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	resp := listStacksResponse{Stacks: make([]stackSummary, 0, len(stacks))}
	for i := range stacks {
		resp.Stacks = append(resp.Stacks, stackSummary{
			OrgName:       stacks[i].Ref.Namespace.String(),
			ProjectName:   stacks[i].Ref.Project.String(),
			StackName:     stacks[i].Ref.Stack.String(),
			LastUpdate:    stacks[i].UpdatedAt.Unix(),
			ResourceCount: 0,
		})
	}

	h.writeJSON(ctx, w, http.StatusOK, resp)
}

type createStackRequest struct {
	Tags      map[string]string `json:"tags"`
	StackName string            `json:"stackName"`
}

type createStackResponse struct{}

func (h *Handler) createStack(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
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
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, createStackResponse{})
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
