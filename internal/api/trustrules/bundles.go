package trustrules

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// The routes of the trust bundle of a namespace.
const (
	// BundlesPath is the list and the save of the trust bundle of a namespace.
	BundlesPath = "/api/dustpan/v1/namespaces/{namespace}/trust-bundles"
	// BundlePath is the read and the rotation of one trust bundle.
	BundlePath = "/api/dustpan/v1/namespaces/{namespace}/trust-bundles/{bundle}"
	// BundleVersionPath is the delete of one retired version of a bundle.
	BundleVersionPath = "/api/dustpan/v1/namespaces/{namespace}/trust-bundles/{bundle}/versions/{version}"
)

// maxBundleBodyBytes bounds the body of a bundle write. The PEM text is at
// most trustrule.MaxBundleBytes, and JSON escapes of new lines make it larger.
const maxBundleBodyBytes = 3 * trustrule.MaxBundleBytes

// bundleVersionDocument is one version of a trust bundle on the wire. CACount
// is the number of CA certificates of the version. OverlapEndsAt is null for a
// version that no rotation made.
//
// Contract: never remove or rename these fields.
type bundleVersionDocument struct {
	Number        int64      `json:"number"`
	CACount       int        `json:"caCount"`
	CreatedAt     time.Time  `json:"createdAt"`
	OverlapEndsAt *time.Time `json:"overlapEndsAt"`
}

// bundleDocument is the trust bundle of a namespace on the wire. Active is the
// number of the active version. OverlapEndsAt is the end of the overlap of the
// active version, and null when it has none.
//
// Contract: never remove or rename these fields.
type bundleDocument struct {
	ID            string                  `json:"id"`
	Namespace     string                  `json:"namespace"`
	Active        int64                   `json:"active"`
	Versions      []bundleVersionDocument `json:"versions"`
	OverlapEndsAt *time.Time              `json:"overlapEndsAt"`
	Revision      int64                   `json:"revision"`
	CreatedAt     time.Time               `json:"createdAt"`
}

// bundleListDocument is the list of the trust bundles of a namespace: none or
// one. Bundles is never null.
type bundleListDocument struct {
	Bundles []bundleDocument `json:"bundles"`
}

// saveBundleRequest is the body of a bundle save. PEM is the bundle in PEM
// text.
type saveBundleRequest struct {
	PEM *string `dustpan:"required" json:"pem"`
}

// rotateBundleRequest is the body of a bundle rotation. OverlapSeconds is the
// time that the previous version stays valid, 0 when it is missing.
type rotateBundleRequest struct {
	PEM            *string `dustpan:"required" json:"pem"`
	OverlapSeconds *int64  `dustpan:"optional" json:"overlapSeconds"`
}

// timePtr returns a pointer to t in UTC, and nil for a zero time.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	u := t.UTC()

	return &u
}

// bundleDocumentOf returns the wire form of b, with no null list.
func bundleDocumentOf(b *trustrule.Bundle) bundleDocument {
	doc := bundleDocument{
		ID: string(b.ID), Namespace: b.Namespace.String(), Active: b.Active,
		Versions: make([]bundleVersionDocument, 0, len(b.Versions)), OverlapEndsAt: nil, Revision: b.Revision,
		CreatedAt: b.CreatedAt.UTC(),
	}

	for i := range b.Versions {
		v := &b.Versions[i]

		doc.Versions = append(doc.Versions, bundleVersionDocument{
			Number: v.Number, CACount: len(v.Certificates), CreatedAt: v.CreatedAt.UTC(),
			OverlapEndsAt: timePtr(v.OverlapEndsAt),
		})

		if v.Number == b.Active {
			doc.OverlapEndsAt = timePtr(v.OverlapEndsAt)
		}
	}

	return doc
}

// listBundles serves GET on BundlesPath. A namespace with no bundle answers
// 200 with an empty list.
func (h *Handler) listBundles(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()
	doc := bundleListDocument{Bundles: []bundleDocument{}}

	b, err := h.rules.GetBundle(ctx, obj.Namespace())

	switch {
	case err == nil:
		doc.Bundles = append(doc.Bundles, bundleDocumentOf(&b))
	case !errors.Is(err, trustrule.ErrNotFound):
		h.writeError(ctx, w, err)

		return
	}

	problem.WriteJSON(ctx, h.logger, w, http.StatusOK, doc)
}

