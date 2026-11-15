package tfe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// The provider registry protocol of OpenTofu lives under this path. The service
// discovery document names it as providers.v1.
const registryPrefix = "/v1/providers/"

// Bounds of the signed file URL.
const (
	// DefaultRegistryURLTTL is how long a signed file URL stays valid. OpenTofu
	// fetches the files right after it reads the download document.
	DefaultRegistryURLTTL = 15 * time.Minute
	// MinRegistryURLKeyBytes is the shortest key that signs file URLs.
	MinRegistryURLKeyBytes = 32
	// registryHostGateSubject is the subject of the audit entry of a request for
	// another host: the gate runs before sign-in, so the request has no subject.
	registryHostGateSubject = "registry-host-gate"
	// keyIDLength is the length of the long key ID: the last 16 hex digits of a
	// fingerprint.
	keyIDLength = 16
	// urlLabel separates the signature of a file URL from every other use of
	// the key.
	urlLabel = "opendustpan registry file url v1"
)

// RegistryService is what the registry protocol routes need. The registry
// service implements it.
type RegistryService interface {
	// ServableReleases returns the releases of a provider that have a download,
	// from the lowest version to the highest: a release whose signing key the
	// namespace removed is left out. It returns registry.ErrNotFound when none
	// is left.
	ServableReleases(ctx context.Context, ns namespace.Name, name string) ([]registry.Release, error)
	// Release returns one release, or registry.ErrNotFound.
	Release(ctx context.Context, ns namespace.Name, name, version string) (registry.Release, error)
	// SigningKey returns the trusted key that signed rel, or
	// registry.ErrKeyNotFound when the namespace no longer trusts it.
	SigningKey(ctx context.Context, rel registry.Release) (registry.TrustedKey, error)
	// OpenFile opens a file of a published release by its SHA-256, or returns
	// registry.ErrNotFound. The caller closes the reader.
	OpenFile(ctx context.Context, ns namespace.Name, name, version, sum string) (size int64, r io.ReadCloser, err error)
	// ModuleReleases returns the releases of a module, from the lowest version
	// to the highest, or registry.ErrNotFound when it has none.
	ModuleReleases(ctx context.Context, addr registry.Address) ([]registry.Release, error)
	// ModuleRelease returns one release of a module, or registry.ErrNotFound.
	ModuleRelease(ctx context.Context, ns namespace.Name, name, system, version string) (registry.Release, error)
	// OpenModuleFile opens the archive of a published module release by its
	// SHA-256, or returns registry.ErrNotFound. The caller closes the reader.
	OpenModuleFile(
		ctx context.Context, addr registry.Address, version, sum string,
	) (size int64, r io.ReadCloser, err error)
}

