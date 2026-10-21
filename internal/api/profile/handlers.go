package profile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

const (
	defaultPageSize = 100
	maxPageSize     = 500
	// maxBodyBytes bounds a request body: a document of 256 KiB, escaped.
	maxBodyBytes = 1 << 20
	// noStore is the Cache-Control of an answer that can hold a secret.
	noStore = "no-store"
)

// profileEntry is one profile as the API answers it. Revision is the number
// of the newest revision. LatestRevision is set by the writes that make one.
type profileEntry struct {
	ID             string         `json:"id"`
	Namespace      string         `json:"namespace"`
	Name           string         `json:"name"`
	Folder         string         `json:"folder,omitempty"`
	Revision       int            `json:"revision"`
	ETag           string         `json:"etag"`
	CreatedAt      string         `json:"createdAt"`
	CreatedBy      string         `json:"createdBy"`
	UpdatedAt      string         `json:"updatedAt"`
	LatestRevision *revisionEntry `json:"latestRevision,omitempty"`
}

// revisionEntry is one revision. Document and Entries are set when one
// revision is read, not in a list.
type revisionEntry struct {
	Profile   string       `json:"profile"`
	Revision  int          `json:"revision"`
	SHA256    string       `json:"sha256"`
	Author    string       `json:"author"`
	CreatedAt string       `json:"createdAt"`
	Reason    string       `json:"reason,omitempty"`
	Document  *string      `json:"document,omitempty"`
	Entries   []entryValue `json:"entries,omitempty"`
}

