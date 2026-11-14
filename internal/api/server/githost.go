package server

// Routes of the Git host connections. The operator of the installation
// registers the GitHub App and assigns each of its installations to a
// namespace (the installation routes). A namespace administrator makes a
// connection to an installation that the operator assigned, and checks it (the
// namespace routes).
//
//   - server:read-git-host and server:manage-git-host guard the registration
//     and the map, on the object @server,
//   - namespace:read-git-host-connections and
//     namespace:manage-git-host-connections guard the connections, on the
//     namespace of the path.
//
// No answer holds a private key, an event secret or a token: a read says that a
// secret is set, with its set ID and its creation time. A connection of the
// installation-level app takes no key, no event secret, no API address and no
// app ID: the registration of the operator owns them.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Paths of the Git host routes.
const (
	gitHostAppPath           = "/api/dustpan/v1/git-host-apps/{kind}"
	gitHostInstallationsPath = "/api/dustpan/v1/git-host-installations"
	gitHostInstallationPath  = "/api/dustpan/v1/git-host-installations/{installation_id}"
	gitHostConnectionsPath   = "/api/dustpan/v1/namespaces/{namespace}/git-host-connections"
	gitHostConnectionPath    = gitHostConnectionsPath + "/{id}"
	gitHostDefaultPath       = gitHostConnectionPath + "/make-default"
	gitHostCheckPath         = gitHostConnectionPath + "/check"
)

// maxGitHostBodyBytes bounds the body of a Git host route. A private key of an
// app has about 3 KiB, so this is ample.
const maxGitHostBodyBytes = 64 * 1024

// Errors of the Git host routes that no domain sentinel carries.
var (
	errGitHostAppBody = errors.New(`invalid GitHub App body: send {"appID":"<ID of the app>",` +
		`"apiAddress":"https://api.github.com","privateKey":"<PEM text>","eventSecret":"<at least 32 bytes>"}`)
	errGitHostInstallationBody = errors.New(`invalid installation body: send {"namespace":"<namespace>"}`)
	errGitHostConnectionBody   = errors.New(
		`invalid connection body: send {"kind":"github","auth":"app","installationID":"<ID>","push":false}`,
	)
	errGitHostOperatorOwns = errors.New(
		"a connection of the installation-level GitHub App takes no key, no event secret, no API address and no " +
			"app ID: the operator sets them when the operator registers the app",
	)
	errGitHostKind = errors.New("the Git host kind in the path is not supported: the kind is github")
)

// gitHostCoded are the errors with a stable machine code.
var (
	errGitHostIdempotencyKey = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestIdempotencyKeyUnsupported, args: nil,
		message: "this route does not take the Idempotency-Key header: send the request without it",
	}
	errGitHostIdempotencyOff = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestIdempotencyKeyUnavailable, args: nil,
		message: "the Idempotency-Key header is not available on this server: send the request without it",
	}
	errGitHostNoRegistration = &codedError{
		status: http.StatusConflict, code: errcode.GitHostAppNotRegistered, args: nil,
		message: "the operator has not registered the GitHub App: ask the operator to register it",
	}
	errGitHostNotAssigned = &codedError{
		status: http.StatusConflict, code: errcode.GitHostInstallationNotAssigned, args: nil,
		message: "the operator did not assign this installation to this namespace: ask the operator to assign it",
	}
)

// gitHostRoutes returns the Git host rows of the route table. A handler with
// no Git host service mounts no route.
func (h *Handler) gitHostRoutes() []Route {
	if h.gitHost == nil {
		return nil
	}

	return append(h.gitHostInstallationRoutes(), h.gitHostNamespaceRoutes()...)
}