// RegistryConfig holds the registry protocol settings of a Handler. A Handler
// with a nil config, or a config with a nil Service, serves no registry route
// and no providers.v1 entry.
type RegistryConfig struct {
	Service RegistryService
	// Host is the host of the server, as the clients write it in a provider
	// address: lowercase, with the port only when it is not the default port of
	// the scheme. The routes answer 404 for every request whose host is another
	// host. An empty Host makes every request fail (fail closed).
	Host string
	// DefaultPort is the port that the scheme of the public URL implies, "443"
	// for https. The request host is compared without it.
	DefaultPort string
	// URLKey signs the file URLs. It has at least MinRegistryURLKeyBytes bytes.
	URLKey []byte
	// URLTTL is the validity of a file URL. Zero means DefaultRegistryURLTTL.
	URLTTL time.Duration
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// registryState is the registry part of a Handler.
type registryState struct {
	service     RegistryService
	host        string
	defaultPort string
	key         []byte
	ttl         time.Duration
	now         func() time.Time
}

// newRegistryState checks cfg. A nil Service gives nil: no registry route.
func newRegistryState(cfg *RegistryConfig) (*registryState, error) {
	if cfg == nil || cfg.Service == nil {
		return nil, nil //nolint:nilnil // No registry routes: the config is optional.
	}

	if len(cfg.URLKey) < MinRegistryURLKeyBytes {
		return nil, fmt.Errorf(
			"%w: registry URL key of at least %d bytes",
			ErrMissingDependency,
			MinRegistryURLKeyBytes,
		)
	}

	st := &registryState{
		service:     cfg.Service,
		host:        strings.ToLower(cfg.Host),
		defaultPort: cfg.DefaultPort,
		key:         append([]byte(nil), cfg.URLKey...),
		ttl:         cfg.URLTTL,
		now:         cfg.Now,
	}

	if st.ttl <= 0 {
		st.ttl = DefaultRegistryURLTTL
	}

	if st.now == nil {
		st.now = time.Now
	}

	return st, nil
}

// canonicalHost returns the host part of a request in the form that Host uses:
// lowercase, and without the default port of the scheme. A value that is not a
// plain host or host:port (a path, user information, a space) returns "", so it
// never equals the configured host.
func (s *registryState) canonicalHost(raw string) string {
	if raw == "" || strings.ContainsAny(raw, "/\\@?# \t\r\n%") {
		return ""
	}

	host := strings.ToLower(raw)

	name, port, err := net.SplitHostPort(host)
	if err != nil || port != s.defaultPort {
		return host
	}

	if strings.Contains(name, ":") {
		return "[" + name + "]"
	}

	return name
}

// hostAllowed reports whether the request is for the configured host. It
// compares the host of the request with the configuration, exactly, and never
// takes a header such as X-Forwarded-Host. With no configured host it denies.
func (s *registryState) hostAllowed(r *http.Request) bool {
	return s.host != "" && s.canonicalHost(r.Host) == s.host
}

// registryRoutes returns the routes of the provider registry protocol.
func (h *Handler) registryRoutes() []Route {
	if h.registry == nil {
		return nil
	}

	return append(h.moduleRoutes(), []Route{
		{
			Method: http.MethodGet,
			Path:   registryPrefix + "{namespace}/{type}/versions",
			// The list of versions needs registry:read on the namespace. The
			// namespace of the address is the namespace of OpenDustpan. A host
			// other than the configured host answers 404.
			Scope:   ScopeRegistry,
			Actions: []identity.Action{identity.ActionRegistryRead},
			Resolve: resolveRegistryNamespace,
			Serve:   h.registryVersions,
		},
		{
			Method: http.MethodGet,
			Path:   registryPrefix + "{namespace}/{type}/{version}/download/{os}/{arch}",
			// The download document needs registry:read on the namespace. It
			// holds the signed URLs of the files and the key that signed the
			// release. A release whose key the namespace removed answers 404.
			Scope:   ScopeRegistry,
			Actions: []identity.Action{identity.ActionRegistryRead},
			Resolve: resolveRegistryNamespace,
			Serve:   h.registryDownload,
		},
		{
			Method: http.MethodGet,
			Path:   registryPrefix + "{namespace}/{type}/{version}/files/{sha256}",
			// OpenTofu fetches the files of a release without credentials, so the
			// signature in the query is the credential: the download document
			// minted it for a caller who held registry:read. It expires, and it
			// names one file of one release.
			Scope:   ScopeSigned,
			Actions: []identity.Action{identity.ActionRegistryRead},
			Resolve: nil,
			Serve:   h.registryFile,
		},
	}...)
}

// resolveRegistryNamespace returns the namespace in the path.
func resolveRegistryNamespace(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, err
	}

	return identity.NamespaceObject(ns), nil
}

// registryMediaType is the media type of every answer of the registry
// protocol, errors included.
const registryMediaType = "application/json"

// registryErrors is the error document of the registry protocol. The protocol
// fixes only errors, a list of strings. Code and Meta hold the code of the
// catalog and its arguments, with the member names of the JSON:API errors of
// the rest of the adapter.
type registryErrors struct {
	Errors []string           `json:"errors"`
	Code   string             `json:"code"`
	Meta   *registryErrorMeta `json:"meta,omitempty"`
}

// registryErrorMeta is the meta member of the registry error document.
type registryErrorMeta struct {
	Args map[string]any `json:"args"`
}

// writeRegistryError answers in the form of the registry protocol, with the
// code of the catalog. A code that the catalog refuses is a bug in this
// adapter: the answer is then 500 with server.internal, and the log names the
// code.
func (h *Handler) writeRegistryError(ctx context.Context, w http.ResponseWriter, status int, text, code string) {
	answer, err := errcode.New(code)
	if err != nil {
		faultlog.LogInternal(ctx, h.logger, "error code failed", faultlog.CauseUnclassified, code)
		h.writeRegistryAnswer(ctx, w, http.StatusInternalServerError, titleInternal, errcode.Internal())

		return
	}

	h.writeRegistryAnswer(ctx, w, status, text, answer)
}