// entryValue is one entry. A secret entry holds no value unless the caller
// revealed it with showSecrets.
type entryValue struct {
	Path        string `json:"path"`
	Secret      bool   `json:"secret"`
	Final       bool   `json:"final,omitempty"`
	NeverStore  bool   `json:"neverStore,omitempty"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Value       any    `json:"value,omitempty"`
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func toProfile(p profile.Profile) profileEntry {
	return profileEntry{
		ID: string(p.ID), Namespace: p.Namespace.String(), Name: string(p.Name), Folder: p.Name.Folder(),
		Revision: p.Latest, ETag: p.ETag(), CreatedAt: formatTime(p.CreatedAt), CreatedBy: p.CreatedBy,
		UpdatedAt: formatTime(p.UpdatedAt), LatestRevision: nil,
	}
}

func toRevision(r profile.Revision) revisionEntry {
	return revisionEntry{
		Profile: string(r.Profile), Revision: r.Number, SHA256: r.SHA256, Author: r.Author,
		CreatedAt: formatTime(r.CreatedAt), Reason: r.Reason, Document: nil, Entries: nil,
	}
}

func toEntries(entries []profile.Entry, secrets map[string]string) []entryValue {
	out := make([]entryValue, 0, len(entries))

	for _, e := range entries {
		v := entryValue{
			Path: e.Path, Secret: e.Secret, Final: e.Final, NeverStore: e.NeverStore,
			Description: e.Description, Kind: e.Kind, Value: e.Value,
		}

		if e.Secret {
			v.Value = nil

			if plain, ok := secrets[e.Path]; ok {
				v.Value = plain
			}
		}

		out = append(out, v)
	}

	return out
}

func actorOf(r *http.Request, p identity.Principal) *profile.Actor {
	by := audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: clientip.String(r),
		UserAgent: truncateUserAgent(r.UserAgent()), Channel: audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: requestid.FromContext(r.Context()),
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	return &profile.Actor{Principal: p, By: by}
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
		return badRequest("request.body-invalid", "the request body is not readable or is over 1 MiB")
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err = dec.Decode(v); err != nil || dec.More() {
		return badRequest("request.body-invalid", "the request body must be one JSON object with known fields")
	}

	return nil
}

// targetOf reads the profile of the path in the namespace of obj. A name of
// two segments is one escaped segment (payments%2Faws-prod).
func targetOf(r *http.Request, obj identity.Object) (profile.Target, error) {
	ref, err := profile.ParseRef(r.PathValue("profile"))
	if err != nil {
		return profile.Target{Namespace: "", Ref: profile.RefByName("")}, fmt.Errorf("profile: %w", err)
	}

	return profile.Target{Namespace: obj.Namespace(), Ref: ref}, nil
}

type createBody struct {
	Name     string `json:"name"`
	Document string `json:"document"`
	Reason   string `json:"reason"`
}

// create serves POST /profiles.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	var body createBody
	if err := readBody(w, r, &body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	name, err := profile.ParseName(body.Name)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	in := profile.NewProfile{Name: name, Document: []byte(body.Document), Reason: body.Reason}

	created, rev, err := h.profiles.Create(ctx, actorOf(r, p), obj.Namespace(), in)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.send(ctx, w, createdAnswer(obj), profile.Written{Profile: created, Revision: &rev})
}

// answerFunc makes the answer of a write from the result that the store
// passes: a profile.Written. The answer is the same when a store keeps it
// for a replay.
type answerFunc func(result any) (idempotency.Answer, error)

// createdAnswer answers a create: 201 with the profile, its first revision,
// its ETag and its Location.
func createdAnswer(obj identity.Object) answerFunc {
	return func(result any) (idempotency.Answer, error) {
		w, ok := result.(profile.Written)
		if !ok || w.Revision == nil {
			return idempotency.Answer{}, errWrittenResult
		}

		a, err := profileAnswer(http.StatusCreated, w.Profile, w.Revision)
		if err != nil {
			return idempotency.Answer{}, err
		}

		a.Headers["Location"] = location(obj.Namespace(), w.Profile)

		return a, nil
	}
}

// savedAnswer answers a write of a revision with status: the profile, the
// revision and the ETag.
func savedAnswer(status int) answerFunc {
	return func(result any) (idempotency.Answer, error) {
		w, ok := result.(profile.Written)
		if !ok || w.Revision == nil {
			return idempotency.Answer{}, errWrittenResult
		}

		return profileAnswer(status, w.Profile, w.Revision)
	}
}

// deletedAnswer answers a delete: 204 with no body.
func deletedAnswer() answerFunc {
	return func(any) (idempotency.Answer, error) {
		return idempotency.Answer{Headers: map[string]string{}, Body: nil, Status: http.StatusNoContent}, nil
	}
}

var errWrittenResult = errors.New("the store passed a result that is not a written profile")

// profileAnswer is the answer of a write: the profile with the revision that
// the write made.
func profileAnswer(status int, p profile.Profile, rev *profile.Revision) (idempotency.Answer, error) {
	entry := toProfile(p)
	r := toRevision(*rev)
	entry.LatestRevision = &r

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(entry); err != nil {
		return idempotency.Answer{}, fmt.Errorf("encode profile: %w", err)
	}

	return idempotency.Answer{
		Headers: map[string]string{"Content-Type": "application/json", "ETag": p.ETag()},
		Body:    buf.Bytes(),
		Status:  status,
	}, nil
}

// send writes the answer that build makes for the result of a write.
func (h *Handler) send(ctx context.Context, w http.ResponseWriter, build answerFunc, written profile.Written) {
	a, err := build(written)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	idempotent.Write(w, a)
}

func location(ns namespace.Name, p profile.Profile) string {
	return v1Prefix + "/namespaces/" + url.PathEscape(ns.String()) + "/profiles/" + string(p.ID)
}

// writeProfile answers a profile, with the revision that a write made.
func (h *Handler) writeProfile(
	ctx context.Context, w http.ResponseWriter, status int, p profile.Profile, rev *profile.Revision,
) {
	entry := toProfile(p)

	if rev != nil {
		r := toRevision(*rev)
		entry.LatestRevision = &r
	}

	h.writeJSON(ctx, w, status, entry)
}

// get serves GET /profiles/{profile}.
func (h *Handler) get(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	target, err := targetOf(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	got, err := h.profiles.Get(ctx, actorOf(r, p), target)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", got.ETag())
	h.writeProfile(ctx, w, http.StatusOK, got, nil)
}

// listResponse is one page of profiles.
type listResponse struct {
	Profiles      []profileEntry `json:"profiles"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
}

type pageToken struct {
	After       string `json:"a"`
	Fingerprint string `json:"f"`
}

func fingerprint(ns namespace.Name, folder string) string {
	return ns.String() + "\x00" + folder
}

