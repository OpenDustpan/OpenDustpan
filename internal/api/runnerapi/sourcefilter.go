package runnerapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/internal/source"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner"
)

// sourceFilterPath is the route of the source filter of a namespace. It sits
// under the prefix of the tool-neutral REST API. The management routes of the
// runner pools stay under /api/runners.
const sourceFilterPath = "/api/dustpan/v1/namespaces/{namespace}/source-filter"

// sourceFilterChangeKey is the key of the one change of the audit entry of a
// change of the source filter. Its new value is the text of the filter that
// source.EncodeFilter gives, and the store sets its old value.
const sourceFilterChangeKey = "sourceFilter"

// errSourceFilterUnreadable names a stored source filter that fails the
// checks of a filter. Its text is the answer of the 500.
var errSourceFilterUnreadable = errors.New(
	"the stored source filter fails the checks of a filter: send a PUT with a valid filter to replace it",
)

// sourceFilterDocument is the source filter of a namespace on the wire. A
// list is never null. A namespace that never set the filter has four empty
// values.
//
// Contract: never remove or rename these fields.
type sourceFilterDocument struct {
	Submodules        []string `json:"submodules"`
	LargeFiles        []string `json:"largeFiles"`
	FailOnDenied      bool     `json:"failOnDenied"`
	ProtectedBranches []string `json:"protectedBranches"`
}

// sourceFilterRequest is the body of a change of the source filter. The change
// replaces the whole filter, so the body holds each field. A pointer makes a
// missing value differ from an empty one.
type sourceFilterRequest struct {
	Submodules        *[]string `dustpan:"required" json:"submodules"`
	LargeFiles        *[]string `dustpan:"required" json:"largeFiles"`
	FailOnDenied      *bool     `dustpan:"required" json:"failOnDenied"`
	ProtectedBranches *[]string `dustpan:"required" json:"protectedBranches"`
}

// sourceFilterRoutes returns the rows of the source filter of a namespace. A
// read needs namespace:read-runners, and a change needs
// namespace:manage-runners. As on every authorized route of this adapter, a
// caller who cannot read the namespace gets the 404 of an unknown namespace.
func (h *Handler) sourceFilterRoutes() []Route {
	read := []identity.Action{identity.ActionNamespaceReadRunners}
	manage := []identity.Action{identity.ActionNamespaceManageRunners}

	return []Route{
		{
			Method:  http.MethodGet,
			Path:    sourceFilterPath,
			Scope:   ScopeAuthorized,
			Actions: read,
			Serve:   h.getSourceFilter,
		},
		{
			Method:  http.MethodPut,
			Path:    sourceFilterPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Serve:   h.putSourceFilter,
		},
	}
}

// getSourceFilter serves GET on the route. It answers 200 with the filter. A
// namespace that never set it answers the empty filter. A stored value that
// fails the checks of a filter answers 500, so a caller never reads it as a
// value.
func (h *Handler) getSourceFilter(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	f, err := h.sourceFilters.Filter(ctx, c.ns.Name)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, unreadableFilter(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, sourceFilterDocumentOf(&f))
}

// putSourceFilter serves PUT on the route. It answers 200 with the filter as
// stored. A body that is not one object with each field, or a filter that
// fails the checks of a filter, answers 400 and changes nothing. The change
// and its audit entry are one transaction in the store. A new filter applies
// to the fetches that start later. It changes no snapshot that exists.
func (h *Handler) putSourceFilter(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	f, err := readSourceFilter(c.body)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	text, err := source.EncodeFilter(f)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	fence, err := h.writable(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	rule := runner.AuditRule{Action: identity.ActionNamespaceManageRunners, Kind: audit.KindChange}
	e := h.entry(r, c.ns.Name, c.principal.Subject, rule, h.now())
	e.TokenID = c.principal.TokenID
	e.PrincipalKind = string(c.principal.Kind)
	e.Changes = []audit.Change{{Key: sourceFilterChangeKey, Old: nil, New: text, Layer: "", TakesEffect: ""}}

	if err = h.sourceFilters.SetFilter(ctx, c.ns.Name, f, fence, e); err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, sourceFilterDocumentOf(&f))
}

// unreadableFilter marks the error of a read of the stored filter that says the
// stored value is not a filter. No caller sent that value, so the answer is
// not the 400 of a bad request.
func unreadableFilter(err error) error {
	if errors.Is(err, source.ErrInvalid) {
		return fmt.Errorf("%w: %w", errSourceFilterUnreadable, err)
	}

	return err
}

// readSourceFilter decodes the body of a change of the filter. It refuses an
// unknown field, a body that is not one object, a field that is missing or
// null, and a value that is not valid for the field. The filter checks of the
// service and the store run again on the result.
func readSourceFilter(body []byte) (source.Filter, error) {
	var none source.Filter

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in sourceFilterRequest
	if err := dec.Decode(&in); err != nil {
		return none, fmt.Errorf("%w: %w", errBadBody, err)
	}

	// Nothing may follow the object, also not a second object.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return none, fmt.Errorf("%w: data follows the object", errBadBody)
	}

	if in.Submodules == nil || in.LargeFiles == nil || in.FailOnDenied == nil || in.ProtectedBranches == nil {
		return none, fmt.Errorf(
			"%w: provide submodules, largeFiles, failOnDenied and protectedBranches", errBadBody,
		)
	}

	return source.Filter{
		Submodules:        patternsOf(*in.Submodules),
		LargeFiles:        patternsOf(*in.LargeFiles),
		FailOnDenied:      *in.FailOnDenied,
		ProtectedBranches: *in.ProtectedBranches,
	}, nil
}

// patternsOf returns the patterns that the texts of the body hold. It does not
// check them: source.Filter.Validate does.
func patternsOf(texts []string) []source.URLPattern {
	out := make([]source.URLPattern, 0, len(texts))
	for _, t := range texts {
		out = append(out, source.URLPattern(t))
	}

	return out
}

// sourceFilterDocumentOf returns the wire form of f, with no null list.
func sourceFilterDocumentOf(f *source.Filter) sourceFilterDocument {
	doc := sourceFilterDocument{
		Submodules:        make([]string, 0, len(f.Submodules)),
		LargeFiles:        make([]string, 0, len(f.LargeFiles)),
		FailOnDenied:      f.FailOnDenied,
		ProtectedBranches: make([]string, 0, len(f.ProtectedBranches)),
	}

	for _, u := range f.Submodules {
		doc.Submodules = append(doc.Submodules, string(u))
	}

	for _, u := range f.LargeFiles {
		doc.LargeFiles = append(doc.LargeFiles, string(u))
	}

	doc.ProtectedBranches = append(doc.ProtectedBranches, f.ProtectedBranches...)

	return doc
}