func (h *Handler) gitHostInstallationRoutes() []Route {
	read := []identity.Action{identity.ActionServerReadGitHost}
	manage := []identity.Action{identity.ActionServerManageGitHost}

	row := func(method, path string, actions []identity.Action, serve RouteHandler) Route {
		return Route{
			Method: method, Path: path, Scope: ScopeAuthorized, Actions: actions, Resolve: resolveServer,
			ActionsFor: nil, OpaqueDenial: false, Serve: serve,
		}
	}

	return []Route{
		row(http.MethodGet, gitHostAppPath, read, h.readGitHostApp),
		row(http.MethodPut, gitHostAppPath, manage, h.putGitHostApp),
		row(http.MethodDelete, gitHostAppPath, manage, h.deleteGitHostApp),
		row(http.MethodGet, gitHostInstallationsPath, read, h.listGitHostInstallations),
		row(http.MethodGet, gitHostInstallationPath, read, h.readGitHostInstallation),
		row(http.MethodPut, gitHostInstallationPath, manage, h.putGitHostInstallation),
		row(http.MethodDelete, gitHostInstallationPath, manage, h.deleteGitHostInstallation),
	}
}

func (h *Handler) gitHostNamespaceRoutes() []Route {
	read := []identity.Action{identity.ActionNamespaceReadGitHostConnections}
	manage := []identity.Action{identity.ActionNamespaceManageGitHostConnections}

	row := func(method, path string, actions []identity.Action, serve RouteHandler) Route {
		return Route{
			Method: method, Path: path, Scope: ScopeAuthorized, Actions: actions, Resolve: resolveNamespacePath,
			ActionsFor: nil, OpaqueDenial: false, Serve: serve,
		}
	}

	return []Route{
		row(http.MethodGet, gitHostConnectionsPath, read, h.listGitHostConnections),
		row(http.MethodPost, gitHostConnectionsPath, manage, h.createGitHostConnection),
		row(http.MethodGet, gitHostConnectionPath, read, h.readGitHostConnection),
		row(http.MethodDelete, gitHostConnectionPath, manage, h.deleteGitHostConnection),
		row(http.MethodPost, gitHostDefaultPath, manage, h.makeGitHostDefault),
		row(http.MethodPost, gitHostCheckPath, manage, h.checkGitHostConnection),
	}
}

// keyedCall is a write that may carry the Idempotency-Key header.
type keyedCall struct {
	// build makes the answer from the result of the write, for the store that
	// keeps the answer in the transaction of the write. serve writes the same
	// answer.
	build func(result any) (idempotency.Answer, error)
	// serve runs the write and writes its answer.
	serve http.HandlerFunc
}

// keyed runs the call. A request with the Idempotency-Key header runs inside the
// middleware, which keeps the answer in the transaction of the write. The
// caller has authorized the request already: a replay never skips that check.
func (h *Handler) keyed(w http.ResponseWriter, r *http.Request, p identity.Principal, call keyedCall) {
	if !idempotent.Present(r) {
		call.serve(w, r)

		return
	}

	if h.idem == nil {
		h.writeError(r.Context(), w, errGitHostIdempotencyOff)

		return
	}

	err := h.idem.Handle(w, r, idempotent.Call{
		Build: call.build, SecretAnswer: nil, Serve: call.serve, Principal: p.Subject, Required: false,
	})
	if err != nil {
		h.writeError(r.Context(), w, err)
	}
}

// classifyGitHost maps the errors of the Git host routes to a status and a
// message that holds no credential and no address. An error that wraps a
// refusal of the Git host answers a fixed sentence, because the chain can name
// an internal step.
func classifyGitHost(err error) (failure, bool) {
	for _, try := range []func(error) (failure, bool){classifyGitHostConflict, classifyGitHostRequest} {
		if f, ok := try(err); ok {
			return f, true
		}
	}

	return fail(0, ""), false
}