// writeRegistryAnswer writes the error document of the registry protocol with
// text, and the code and the arguments of answer.
func (h *Handler) writeRegistryAnswer(
	ctx context.Context, w http.ResponseWriter, status int, text string, answer errcode.Answer,
) {
	w.Header().Set("Content-Type", registryMediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	body := registryErrors{Errors: []string{text}, Code: answer.Code(), Meta: nil}
	if args := answer.Args(); len(args) > 0 {
		body.Meta = &registryErrorMeta{Args: args}
	}

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// registryNotFound answers every request that names nothing the caller may
// know: an unknown host, namespace, provider, version or file.
func (h *Handler) registryNotFound(ctx context.Context, w http.ResponseWriter) {
	h.writeRegistryError(ctx, w, http.StatusNotFound, notFoundDetail, errcode.RegistryNotFound)
}

// denyHost answers a request for another host: it writes an audit entry for the
// namespace of the path, within the bound of the limiter, and answers 404 as
// for a missing address.
func (h *Handler) denyHost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	obj := identity.ServerObject()
	if ns, err := namespace.ParseName(r.PathValue("namespace")); err == nil {
		obj = identity.NamespaceObject(ns)
	}

	// The gate runs before sign-in, so the entry names the gate as its subject.
	var gate identity.Principal

	gate.Subject = registryHostGateSubject

	h.guard.Record(ctx, r, gate, refusal.Denial{
		Action: identity.ActionRegistryRead, Object: obj, Reason: "request for another host",
		ReasonWithin: nil, Window: 0,
	})

	h.registryNotFound(ctx, w)
}

// enforceRegistry serves a route of the registry protocol that needs a
// signed-in principal. The checks run in this order: the host, then the
// credentials, then the namespace, then the action. A request that fails the
// host check reaches nothing else.
func (h *Handler) enforceRegistry(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if !h.registry.hostAllowed(r) {
			h.denyHost(w, r)

			return
		}

		p, err := h.authenticate(r)
		if err != nil {
			h.writeRegistryAuthError(ctx, w, err)

			return
		}

		obj, err := route.Resolve(w, r)
		if err != nil {
			h.registryNotFound(ctx, w)

			return
		}

		if authErr := h.registryAuthorize(ctx, r, route, p, obj); authErr != nil {
			h.writeRegistryDenial(ctx, w, authErr)

			return
		}

		route.Serve(w, r, p, obj)
	}
}

// writeRegistryAuthError answers a request whose credentials fail.
func (h *Handler) writeRegistryAuthError(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, identity.ErrRateLimited) {
		h.writeRegistryError(
			ctx,
			w,
			http.StatusTooManyRequests,
			"too many failed sign-ins from this address: try again later",
			errcode.AuthRateLimited,
		)

		return
	}

	h.writeRegistryError(ctx, w, http.StatusUnauthorized, "unauthenticated", errcode.AuthUnauthenticated)
}

// registryAuthorize authorizes every declared action of route on obj. It
// returns the first error of the Authorizer.
func (h *Handler) registryAuthorize(
	ctx context.Context, r *http.Request, route Route, p identity.Principal, obj identity.Object,
) error {
	for _, action := range route.Actions {
		if err := h.guard.Authorize(ctx, r, p, action, obj); err != nil {
			return err
		}
	}

	return nil
}

// writeRegistryDenial answers a request that the Authorizer refused or could
// not decide. Any error but a denial is an internal error, so the request
// fails closed.
func (h *Handler) writeRegistryDenial(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, identity.ErrDenied) {
		h.writeRegistryError(ctx, w, http.StatusForbidden, titleForbidden, errcode.Forbidden(err))

		return
	}

	faultlog.LogInternal(ctx, h.logger, "registry authorization failed", faultlog.Cause(err), errcode.ServerInternal)
	h.writeRegistryError(ctx, w, http.StatusInternalServerError, titleInternal, errcode.ServerInternal)
}

