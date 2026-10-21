package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/service"
)

// maxBodyBytes bounds a JSON request body: a release names at most 66 files and
// a key is at most 1 MiB, in armor and escaped.
const maxBodyBytes = 2 << 20

// fixedFiles is the number of files of a release that are not archives: the
// checksums file and the signature file.
const fixedFiles = 2

// Wire shapes of the registry API.
//
// Contract: never remove or rename these fields.
type stagedDocument struct {
	Upload string `json:"upload"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type publishFile struct {
	Role   string `json:"role"`
	Name   string `json:"name"`
	Upload string `json:"upload"`
}

type publishBody struct {
	Protocols []string      `json:"protocols"`
	Files     []publishFile `json:"files"`
}

type releaseFile struct {
	Role     string `json:"role"`
	Name     string `json:"name"`
	SHA256   string `json:"sha256"`
	Platform string `json:"platform,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

type releaseDocument struct {
	Namespace      string        `json:"namespace"`
	Name           string        `json:"name"`
	Version        string        `json:"version"`
	Protocols      []string      `json:"protocols"`
	PublishedAt    string        `json:"publishedAt"`
	PublishedBy    string        `json:"publishedBy"`
	KeyFingerprint string        `json:"keyFingerprint"`
	Files          []releaseFile `json:"files"`
}

type listReleasesResponse struct {
	Releases []releaseDocument `json:"releases"`
}

type keyBody struct {
	ArmoredPublicKey string `json:"armoredPublicKey"`
}

type keyDocument struct {
	Fingerprint      string `json:"fingerprint"`
	AddedAt          string `json:"addedAt"`
	AddedBy          string `json:"addedBy"`
	ArmoredPublicKey string `json:"armoredPublicKey"`
}

type listKeysResponse struct {
	Keys []keyDocument `json:"keys"`
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func toRelease(r registry.Release) releaseDocument {
	files := make([]releaseFile, 0, fixedFiles+len(r.Archives))
	files = append(files,
		releaseFile{Role: string(service.RoleSums), Name: r.SumsFile, SHA256: r.SumsSHA256, Platform: "", Size: 0},
		releaseFile{
			Role: string(
				service.RoleSignature,
			),
			Name:     r.SignatureFile,
			SHA256:   r.SignatureSHA256,
			Platform: "",
			Size:     0,
		},
	)

	for i := range r.Archives {
		a := &r.Archives[i]
		files = append(files, releaseFile{
			Role: string(service.RoleArchive), Name: a.FileName, SHA256: a.SHA256, Platform: a.Platform, Size: a.Size,
		})
	}

	return releaseDocument{
		Namespace: r.Address.Namespace.String(), Name: r.Address.Name, Version: r.Version,
		Protocols: append([]string{}, r.Protocols...), PublishedAt: formatTime(r.PublishedAt),
		PublishedBy: r.PublishedBy, KeyFingerprint: r.KeyFingerprint, Files: files,
	}
}

func toKey(k registry.KeyRecord) keyDocument {
	return keyDocument{
		Fingerprint: k.Key.Fingerprint, AddedAt: formatTime(k.AddedAt), AddedBy: k.AddedBy,
		ArmoredPublicKey: string(k.Key.Armored),
	}
}

// callerOf returns the caller of the service for a request.
func callerOf(r *http.Request, p identity.Principal) *service.Caller {
	by := audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: clientip.String(r),
		UserAgent: truncateUserAgent(r.UserAgent()), Channel: audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: requestid.FromContext(r.Context()),
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: string(p.Kind),
		JobID: p.JobID,
	}

	// The audit log refuses a kind it does not know, so an unknown kind is
	// recorded as no kind.
	switch p.Kind {
	case identity.KindPerson, identity.KindService, identity.KindWorkload:
	default:
		by.PrincipalKind = ""
	}

	return &service.Caller{Principal: p, By: by}
}

func truncateUserAgent(s string) string {
	if len(s) <= audit.MaxUserAgentLen {
		return s
	}

	return s[:audit.MaxUserAgentLen]
}

// readBody decodes one JSON object into v. It refuses an unknown field, a
// second value and a body over the limit.
func readBody(w http.ResponseWriter, r *http.Request, v any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return badRequest("request.body-invalid", "the request body is not readable or is over 2 MiB")
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err = dec.Decode(v); err != nil || dec.More() {
		return badRequest("request.body-invalid", "the request body must be one JSON object with known fields")
	}

	return nil
}

// stage serves POST .../registry/uploads: the body is the bytes of one file.
func (h *Handler) stage(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	// Defense in depth: the service stops the write at the same limit. The early
	// answer saves the read of a body that the service would refuse.
	if r.ContentLength > h.maxFile {
		h.writeError(ctx, w, service.ErrTooLarge)

		return
	}

	body := http.MaxBytesReader(w, r.Body, h.maxFile+1)

	staged, err := h.service.Stage(ctx, callerOf(r, p), obj.Namespace(), body)
	if err != nil {
		if limit, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit && limit != nil {
			err = service.ErrTooLarge
		}

		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(
		ctx,
		w,
		http.StatusCreated,
		stagedDocument{Upload: staged.Token, SHA256: staged.SHA256, Size: staged.Size},
	)
}

// publish serves PUT .../registry/providers/{name}/versions/{version}.
func (h *Handler) publish(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	var body publishBody
	if err := readBody(w, r, &body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	req := service.PublishRequest{
		Namespace: obj.Namespace(), Name: r.PathValue("name"), Version: r.PathValue("version"),
		Protocols: body.Protocols, Files: make([]service.StagedFile, 0, len(body.Files)),
	}

	for i := range body.Files {
		f := &body.Files[i]
		req.Files = append(req.Files, service.StagedFile{Role: service.FileRole(f.Role), Name: f.Name, Token: f.Upload})
	}

	rel, err := h.service.Publish(ctx, callerOf(r, p), req)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.send(ctx, w, releaseAnswer(obj), rel)
}

// listReleases serves GET .../registry/providers/{name}/versions.
func (h *Handler) listReleases(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	found, err := h.service.Releases(ctx, obj.Namespace(), r.PathValue("name"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := make([]releaseDocument, 0, len(found))
	for i := range found {
		out = append(out, toRelease(found[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, listReleasesResponse{Releases: out})
}

// getRelease serves GET .../registry/providers/{name}/versions/{version}.
func (h *Handler) getRelease(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	rel, err := h.service.Release(ctx, obj.Namespace(), r.PathValue("name"), r.PathValue("version"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, toRelease(rel))
}

// addKey serves POST .../registry/keys.
func (h *Handler) addKey(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	var body keyBody
	if err := readBody(w, r, &body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	rec, err := h.service.AddKey(ctx, callerOf(r, p), obj.Namespace(), []byte(body.ArmoredPublicKey))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.send(ctx, w, keyAnswer(obj), rec)
}

// listKeys serves GET .../registry/keys.
func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	found, err := h.service.Keys(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := make([]keyDocument, 0, len(found))
	for _, k := range found {
		out = append(out, toKey(k))
	}

	h.writeJSON(ctx, w, http.StatusOK, listKeysResponse{Keys: out})
}

// removeKey serves DELETE .../registry/keys/{fingerprint}.
func (h *Handler) removeKey(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	fingerprint := r.PathValue("fingerprint")
	if err := h.service.RemoveKey(ctx, callerOf(r, p), obj.Namespace(), fingerprint); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.send(ctx, w, removedKeyAnswer(obj), fingerprint)
}

// answerFunc makes the answer of a write from the result that the store passes.
// The answer is the same when a store keeps it for a replay.
type answerFunc func(result any) (idempotency.Answer, error)

var errResult = errors.New("the store passed a result of another type")

// jsonAnswer makes an answer with a JSON body.
func jsonAnswer(status int, body any) (idempotency.Answer, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return idempotency.Answer{}, fmt.Errorf("encode answer: %w", err)
	}

	return idempotency.Answer{
		Headers: map[string]string{"Content-Type": "application/json"}, Body: buf.Bytes(), Status: status,
	}, nil
}

// releaseAnswer answers a publish: 200 with the release. A repeat of a
// published version answers 200 too.
func releaseAnswer(identity.Object) answerFunc {
	return func(result any) (idempotency.Answer, error) {
		rel, ok := result.(registry.Release)
		if !ok {
			return idempotency.Answer{}, errResult
		}

		return jsonAnswer(http.StatusOK, toRelease(rel))
	}
}

// keyAnswer answers a key registration: 201 with the key.
func keyAnswer(identity.Object) answerFunc {
	return func(result any) (idempotency.Answer, error) {
		rec, ok := result.(registry.KeyRecord)
		if !ok {
			return idempotency.Answer{}, errResult
		}

		return jsonAnswer(http.StatusCreated, toKey(rec))
	}
}

// removedKeyAnswer answers a key removal: 204 with no body.
func removedKeyAnswer(identity.Object) answerFunc {
	return func(any) (idempotency.Answer, error) {
		return idempotency.Answer{Headers: map[string]string{}, Body: nil, Status: http.StatusNoContent}, nil
	}
}

// send writes the answer that build makes for the result of a write.
func (h *Handler) send(ctx context.Context, w http.ResponseWriter, build answerFunc, result any) {
	a, err := build(result)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	idempotent.Write(w, a)
}
