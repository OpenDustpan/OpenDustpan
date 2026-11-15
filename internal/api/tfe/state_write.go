package tfe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	// maxStateCreateBytes bounds a state version create body: the base64
	// outputs document alone may hold 16 MiB.
	maxStateCreateBytes = 32 << 20
	// maxOutputNameLen caps one output name of the json-state-outputs
	// document, in bytes. The version keeps the document, so a longer
	// name is refused instead of stored.
	maxOutputNameLen = 256
	// inlineStateDetail is the 422 detail when a client sends the inline
	// state attribute, which the server never serves: state always follows
	// through the upload capability.
	inlineStateDetail = "inline state is not supported: upload through hosted-state-upload-url"
)

// stateVersionCreateAttributes are the attributes of a state version create:
// the serial, lineage, MD5 and force flag the server checks, the base64
// json-state-outputs document it keeps, and the inline state attributes it
// never serves. The json-state rendering is ignored by design: the server
// keeps the state itself from the upload and the outputs from this create.
type stateVersionCreateAttributes struct {
	Serial           *int64  `json:"serial"`
	Lineage          *string `json:"lineage"`
	MD5              *string `json:"md5"`
	Force            *bool   `json:"force"`
	State            *string `json:"state"`
	JSONState        *string `json:"json-state"`         //nolint:tagliatelle // The TFE wire name; it must not change.
	JSONStateOutputs *string `json:"json-state-outputs"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateVersionCreateData holds the data object of a state version create.
type stateVersionCreateData struct {
	Attributes stateVersionCreateAttributes `json:"attributes"`
}

// stateVersionCreateRequest is the body of a state version create.
type stateVersionCreateRequest struct {
	Data stateVersionCreateData `json:"data"`
}

// parseStateVersionCreate reads a state version create body into the NewState
// the service declares. The inline state attribute is not served: a client
// that sends it gets 422. Missing attributes get 422 too; the service checks
// the values.
func parseStateVersionCreate(body []byte) (workspace.NewState, error) {
	var req stateVersionCreateRequest

	if err := json.Unmarshal(body, &req); err != nil {
		return workspace.NewState{}, errInvalidBody
	}

	attrs := req.Data.Attributes

	if attrs.State != nil && *attrs.State != "" {
		return workspace.NewState{}, fmt.Errorf("%w: %s", errInvalidBody, inlineStateDetail)
	}

	if attrs.Serial == nil || attrs.Lineage == nil || attrs.MD5 == nil {
		return workspace.NewState{}, fmt.Errorf(
			"%w: state version needs serial, lineage and md5",
			workspace.ErrInvalidState,
		)
	}

	outputs, err := decodeStateOutputs(attrs.JSONStateOutputs)
	if err != nil {
		return workspace.NewState{}, err
	}

	force := attrs.Force != nil && *attrs.Force

	return workspace.NewState{
		Lineage: *attrs.Lineage,
		MD5:     *attrs.MD5,
		Outputs: outputs,
		Serial:  *attrs.Serial,
		Force:   force,
	}, nil
}

// decodeStateOutputs decodes the base64 json-state-outputs attribute into
// the opaque outputs document the state version keeps. A missing attribute
// means no outputs. Anything else that does not decode, or that is not the
// outputs shape the read routes render, fails with a 422 error. An output
// name over maxOutputNameLen bytes fails too: the version keeps the
// document, so a longer name is refused instead of stored. The CLI
// sends the flat map of outputs at the top level; the wrapped form stays
// accepted for versions stored before the flat shape was served.
func decodeStateOutputs(raw *string) ([]byte, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}

	decoded, err := base64.StdEncoding.DecodeString(*raw)
	if err != nil {
		return nil, fmt.Errorf("%w: json-state-outputs is not valid base64", workspace.ErrInvalidState)
	}

	outputs, err := parseStoredOutputs(decoded)
	if err != nil {
		return nil, fmt.Errorf("%w: json-state-outputs is not valid: %w", workspace.ErrInvalidState, err)
	}

	for name := range outputs {
		if len(name) > maxOutputNameLen {
			return nil, fmt.Errorf("%w: output name over %d bytes", workspace.ErrInvalidState, maxOutputNameLen)
		}
	}

	return decoded, nil
}

// readCreateBody reads a state version create body, bounded by
// maxStateCreateBytes. An oversized body answers 413, an unreadable one 422.
func (h *Handler) readCreateBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(
		io.LimitReader(http.MaxBytesReader(w, r.Body, maxStateCreateBytes), maxStateCreateBytes+1),
	)
	if err != nil {
		if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok && tooLarge != nil {
			h.writeError(
				r.Context(),
				w,
				fmt.Errorf("%w: body over %d bytes", workspace.ErrTooLarge, maxStateCreateBytes),
			)
		} else {
			h.writeError(r.Context(), w, errInvalidBody)
		}

		return nil, false
	}

	if int64(len(body)) > maxStateCreateBytes {
		h.writeError(
			r.Context(),
			w,
			fmt.Errorf("%w: body over %d bytes", workspace.ErrTooLarge, maxStateCreateBytes),
		)

		return nil, false
	}

	return body, true
}

// createStateVersion serves POST /api/v2/workspaces/{id}/state-versions. It
// declares the version with the caller as the holder and answers 201 with
// the state version document holding the absolute upload URLs. A caller
// without the lock gets 409.
func (h *Handler) createStateVersion(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	release, ok := h.hold(w, r, bodygate.Reservation(r, maxStateCreateBytes))
	if !ok {
		return
	}

	defer release()

	body, ok := h.readCreateBody(w, r)
	if !ok {
		return
	}

	in, err := parseStateVersionCreate(body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	id := workspace.ID(r.PathValue("id"))

	// A workspace that no collection holds takes no state.
	if err = h.placer.Require(ctx, obj); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	allow, err := h.allowance(ctx, p, id, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The domain reports each override the declare uses, before it writes: the
	// audit entry of that override is written by the record callback, and a
	// failed entry refuses the declare (ADR 41, decision 5).
	version := strconv.FormatInt(in.Serial, 10)
	record := func(override workspace.Override) error {
		return h.appendOverrideAudit(r, p, obj, tofuunit.OverrideAction(override), version)
	}

	auth := workspace.Authority{Allow: allow, Record: record}

	// The job of a job token is the origin of the declared version, which the
	// finalize of that version reads. Any other token passes no job.
	pending, err := h.workspaces.BeginState(workspace.WithJob(ctx, p.JobTokenID()), id, p.Subject, in, auth)
	if err != nil {
		h.rules().RecordOverrideRefusal(ctx, r, p, obj, err)
		h.writeError(ctx, w, h.lineageLockedDetail(ctx, id, p.Subject, err))

		return
	}

	h.writeStateVersionCreate(ctx, w, r, pending)
}

// lineageLockedDetail names the force-unlock command in a refused state
// upload while the caller holds the lock of the workspace: the CLI took the
// lock for the upload and does not release it after the refusal. The lock
// ID is the namespace and workspace name the CLI accepts. A lock of another
// holder is a lock of another job, so the error never tells the caller to
// clear it. An unlocked workspace, an unreadable workspace and any other
// error keep the error as is.
func (h *Handler) lineageLockedDetail(ctx context.Context, id workspace.ID, subject string, err error) error {
	if !errors.Is(err, workspace.ErrLineageMismatch) {
		return err
	}

	ws, readErr := h.workspaces.GetByID(ctx, id)
	if readErr != nil || !ws.Lock.Locked() || ws.Lock.Holder != subject {
		return err
	}

	return fmt.Errorf("%w: workspace stays locked, run tofu force-unlock %s to clear the lock", err, ws.Ref)
}

// writeStateVersionCreate writes the 201 create response: the state version
// document with the absolute upload URLs of the minted capability.
func (h *Handler) writeStateVersionCreate(
	ctx context.Context, w http.ResponseWriter, r *http.Request, pending workspace.PendingState,
) {
	doc, err := h.stateVersionDocument(r, pending.Version)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	base := h.ExternalBaseURL(r)
	id := pending.Version.ID.String()
	revealed := pending.Upload.Reveal()
	doc.Attributes.UploadURL = base + "/api/v2/state-uploads/" + id + "/" + revealed
	doc.Attributes.JSONUploadURL = base + "/api/v2/json-state-uploads/" + id + "/" + revealed

	h.writeJSON(ctx, w, http.StatusCreated, stateVersionDocument{Data: doc})
}

// parseUploadToken reads the capability in the token path value. A malformed
// token is denied as a 403, like a wrong one.
func parseUploadToken(r *http.Request) (token.Token, error) {
	tok, err := token.Parse(r.PathValue("token"))
	if err != nil {
		return token.Token{}, fmt.Errorf("%w: upload capability is not valid", workspace.ErrUploadDenied)
	}

	return tok, nil
}

// uploadState serves PUT /api/v2/state-uploads/{id}/{token}. It carries no
// Authorization header: the token in the path is the credential. A retry of
// a finalized upload with the same capability and the same content answers
// 200. A wrong capability answers 403, a lost lock 409 and a checksum
// mismatch 422.
func (h *Handler) uploadState(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	tok, err := parseUploadToken(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	id := workspace.StateVersionID(r.PathValue("id"))

	ns, checkErr := h.workspaces.CheckUpload(ctx, id, tok)
	if checkErr != nil {
		h.writeError(ctx, w, checkErr)

		return
	}

	if h.addressGate != nil {
		if gateErr := h.addressGate.CheckAddress(ctx, ns); gateErr != nil {
			h.writeError(ctx, w, gateErr)

			return
		}
	}

	release, ok := h.holdLease(w, r, bodygate.StreamReservation(r))
	if !ok {
		return
	}

	defer release.Release()

	ctx = workspace.WithBudget(ctx, release)

	if _, err := h.workspaces.Upload(ctx, id, tok, r.Body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// uploadJSONState serves PUT /api/v2/json-state-uploads/{id}/{token}. It
// carries no Authorization header: the token in the path is the credential.
// The server reads the JSON rendering for the sensitive value paths that
// sensitive_values marks and keeps only those paths, never a value. The rest
// of the rendering is discarded.
func (h *Handler) uploadJSONState(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	tok, err := parseUploadToken(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	id := workspace.StateVersionID(r.PathValue("id"))

	ns, checkErr := h.workspaces.CheckJSONRenderingUpload(ctx, id, tok)
	if checkErr != nil {
		h.writeError(ctx, w, checkErr)

		return
	}

	if h.addressGate != nil {
		if gateErr := h.addressGate.CheckAddress(ctx, ns); gateErr != nil {
			h.writeError(ctx, w, gateErr)

			return
		}
	}

	release, ok := h.hold(w, r, bodygate.Reservation(r, h.maxState))
	if !ok {
		return
	}

	defer release()

	discardErr := h.workspaces.DiscardJSONRendering(ctx, id, tok, r.Body)
	if discardErr != nil {
		h.writeError(ctx, w, discardErr)

		return
	}

	w.WriteHeader(http.StatusOK)
}
