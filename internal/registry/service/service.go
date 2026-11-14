// Package service holds the use cases of the private registry: it stages the
// files of an upload, publishes a provider release after the checks, manages
// the trusted signing keys of a namespace and opens the files of a published
// release. It adds the fence, the time, the blob keys and the audit entry to
// the calls of the ports of package registry.
//
// A release becomes readable only after every check passed. The upload goes to
// a staging key first. The publish measures each staged file, checks the
// signature over the raw bytes of the checksums file before it parses them,
// compares the hashes, copies the bytes to their content keys through a reader
// that checks the hash again, and only then calls Store.Publish. A reader of
// the registry opens a content key only through a published release.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// Limits of the upload. The values are proposals of the design, and a setting
// of the installation can raise them later.
const (
	// DefaultMaxArchiveBytes is the largest file of an upload.
	DefaultMaxArchiveBytes int64 = 512 << 20
	// DefaultMaxReleaseBytes is the largest sum of the files of one release.
	DefaultMaxReleaseBytes int64 = 2 << 30
	// MaxSignatureBytes is the largest detached signature file.
	MaxSignatureBytes int64 = 64 << 10
	// StagingGrace is the age at which a staged file that no publish moved is
	// removed.
	StagingGrace = 24 * time.Hour
	// maxStagedPerSweep bounds the work of one sweep of the staging directory.
	maxStagedPerSweep = 1000
)

// errSweepBound stops the walk of a sweep that reached maxStagedPerSweep files.
var errSweepBound = errors.New("the sweep reached its bound")

// Sentinel errors of the use cases.
var (
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
	// ErrTooLarge reports a file or a release above the limit.
	ErrTooLarge = errors.New("the upload is larger than the limit")
	// ErrNotStaged reports a publish that names a staged file that does not
	// exist. A staged file is removed after a publish and after the grace
	// period.
	ErrNotStaged = errors.New("the staged file does not exist: upload the file again")
	// ErrNoKeys reports a publish into a namespace that trusts no signing key.
	ErrNoKeys = errors.New("the namespace trusts no signing key: register the public key first")
	// ErrBadRequest reports a publish request that fails its checks. The text
	// names the field.
	ErrBadRequest = errors.New("invalid publish request")
	// ErrIntegrity reports a blob whose bytes are not the bytes that were
	// checked. Nothing is published.
	ErrIntegrity = errors.New("the stored bytes are not the bytes that were checked")
)