// enforceSigned serves the file route of the registry protocol. The host check
// runs first, and the handler checks the signature before it reads anything.
func (h *Handler) enforceSigned(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.registry.hostAllowed(r) {
			h.denyHost(w, r)

			return
		}

		var guest identity.Principal

		var noObject identity.Object

		route.Serve(w, r, guest, noObject)
	}
}

// registryPlatform is a platform in the version list.
type registryPlatform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// registryVersion is one version in the version list.
type registryVersion struct {
	Version   string             `json:"version"`
	Protocols []string           `json:"protocols"`
	Platforms []registryPlatform `json:"platforms"`
}

type registryVersions struct {
	Versions []registryVersion `json:"versions"`
}

// splitPlatform returns the operating system and the architecture of a
// platform such as "linux_amd64".
func splitPlatform(platform string) [2]string {
	goos, goarch, _ := strings.Cut(platform, "_")

	return [2]string{goos, goarch}
}

// registryVersions serves GET /v1/providers/{namespace}/{type}/versions.
func (h *Handler) registryVersions(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	releases, err := h.registry.service.ServableReleases(ctx, obj.Namespace(), r.PathValue("type"))
	if err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	out := registryVersions{Versions: make([]registryVersion, 0, len(releases))}

	for i := range releases {
		rel := &releases[i]
		v := registryVersion{
			Version: rel.Version, Protocols: append([]string{}, rel.Protocols...),
			Platforms: make([]registryPlatform, 0, len(rel.Archives)),
		}

		for _, a := range rel.Archives {
			platform := splitPlatform(a.Platform)
			v.Platforms = append(v.Platforms, registryPlatform{OS: platform[0], Arch: platform[1]})
		}

		out.Versions = append(out.Versions, v)
	}

	h.writeRegistryJSON(ctx, w, out)
}

// registryServiceError answers an error of the registry service. Everything
// that names a missing or hidden object is 404, so a caller cannot tell the
// causes apart.
func (h *Handler) registryServiceError(ctx context.Context, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrNotFound), errors.Is(err, registry.ErrKeyNotFound),
		errors.Is(err, registry.ErrInvalid), errors.Is(err, namespace.ErrNotFound):
		h.registryNotFound(ctx, w)
	default:
		faultlog.LogInternal(ctx, h.logger, "registry request failed", faultlog.Cause(err), "")
		h.writeRegistryError(ctx, w, http.StatusInternalServerError, titleInternal, errcode.ServerInternal)
	}
}