func classifyGitHostConflict(err error) (failure, bool) {
	switch {
	case errors.Is(err, forge.ErrInstallationNotAssigned):
		return fail(http.StatusConflict, forge.ErrInstallationNotAssigned.Error()), true
	case errors.Is(err, forge.ErrAddressChanged):
		return fail(http.StatusConflict, "the address of the GitHub App changed after its key was stored: "+
			"ask the operator to register the app again"), true
	case errors.Is(err, forge.ErrKeyNotOpened):
		return fail(http.StatusConflict, "the server cannot open the stored key of the GitHub App: "+
			"ask the operator to register the app again"), true
	case errors.Is(err, forge.ErrAddressRefused):
		return fail(http.StatusConflict, "the address rules of the server refuse the API address of the GitHub App: "+
			"ask the operator to change the address or the allowed ranges"), true
	case errors.Is(err, forge.ErrConnectionExists), errors.Is(err, forge.ErrConnectionLimit),
		errors.Is(err, forge.ErrDefaultConnection), errors.Is(err, forge.ErrRegistrationInUse):
		return fail(http.StatusConflict, rootText(err)), true
	case errors.Is(err, forge.ErrVersionConflict):
		return fail(http.StatusPreconditionFailed, forge.ErrVersionConflict.Error()), true
	default:
		return fail(0, ""), false
	}
}

func classifyGitHostRequest(err error) (failure, bool) {
	switch {
	case errors.Is(err, forge.ErrConnectionNotFound), errors.Is(err, forge.ErrAssignmentNotFound),
		errors.Is(err, forge.ErrNoRegistration):
		return fail(http.StatusNotFound, rootText(err)), true
	case errors.Is(err, forge.ErrInvalid):
		return fail(http.StatusBadRequest, invalidText(err)), true
	case errors.Is(err, errGitHostAppBody),
		errors.Is(err, errGitHostInstallationBody),
		errors.Is(err, errGitHostConnectionBody),
		errors.Is(err, errGitHostOperatorOwns),
		errors.Is(err, errGitHostKind):
		return fail(http.StatusBadRequest, err.Error()), true
	case errors.Is(err, errGitHostBodyTooLarge):
		return fail(http.StatusRequestEntityTooLarge, errGitHostBodyTooLarge.Error()), true
	case errors.Is(err, forge.ErrRateLimited):
		return fail(http.StatusTooManyRequests, "the connection was checked too often: try again later"), true
	case errors.Is(err, forge.ErrAuth):
		return fail(http.StatusUnprocessableEntity, "GitHub refused the credential of the connection"), true
	case errors.Is(err, forge.ErrUnavailable):
		return fail(http.StatusServiceUnavailable, "GitHub did not answer: try again later"), true
	default:
		return fail(0, ""), false
	}
}

// rootText returns the sentence of the domain sentinel in err, without the
// names of the steps that wrapped it.
func rootText(err error) string {
	for _, sentinel := range []error{
		forge.ErrConnectionNotFound, forge.ErrAssignmentNotFound, forge.ErrNoRegistration,
		forge.ErrConnectionExists, forge.ErrConnectionLimit, forge.ErrDefaultConnection, forge.ErrRegistrationInUse,
	} {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}

	return "request refused"
}

// invalidText returns the sentence of the domain sentinel in err and the
// reason of the check that refused the input, without the names of the steps
// that wrapped them. The reason follows the last sentence in the chain.
func invalidText(err error) string {
	sentinel := forge.ErrInvalid.Error()

	if i := strings.LastIndex(err.Error(), sentinel); i >= 0 {
		return err.Error()[i:]
	}

	return sentinel
}

// maxRetryAfterSeconds caps the Retry-After header of the Git host routes. A
// Git host can ask for a wait of an hour, and a client must come back sooner to
// see the state of the connection.
const maxRetryAfterSeconds = 60

// retryAfterSeconds returns the Retry-After value of an ErrUnavailable or an
// ErrRateLimited that holds a wait time, in whole seconds rounded up and not
// above maxRetryAfterSeconds, and false when it holds none.
func retryAfterSeconds(err error) (string, bool) {
	after, ok := forge.RetryAfter(err)
	if !ok {
		after, ok = forge.RateLimitWait(err)
	}

	if !ok {
		return "", false
	}

	seconds := int64((after + time.Second - 1) / time.Second)

	return strconv.FormatInt(min(max(seconds, 1), maxRetryAfterSeconds), 10), true
}
