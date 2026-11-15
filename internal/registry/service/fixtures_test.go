package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/registry/pgpfixture"
	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/pgpverify"
)

const (
	acme    namespace.Name = "acme"
	beta    namespace.Name = "beta"
	subject                = "user:ada"
)

// addressCall records one call of memStore.Addresses: the limit of the call,
// the number of address rows that the store read, and the number of addresses
// that the call returned.
type addressCall struct {
	limit    int
	rows     int
	returned int
}

// memStore is registry.Store and registry.KeyStore in memory.
type memStore struct {
	mu       sync.Mutex
	releases map[string]registry.Release
	keys     map[namespace.Name][]registry.KeyRecord
	audits   []audit.Entry
	// addressCalls records each Addresses call, oldest first.
	addressCalls []addressCall
	// beforeCommit, when it is not nil, runs at the start of Publish, before the
	// key check. A test uses it to remove a key between the checks of the
	// service and the commit.
	beforeCommit func()
	// publishErr, when it is not nil, is the answer of the next Publish call
	// after beforeCommit. A test uses it for a write that fails in the commit.
	publishErr error
}

func newMemStore() *memStore {
	return &memStore{
		mu: sync.Mutex{}, releases: map[string]registry.Release{}, keys: map[namespace.Name][]registry.KeyRecord{},
		audits: nil, addressCalls: nil, beforeCommit: nil, publishErr: nil,
	}
}

func relKey(a registry.Address, v string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", a.Namespace, a.Kind, a.Name, a.System, v)
}

func (m *memStore) Publish(_ context.Context, r registry.Release, _ namespace.Fence, e audit.Entry) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("validate: %w", err)
	}

	if m.beforeCommit != nil {
		m.beforeCommit()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.publishErr != nil {
		err := m.publishErr
		m.publishErr = nil

		return err
	}

	// The contract of the port: the key is trusted at the time of the write.
	// A module has no signature and no key.
	if r.KeyFingerprint != "" && !slices.ContainsFunc(m.keys[r.Address.Namespace], func(k registry.KeyRecord) bool {
		return k.Key.Fingerprint == r.KeyFingerprint
	}) {
		return registry.ErrKeyNotFound
	}

	if old, ok := m.releases[relKey(r.Address, r.Version)]; ok {
		if old.SameContent(r) {
			return nil
		}

		return registry.ErrVersionExists
	}

	m.releases[relKey(r.Address, r.Version)] = r
	m.audits = append(m.audits, e)

	return nil
}

func (m *memStore) Get(_ context.Context, a registry.Address, v string) (registry.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.releases[relKey(a, v)]
	if !ok {
		return registry.Release{}, registry.ErrNotFound
	}

	return r, nil
}

func (m *memStore) List(_ context.Context, a registry.Address) ([]registry.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]registry.Release, 0, len(m.releases))

	for k := range m.releases {
		if m.releases[k].Address == a {
			out = append(out, m.releases[k])
		}
	}

	slices.SortFunc(out, func(x, y registry.Release) int { return registry.CompareVersions(x.Version, y.Version) })

	return out, nil
}

func (m *memStore) Addresses(
	_ context.Context, ns namespace.Name, kind registry.Kind, after string, limit int,
) ([]registry.Address, string, error) {
	if limit < 1 || limit > registry.MaxAddressPage {
		return nil, "", fmt.Errorf("%w: limit %d", registry.ErrInvalid, limit)
	}

	// Defense in depth: the callers check the cursor too; the store refuses a
	// cursor that does not hold an address of the kind.
	cursor, err := registry.ParseAddressKey(ns, kind, after)
	if err != nil {
		return nil, "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	seen := map[registry.Address]bool{}
	out := []registry.Address{}

	for k := range m.releases {
		a := m.releases[k].Address
		if a.Namespace == ns && a.Kind == kind && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}

	slices.SortFunc(out, registry.CompareAddresses)

	if cursor.Name != "" {
		out = slices.DeleteFunc(out, func(a registry.Address) bool {
			return registry.CompareAddresses(a, cursor) <= 0
		})
	}

	// The store reads one address more than the page, as the SQL store does,
	// so that the extra address tells whether a next page exists.
	rows := min(limit+1, len(out))
	out = out[:rows]

	next := ""

	if len(out) > limit {
		out = out[:limit]
		next = registry.AddressKey(out[limit-1])
	}

	m.addressCalls = append(m.addressCalls, addressCall{limit: limit, rows: rows, returned: len(out)})

	return out, next, nil
}

func (m *memStore) AddKey(_ context.Context, k registry.KeyRecord, _ namespace.Fence, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.keys[k.Namespace] = append(m.keys[k.Namespace], k)
	m.audits = append(m.audits, e)

	return nil
}

func (m *memStore) RemoveKey(_ context.Context, ns namespace.Name, fp string, _ namespace.Fence, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	before := len(m.keys[ns])
	m.keys[ns] = slices.DeleteFunc(m.keys[ns], func(k registry.KeyRecord) bool { return k.Key.Fingerprint == fp })

	if len(m.keys[ns]) == before {
		return registry.ErrKeyNotFound
	}

	m.audits = append(m.audits, e)

	return nil
}

func (m *memStore) Keys(_ context.Context, ns namespace.Name) ([]registry.KeyRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.keys[ns]), nil
}