func (h *Handler) writeRegistryJSON(ctx context.Context, w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", registryMediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// registryDownload is the download document of the registry protocol.
//
//nolint:tagliatelle // OpenTofu fixes these field names.
type registryDownload struct {
	Protocols           []string    `json:"protocols"`
	OS                  string      `json:"os"`
	Arch                string      `json:"arch"`
	Filename            string      `json:"filename"`
	DownloadURL         string      `json:"download_url"`
	ShasumsURL          string      `json:"shasums_url"`
	ShasumsSignatureURL string      `json:"shasums_signature_url"`
	Shasum              string      `json:"shasum"`
	SigningKeys         signingKeys `json:"signing_keys"`
}

//nolint:tagliatelle // OpenTofu fixes these field names.
type signingKeys struct {
	GPGPublicKeys []gpgPublicKey `json:"gpg_public_keys"`
}

//nolint:tagliatelle // OpenTofu fixes these field names.
type gpgPublicKey struct {
	KeyID      string `json:"key_id"`
	ASCIIArmor string `json:"ascii_armor"`
}

// registryDownload serves
// GET /v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}.
func (h *Handler) registryDownload(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()
	ns, name, version := obj.Namespace(), r.PathValue("type"), r.PathValue("version")

	rel, err := h.registry.service.Release(ctx, ns, name, version)
	if err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	archive, ok := archiveOf(rel, r.PathValue("os")+"_"+r.PathValue("arch"))
	if !ok {
		h.registryNotFound(ctx, w)

		return
	}

	// Fail closed: the release is served only while the namespace trusts the key
	// that signed it. A removed key stops the download.
	key, err := h.registry.service.SigningKey(ctx, rel)
	if err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	base := h.baseURL.BaseURL(r)
	expires := h.registry.now().Add(h.registry.ttl)
	platform := splitPlatform(archive.Platform)

	h.writeRegistryJSON(ctx, w, registryDownload{
		Protocols: append([]string{}, rel.Protocols...), OS: platform[0], Arch: platform[1], Filename: archive.FileName,
		DownloadURL:         h.registry.fileURL(base, registryFile{ns, name, version, archive.SHA256}, expires),
		ShasumsURL:          h.registry.fileURL(base, registryFile{ns, name, version, rel.SumsSHA256}, expires),
		ShasumsSignatureURL: h.registry.fileURL(base, registryFile{ns, name, version, rel.SignatureSHA256}, expires),
		Shasum:              archive.SHA256,
		SigningKeys: signingKeys{GPGPublicKeys: []gpgPublicKey{
			{KeyID: keyID(key.Fingerprint), ASCIIArmor: string(key.Armored)},
		}},
	})
}

// archiveOf finds the archive of a platform.
func archiveOf(rel registry.Release, platform string) (registry.Archive, bool) {
	var none registry.Archive

	for _, a := range rel.Archives {
		if a.Platform == platform {
			return a, true
		}
	}

	return none, false
}

// keyID returns the long key ID of a fingerprint: its last 16 hex digits.
func keyID(fingerprint string) string {
	if len(fingerprint) < keyIDLength {
		return fingerprint
	}

	return fingerprint[len(fingerprint)-keyIDLength:]
}

// fileMAC returns the signature of one file URL: the HMAC-SHA-256, keyed with
// the key of the handler, of the label, the address, the version, the file and
// the expiry. The fields are joined by a newline, which no field can hold.
func (s *registryState) fileMAC(ns namespace.Name, name, version, sum string, expires int64) []byte {
	mac := hmac.New(sha256.New, s.key)

	for _, part := range []string{urlLabel, ns.String(), name, version, sum, strconv.FormatInt(expires, 10)} {
		mac.Write([]byte(part))
		mac.Write([]byte{'\n'})
	}

	return mac.Sum(nil)
}

// registryFile names one file of a release for a signed URL.
type registryFile struct {
	ns      namespace.Name
	name    string
	version string
	sum     string
}

// fileURL returns the signed URL of one file of a release.
func (s *registryState) fileURL(base string, f registryFile, expires time.Time) string {
	expSec := expires.Unix()
	sig := base64.RawURLEncoding.EncodeToString(s.fileMAC(f.ns, f.name, f.version, f.sum, expSec))

	return base + registryPrefix + url.PathEscape(f.ns.String()) + "/" + url.PathEscape(f.name) + "/" +
		url.PathEscape(f.version) + "/files/" + url.PathEscape(f.sum) + "?expires=" + strconv.FormatInt(expSec, 10) +
		"&sig=" + sig
}

// validSignature reports whether the query of r holds a signature that the
// handler made for this file, and that has not expired. It compares the
// signature in constant time. The expiry must be in the future and not further
// than the validity of a URL, so a URL made with a long expiry by a leaked key
// of another version cannot live long.
func (s *registryState) validSignature(r *http.Request, ns namespace.Name, name, version, sum string) bool {
	q := r.URL.Query()

	expires, err := strconv.ParseInt(q.Get("expires"), 10, 64)
	if err != nil {
		return false
	}

	now := s.now()
	if expires <= now.Unix() || expires > now.Add(s.ttl).Unix() {
		return false
	}

	sig, err := base64.RawURLEncoding.DecodeString(q.Get("sig"))
	if err != nil {
		return false
	}

	return hmac.Equal(sig, s.fileMAC(ns, name, version, sum, expires))
}

// registryFile serves GET /v1/providers/{namespace}/{type}/{version}/files/{sha256}.
func (h *Handler) registryFile(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		h.registryNotFound(ctx, w)

		return
	}

	name, version, sum := r.PathValue("type"), r.PathValue("version"), r.PathValue("sha256")

	// The signature is checked before the store is read, so a request without a
	// valid signature learns nothing about what exists.
	if !h.registry.validSignature(r, ns, name, version, sum) {
		h.registryNotFound(ctx, w)

		return
	}

	size, body, err := h.registry.service.OpenFile(ctx, ns, name, version, sum)
	if err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	defer func() {
		if closeErr := body.Close(); closeErr != nil {
			h.logger.WarnContext(ctx, "close registry file failed")
		}
	}()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")

	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}

	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, body); err != nil {
		h.logger.WarnContext(ctx, "write registry file failed")
	}
}