// Namespaces reads the namespace that a use case acts in. namespace.Store
// implements it.
type Namespaces interface {
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Config holds the dependencies of a Service. Every field except Now and
// Limits is required.
type Config struct {
	Store      registry.Store
	Keys       registry.KeyStore
	Verifier   registry.SignatureVerifier
	Inspector  registry.KeyInspector
	Blobs      blob.Store
	Namespaces Namespaces
	// Audit is required: it records the refused publishes.
	Audit Auditor
	// Logger is required: it logs a refused publish whose audit entry could not be written.
	Logger *slog.Logger
	// OnAuditFailure is required: it runs once for each refused publish whose audit entry
	// could not be written, so that the loss is counted.
	OnAuditFailure func()
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// MaxArchiveBytes and MaxReleaseBytes override the defaults when above zero.
	MaxArchiveBytes int64
	MaxReleaseBytes int64
	// MaxModuleArchiveBytes overrides DefaultMaxModuleArchiveBytes when above zero.
	MaxModuleArchiveBytes int64
}

// Auditor appends the entries of refused publishes. audit.Store satisfies it.
type Auditor interface {
	Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
}

// Service implements the use cases of the registry.
type Service struct {
	store      registry.Store
	keys       registry.KeyStore
	verifier   registry.SignatureVerifier
	inspector  registry.KeyInspector
	blobs      blob.Store
	namespaces Namespaces
	auditor    Auditor
	logger     *slog.Logger
	onAudit    func()
	now        func() time.Time
	maxFile    int64
	maxRelease int64
	maxModule  int64
}

// checkConfig returns ErrMissingDependency for the first port of cfg that is nil.
func checkConfig(cfg Config) error {
	missing := ""

	switch {
	case cfg.Store == nil:
		missing = "store"
	case cfg.Keys == nil:
		missing = "key store"
	case cfg.Verifier == nil:
		missing = "signature verifier"
	case cfg.Inspector == nil:
		missing = "key inspector"
	case cfg.Blobs == nil:
		missing = "blob store"
	case cfg.Namespaces == nil:
		missing = "namespaces"
	case cfg.Audit == nil:
		missing = "audit"
	case cfg.Logger == nil:
		missing = "logger"
	case cfg.OnAuditFailure == nil:
		missing = "audit failure hook"
	default:
		// Every port is set: missing stays empty.
	}

	if missing != "" {
		return fmt.Errorf("%w: %s", ErrMissingDependency, missing)
	}

	return nil
}

// New returns a Service. It returns ErrMissingDependency when a port is nil.
func New(cfg Config) (*Service, error) {
	if err := checkConfig(cfg); err != nil {
		return nil, err
	}

	s := &Service{
		store: cfg.Store, keys: cfg.Keys, verifier: cfg.Verifier, inspector: cfg.Inspector, blobs: cfg.Blobs,
		namespaces: cfg.Namespaces, auditor: cfg.Audit, logger: cfg.Logger, onAudit: cfg.OnAuditFailure,
		now: cfg.Now, maxFile: cfg.MaxArchiveBytes,
		maxRelease: cfg.MaxReleaseBytes, maxModule: cfg.MaxModuleArchiveBytes,
	}

	if s.now == nil {
		s.now = time.Now
	}

	if s.maxFile <= 0 {
		s.maxFile = DefaultMaxArchiveBytes
	}

	if s.maxRelease <= 0 {
		s.maxRelease = DefaultMaxReleaseBytes
	}

	if s.maxModule <= 0 {
		s.maxModule = DefaultMaxModuleArchiveBytes
	}

	return s, nil
}

// Caller is the principal who calls a use case. By is the template of the audit
// entry: the adapter fills where the request came from, and the service fills
// the action, the object and the time.
type Caller struct {
	Principal identity.Principal
	By        audit.Entry
}

// check refuses a caller that has no subject, because every release and every
// key records who made it.
func (c *Caller) check() error {
	// Defense in depth: the adapter authorizes the action before it calls the
	// service. This check stays so that a new caller of the service cannot write
	// a record that names nobody.
	if c == nil || c.Principal.Subject == "" {
		return identity.ErrUnauthenticated
	}

	return nil
}

// entry returns the audit entry of an action on object. The entry names the
// namespace, the action and the object, and the template gives the rest.
func (s *Service) entry(c *Caller, ns namespace.Name, action identity.Action, object string) audit.Entry {
	e := c.By
	e.At = s.now().UTC()
	e.Namespace = ns
	e.Subject = c.Principal.Subject
	e.TokenID = c.Principal.TokenID
	e.Action = string(action)
	e.Object = object

	return e
}

// scope returns the view of the blob store for the namespace.
func (s *Service) scope(ns namespace.Namespace) (*blob.Scoped, error) {
	view, err := blob.NewScoped(s.blobs, ns.ID)
	if err != nil {
		return nil, fmt.Errorf("scope the blob store: %w", err)
	}

	return view, nil
}

// newToken returns a random upload token: 32 lowercase letters and digits.
func newToken() (string, error) {
	var b [20]byte

	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("make an upload token: %w", err)
	}

	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])), nil
}

// measure is a reader that counts and hashes what passes through it, and
// stops at limit+1 bytes so that a caller sees the overflow.
type measure struct {
	r     io.Reader
	h     hash.Hash
	n     int64
	limit int64
}

func newMeasure(r io.Reader, limit int64) *measure {
	return &measure{r: io.LimitReader(r, limit+1), h: sha256.New(), n: 0, limit: limit}
}

func (m *measure) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	m.n += int64(n)
	m.h.Write(p[:n])

	if m.n > m.limit {
		return n, ErrTooLarge
	}

	if err != nil {
		return n, err //nolint:wrapcheck // The reader contract: callers compare io.EOF.
	}

	return n, nil
}

func (m *measure) sum() string {
	return hex.EncodeToString(m.h.Sum(nil))
}