// nsStub answers the namespaces of a test.
type nsStub map[namespace.Name]namespace.Namespace

func (n nsStub) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	ns, ok := n[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

// flipBlobs is a blob.Store that changes the bytes of a staged key the second
// time that the key is read, as a store that fails or an attacker with write
// access would.
type flipBlobs struct {
	blob.Store

	mu    sync.Mutex
	reads map[blob.Key]int
	flip  bool
}

func (f *flipBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	r, err := f.Store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get: %w", err)
	}

	f.mu.Lock()
	f.reads[key]++
	n := f.reads[key]
	f.mu.Unlock()

	if !f.flip || !strings.Contains(key.String(), "/staging/") || n < 2 {
		return r, nil
	}

	data, readErr := io.ReadAll(r)
	closeQuietly(r)

	if readErr != nil {
		return nil, fmt.Errorf("read: %w", readErr)
	}

	if len(data) > 0 {
		data[0] ^= 0xff
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

// closeQuietly closes c. A failed close of a test double is not a result.
func closeQuietly(c io.Closer) {
	_ = c.Close() //nolint:errcheck // A failed close of a test double is not a result.
}

// env is a service with a fresh blob directory, store and two namespaces.
type env struct {
	svc    *service.Service
	store  *memStore
	blobs  blob.Store
	ids    map[namespace.Name]namespace.ID
	caller *service.Caller
	now    time.Time
	aud    *memAudit
	// logs holds the log lines of the service; failures counts the audit failures.
	logs     *bytes.Buffer
	failures *int
}

// memAudit is an audit store for a test. It holds the appended entries in
// memory, and it fails every append when err is set.
type memAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
	err     error
}

func newMemAudit() *memAudit {
	return &memAudit{mu: sync.Mutex{}, entries: nil, err: nil}
}

// Append records e, or returns the error of the audit store.
func (m *memAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return audit.Entry{}, m.err
	}

	m.entries = append(m.entries, e)

	return e, nil
}

// principalOf returns a person with the given subject.
func principalOf(subject string) identity.Principal {
	var p identity.Principal

	p.Subject, p.Kind = subject, identity.KindPerson

	return p
}

// callerOf returns the caller of the service for subject.
func callerOf(subject string) *service.Caller {
	var by audit.Entry

	by.Channel, by.Kind, by.Outcome, by.SourceIP = audit.ChannelAPI, audit.KindChange, audit.OutcomeSuccess, "192.0.2.1"

	return &service.Caller{Principal: principalOf(subject), By: by}
}

// fixtureNow returns the clock of the service in these tests. It is fixed, and
// the signing keys of the tests are made at this time, so that a key is never
// newer than the clock, whatever the date of the run.
func fixtureNow() time.Time {
	return time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
}

// newSigner makes a signing key whose creation time is fixtureNow.
func newSigner(tb testing.TB) pgpfixture.Signer {
	tb.Helper()

	return pgpfixture.NewSignerAt(tb, fixtureNow())
}