func pageSizeOf(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("pageSize")
	if raw == "" {
		return defaultPageSize, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxPageSize {
		return 0, badRequest(
			"request.invalid-query",
			fmt.Sprintf("pageSize must be a number from 1 to %d", maxPageSize),
		)
	}

	return n, nil
}

// list serves GET /profiles.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()
	ns := obj.Namespace()
	folder := r.URL.Query().Get("folder")

	size, err := pageSizeOf(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	after, err := decodeToken(r.URL.Query().Get("pageToken"), fingerprint(ns, folder))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	page, err := h.profiles.List(
		ctx,
		actorOf(r, p),
		profile.Filter{Namespace: ns, Folder: folder, After: after, Limit: size},
	)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	resp := listResponse{Profiles: make([]profileEntry, 0, len(page.Profiles)), NextPageToken: ""}
	for _, pr := range page.Profiles {
		resp.Profiles = append(resp.Profiles, toProfile(pr))
	}

	if page.Next != "" {
		raw, marshalErr := json.Marshal(pageToken{After: string(page.Next), Fingerprint: fingerprint(ns, folder)})
		if marshalErr != nil {
			h.writeError(ctx, w, marshalErr)

			return
		}

		resp.NextPageToken = base64.RawURLEncoding.EncodeToString(raw)
	}

	h.writeJSON(ctx, w, http.StatusOK, resp)
}

func decodeToken(raw, fp string) (profile.Name, error) {
	if raw == "" {
		return "", nil
	}

	bad := badRequest("request.invalid-query", "pageToken does not belong to this query: start again without it")

	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", bad
	}

	var t pageToken
	if err = json.Unmarshal(data, &t); err != nil || t.Fingerprint != fp {
		return "", bad
	}

	return profile.Name(t.After), nil
}

var (
	errPrecondition = &statusError{
		status: http.StatusPreconditionFailed, code: "request.precondition-failed",
		message: "If-Match does not match the profile: read it again and send the new ETag",
	}
	errPreconditionRequired = &statusError{
		status: http.StatusPreconditionRequired, code: "request.precondition-required",
		message: "send the ETag that you read in If-Match",
	}
)

// ifMatch reads the If-Match header against the profile got. It returns the
// revision number in the tag, and false when the header is missing. A tag
// for another profile, and a weak tag, answer errPrecondition: If-Match
// compares tags in the strong way.
func ifMatch(r *http.Request, got profile.Profile) (int, bool, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, false, nil
	}

	if strings.HasPrefix(raw, "W/") {
		return 0, true, errPrecondition
	}

	raw = strings.Trim(raw, `"`)

	id, num, found := strings.Cut(raw, ".")
	n, convErr := strconv.Atoi(num)

	if !found || convErr != nil || n < 1 || id != string(got.ID) {
		return 0, true, errPrecondition
	}

	return n, true, nil
}

// stale turns a conflict of a write that carries If-Match into 412.
func stale(err error) error {
	if errors.Is(err, profile.ErrConflict) {
		return &statusError{
			status:  http.StatusPreconditionFailed,
			code:    "request.precondition-failed",
			message: err.Error(),
		}
	}

	return err
}

// delete serves DELETE /profiles/{profile}.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	target, err := targetOf(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	a := actorOf(r, p)

	got, err := h.profiles.Locate(ctx, a, target, identity.ActionProfileManage)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	rev, _, err := ifMatch(r, got)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	target.Ref = profile.RefByID(got.ID)

	err = h.profiles.Delete(ctx, a, target, profile.Removal{IfLatest: rev, Reason: r.URL.Query().Get("reason")})
	if err != nil {
		h.writeError(ctx, w, stale(err))

		return
	}

	var none profile.Written

	h.send(ctx, w, deletedAnswer(), none)
}

