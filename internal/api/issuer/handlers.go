package issuer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// keyBody is the metadata of one signing key. It holds no key material.
type keyBody struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	Algorithm   string `json:"algorithm"`
	CreatedAt   string `json:"createdAt"`
	ActivatedAt string `json:"activatedAt,omitempty"`
	RetiredAt   string `json:"retiredAt,omitempty"`
	DeletedAt   string `json:"deletedAt,omitempty"`
	// ExpiresAt is when the active key stops signing by the schedule.
	ExpiresAt string `json:"expiresAt,omitempty"`
}

func toKey(k profile.KeyInfo) keyBody {
	out := keyBody{
		ID: string(k.ID), State: string(k.State), Algorithm: k.Algorithm, CreatedAt: formatTime(k.CreatedAt),
		ActivatedAt: "", RetiredAt: "", DeletedAt: "", ExpiresAt: "",
	}

	for _, f := range []struct {
		to   *string
		from interface{ IsZero() bool }
		text string
	}{
		{&out.ActivatedAt, k.ActivatedAt, formatTime(k.ActivatedAt)},
		{&out.RetiredAt, k.RetiredAt, formatTime(k.RetiredAt)},
		{&out.DeletedAt, k.DeletedAt, formatTime(k.DeletedAt)},
		{&out.ExpiresAt, k.ExpiresAt, formatTime(k.ExpiresAt)},
	} {
		if !f.from.IsZero() {
			*f.to = f.text
		}
	}

	return out
}

func toKeys(in []profile.KeyInfo) []keyBody {
	out := make([]keyBody, 0, len(in))
	for _, k := range in {
		out = append(out, toKey(k))
	}

	return out
}

// settingsBody is the settings of the issuer of a namespace. A default shows
// its effective value.
type settingsBody struct {
	IssuerURL          string   `json:"issuerUrl"`
	SubjectTemplate    []string `json:"subjectTemplate"`
	AllowBlockSubjects bool     `json:"allowBlockSubjects"`
	IncludeActorName   bool     `json:"includeActorName"`
	ETag               string   `json:"etag"`
	UpdatedAt          string   `json:"updatedAt,omitempty"`
	UpdatedBy          string   `json:"updatedBy,omitempty"`
}

func toSettings(s profile.IssuerSettings) settingsBody {
	out := settingsBody{
		IssuerURL: s.IssuerURL, SubjectTemplate: append([]string{}, s.SubjectTemplate...),
		AllowBlockSubjects: s.AllowBlockSubjects, IncludeActorName: s.IncludeActorName, ETag: s.ETag(),
		UpdatedAt: "", UpdatedBy: s.UpdatedBy,
	}

	if !s.UpdatedAt.IsZero() {
		out.UpdatedAt = formatTime(s.UpdatedAt)
	}

	return out
}

// copyStepBody is the outcome of one step of the copy check.
type copyStepBody struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// copyCheckBody is the outcome of a copy check. VerifiedKeys holds the IDs of
// the keys that a check found in the copied JWKS.
type copyCheckBody struct {
	At           string         `json:"at"`
	OK           bool           `json:"ok"`
	Steps        []copyStepBody `json:"steps"`
	VerifiedKeys []string       `json:"verifiedKeys"`
}

func toCopyCheck(c *profile.CopyCheck) *copyCheckBody {
	if c == nil {
		return nil
	}

	out := &copyCheckBody{
		At: formatTime(c.At), OK: c.OK, Steps: make([]copyStepBody, 0, len(c.Steps)),
		VerifiedKeys: make([]string, 0, len(c.Verified)),
	}

	for _, s := range c.Steps {
		out.Steps = append(out.Steps, copyStepBody{Name: s.Name, OK: s.OK, Detail: s.Detail})
	}

	for _, id := range c.Verified {
		out.VerifiedKeys = append(out.VerifiedKeys, string(id))
	}

	return out
}

// issuerBody is what a read of the issuer answers.
type issuerBody struct {
	Settings           settingsBody   `json:"settings"`
	EffectiveIssuerURL string         `json:"effectiveIssuerUrl,omitempty"`
	IssuerHostSet      bool           `json:"issuerHostConfigured"`
	Keys               []keyBody      `json:"keys"`
	CopyCheck          *copyCheckBody `json:"copyCheck"`
	Warnings           []string       `json:"warnings"`
}

func actorOf(r *http.Request, p identity.Principal) *profile.Actor {
	by := audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: clientip.String(r),
		UserAgent: audit.TruncateUserAgent(apidesc.Header(r, "User-Agent")),
		Channel:   sessionauth.AuditChannel(r, channelHeader), Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: requestid.FromContext(r.Context()), ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	return &profile.Actor{Principal: p, By: by}
}

