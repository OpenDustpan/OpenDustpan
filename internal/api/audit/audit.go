// Package audit serves the tool-neutral audit API that the web console
// reads for the audit entries of one namespace. It exposes the entries of
// the audit.Store of the namespace and the result of its chain check.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone (ADR 12).
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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

const tokenScheme = "token "

// Config holds the dependencies of a Handler.
type Config struct {
	Namespaces *namespace.Service
	Entries    audit.Store
	// EntryReader reads one entry by its scope and sequence. Nil answers
	// 501 on the route that reads one entry.
	EntryReader   audit.EntryReader
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
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
	logger     *slog.Logger
	mux        *patternMux
	routes     []Route
}

// NewHandler returns a Handler. Every Config field but EntryReader and
// Sessions is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Entries == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	h := &Handler{
		namespaces: cfg.Namespaces,
		entries:    cfg.Entries,
		reader:     cfg.EntryReader,
		authn:      cfg.Authenticator,
		sessions:   cfg.Sessions,
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
}

// toAuditEntry maps one domain entry to its wire shape.
func toAuditEntry(e audit.Entry) auditEntry {
	return auditEntry{
		AtMs:         e.AtMs(),
		Sequence:     e.Sequence,
		Subject:      e.Subject,
		IDPSubject:   e.IDPSubject,
		TokenID:      e.TokenID,
		Action:       e.Action,
		Object:       e.Object,
		Version:      e.Version,
		ValuePath:    e.ValuePath,
		ValueCount:   e.ValueCount,
		Changes:      toAuditChanges(e.Changes),
		SourceIP:     e.SourceIP,
		UserAgent:    e.UserAgent,
		Channel:      string(e.Channel),
		Kind:         string(e.Kind),
		Outcome:      string(e.Outcome),
		RequestID:    e.RequestID,
		ObjectID:     e.ObjectID,
		PrevHash:     e.PrevHash,
		Hash:         e.Hash,
		FenceVersion: e.FenceVersion,
	}
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
// Contract: never remove or rename these fields.
type listEntriesResponse struct {
	Entries []auditEntry `json:"entries"`
	Total   int64        `json:"total"`
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
// on the object. An unknown namespace answers 404.
func (h *Handler) listEntries(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	ns, err := h.auditScope(ctx, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	page, err := parsePage(r)
	if err != nil {
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

	h.writeJSON(ctx, w, http.StatusOK, listEntriesResponse{Entries: entries, Total: listing.Total})
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

// errInvalidPage reports a page[number] or page[size] value that is not a
// positive number. classify maps it to the 400 the console expects.
var errInvalidPage = errors.New("invalid page[number] or page[size]: send positive numbers")

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

// parsePage reads page[number] and page[size] from the request. Missing
// values read the defaults of the audit port; values above the maximum read
// the maximum.
func parsePage(r *http.Request) (audit.Page, error) {
	query := r.URL.Query()

	page := audit.Page{Number: 1, Size: audit.DefaultPageSize}

	if raw := query.Get("page[number]"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return audit.Page{}, errInvalidPage
		}

		page.Number = n
	}

	if raw := query.Get("page[size]"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return audit.Page{}, errInvalidPage
		}

		page.Size = n
	}

	return page.Normalized(), nil
}

// authenticate resolves the "Authorization: token <t>" header to a principal.
// A request with no Authorization header uses the console session cookie,
// and the bool reports that the cookie signed it in. A request with an
// Authorization header never uses the cookie.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, bool, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		p, _, err := h.sessions.Authenticate(r)

		return p, err == nil, err
	}

	raw, ok := strings.CutPrefix(header, tokenScheme)
	if !ok || raw == "" {
		return identity.Principal{}, false, identity.ErrUnauthenticated
	}

	p, err := h.authn.AuthenticateToken(r.Context(), raw)

	return p, false, err
}

// errorResponse is the error body the console expects, as in the API the
// console reads today.
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
	}

	h.writeJSON(ctx, w, status, errorResponse{Code: status, Message: message})
}

// classify maps domain errors to an HTTP status and a message safe to show callers.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return http.StatusTooManyRequests, "too many failed sign-ins from this address: try again later"
	case errors.Is(err, identity.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return http.StatusForbidden, err.Error()
	case errors.Is(err, identity.ErrDenied):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, errMissingOrganization), errors.Is(err, errInvalidPage), errors.Is(err, errInvalidSequence):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, namespace.ErrInvalidName), errors.Is(err, identity.ErrInvalidObject):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, namespace.ErrNotFound), errors.Is(err, audit.ErrEntryNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, errNoEntryReader):
		return http.StatusNotImplemented, err.Error()
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