// namespaceOf returns the namespace record of name with the given ID.
func namespaceOf(name namespace.Name, id namespace.ID, now time.Time) namespace.Namespace {
	return namespace.Namespace{
		CreatedAt: now, ID: id, Name: name, PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
}

// config returns the service config of a test over the given stores.
func config(store *memStore, blobs blob.Store, ns nsStub, at time.Time) service.Config {
	return configAudit(store, blobs, ns, at, auditSinks{
		aud: newMemAudit(), logger: slog.New(slog.DiscardHandler), onFail: func() {},
	})
}

// auditSinks are the audit dependencies of a service config: the audit store,
// the logger and the audit failure hook.
type auditSinks struct {
	aud    service.Auditor
	logger *slog.Logger
	onFail func()
}

// configAudit returns the service config of a test over the given stores and
// audit sinks.
func configAudit(store *memStore, blobs blob.Store, ns nsStub, at time.Time, sinks auditSinks) service.Config {
	v := pgpverify.Verifier{Now: func() time.Time { return at }}

	return service.Config{
		Store:                 store,
		Keys:                  store,
		Verifier:              v,
		Inspector:             v,
		Blobs:                 blobs,
		Namespaces:            ns,
		Audit:                 sinks.aud,
		Logger:                sinks.logger,
		OnAuditFailure:        sinks.onFail,
		Now:                   func() time.Time { return at },
		MaxArchiveBytes:       1 << 20,
		MaxReleaseBytes:       3 << 20,
		MaxModuleArchiveBytes: 0,
	}
}

func newEnv(t *testing.T, flip bool) *env {
	t.Helper()

	dir, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blobs: %v", err)
	}

	t.Cleanup(func() { closeQuietly(dir) })

	blobs := &flipBlobs{Store: dir, mu: sync.Mutex{}, reads: map[blob.Key]int{}, flip: flip}
	store := newMemStore()
	now := fixtureNow()

	ids := map[namespace.Name]namespace.ID{}
	ns := nsStub{}

	for _, name := range []namespace.Name{acme, beta} {
		ids[name] = namespace.NewID()
		ns[name] = namespaceOf(name, ids[name], now)
	}

	aud := newMemAudit()
	logs := &bytes.Buffer{}
	failures := new(int)

	logger := slog.New(slog.NewTextHandler(logs, nil))

	svc, err := service.New(configAudit(store, blobs, ns, now, auditSinks{
		aud: aud, logger: logger, onFail: func() { *failures++ },
	}))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return &env{
		svc: svc, store: store, blobs: blobs, ids: ids, caller: callerOf(subject), now: now, aud: aud,
		logs: logs, failures: failures,
	}
}

// trust registers the key of s in ns, through the service.
func (e *env) trust(t *testing.T, ns namespace.Name, s pgpfixture.Signer) {
	t.Helper()

	if _, err := e.svc.AddKey(context.Background(), e.caller, ns, s.Key.Armored); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
}

// set is an archive set that a build tool writes.
type set struct {
	name, version string
	sums, sig     []byte
	archives      map[string][]byte
	order         []string
}

func (s *set) sumsName() string { return releasecheck.SumsName(s.name, s.version) }
func (s *set) sigName() string  { return releasecheck.SignatureName(s.name, s.version) }

// buildSet writes the archive set of a release for the given platforms and signs
// the checksums file with sg.
func buildSet(t *testing.T, sg pgpfixture.Signer, name, version string, platforms ...string) *set {
	t.Helper()

	s := &set{
		name: name, version: version, sums: nil, sig: nil, archives: map[string][]byte{},
		order: make([]string, 0, len(platforms)),
	}

	var sums strings.Builder

	for _, p := range platforms {
		file := fmt.Sprintf("terraform-provider-%s_%s_%s.zip", name, version, p)
		data := []byte("zip bytes of " + file)

		fmt.Fprintf(&sums, "%s  %s\n", hashOf(data), file)

		s.archives[file] = data
		s.order = append(s.order, file)
	}

	s.sums = []byte(sums.String())
	s.sig = sg.Detach(t, s.sums)

	return s
}

// hashOf returns the hex SHA-256 of data.
func hashOf(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// stage stores every file of s and returns the request that names them.
func (e *env) stage(t *testing.T, ns namespace.Name, s *set) service.PublishRequest {
	t.Helper()

	put := func(data []byte) string {
		t.Helper()

		st, err := e.svc.Stage(context.Background(), e.caller, ns, bytes.NewReader(data))
		if err != nil {
			t.Fatalf("Stage: %v", err)
		}

		return st.Token
	}

	files := make([]service.StagedFile, 0, 2+len(s.order))
	files = append(files,
		service.StagedFile{Role: service.RoleSums, Name: s.sumsName(), Token: put(s.sums)},
		service.StagedFile{Role: service.RoleSignature, Name: s.sigName(), Token: put(s.sig)},
	)

	for _, name := range s.order {
		files = append(files, service.StagedFile{Role: service.RoleArchive, Name: name, Token: put(s.archives[name])})
	}

	return service.PublishRequest{
		Namespace: ns, Name: s.name, Version: s.version, Protocols: []string{"5.0"}, Files: files,
	}
}

// listing returns the keys under dir of the registry of the namespace.
func (e *env) listing(t *testing.T, id namespace.ID, dir string) []string {
	t.Helper()

	objects, err := blobtest.ListAll(context.Background(), e.blobs, "namespaces/"+id.String()+"/registry/"+dir)
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}

	out := make([]string, 0, len(objects))
	for _, o := range objects {
		out = append(out, o.Key.String())
	}

	return out
}

// publish stages s in ns and publishes it.
func (e *env) publish(t *testing.T, ns namespace.Name, s *set) (registry.Release, error) {
	t.Helper()

	return e.svc.Publish(context.Background(), e.caller, e.stage(t, ns, s))
}
