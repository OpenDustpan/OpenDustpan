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
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	maxBodyBytes = 1 << 20
	tokenScheme  = "token "
)

// Config holds the dependencies of a Handler.
type Config struct {
	Stacks        *stack.Service
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Logger        *slog.Logger
}

// Handler serves the Pulumi CLI API under /api/.
type Handler struct {
	stacks *stack.Service
	authn  identity.Authenticator
	authz  identity.Authorizer
	logger *slog.Logger
	mux    *http.ServeMux
}

// NewHandler returns a Handler. Every Config field is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Stacks == nil:
		return nil, fmt.Errorf("%w: stack service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	h := &Handler{
		stacks: cfg.Stacks,
		authn:  cfg.Authenticator,
		authz:  cfg.Authorizer,
		logger: cfg.Logger,
		mux:    http.NewServeMux(),
	}

	h.mux.HandleFunc("GET /api/capabilities", h.capabilities)
	h.mux.HandleFunc("GET /api/user", h.authenticated(h.currentUser))
	h.mux.HandleFunc("GET /api/user/stacks", h.authenticated(h.listStacks))
	h.mux.HandleFunc("POST /api/stacks/{namespace}/{project}", h.authenticated(h.createStack))
	h.mux.HandleFunc("GET /api/stacks/{namespace}/{project}/{stack}", h.authenticated(h.getStack))

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

type capabilitiesResponse struct {
	Capabilities []string `json:"capabilities"`
}

func (h *Handler) capabilities(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(r.Context(), w, http.StatusOK, capabilitiesResponse{Capabilities: []string{}})
}

type userResponse struct {
	GithubLogin   string   `json:"githubLogin"`
	Name          string   `json:"name"`
	Organizations []string `json:"organizations"`
}

func (h *Handler) currentUser(w http.ResponseWriter, r *http.Request, p identity.Principal) {
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

func (h *Handler) listStacks(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()

	ns, err := namespace.ParseName(r.URL.Query().Get("organization"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var project stack.ProjectName
	if raw := r.URL.Query().Get("project"); raw != "" {
		if project, err = stack.ParseProjectName(raw); err != nil {
			h.writeError(ctx, w, err)

			return
		}
	}

	if !h.authorize(ctx, w, p, identity.ActionStackRead, ns) {
		return
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

func (h *Handler) createStack(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()

	var req createStackRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		h.writeJSON(
			ctx,
			w,
			http.StatusBadRequest,
			errorResponse{Code: http.StatusBadRequest, Message: "invalid JSON body"},
		)

		return
	}

	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), req.StackName)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !h.authorize(ctx, w, p, identity.ActionStackWrite, ref.Namespace) {
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

func (h *Handler) getStack(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()

	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), r.PathValue("stack"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !h.authorize(ctx, w, p, identity.ActionStackRead, ref.Namespace) {
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

// authorize reports whether p may perform action in ns, writing the error response if not.
func (h *Handler) authorize(
	ctx context.Context,
	w http.ResponseWriter,
	p identity.Principal,
	action identity.Action,
	ns namespace.Name,
) bool {
	if err := h.authz.Authorize(ctx, p, action, ns); err != nil {
		h.writeError(ctx, w, err)

		return false
	}

	return true
}

type authenticatedHandler func(w http.ResponseWriter, r *http.Request, p identity.Principal)

// authenticated resolves the "Authorization: token <t>" header to a principal.
func (h *Handler) authenticated(next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), tokenScheme)
		if !ok || token == "" {
			h.writeError(r.Context(), w, identity.ErrUnauthenticated)

			return
		}

		p, err := h.authn.AuthenticateToken(r.Context(), token)
		if err != nil {
			h.writeError(r.Context(), w, err)

			return
		}

		next(w, r, p)
	}
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
	case errors.Is(err, namespace.ErrInvalidName), errors.Is(err, stack.ErrInvalidName):
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
