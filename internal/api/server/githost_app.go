package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// credentialSetDocument tells that a secret is set, with its set ID and its
// creation time. It never holds the secret.
//
// Contract: never remove or rename these fields.
type credentialSetDocument struct {
	SetID     string `json:"setID"`
	CreatedAt string `json:"createdAt"`
	Set       bool   `json:"set"`
}

// gitHostAppDocument is the registration of the GitHub App on the wire.
//
// Contract: never remove or rename these fields.
type gitHostAppDocument struct {
	Kind        string                `json:"kind"`
	AppID       string                `json:"appID"`
	APIAddress  string                `json:"apiAddress"`
	HookID      string                `json:"hookID"`
	PrivateKey  credentialSetDocument `json:"privateKey"`
	EventSecret credentialSetDocument `json:"eventSecret"`
	CreatedAt   string                `json:"createdAt"`
	UpdatedAt   string                `json:"updatedAt"`
	Version     int64                 `json:"version"`
}

// gitHostAppRequest is the body of a registration. Every field is required:
// a registration holds an address, an app ID, a key and an event secret that
// belong together, and a change of any of them is a new registration.
type gitHostAppRequest struct {
	AppID       *string `json:"appID"`
	APIAddress  *string `json:"apiAddress"`
	PrivateKey  *string `json:"privateKey"`
	EventSecret *string `json:"eventSecret"`
}

func gitHostAppOf(v forge.RegistrationView) gitHostAppDocument {
	return gitHostAppDocument{
		Kind:       string(v.Kind),
		AppID:      v.AppID,
		APIAddress: v.BaseURL,
		HookID:     v.HookID,
		PrivateKey: credentialSetDocument{SetID: v.Key.ID, CreatedAt: wireTime(v.Key.CreatedAt), Set: true},
		EventSecret: credentialSetDocument{
			SetID:     v.EventSecret.ID,
			CreatedAt: wireTime(v.EventSecret.CreatedAt),
			Set:       true,
		},
		CreatedAt: wireTime(v.CreatedAt),
		UpdatedAt: wireTime(v.UpdatedAt),
		Version:   v.Version,
	}
}

// gitHostAppAnswer answers a write of the registration: 201 for the first
// registration and 200 for a replacement, with the ETag of its version. The
// result is what the store wrote, and it holds the sealed values: the answer
// takes the view of it and never a secret.
func gitHostAppAnswer(result any) (idempotency.Answer, error) {
	reg, ok := result.(forge.Registration)
	if !ok {
		return idempotency.Answer{}, errGitHostResult
	}

	status := http.StatusOK
	if reg.Version == 1 {
		status = http.StatusCreated
	}

	return jsonAnswer(status, reg.Version, gitHostAppOf(reg.View()), "")
}

var errGitHostResult = errors.New("the store passed a result of another type")

// writeAnswer sends the answer of a route, as the stored answer of a keyed
// request is sent: the stored answer and the sent answer are one.
func writeAnswer(w http.ResponseWriter, a idempotency.Answer) {
	idempotent.Write(w, a)
}

// noContentAnswer answers a delete: 204 with no body.
func noContentAnswer(any) (idempotency.Answer, error) {
	return idempotency.Answer{Status: http.StatusNoContent, Headers: map[string]string{}, Body: nil}, nil
}

// jsonAnswer makes the answer of a route: the status, the document, the ETag of
// the version when it is above 0, and the Location when it is not empty.
func jsonAnswer(status int, version int64, doc any, location string) (idempotency.Answer, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(doc); err != nil {
		return idempotency.Answer{}, fmt.Errorf("encode answer: %w", err)
	}

	headers := map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}
	if version > 0 {
		headers["Etag"] = etag(version)
	}

	if location != "" {
		headers["Location"] = location
	}

	return idempotency.Answer{Status: status, Headers: headers, Body: buf.Bytes()}, nil
}

// readGitHostBody reads the body of a Git host write under maxGitHostBodyBytes
// and decodes one JSON object with no unknown field into dst.
func readGitHostBody(w http.ResponseWriter, r *http.Request, dst any, bad error) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGitHostBodyBytes))
	if tooLarge, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit && tooLarge.Limit > 0 {
		return errGitHostBodyTooLarge
	}

	if err != nil {
		return bad
	}

	return decodeBody(body, dst, bad)
}

var errGitHostBodyTooLarge = errors.New("request body too large: the limit is 65536 bytes")

// gitHostKind reads the kind of the path. Only github has an adapter.
func gitHostKind(r *http.Request) (forge.Kind, error) {
	if kind := forge.Kind(r.PathValue("kind")); kind == forge.KindGitHub {
		return kind, nil
	}

	return "", errGitHostKind
}

// readGitHostApp serves GET /api/dustpan/v1/git-host-apps/{kind}.
func (h *Handler) readGitHostApp(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	kind, err := gitHostKind(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	view, err := h.gitHost.GetRegistration(ctx, kind)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(view.Version))
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, gitHostAppOf(view))
}

