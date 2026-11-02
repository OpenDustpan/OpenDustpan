package sourcefilter

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/source"
)

// maxBodyBytes bounds the body of a change. A filter has at most 128 URL
// patterns and 64 branch names, so a valid body is far below it.
const maxBodyBytes = 64 << 10

// channelHeader names the request header that says where a request came
// from, for the audit entry.
const channelHeader = "Dustpan-Channel"

// changeKey is the key of the one change of the audit entry of a change of the
// filter. Its new value is the text that source.EncodeFilter gives, and the
// store sets its old value.
const changeKey = "sourceFilter"

// errUnreadable names a stored filter that fails the checks of a filter. Its
// text is the answer of the 500.
var errUnreadable = errors.New(
	"the stored source filter fails the checks of a filter: send a PUT with a valid filter to replace it",
)

// document is the source filter of a namespace on the wire. A list is never
// null. A namespace that never set the filter has four empty values.
//
// Contract: never remove or rename these fields.
type document struct {
	Submodules        []string `json:"submodules"`
	LargeFiles        []string `json:"largeFiles"`
	FailOnDenied      bool     `json:"failOnDenied"`
	ProtectedBranches []string `json:"protectedBranches"`
}

// request is the body of a change. The change replaces the whole filter, so
// the body holds each field. A pointer makes a missing value differ from an
// empty one.
type request struct {
	Submodules        *[]string `dustpan:"required" json:"submodules"`
	LargeFiles        *[]string `dustpan:"required" json:"largeFiles"`
	FailOnDenied      *bool     `dustpan:"required" json:"failOnDenied"`
	ProtectedBranches *[]string `dustpan:"required" json:"protectedBranches"`
}

// get serves GET on the route. It answers 200 with the filter. A namespace
// that never set it answers the empty filter. A stored value that fails the
// checks of a filter answers 500, so a caller never reads it as a value.
func (h *Handler) get(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	f, err := h.filters.Filter(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, unreadable(err))

		return
	}

	problem.WriteJSON(ctx, h.logger, w, http.StatusOK, documentOf(&f))
}

// put serves PUT on the route. It answers 200 with the filter as stored. A body
// that is not one object with each field, or a filter that fails the checks of
// a filter, answers 400 and changes nothing. The change and its audit entry
// are one transaction in the store. The server stores the filter. No fetch
// reads it yet.
func (h *Handler) put(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	f, err := readFilter(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	text, err := source.EncodeFilter(f)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The namespace that is writable here gives the fence of the change. An
	// unknown namespace answers the same 404 as a namespace that the caller
	// cannot read.
	ns, err := h.namespaces.WritableHere(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, fmt.Errorf("namespace is writable here: %w", err))

		return
	}

	entry := h.changeEntry(r, p, obj, text)
	if err = h.filters.SetFilter(ctx, obj.Namespace(), f, ns.Fence(), entry); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	problem.WriteJSON(ctx, h.logger, w, http.StatusOK, documentOf(&f))
}

// changeEntry returns the audit entry of a change of the filter by p, with the
// one change of the filter. The store sets the old value of the change.
func (h *Handler) changeEntry(r *http.Request, p identity.Principal, obj identity.Object, text string) audit.Entry {
	return audit.Entry{
		At: h.now().UTC(), Namespace: obj.Namespace(), Subject: p.Subject, IDPSubject: "", TokenID: p.TokenID,
		Action: string(identity.ActionNamespaceManageRunners), Object: obj.String(), Version: "", ValuePath: "",
		ValueCount: nil,
		Changes:    []audit.Change{{Key: changeKey, Old: nil, New: text, Layer: "", TakesEffect: ""}},
		SourceIP:   clientip.String(r), UserAgent: audit.TruncateUserAgent(apidesc.Header(r, "User-Agent")),
		Channel: sessionauth.AuditChannel(r, channelHeader), Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: requestid.FromContext(r.Context()), ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: string(p.Kind), JobID: "", Reason: "",
	}
}

// unreadable marks the error of a read of the stored filter that says the stored
// value is not a filter. No caller sent that value, so the answer is not the
// 400 of a bad request.
func unreadable(err error) error {
	if errors.Is(err, source.ErrInvalid) {
		return fmt.Errorf("%w: %w", errUnreadable, err)
	}

	return err
}

// readFilter decodes the body of a change. It refuses an unknown field, a body
// that is not one object, a field that is missing or null, and a value of the
// wrong type. The checks of a filter run on the result in source.EncodeFilter,
// which put calls, and again in the store.
func readFilter(w http.ResponseWriter, r *http.Request) (source.Filter, error) {
	var in request
	if err := route.DecodeBody(w, r, maxBodyBytes, &in); err != nil {
		return source.Filter{}, fmt.Errorf("source filter body: %w", err)
	}

	if in.Submodules == nil || in.LargeFiles == nil || in.FailOnDenied == nil || in.ProtectedBranches == nil {
		return source.Filter{}, problem.BadRequest(errcode.RequestBodyInvalid,
			"the body must hold submodules, largeFiles, failOnDenied and protectedBranches")
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

// documentOf returns the wire form of f, with no null list.
func documentOf(f *source.Filter) document {
	doc := document{
		Submodules:        make([]string, 0, len(f.Submodules)),
		LargeFiles:        make([]string, 0, len(f.LargeFiles)),
		FailOnDenied:      f.FailOnDenied,
		ProtectedBranches: append(make([]string, 0, len(f.ProtectedBranches)), f.ProtectedBranches...),
	}

	for _, u := range f.Submodules {
		doc.Submodules = append(doc.Submodules, string(u))
	}

	for _, u := range f.LargeFiles {
		doc.LargeFiles = append(doc.LargeFiles, string(u))
	}

	return doc
}

// writeError answers err through the shared error module of the tool-neutral
// APIs, with the classes of the source filter.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: classify}.Write(ctx, w, err)
}

// classify maps the errors of the source filter routes to an answer that is
// safe to show callers. The shared module answers the rest, among them the
// unknown namespace, a fenced write and the maintenance mode.
func classify(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errUnreadable):
		return problem.New(http.StatusInternalServerError, errcode.SourceFilterUnreadable, errUnreadable.Error()), true
	case errors.Is(err, source.ErrInvalid):
		return problem.RefusedText(
			problem.Detail(err, source.ErrInvalid), http.StatusBadRequest, errcode.SourceFilterInvalid,
		), true
	case errors.Is(err, source.ErrFilterTooLarge):
		return problem.RefusedText(
			problem.Detail(err, source.ErrFilterTooLarge), http.StatusBadRequest, errcode.SourceFilterInvalid,
		), true
	}

	// Not an error of the filter: package problem answers it.
	return problem.Pass()
}
