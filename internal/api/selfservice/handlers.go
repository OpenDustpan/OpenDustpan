package selfservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservicesvc"
	"opendustpan.org/opendustpan/pkg/identity"
)

// maxPublishBody is the largest body of a publish request.
const maxPublishBody = 1 << 10

// errIfMatch means that a draft write has no If-Match header with a version.
var errIfMatch = errors.New("send the If-Match header with the draft version")

// versionMetadata describes one published version without its document.
type versionMetadata struct {
	Namespace    string `json:"namespace"`
	Blueprint    string `json:"blueprint"`
	Number       int    `json:"number"`
	DraftVersion int64  `json:"draftVersion"`
	PublishedAt  string `json:"publishedAt"`
	PublishedBy  string `json:"publishedBy"`
	ExprVersion  string `json:"exprVersion"`
	Retired      bool   `json:"retired"`
}

// metadata returns the description of v.
func metadata(v selfservice.Version) versionMetadata {
	return versionMetadata{
		Namespace:    v.Namespace.String(),
		Blueprint:    v.Blueprint.Name,
		Number:       v.Number,
		DraftVersion: v.DraftVersion,
		PublishedAt:  v.PublishedAt.UTC().Format(time.RFC3339),
		PublishedBy:  v.PublishedBy,
		ExprVersion:  string(v.Blueprint.ExprVersion),
		Retired:      v.Retired,
	}
}

// draftResponse is the answer of a draft read.
type draftResponse struct {
	Version   int64                 `json:"version"`
	Blueprint selfservice.Blueprint `json:"blueprint"`
}

// writeResponse is the answer of a draft write.
type writeResponse struct {
	Version int64 `json:"version"`
}

// publishBody is the body of a publish request.
type publishBody struct {
	DraftVersion int64 `json:"draftVersion"`
}

// listResponse is the answer of a list: the newest version of each blueprint.
type listResponse struct {
	Versions []versionMetadata `json:"versions"`
}

// list answers the newest version of each blueprint of the namespace.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	versions, err := h.blueprints.ListBlueprints(r.Context(), p, t.ref.Namespace)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	result := make([]versionMetadata, 0, len(versions))
	for i := range versions {
		result = append(result, metadata(versions[i]))
	}

	h.writeJSON(r.Context(), w, http.StatusOK, listResponse{Versions: result})
}

// readDraft answers the draft of the blueprint, with its version as the ETag.
func (h *Handler) readDraft(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	bp, version, err := h.blueprints.ReadDraft(r.Context(), p, t.ref)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	w.Header().Set("ETag", fmt.Sprintf("%q", strconv.FormatInt(version, 10)))
	h.writeJSON(r.Context(), w, http.StatusOK, draftResponse{Version: version, Blueprint: bp})
}

// writeDraft stores the draft in the body. The If-Match header names the
// version that the caller read.
func (h *Handler) writeDraft(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(selfservice.MaxBlueprintBytes)+1))
	if err != nil {
		h.writeError(r.Context(), w, fmt.Errorf("read blueprint body: %w", err))

		return
	}

	if len(body) > selfservice.MaxBlueprintBytes {
		h.writeError(r.Context(), w, fmt.Errorf("blueprint body: %w", selfservice.ErrLimit))

		return
	}

	bp, err := selfservicesvc.ParseDraft(body)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	version, err := parseIfMatch(apidesc.Header(r, "If-Match"))
	if err != nil {
		h.writeError(r.Context(), w, errIfMatch)

		return
	}

	next, err := h.blueprints.WriteDraft(r.Context(), p, t.ref, bp, version)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	w.Header().Set("ETag", fmt.Sprintf("%q", strconv.FormatInt(next, 10)))
	h.writeJSON(r.Context(), w, http.StatusOK, writeResponse{Version: next})
}

// parseIfMatch reads the quoted draft version of an If-Match header.
func parseIfMatch(header string) (int64, error) {
	raw, err := strconv.Unquote(header)
	if err != nil {
		return 0, fmt.Errorf("if-match: %w", err)
	}

	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("if-match: %w", err)
	}

	return version, nil
}

// publish publishes the draft version named in the body.
func (h *Handler) publish(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	var body publishBody
	if err := route.DecodeBody(w, r, maxPublishBody, &body); err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	v, err := h.blueprints.Publish(r.Context(), p, t.ref, body.DraftVersion)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	h.writeJSON(r.Context(), w, http.StatusCreated, metadata(v))
}

// readVersion answers one published version.
func (h *Handler) readVersion(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	v, err := h.blueprints.ReadVersion(r.Context(), p, t.ref, t.version)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	h.writeJSON(r.Context(), w, http.StatusOK, metadata(v))
}

// retire retires one published version.
func (h *Handler) retire(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	if err := h.blueprints.Retire(r.Context(), p, t.ref, t.version); err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeJSON writes value as the JSON answer with status.
func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		h.writeError(ctx, w, fmt.Errorf("encode response: %w", err))

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if _, err := w.Write(append(data, '\n')); err != nil {
		faultlog.LogFault(ctx, h.logger, "cannot write the response", faultlog.Cause(err), "selfservice.response")
	}
}

// writeError answers err with the problem of its class.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: classify}.Write(ctx, w, err)
}

// classify maps the errors of the blueprint routes to answers that are safe to
// show. The shared module answers the rest.
func classify(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errIfMatch):
		return problem.New(http.StatusPreconditionRequired, errcode.SelfServiceBlueprintStale, errIfMatch.Error()), true
	case errors.Is(err, selfservice.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.SelfServiceBlueprintNotFound,
			selfservice.ErrNotFound.Error()), true
	case errors.Is(err, selfservice.ErrInvalid):
		return problem.New(http.StatusBadRequest, errcode.SelfServiceBlueprintInvalid,
			selfservice.ErrInvalid.Error()), true
	case errors.Is(err, selfservice.ErrLimit):
		return problem.New(http.StatusRequestEntityTooLarge, errcode.SelfServiceBlueprintTooLarge,
			selfservice.ErrLimit.Error()), true
	case errors.Is(err, selfservice.ErrConflict):
		return problem.New(http.StatusConflict, errcode.SelfServiceBlueprintStale,
			selfservice.ErrConflict.Error()), true
	case errors.Is(err, selfservice.ErrAlreadyPublished):
		return problem.New(http.StatusConflict, errcode.SelfServiceBlueprintAlreadyPublished,
			selfservice.ErrAlreadyPublished.Error()), true
	}

	// Not an error of the blueprint routes: package problem answers it.
	return problem.Pass()
}