// putGitHostApp serves PUT /api/dustpan/v1/git-host-apps/{kind}. It answers 201
// for the first registration and 200 for a replacement. With If-Match, the
// write replaces that version only. A console session needs a recent sign-in.
func (h *Handler) putGitHostApp(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	h.keyed(w, r, p, keyedCall{
		build: gitHostAppAnswer,
		serve: func(w http.ResponseWriter, r *http.Request) { h.writeGitHostApp(w, r, p) },
	})
}

func (h *Handler) writeGitHostApp(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()

	view, err := h.registerGitHostApp(w, r, p)
	if err != nil {
		h.failGitHostApp(w, r, p, err)

		return
	}

	answer, err := jsonAnswer(statusOfVersion(view.Version), view.Version, gitHostAppOf(view), "")
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	writeAnswer(w, answer)
}

// registerGitHostApp checks the freshness of a console session, reads the body
// and registers the app. A missing field fails with 400, so a request cannot
// change the address and keep the old key.
func (h *Handler) registerGitHostApp(
	w http.ResponseWriter, r *http.Request, p identity.Principal,
) (forge.RegistrationView, error) {
	var none forge.RegistrationView

	if err := h.checkTokenFreshness(r); err != nil {
		return none, err
	}

	kind, err := gitHostKind(r)
	if err != nil {
		return none, err
	}

	var req gitHostAppRequest
	if bodyErr := readGitHostBody(w, r, &req, errGitHostAppBody); bodyErr != nil {
		return none, bodyErr
	}

	if req.AppID == nil || req.APIAddress == nil || req.PrivateKey == nil || req.EventSecret == nil {
		return none, errGitHostAppBody
	}

	version, _, err := ifMatch(r)
	if err != nil {
		return none, err
	}

	in := forge.RegistrationInput{
		AppID: *req.AppID, BaseURL: *req.APIAddress, PrivateKey: []byte(*req.PrivateKey),
		EventSecret: []byte(*req.EventSecret),
	}

	return h.gitHost.PutRegistration(
		r.Context(), callerEntry(r, p, identity.ActionServerManageGitHost), kind, in, version,
	)
}

// statusOfVersion answers 201 for the version 1 of an object and 200 for a
// later version.
func statusOfVersion(version int64) int {
	if version == 1 {
		return http.StatusCreated
	}

	return http.StatusOK
}

// deleteGitHostApp serves DELETE /api/dustpan/v1/git-host-apps/{kind}. It
// answers 204. A registration with an assigned installation answers 409.
func (h *Handler) deleteGitHostApp(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	h.keyed(w, r, p, keyedCall{
		build: noContentAnswer,
		serve: func(w http.ResponseWriter, r *http.Request) { h.removeGitHostApp(w, r, p) },
	})
}

func (h *Handler) removeGitHostApp(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	err := h.checkTokenFreshness(r)

	var (
		kind    forge.Kind
		version int64
	)

	if err == nil {
		kind, err = gitHostKind(r)
	}

	if err == nil {
		version, _, err = ifMatch(r)
	}

	if err == nil {
		err = h.gitHost.DeleteRegistration(
			r.Context(), callerEntry(r, p, identity.ActionServerManageGitHost), kind, version,
		)
	}

	if err != nil {
		h.failGitHostApp(w, r, p, err)

		return
	}

	writeAnswer(w, idempotency.Answer{Status: http.StatusNoContent, Headers: map[string]string{}, Body: nil})
}

// failGitHostApp writes the failure entry of a refused change of the
// registration, and answers err. The entry names the app by a fixed object.
func (h *Handler) failGitHostApp(w http.ResponseWriter, r *http.Request, p identity.Principal, err error) {
	h.appendServerChange(r, p, serverChange{
		at: h.now(), action: identity.ActionServerManageGitHost, object: "git-host-app:" + string(forge.KindGitHub),
		outcome: audit.OutcomeFailure,
	})
	h.writeGitHostError(r, w, err)
}

// writeGitHostError answers err, with the Retry-After header of a Git host that
// gave a wait time.
func (h *Handler) writeGitHostError(r *http.Request, w http.ResponseWriter, err error) {
	if seconds, ok := retryAfterSeconds(err); ok {
		w.Header().Set("Retry-After", seconds)
	}

	h.writeError(r.Context(), w, err)
}

