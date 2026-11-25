// Package audit serves the tool-neutral audit API that the web console
// reads for the audit entries of one namespace. It exposes the entries of
// the audit.Store of the namespace and the result of its chain check.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone (ADR 12).
package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// errMissingOrganization reports a request without the organization query
// value. classify maps it to the 400 response the console expects.
var errMissingOrganization = errors.New("missing organization query parameter")

// Config holds the dependencies of a Handler.
type Config struct {
	Namespaces *namespace.Service
	Entries    audit.Store
	// EntryReader reads one entry by its scope and sequence. Nil answers
	// 501 on the route that reads one entry.
	EntryReader   audit.EntryReader
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// OnAuditFailure, when set, runs once for each refusal whose entry could not
	// be written, so the server can count it. Entries is the store of the
	// entries of the refusals.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter, so its bound on keys holds for all
	// of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Nil accepts access tokens only.
	Sessions *sessionauth.Checker
	Logger   *slog.Logger
}

// Handler serves the audit API under /api/audit/.
type Handler struct {
	namespaces *namespace.Service
	entries    audit.Store
	reader     audit.EntryReader
	authn      identity.Authenticator
	sessions   *sessionauth.Checker
	authz      identity.Authorizer
	guard      *refusal.Recorder
	logger     *slog.Logger
	mux        *route.Mux
	routes     []Route
	// exportLimit bounds the run time of one export.
	exportLimit time.Duration
}