// get serves GET .../issuer.
func (h *Handler) get(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	st, err := h.issuer.Get(ctx, actorOf(r, p), obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", st.Settings.ETag())
	h.writeJSON(ctx, w, http.StatusOK, issuerBody{
		Settings: toSettings(st.Settings), EffectiveIssuerURL: st.EffectiveIssuerURL, IssuerHostSet: st.HostConfigured,
		Keys: toKeys(st.Keys), CopyCheck: toCopyCheck(st.Settings.CopyCheck), Warnings: nonNilStrings(st.Warnings),
	})
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

// patchBody is a JSON Merge Patch of the settings. A field that is absent
// keeps its value. A field set to null restores its default.
type patchBody struct {
	IssuerURL          json.RawMessage `json:"issuerUrl"`
	SubjectTemplate    json.RawMessage `json:"subjectTemplate"`
	AllowBlockSubjects json.RawMessage `json:"allowBlockSubjects"`
	IncludeActorName   json.RawMessage `json:"includeActorName"`
}

var (
	errPrecondition = problem.Fixed(
		http.StatusPreconditionFailed, errcode.RequestPreconditionFailed,
		"If-Match does not match the issuer settings: read them again and send the new ETag",
	)
	errPreconditionRequired = problem.Fixed(
		http.StatusPreconditionRequired, errcode.RequestPreconditionRequired,
		"send the ETag that you read in If-Match",
	)
	errMediaType = problem.Fixed(
		http.StatusUnsupportedMediaType, errcode.RequestBodyInvalid,
		"send the patch as application/merge-patch+json or application/json",
	)
)

// versionOf reads the settings version in the If-Match header. A missing
// header answers 428. A weak tag and a tag that is not a tag of the settings
// answer 412, as If-Match compares tags in the strong way.
func versionOf(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(apidesc.Header(r, "If-Match"))
	if raw == "" {
		return 0, errPreconditionRequired
	}

	num, ok := strings.CutPrefix(raw, `"issuer.`)
	if !ok || !strings.HasSuffix(num, `"`) {
		return 0, errPrecondition
	}

	v, err := strconv.ParseInt(strings.TrimSuffix(num, `"`), 10, 64)
	if err != nil || v < 0 {
		return 0, errPrecondition
	}

	return v, nil
}

func (b patchBody) toPatch() (profile.SettingsPatch, error) {
	var p profile.SettingsPatch

	bad := func(field string) error {
		return problem.BadRequest(errcode.RequestBodyInvalid, fmt.Sprintf("%s has a value of the wrong type", field))
	}

	if len(b.IssuerURL) > 0 {
		v := ""
		if !isNull(b.IssuerURL) && json.Unmarshal(b.IssuerURL, &v) != nil {
			return p, bad("issuerUrl")
		}

		p.IssuerURL = &v
	}

	if len(b.SubjectTemplate) > 0 {
		v := []string{}
		if !isNull(b.SubjectTemplate) && json.Unmarshal(b.SubjectTemplate, &v) != nil {
			return p, bad("subjectTemplate")
		}

		p.SubjectTemplate = &v
	}

	if v, ok, err := flag(b.AllowBlockSubjects, true); err != nil {
		return p, bad("allowBlockSubjects")
	} else if ok {
		p.AllowBlockSubjects = &v
	}

	if v, ok, err := flag(b.IncludeActorName, false); err != nil {
		return p, bad("includeActorName")
	} else if ok {
		p.IncludeActorName = &v
	}

	return p, nil
}

func isNull(raw json.RawMessage) bool { return strings.TrimSpace(string(raw)) == "null" }

// flag reads a boolean of a merge patch. present is false for an absent
// field, and null gives def.
func flag(raw json.RawMessage, def bool) (value, present bool, err error) {
	if len(raw) == 0 {
		return false, false, nil
	}

	value = def
	if !isNull(raw) {
		if err = json.Unmarshal(raw, &value); err != nil {
			return false, true, fmt.Errorf("decode flag: %w", err)
		}
	}

	return value, true, nil
}

// patch serves PATCH .../issuer.
func (h *Handler) patch(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	if ct := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0])); ct != merge &&
		ct != jsonType {
		h.writeError(ctx, w, errMediaType)

		return
	}

	version, err := versionOf(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var body patchBody
	if decodeErr := route.DecodeBody(w, r, maxPatchBytes, &body); decodeErr != nil {
		h.writeError(ctx, w, decodeErr)

		return
	}

	patch, err := body.toPatch()
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	got, err := h.issuer.Patch(ctx, actorOf(r, p), obj.Namespace(), patch, version, func() error { return h.checkFresh(r) })
	if errors.Is(err, profile.ErrConflict) {
		// The only conflict of a patch is a stale version.
		err = errPrecondition
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", got.ETag())
	h.writeJSON(ctx, w, http.StatusOK, toSettings(got))
}

// keysBody is the answer of a make of the keys.
type keysBody struct {
	Keys []keyBody `json:"keys"`
}

// createKeys serves POST .../issuer/keys.
func (h *Handler) createKeys(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	if err := emptyBody(w, r); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	keys, err := h.issuer.CreateKeys(ctx, actorOf(r, p), obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, keysBody{Keys: toKeys(keys)})
}

// emptyBody refuses a body on a route that takes none.
func emptyBody(w http.ResponseWriter, r *http.Request) error {
	var none struct{}

	if r.ContentLength == 0 || r.Body == nil || r.Body == http.NoBody {
		return nil
	}

	return route.DecodeBody(w, r, 2, &none)
}

// rotateRequest is the body of a rotation.
type rotateRequest struct {
	Emergency bool   `json:"emergency"`
	Reason    string `json:"reason"`
}

// rotateBody is the answer of a rotation: the keys after it, and the keys that
// it deleted.
type rotateBody struct {
	Keys     []keyBody `json:"keys"`
	Deleted  []string  `json:"deleted"`
	Promoted bool      `json:"promoted"`
}

// rotate serves POST .../issuer/keys/rotate.
func (h *Handler) rotate(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	var req rotateRequest
	if r.ContentLength != 0 && r.Body != nil && r.Body != http.NoBody {
		if err := route.DecodeBody(w, r, maxRotateBytes, &req); err != nil {
			h.writeError(ctx, w, err)

			return
		}
	}

	res, err := h.issuer.Rotate(ctx, actorOf(r, p), obj.Namespace(), issuersvc.RotateRequest{
		Reason: req.Reason, Emergency: req.Emergency,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	deleted := make([]string, 0, len(res.Deleted))
	for _, id := range res.Deleted {
		deleted = append(deleted, string(id))
	}

	h.writeJSON(ctx, w, http.StatusOK, rotateBody{Keys: toKeys(res.Keys), Deleted: deleted, Promoted: res.Promoted})
}

// checkCopy serves POST .../issuer/keys/check-copy.
func (h *Handler) checkCopy(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	if err := emptyBody(w, r); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	check, err := h.issuer.CheckCopy(ctx, actorOf(r, p), obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, toCopyCheck(&check))
}

// exportFile is one file for the issuerURL. Path is relative to the issuerURL,
// and Content is the exact text to save.
type exportFile struct {
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

// exportBody is the answer of an export.
type exportBody struct {
	IssuerURL string       `json:"issuerUrl"`
	Files     []exportFile `json:"files"`
}

// export serves GET .../issuer/export.
func (h *Handler) export(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	files, err := h.issuer.Export(ctx, actorOf(r, p), obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	st, err := h.issuer.Get(ctx, actorOf(r, p), obj.Namespace())
	if err != nil {
		// The caller holds manage-issuer but not namespace:read: the URL is a
		// setting that it can read from the discovery file it just got.
		st.Settings.IssuerURL = ""
	}

	h.writeJSON(ctx, w, http.StatusOK, exportBody{IssuerURL: st.Settings.IssuerURL, Files: []exportFile{
		{Path: ".well-known/openid-configuration", ContentType: jsonType, Content: string(files.Discovery)},
		{Path: "jwks", ContentType: jsonType, Content: string(files.JWKS)},
	}})
}

// nsidOf reads the namespace ID of the path. An ID that is not valid is an
// unknown namespace, so the answer tells a wrong ID from a right one that has
// no namespace by nothing.
func nsidOf(r *http.Request) (namespace.ID, error) {
	id, err := namespace.ParseID(r.PathValue("nsid"))
	if err != nil {
		return "", fmt.Errorf("%w: the namespace does not exist", namespace.ErrNotFound)
	}

	return id, nil
}

// writePublic writes a public document with the cache time of the protocol.
func (h *Handler) writePublic(w http.ResponseWriter, r *http.Request, doc any) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "max-age="+strconv.Itoa(publicMaxAge))
	h.writeJSON(ctx, w, http.StatusOK, doc)
}

// discovery serves GET /oidc/{nsid}/.well-known/openid-configuration.
func (h *Handler) discovery(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	id, err := nsidOf(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc, err := h.issuer.Discovery(ctx, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writePublic(w, r, doc)
}

// jwks serves GET /oidc/{nsid}/jwks.
func (h *Handler) jwks(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	id, err := nsidOf(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc, err := h.issuer.JWKS(ctx, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writePublic(w, r, doc)
}
