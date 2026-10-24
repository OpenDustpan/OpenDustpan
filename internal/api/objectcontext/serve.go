package objectcontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// maxBodyBytes bounds the body of a context write: a runbook of 64 KiB, and
// the other fields, with room for the escapes of JSON.
const maxBodyBytes = 256 << 10

// readResponse is the answered document of a read: the document of the read
// API, and the mark of a unit that is soft-deleted (a restore brings its
// context back with it).
//
// Contract: never remove or rename these fields.
type readResponse struct {
	objectcontext.Document

	Deleted bool `json:"deleted,omitempty"`
}

// readContext serves the GET routes. The owners and the on-call text of a
// unit resolve over its collection and its namespace. A layer that the caller
// cannot read still gives its owners and its on-call text.
func (h *Handler) readContext(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	ctx := r.Context()

	include, err := parseInclude(queryOf(r).Get("include"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	src := &requestSource{h: h, p: p, t: t}

	reader, err := objectcontext.NewReader(objectcontext.Sources{
		Inventory: src, Context: h.entries, Access: src,
		Dependencies: nil, History: nil, Jobs: nil, Findings: nil, Audit: nil,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc, err := reader.Read(ctx, objectcontext.Request{Object: t.ref, Include: include})
	if errors.Is(err, objectcontext.ErrNotFound) {
		err = errNotFound
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, readResponse{Document: doc, Deleted: t.deleted != nil})
}

// parseInclude parses the include query value: section names separated by
// commas. The empty value asks for the default sections.
func parseInclude(raw string) ([]objectcontext.Section, error) {
	if raw == "" {
		return nil, nil
	}

	var out []objectcontext.Section

	for name := range strings.SplitSeq(raw, ",") {
		if name == "" {
			return nil, errInclude
		}

		out = append(out, objectcontext.Section(name))
	}

	return out, nil
}

// patchContext serves the PATCH routes. The body names only the fields that
// it changes. A field with an empty value clears the field. It answers 200
// with the API entry of the object. The API refuses a field that the output
// of the same unit sets (409), and a unit that is soft-deleted (409).
func (h *Handler) patchContext(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	ctx := r.Context()

	patch, err := readPatch(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if t.deleted != nil {
		h.writeError(ctx, w, objectcontext.ErrUnitDeleted)

		return
	}

	if _, err = h.namespaces.WritableHere(ctx, t.ns.Name); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	entry, err := h.writer.Set(ctx, t.ref, patch, p.Subject, auditEntry(r, p, t))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, entry)
}

// deleteContext serves the DELETE routes: it deletes the API context of the
// object. It answers 204, and 404 for an object with no API context. A
// soft-deleted unit accepts it, so a person can clear a value before a
// restore.
func (h *Handler) deleteContext(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	ctx := r.Context()

	if _, err := h.namespaces.WritableHere(ctx, t.ns.Name); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if err := h.writer.Clear(ctx, t.ref, auditEntry(r, p, t)); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// patchBody is the body of a PATCH. A field that the body leaves out stays as
// it is.
type patchBody struct {
	Summary *string               `dustpan:"optional" json:"summary"`
	Runbook *string               `dustpan:"optional" json:"runbook"`
	OnCall  *string               `dustpan:"optional" json:"onCall"`
	Owners  *[]string             `dustpan:"optional" json:"owners"`
	Links   *[]objectcontext.Link `dustpan:"optional" json:"links"`
}

// readPatch decodes a PATCH body. It refuses a body that is not valid UTF-8
// or that holds a lone surrogate escape, an unknown field, a body that is not
// one object, and a body that changes no field.
func readPatch(w http.ResponseWriter, r *http.Request) (objectcontext.Patch, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return objectcontext.Patch{}, errBody
	}

	// encoding/json replaces each invalid UTF-8 byte and each lone surrogate
	// escape with U+FFFD, so the check of the text fields could never see
	// what the client sent. Refuse the body here, before the decode changes it.
	// Defense in depth: Fields.Validate checks for valid UTF-8 too; this check
	// stays so that a decode that changes the text cannot hide it from it.
	if textbody.Refused(body) {
		return objectcontext.Patch{}, errBodyText
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in patchBody
	if err := dec.Decode(&in); err != nil {
		return objectcontext.Patch{}, errBody
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return objectcontext.Patch{}, errBody
	}

	p := objectcontext.Patch{
		Summary: in.Summary, Runbook: in.Runbook, OnCall: in.OnCall, Owners: in.Owners, Links: in.Links,
	}
	if len(p.Fields()) == 0 {
		return objectcontext.Patch{}, errBody
	}

	return p, nil
}

// auditEntry returns the audit entry of a context write on t. The service
// sets its time, and the store sets its changes.
func auditEntry(r *http.Request, p identity.Principal, t target) audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     t.ns.Name,
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        string(objectcontext.SetAction(t.ref.Kind)),
		Object:        t.obj.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		SourceIP:      clientip.String(r),
		UserAgent:     truncateUserAgent(userAgent(r)),
		Channel:       sessionauth.AuditChannel(r, channelHeader),
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     requestid.FromContext(r.Context()),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		Changes:       nil,
		ObjectID:      t.obj.UnitID(),
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}