// NewHandler returns a Handler. Every Config field but EntryReader and
// Sessions is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Entries == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	recorder, err := refusal.New(cfg.Authorizer, cfg.Entries, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	h := &Handler{
		namespaces:  cfg.Namespaces,
		entries:     cfg.Entries,
		reader:      cfg.EntryReader,
		authn:       cfg.Authenticator,
		sessions:    cfg.Sessions,
		authz:       cfg.Authorizer,
		guard:       recorder.WithLimiter(cfg.DenialLimiter).WithSurface("audit"),
		logger:      cfg.Logger,
		mux:         route.NewMux(),
		routes:      nil,
		exportLimit: exportTimeLimit,
	}

	h.routes = h.buildRoutes()
	for _, rt := range h.routes {
		h.mux.HandleFunc(rt.Method+" "+rt.Path, h.enforce(rt))
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// auditEntry is one audit entry as the console reads it, in camelCase. AtMs
// is the time of the action in Unix milliseconds. Sequence numbers the entry
// in its namespace from 1. ObjectID is the stable ID of the unit of the
// entry, and is omitted when the entry has none. PrevHash is empty for the
// first entry of a namespace.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it ships on its own schedule, so a released console keeps working
// only while every field stays.
type auditEntry struct {
	AtMs       int64  `json:"atMs"`
	Sequence   int64  `json:"sequence"`
	Subject    string `json:"subject"`
	IDPSubject string `json:"idpSubject"`
	TokenID    string `json:"tokenID"`
	Action     string `json:"action"`
	Object     string `json:"object"`
	Version    string `json:"version"`
	ValuePath  string `json:"valuePath"`
	ValueCount *int64 `json:"valueCount,omitempty"`
	// Changes holds the keys of the entry with their old and new values, for
	// example the settings of a startup or config-file entry (ADR 63). It is
	// omitted for an entry without them.
	Changes   []auditChange `json:"changes,omitempty"`
	SourceIP  string        `json:"sourceIP"`
	UserAgent string        `json:"userAgent"`
	Channel   string        `json:"channel"`
	Kind      string        `json:"kind"`
	Outcome   string        `json:"outcome"`
	RequestID string        `json:"requestID"`
	ObjectID  string        `json:"objectID,omitempty"`
	PrevHash  string        `json:"prevHash"`
	Hash      string        `json:"hash"`
	// FenceVersion is the failover version of the namespace when the entry
	// was appended, which the hash covers. It is omitted when it is 0, as in
	// @server.
	FenceVersion int64 `json:"fenceVersion,omitempty"`
	// Reason is the text that a person gave for the action, for example for
	// a force-unlock. It is omitted for an entry without one.
	Reason string `json:"reason,omitempty"`
	// PrincipalKind is the kind of the principal record that acted:
	// "person", "service" or "workload". It is omitted when the server
	// cannot name the kind.
	PrincipalKind string `json:"principalKind,omitempty"`
	// JobID is the job whose token acted. It is omitted for every other
	// credential.
	JobID string `json:"jobID,omitempty"`
	// Agent holds the agent block of the entry: the agent that acted for
	// the subject. It is omitted for an entry without agent data.
	Agent *auditAgentBlock `json:"agent,omitempty"`
}

// auditAgentBlock is the wire shape of the agent data of an entry: the run
// of a call of the built-in agent, the MCP session ID of a call of an
// outside agent or the run ID, the tool of the call, the digest of its
// arguments, the break-glass flag, the closed secret stage and the
// delegation under which the agent acted. A job entry
// of an agent leaves the tool, the digest and the stage empty.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it ships on its own schedule, so a released console keeps working
// only while every field stays.
type auditAgentBlock struct {
	AgentRun     string `json:"agentRun"`
	AgentSession string `json:"agentSession"`
	Tool         string `json:"tool"`
	ArgsDigest   string `json:"argsDigest"`
	BreakGlass   bool   `json:"breakGlass"`
	SecretStage  string `json:"secretStage"`
	DelegationID string `json:"delegationID"`
}

// toAuditEntry maps one domain entry to its wire shape.
func toAuditEntry(e audit.Entry) auditEntry {
	entry := auditEntry{
		AtMs:          e.AtMs(),
		Sequence:      e.Sequence,
		Subject:       e.Subject,
		IDPSubject:    e.IDPSubject,
		TokenID:       e.TokenID,
		Action:        e.Action,
		Object:        e.Object,
		Version:       e.Version,
		ValuePath:     e.ValuePath,
		ValueCount:    e.ValueCount,
		Changes:       toAuditChanges(e.Changes),
		SourceIP:      e.SourceIP,
		UserAgent:     e.UserAgent,
		Channel:       string(e.Channel),
		Kind:          string(e.Kind),
		Outcome:       string(e.Outcome),
		RequestID:     e.RequestID,
		ObjectID:      e.ObjectID,
		PrevHash:      e.PrevHash,
		Hash:          e.Hash,
		FenceVersion:  e.FenceVersion,
		Reason:        e.Reason,
		PrincipalKind: e.PrincipalKind,
		JobID:         e.JobID,
		Agent:         nil,
	}

	if o := e.Origin; o != nil {
		entry.Agent = &auditAgentBlock{
			AgentRun:     o.Run(),
			AgentSession: o.Session(),
			Tool:         o.Tool(),
			ArgsDigest:   o.ArgsDigest(),
			BreakGlass:   o.BreakGlass(),
			SecretStage:  o.SecretStage(),
			DelegationID: o.DelegationID(),
		}
	}

	return entry
}

// auditChange is the wire shape of one change of an entry: the key, the old
// and the new value, the layer of a setting (ADR 63) and when a recorded
// change applies. A sensitive key holds only set or not set.
type auditChange struct {
	Key         string  `json:"key"`
	Old         *string `json:"old,omitempty"`
	New         string  `json:"new"`
	Layer       string  `json:"layer,omitempty"`
	TakesEffect string  `json:"takesEffect,omitempty"`
}

// toAuditChanges maps the changes of an entry to their wire shape, nil for
// none.
func toAuditChanges(changes []audit.Change) []auditChange {
	if len(changes) == 0 {
		return nil
	}

	out := make([]auditChange, 0, len(changes))

	for _, c := range changes {
		out = append(out, auditChange{Key: c.Key, Old: c.Old, New: c.New, Layer: c.Layer, TakesEffect: c.TakesEffect})
	}

	return out
}

// listEntriesResponse is the answered document of the entry list.
//
// The console does not read this route. The answer follows the list rule of
// the tool-neutral API: NextPageToken is always present and is the empty
// string on the last page, and PageSize is the size that the server used.
// The list has no total.
type listEntriesResponse struct {
	Entries       []auditEntry `json:"entries"`
	NextPageToken string       `json:"nextPageToken"`
	PageSize      int          `json:"pageSize"`
}

// checkResponse is the answered document of the chain check.
//
// Contract: never remove or rename these fields. BadSequence is omitted
// when the chain is valid. EntriesUnchained counts the entries without a
// hash: the chain was off when they were appended.
type checkResponse struct {
	EntriesChecked   int64 `json:"entriesChecked"`
	EntriesUnchained int64 `json:"entriesUnchained"`
	BadSequence      int64 `json:"badSequence,omitempty"`
	Valid            bool  `json:"valid"`
}

// auditScope returns the scope of the audit entries of obj: @server for the
// installation, else the namespace. The namespace must exist; @server is not a
// namespace, so it needs no lookup. An unknown namespace answers 404.
func (h *Handler) auditScope(ctx context.Context, obj identity.Object) (namespace.Name, error) {
	if obj.IsServer() {
		return audit.ServerScope, nil
	}

	ns := obj.Namespace()

	if _, err := h.namespaces.Get(ctx, ns); err != nil {
		return "", err
	}

	return ns, nil
}

// listEntries answers GET /api/audit/entries with one page of the entries
// of the scope, newest first. The route wrapper already allowed the action
// on the object. An unknown namespace answers 404. A refused page token is
// recorded as a denial and does not reach the store.
func (h *Handler) listEntries(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	ns, err := h.auditScope(ctx, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	page, err := parsePage(r, ns)
	if err != nil {
		if errors.Is(err, errPageToken) {
			action := identity.ActionNamespaceReadAudit
			if obj.IsServer() {
				action = identity.ActionServerReadAudit
			}

			// Defense in depth: the route wrapper already checked the action
			// before the handler read the token; this record stays so that a
			// refused token is counted and bounded by the denial limiter.
			h.guard.Record(ctx, r, p, refusal.Denial{
				Action:       action,
				Object:       obj,
				Reason:       errcode.RequestPageTokenInvalid,
				ReasonWithin: nil,
				Window:       0,
			})
		}

		h.writeError(ctx, w, err)

		return
	}

	listing, err := h.entries.List(ctx, ns, page)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	entries := make([]auditEntry, 0, len(listing.Entries))
	for i := range listing.Entries {
		entries = append(entries, toAuditEntry(listing.Entries[i]))
	}

	next := ""

	if listing.More && len(listing.Entries) > 0 {
		next, err = encodePageToken(ns, listing.Entries[len(listing.Entries)-1].Sequence)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}
	}

	h.writeJSON(ctx, w, http.StatusOK, listEntriesResponse{Entries: entries, NextPageToken: next, PageSize: page.Size})
}

// getEntry answers GET /api/audit/entries/{sequence} with the one entry of
// the scope at that sequence, in the shape of an entry of the list. The
// route wrapper already allowed the action on the object. An unknown
// namespace or sequence answers 404, and a store that cannot read one entry
// answers 501.
func (h *Handler) getEntry(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	seq, err := parseSequence(r.PathValue("sequence"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if h.reader == nil {
		h.writeError(ctx, w, errNoEntryReader)

		return
	}

	ns, err := h.auditScope(ctx, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	e, err := h.reader.EntryAt(ctx, ns, seq)
	if err == nil && e.Namespace != ns {
		// The answer holds only an entry of the scope that the request
		// names, whatever the store answers.
		err = audit.ErrEntryNotFound
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, toAuditEntry(e))
}

// checkChain answers GET /api/audit/check with the chain check of the scope.
// The route wrapper already allowed the action on the object. An unknown
// namespace answers 404.
func (h *Handler) checkChain(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	ns, err := h.auditScope(ctx, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	result, err := h.entries.Check(ctx, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, checkResponse{
		EntriesChecked:   result.Checked,
		EntriesUnchained: result.Unchained,
		BadSequence:      result.BadSequence,
		Valid:            result.Valid,
	})
}

// errRemovedPaging reports the removed names page[number] and page[size].
// requestErrors maps it to 400.
var errRemovedPaging = errors.New("page[number] and page[size] are not supported: send pageSize and pageToken")

// errPageSize reports a pageSize that is not a decimal number from 1 to 200.
var errPageSize = problem.BadRequest(errcode.RequestPageSizeInvalid,
	fmt.Sprintf("pageSize is a number from 1 to %d: send a number in this range, or send no pageSize for %d",
		audit.MaxPageSize, audit.DefaultPageSize))

// errInvalidSequence reports a sequence in the path that is not a positive
// number in its one decimal form. classify maps it to 400.
var errInvalidSequence = errors.New("invalid sequence: send a positive number with no sign and no leading zero")

// parseSequence reads a sequence from the path. It accepts only the decimal
// form of a positive number, with no sign and no leading zero, so each entry
// has one URL.
func parseSequence(raw string) (int64, error) {
	if raw == "" || raw[0] < '1' || raw[0] > '9' {
		return 0, errInvalidSequence
	}

	for i := 1; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, errInvalidSequence
		}
	}

	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errInvalidSequence
	}

	return seq, nil
}

// errNoEntryReader reports a store that cannot read one entry. classify maps
// it to 501.
var errNoEntryReader = errors.New("this server cannot read one audit entry: list the entries instead")

// parsePageSize reads one pageSize value. It accepts only the decimal form
// of a number from 1 to MaxPageSize, with no sign, no leading zero and no
// space, so each size has one spelling.
func parsePageSize(raw string) (int, error) {
	if raw == "" || len(raw) > 3 || raw[0] < '1' || raw[0] > '9' {
		return 0, errPageSize
	}

	for i := 1; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, errPageSize
		}
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > audit.MaxPageSize {
		return 0, errPageSize
	}

	return n, nil
}

// parsePage reads pageSize and pageToken from the request for scope. The
// scope comes from the object of the route and never from the token. It
// refuses the removed names page[number] and page[size], a repeated value, a
// size outside 1 to 200 and a token that does not belong to scope.
func parsePage(r *http.Request, scope namespace.Name) (audit.Page, error) {
	query := queryOf(r)

	if query.Has("page[number]") || query.Has("page[size]") {
		return audit.Page{Size: 0, Before: 0}, errRemovedPaging
	}

	if len(query.All("pageSize")) > 1 {
		return audit.Page{Size: 0, Before: 0}, errPageSize
	}

	if len(query.All("pageToken")) > 1 {
		return audit.Page{Size: 0, Before: 0}, errPageToken
	}

	page := audit.Page{Size: audit.DefaultPageSize, Before: 0}

	if query.Has("pageSize") {
		n, err := parsePageSize(query.Get("pageSize"))
		if err != nil {
			return audit.Page{Size: 0, Before: 0}, err
		}

		page.Size = n
	}

	if token := query.Get("pageToken"); token != "" {
		cursor, err := decodePageCursor(token, scope)
		if err != nil {
			return audit.Page{Size: 0, Before: 0}, err
		}

		page.Before = cursor.before
	}

	return page, nil
}

// writeError answers err through the shared error module of the
// tool-neutral APIs, with the classes of the audit routes.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: requestErrors}.Write(ctx, w, err)
}