// saveBundle serves POST on BundlesPath. It answers 201 with the bundle, and
// stores one new version of it.
func (h *Handler) saveBundle(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	var in saveBundleRequest
	if err := route.DecodeBody(w, r, maxBundleBodyBytes, &in); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if in.PEM == nil {
		h.writeError(ctx, w, problem.BadRequest(errcode.RequestBodyInvalid, "the body must hold pem"))

		return
	}

	b, err := h.rules.SaveBundle(ctx, p, h.entryOf(r, p), obj.Namespace(), trustrule.BundleFields{
		PEM: *in.PEM, Overlap: 0,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", trustrule.ETag(b.Revision))
	w.Header().Set("Location", BundlesPath[:len(BundlesPath)-len("{bundle}")]+string(b.ID))
	problem.WriteJSON(ctx, h.logger, w, http.StatusCreated, bundleDocumentOf(&b))
}

// bundleOfPath returns the bundle of the namespace when its ID is the ID of
// the path, and trustrule.ErrNotFound otherwise. A namespace has one bundle,
// so another ID names no bundle.
func (h *Handler) bundleOfPath(r *http.Request, obj identity.Object) (trustrule.Bundle, error) {
	id := trustrule.BundleID(r.PathValue("bundle"))
	if id.Validate() != nil {
		return trustrule.Bundle{}, trustrule.ErrNotFound
	}

	b, err := h.rules.GetBundle(r.Context(), obj.Namespace())
	if err != nil {
		return trustrule.Bundle{}, err
	}

	if b.ID != id {
		return trustrule.Bundle{}, trustrule.ErrNotFound
	}

	return b, nil
}

// getBundle serves GET on BundlePath.
func (h *Handler) getBundle(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	b, err := h.bundleOfPath(r, obj)
	if err != nil {
		h.writeBundleError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", trustrule.ETag(b.Revision))
	problem.WriteJSON(ctx, h.logger, w, http.StatusOK, bundleDocumentOf(&b))
}

// rotateBundle serves PATCH on BundlePath. It needs If-Match, stores the PEM
// text as a new version, makes it active and answers 200 with the bundle.
func (h *Handler) rotateBundle(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	revision, err := ifMatchRevision(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var in rotateBundleRequest
	if decodeErr := route.DecodeBody(w, r, maxBundleBodyBytes, &in); decodeErr != nil {
		h.writeError(ctx, w, decodeErr)

		return
	}

	if in.PEM == nil {
		h.writeError(ctx, w, problem.BadRequest(errcode.RequestBodyInvalid, "the body must hold pem"))

		return
	}

	overlap := time.Duration(0)

	if in.OverlapSeconds != nil {
		// Defense in depth: the store checks the overlap too. This check
		// stays so that a huge number never overflows the Duration.
		if *in.OverlapSeconds < 0 || *in.OverlapSeconds > int64(trustrule.MaxOverlap/time.Second) {
			h.writeError(ctx, w, problem.BadRequest(errcode.TrustRuleBundleInvalid,
				"overlapSeconds is 0 to "+strconv.FormatInt(int64(trustrule.MaxOverlap/time.Second), 10)))

			return
		}

		overlap = time.Duration(*in.OverlapSeconds) * time.Second
	}

	if _, pathErr := h.bundleOfPath(r, obj); pathErr != nil {
		h.writeBundleError(ctx, w, pathErr)

		return
	}

	b, err := h.rules.RotateBundle(ctx, p, h.entryOf(r, p), obj.Namespace(), trustrule.BundleFields{
		PEM: *in.PEM, Overlap: overlap,
	}, revision)
	if err != nil {
		h.writeBundleError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", trustrule.ETag(b.Revision))
	problem.WriteJSON(ctx, h.logger, w, http.StatusOK, bundleDocumentOf(&b))
}

// deleteBundleVersion serves DELETE on BundleVersionPath. It answers 204. The
// active version answers 409.
func (h *Handler) deleteBundleVersion(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	if _, err := h.bundleOfPath(r, obj); err != nil {
		h.writeBundleError(ctx, w, err)

		return
	}

	version, err := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if err != nil || version < 1 {
		h.writeBundleError(ctx, w, trustrule.ErrNotFound)

		return
	}

	if err := h.rules.DeleteBundleVersion(ctx, p, h.entryOf(r, p), obj.Namespace(), version); err != nil {
		h.writeBundleError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeBundleError answers err as writeError does, except that a missing
// bundle answers its own code and not the code of a missing rule.
func (h *Handler) writeBundleError(ctx interface {
	Done() <-chan struct{}
	Err() error
	Deadline() (time.Time, bool)
	Value(key any) any
}, w http.ResponseWriter, err error) {
	if errors.Is(err, trustrule.ErrNotFound) {
		h.writeError(ctx, w, problem.Fixed(http.StatusNotFound, errcode.TrustRuleBundleNotFound,
			"the namespace has no trust bundle with this ID"))

		return
	}

	h.writeError(ctx, w, err)
}

// errIfMatchRequired answers a change with no If-Match header.
var errIfMatchRequired = problem.Fixed(http.StatusPreconditionRequired, errcode.RequestPreconditionRequired,
	"send the ETag that you read in If-Match")

// errIfMatchInvalid answers an If-Match header that is not one revision in
// quotes.
var errIfMatchInvalid = problem.Fixed(http.StatusBadRequest, errcode.RequestIfMatchInvalid,
	"If-Match must be one revision in quotes, as the ETag of a read gives it")

// errPrecondition answers a revision that is not the stored revision.
var errPrecondition = problem.Fixed(http.StatusPreconditionFailed, errcode.RequestPreconditionFailed,
	"If-Match does not match the stored revision: read the object again and send the new ETag")

// ifMatchRevision returns the revision in the If-Match header of r. The header
// holds one revision in quotes, as the ETag of a read gives it. A missing
// header answers 428, any other form answers 400, and a weak tag answers 412,
// because it never matches in the strong comparison.
func ifMatchRevision(r *http.Request) (int64, error) {
	values := apidesc.HeaderValues(r, "If-Match")
	if len(values) == 0 {
		return 0, errIfMatchRequired
	}

	if len(values) > 1 {
		return 0, errIfMatchInvalid
	}

	raw := strings.TrimSpace(values[0])

	weak := false
	if rest, ok := strings.CutPrefix(raw, "W/"); ok {
		raw, weak = rest, true
	}

	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return 0, errIfMatchInvalid
	}

	revision, err := strconv.ParseInt(raw[1:len(raw)-1], 10, 64)
	if err != nil || revision < 1 {
		return 0, errIfMatchInvalid
	}

	if weak {
		return 0, errPrecondition
	}

	return revision, nil
}