// listRevisions serves GET /profiles/{profile}/revisions.
func (h *Handler) listRevisions(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	target, err := targetOf(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	size, err := pageSizeOf(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	before := 0

	if raw := r.URL.Query().Get("pageToken"); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 1 {
			h.writeError(ctx, w, badRequest("request.invalid-query", "pageToken is not valid: start again without it"))

			return
		}

		before = n
	}

	revs, err := h.profiles.Revisions(ctx, actorOf(r, p), target, profile.RevisionPage{Before: before, Limit: size + 1})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	resp := struct {
		Revisions     []revisionEntry `json:"revisions"`
		NextPageToken string          `json:"nextPageToken,omitempty"`
	}{Revisions: make([]revisionEntry, 0, len(revs)), NextPageToken: ""}

	for i, rev := range revs {
		if i == size {
			resp.NextPageToken = strconv.Itoa(revs[i-1].Number)

			break
		}

		resp.Revisions = append(resp.Revisions, toRevision(rev))
	}

	h.writeJSON(ctx, w, http.StatusOK, resp)
}

type saveBody struct {
	Document string `json:"document"`
	Reason   string `json:"reason"`
}

// save serves POST /profiles/{profile}/revisions.
func (h *Handler) save(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	var body saveBody
	if err := readBody(w, r, &body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.write(w, r, p, obj, writeOp{
		do: func(a *profile.Actor, t profile.Target, base int) (profile.Profile, profile.Revision, error) {
			in := profile.Change{Base: base, Document: []byte(body.Document), Reason: body.Reason}

			return h.profiles.Save(ctx, a, t, in)
		},
		status: http.StatusCreated,
	})
}

// writeOp is one write of a profile that carries If-Match: the call and the
// status of its answer.
type writeOp struct {
	do     func(a *profile.Actor, t profile.Target, base int) (profile.Profile, profile.Revision, error)
	status int
}

// write runs one write of a profile that carries If-Match.
func (h *Handler) write(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, op writeOp) {
	ctx := r.Context()

	target, err := targetOf(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	a := actorOf(r, p)

	got, err := h.profiles.Locate(ctx, a, target, identity.ActionProfileWrite)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	base, ok, err := ifMatch(r, got)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !ok {
		h.writeError(ctx, w, errPreconditionRequired)

		return
	}

	target.Ref = profile.RefByID(got.ID)

	updated, rev, err := op.do(a, target, base)
	if err != nil {
		h.writeError(ctx, w, stale(err))

		return
	}

	h.send(ctx, w, savedAnswer(op.status), profile.Written{Profile: updated, Revision: &rev})
}

// readOptions reads the revision path value and the showSecrets query value.
func readOptions(r *http.Request, revision string) (profile.ReadOptions, error) {
	opts := profile.ReadOptions{Fresh: nil, Revision: 0, Reveal: false}

	if raw := r.URL.Query().Get("showSecrets"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return opts, badRequest("request.invalid-query", "showSecrets must be true or false")
		}

		opts.Reveal = b
	}

	if revision != "" && revision != "latest" {
		n, err := strconv.Atoi(revision)
		if err != nil || n < 1 {
			return opts, badRequest("request.invalid-path", "revision must be a number of 1 or more, or latest")
		}

		opts.Revision = n
	}

	return opts, nil
}

// readView reads one revision for a route and sets the headers of the
// answer. A reveal is never cached.
func (h *Handler) readView(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, revision string,
) *profile.View {
	ctx := r.Context()

	target, err := targetOf(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return nil
	}

	opts, err := readOptions(r, revision)
	if err != nil {
		h.writeError(ctx, w, err)

		return nil
	}

	// A reveal needs a console session with a recent sign-in. The service runs
	// the check after profile:open, so a refused reveal writes its entry.
	opts.Fresh = func() error { return h.checkFresh(r) }

	view, err := h.profiles.Read(ctx, actorOf(r, p), target, opts)
	if err != nil {
		h.writeError(ctx, w, err)

		return nil
	}

	if opts.Reveal {
		w.Header().Set("Cache-Control", noStore)
	}

	w.Header().Set("ETag", view.Profile.ETag())

	return &view
}

// readRevision serves GET /profiles/{profile}/revisions/{revision}.
func (h *Handler) readRevision(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	view := h.readView(w, r, p, obj, r.PathValue("revision"))
	if view == nil {
		return
	}

	rev := toRevision(view.Revision)
	doc := string(view.Revision.Document)
	rev.Document = &doc
	rev.Entries = toEntries(view.Entries, view.Secrets)

	h.writeJSON(r.Context(), w, http.StatusOK, rev)
}