// requestErrors maps the errors of the request and of the read of the audit
// log. The shared module answers the rest.
func requestErrors(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errMissingOrganization), errors.Is(err, errRemovedPaging), errors.Is(err, errInvalidFormat),
		errors.Is(err, errInvalidTime), errors.Is(err, errInvalidRange), errors.Is(err, errInvalidFromSequence):
		return problem.New(http.StatusBadRequest, errcode.RequestInvalidQuery, err.Error()), true
	case errors.Is(err, errInvalidSequence):
		return problem.New(http.StatusBadRequest, errcode.RequestInvalidPath, err.Error()), true
	case errors.Is(err, namespace.ErrInvalidName):
		return problem.New(http.StatusBadRequest, errcode.NamespaceInvalidName, err.Error()), true
	case errors.Is(err, identity.ErrInvalidObject):
		return problem.New(http.StatusBadRequest, errcode.RequestInvalidObject, err.Error()), true
	case errors.Is(err, namespace.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.NamespaceNotFound, err.Error()), true
	case errors.Is(err, audit.ErrEntryNotFound):
		return problem.New(http.StatusNotFound, errcode.AuditEntryNotFound, err.Error()), true
	case errors.Is(err, errExportNotRecorded):
		return problem.New(http.StatusInternalServerError, errcode.AuditExportNotRecorded, err.Error()), true
	case errors.Is(err, errNoEntryReader):
		return problem.New(http.StatusNotImplemented, errcode.AuditEntryReadUnavailable, err.Error()), true
	default:
		return problem.Pass()
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	problem.WriteJSON(ctx, h.logger, w, status, body)
}