// gitHostInstallationDocument is one assignment on the wire.
//
// Contract: never remove or rename these fields.
type gitHostInstallationDocument struct {
	InstallationID string `json:"installationID"`
	Namespace      string `json:"namespace"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	Version        int64  `json:"version"`
}

// gitHostInstallationsDocument is the list of the assignments, in the order of
// the installation IDs as numbers.
//
// Contract: never remove or rename the installations field.
type gitHostInstallationsDocument struct {
	Installations []gitHostInstallationDocument `json:"installations"`
}

// gitHostInstallationRequest is the body of an assignment.
type gitHostInstallationRequest struct {
	Namespace *string `json:"namespace"`
}

func gitHostInstallationOf(a forge.Assignment) gitHostInstallationDocument {
	return gitHostInstallationDocument{
		InstallationID: a.InstallationID, Namespace: a.Namespace.String(), CreatedAt: wireTime(a.CreatedAt),
		UpdatedAt: wireTime(a.UpdatedAt), Version: a.Version,
	}
}

// gitHostInstallationAnswer answers an assignment: 201 for a new one and 200
// for a move, with the ETag of its version.
func gitHostInstallationAnswer(result any) (idempotency.Answer, error) {
	a, ok := result.(forge.Assignment)
	if !ok {
		return idempotency.Answer{}, errGitHostResult
	}

	return jsonAnswer(statusOfVersion(a.Version), a.Version, gitHostInstallationOf(a), gitHostInstallationLocation(a))
}

func gitHostInstallationLocation(a forge.Assignment) string {
	return "/api/dustpan/v1/git-host-installations/" + a.InstallationID
}

// listGitHostInstallations serves GET /api/dustpan/v1/git-host-installations.
func (h *Handler) listGitHostInstallations(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	list, err := h.gitHost.ListAssignments(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := gitHostInstallationsDocument{Installations: make([]gitHostInstallationDocument, 0, len(list))}
	for i := range list {
		out.Installations = append(out.Installations, gitHostInstallationOf(list[i]))
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, out)
}

// readGitHostInstallation serves GET
// /api/dustpan/v1/git-host-installations/{installation_id}.
func (h *Handler) readGitHostInstallation(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	a, err := h.gitHost.GetAssignment(ctx, r.PathValue("installation_id"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(a.Version))
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, gitHostInstallationOf(a))
}

// putGitHostInstallation serves PUT
// /api/dustpan/v1/git-host-installations/{installation_id}: it assigns the
// installation to a namespace, or moves it to another one. It answers 201 for a
// new assignment and 200 for a move.
func (h *Handler) putGitHostInstallation(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	h.keyed(w, r, p, keyedCall{
		build: gitHostInstallationAnswer,
		serve: func(w http.ResponseWriter, r *http.Request) { h.assignGitHostInstallation(w, r, p) },
	})
}

func (h *Handler) assignGitHostInstallation(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()
	id := r.PathValue("installation_id")

	a, err := h.assign(w, r, p, id)
	if err != nil {
		h.failGitHostInstallation(w, r, p, id, err)

		return
	}

	answer, err := jsonAnswer(
		statusOfVersion(a.Version),
		a.Version,
		gitHostInstallationOf(a),
		gitHostInstallationLocation(a),
	)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	writeAnswer(w, answer)
}

func (h *Handler) assign(
	w http.ResponseWriter, r *http.Request, p identity.Principal, id string,
) (forge.Assignment, error) {
	var none forge.Assignment

	if err := h.checkTokenFreshness(r); err != nil {
		return none, err
	}

	var req gitHostInstallationRequest
	if err := readGitHostBody(w, r, &req, errGitHostInstallationBody); err != nil {
		return none, err
	}

	if req.Namespace == nil {
		return none, errGitHostInstallationBody
	}

	ns, err := namespace.ParseName(*req.Namespace)
	if err != nil {
		return none, err
	}

	version, _, err := ifMatch(r)
	if err != nil {
		return none, err
	}

	return h.gitHost.AssignInstallation(
		r.Context(), callerEntry(r, p, identity.ActionServerManageGitHost), id, ns, version,
	)
}

// deleteGitHostInstallation serves DELETE
// /api/dustpan/v1/git-host-installations/{installation_id}. It answers 204. The
// connections of the namespace stop at their next use.
func (h *Handler) deleteGitHostInstallation(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	h.keyed(w, r, p, keyedCall{
		build: noContentAnswer,
		serve: func(w http.ResponseWriter, r *http.Request) { h.unassignGitHostInstallation(w, r, p) },
	})
}

func (h *Handler) unassignGitHostInstallation(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	id := r.PathValue("installation_id")

	err := h.checkTokenFreshness(r)

	var version int64

	if err == nil {
		version, _, err = ifMatch(r)
	}

	if err == nil {
		err = h.gitHost.UnassignInstallation(
			r.Context(), callerEntry(r, p, identity.ActionServerManageGitHost), id, version,
		)
	}

	if err != nil {
		h.failGitHostInstallation(w, r, p, id, err)

		return
	}

	writeAnswer(w, idempotency.Answer{Status: http.StatusNoContent, Headers: map[string]string{}, Body: nil})
}

// failGitHostInstallation writes the failure entry of a refused change of an
// assignment, and answers err. The entry names an installation ID that is not
// valid with a fixed object, so a request cannot put text of any length into
// the audit log.
func (h *Handler) failGitHostInstallation(
	w http.ResponseWriter, r *http.Request, p identity.Principal, id string, err error,
) {
	object := "git-host-installation:?"
	if forge.ValidNumericID(id) {
		object = "git-host-installation:" + id
	}

	h.appendServerChange(r, p, serverChange{
		at: h.now(), action: identity.ActionServerManageGitHost, object: object, outcome: audit.OutcomeFailure,
	})
	h.writeGitHostError(r, w, err)
}
