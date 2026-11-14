package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
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

// providerDocument is one provider of a namespace in a list.
type providerDocument struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// listProvidersResponse is one page of GET .../registry/providers. Providers
// is never null. NextPageToken is empty on the last page.
type listProvidersResponse struct {
	Providers     []providerDocument `json:"providers"`
	NextPageToken string             `json:"nextPageToken"`
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
		UserAgent: audit.TruncateUserAgent(userAgent(r)), Channel: sessionauth.AuditChannel(r, channelHeader),
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

// readBody decodes one JSON object into v. It refuses an unknown field, a
// second value and a body over the limit.
func readBody(w http.ResponseWriter, r *http.Request, v any) error {
	return route.DecodeBody(w, r, maxBodyBytes, v)
}

// uploadMediaType is the media type of the body of an upload.
const uploadMediaType = "application/octet-stream"

// stage serves POST .../registry/uploads: the body is the bytes of one file.
func (h *Handler) stage(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	// Only raw bytes are a file. A body with another media type, for example a
	// form that curl sends with --data-binary and no header, is refused before
	// it is read.
	mediaType, _, typeErr := mime.ParseMediaType(apidesc.Header(r, "Content-Type"))
	if typeErr != nil || mediaType != uploadMediaType {
		h.writeError(ctx, w, problem.Fixed(
			http.StatusUnsupportedMediaType, errcode.RequestContentTypeUnsupported,
			"send the file with the header Content-Type: "+uploadMediaType,
		))

		return
	}

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

const (
	// defaultProviderPage is the page size when the request has no pageSize.
	defaultProviderPage = 100
	// maxProviderPage is the largest page size a request may ask for. It stays
	// at or below the largest limit of the store, which one page reads.
	maxProviderPage = registry.MaxAddressPage
)

var (
	errProviderPageSize = problem.BadRequest(
		errcode.RequestPageSizeInvalid,
		fmt.Sprintf("invalid page size: pageSize is a number from 1 to %d", maxProviderPage),
	)
	errProviderPageToken = problem.BadRequest(
		errcode.RequestPageTokenInvalid, "invalid page token: provide the nextPageToken of the page before",
	)
	// The causes of the four refusals of a provider page token. Each guard
	// wraps its own cause, so the caller and a test can tell which guard
	// refused.
	errProviderTokenLength  = errors.New("provider page token is over the length limit")
	errProviderTokenDecode  = errors.New("provider page token does not decode")
	errProviderTokenEmpty   = errors.New("provider page token holds no name")
	errProviderTokenAddress = errors.New("provider page token holds no provider name")
)

// providerToken is the opaque page token of the provider list: the name of
// the last provider of the page before.
type providerToken struct {
	After string `json:"a"`
}

// providerQuery is one validated list request: the page size and the name
// after which the page starts. After is empty for the first page.
type providerQuery struct {
	pageSize int
	after    string
}

// parseProviderQuery reads the pageSize and the pageToken. The token is
// opaque JSON. It is untrusted input: the name that it holds is checked
// against the same rules as the name of a provider, and a value that fails is
// refused, never repaired.
func parseProviderQuery(r *http.Request, ns namespace.Name) (providerQuery, error) {
	q := queryOf(r)
	out := providerQuery{pageSize: defaultProviderPage, after: ""}

	if raw := q.Get("pageSize"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxProviderPage {
			return providerQuery{}, errProviderPageSize
		}

		out.pageSize = n
	}

	if raw := q.Get("pageToken"); raw != "" {
		after, err := parseProviderToken(raw, ns)
		if err != nil {
			return providerQuery{}, err
		}

		out.after = after
	}

	return out, nil
}

// parseProviderToken reads the cursor name of a provider page token. The
// token is untrusted input: it is refused, never repaired.
func parseProviderToken(raw string, ns namespace.Name) (string, error) {
	// Defense in depth: pagetoken.Decode checks the length too; this cap
	// stays so that the handler refuses a long token before the decode.
	if len(raw) > pagetoken.MaxLen {
		return "", fmt.Errorf("%w: %w", errProviderTokenLength, errProviderPageToken)
	}

	var token providerToken
	if err := pagetoken.Decode(raw, &token); err != nil {
		return "", fmt.Errorf("%w: %w", errProviderTokenDecode, errProviderPageToken)
	}

	// The first page carries no token: a token that holds no name is not a
	// page token of this list.
	if token.After == "" {
		return "", fmt.Errorf("%w: %w", errProviderTokenEmpty, errProviderPageToken)
	}

	// Defense in depth: the store checks the cursor too; this check stays so
	// that the handler refuses a bad token before the store call.
	addr := registry.Address{Namespace: ns, Kind: registry.KindProvider, Name: token.After, System: ""}
	if addr.Validate() != nil {
		return "", fmt.Errorf("%w: %w", errProviderTokenAddress, errProviderPageToken)
	}

	return addr.Name, nil
}

// listProviders serves GET .../registry/providers: one page of the providers
// of the namespace that have at least one release, ordered by name. A bad
// pageSize or pageToken answers 400.
func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	query, err := parseProviderQuery(r, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	names, next, err := h.service.Providers(ctx, obj.Namespace(), query.after, query.pageSize)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	page := listProvidersResponse{Providers: make([]providerDocument, 0, len(names)), NextPageToken: ""}

	for _, name := range names {
		page.Providers = append(page.Providers, providerDocument{Namespace: obj.Namespace().String(), Name: name})
	}

	if next != "" {
		token, encodeErr := pagetoken.Encode(providerToken{After: next})
		if encodeErr != nil {
			h.writeError(ctx, w, encodeErr)

			return
		}

		page.NextPageToken = token
	}

	h.writeJSON(ctx, w, http.StatusOK, page)
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
