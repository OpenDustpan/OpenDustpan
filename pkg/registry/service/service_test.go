package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/pgpverify"
	"opendustpan.org/opendustpan/pkg/registry/releasecheck"
	"opendustpan.org/opendustpan/pkg/registry/service"
)

// memStore is registry.Store and registry.KeyStore in memory.
type memStore struct {
	mu       sync.Mutex
	releases map[string]registry.Release
	keys     map[namespace.Name][]registry.KeyRecord
	audits   []audit.Entry
}

func newMemStore() *memStore {
	return &memStore{mu: sync.Mutex{}, releases: map[string]registry.Release{}, keys: map[namespace.Name][]registry.KeyRecord{}, audits: nil}
}

func relKey(a registry.Address, v string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", a.Namespace, a.Kind, a.Name, a.System, v)
}

func (m *memStore) Publish(_ context.Context, r registry.Release, _ namespace.Fence, e audit.Entry) error {
	if err := r.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

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

	var out []registry.Release

	for _, r := range m.releases {
		if r.Address == a {
			out = append(out, r)
		}
	}

	slices.SortFunc(out, func(x, y registry.Release) int { return registry.CompareVersions(x.Version, y.Version) })

	return out, nil
}

func (m *memStore) Addresses(context.Context, namespace.Name, registry.Kind) ([]registry.Address, error) {
	return nil, nil
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

// env is a service with a fresh blob directory, store and two namespaces.
type env struct {
	svc    *service.Service
	store  *memStore
	blobs  blob.Store
	ids    map[namespace.Name]namespace.ID
	caller *service.Caller
	now    time.Time
}

// flipBlobs is a blob.Store that changes the bytes of a key the second time that
// the key is read, as a store that fails or an attacker with write access would.
type flipBlobs struct {
	blob.Store

	mu    sync.Mutex
	reads map[blob.Key]int
	flip  bool
}

func (f *flipBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	r, err := f.Store.Get(ctx, key)
	if err != nil {
		return nil, err //nolint:wrapcheck // Test double.
	}

	f.mu.Lock()
	f.reads[key]++
	n := f.reads[key]
	f.mu.Unlock()

	if f.flip && strings.Contains(key.String(), "/staging/") && n >= 2 {
		data, readErr := io.ReadAll(r)
		_ = r.Close()

		if readErr != nil {
			return nil, readErr //nolint:wrapcheck // Test double.
		}

		if len(data) > 0 {
			data[0] ^= 0xff
		}

		return io.NopCloser(bytes.NewReader(data)), nil
	}

	return r, nil
}

func newEnv(t *testing.T, flip bool) *env {
	t.Helper()

	dir, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blobs: %v", err)
	}

	t.Cleanup(func() { _ = dir.Close() })

	blobs := &flipBlobs{Store: dir, mu: sync.Mutex{}, reads: map[blob.Key]int{}, flip: flip}
	store := newMemStore()
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

	ids := map[namespace.Name]namespace.ID{}
	ns := nsStub{}

	for _, name := range []namespace.Name{"acme", "beta"} {
		id := namespace.NewID()
		ids[name] = id
		ns[name] = namespace.Namespace{
			CreatedAt: now, ID: id, Name: name, HomeRegion: "eu-west", FailoverVersion: 1,
			Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		}
	}

	v := pgpverify.Verifier{Now: func() time.Time { return now }}

	svc, err := service.New(service.Config{
		Store: store, Keys: store, Verifier: v, Inspector: v, Blobs: blobs, Namespaces: ns,
		Now: func() time.Time { return now }, MaxArchiveBytes: 1 << 20, MaxReleaseBytes: 3 << 20,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return &env{
		svc: svc, store: store, blobs: blobs, ids: ids, now: now,
		caller: &service.Caller{
			Principal: identity.Principal{Subject: "user:ada", Kind: identity.KindPerson},
			By: audit.Entry{
				Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, SourceIP: "192.0.2.1",
			},
		},
	}
}

// signer is a signing key pair.
type signer struct {
	entity *openpgp.Entity
	key    registry.TrustedKey
}

func newSigner(t *testing.T) signer {
	t.Helper()

	e, err := openpgp.NewEntity("Release", "", "release@example.invalid", &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA})
	if err != nil {
		t.Fatalf("new entity: %v", err)
	}

	var buf bytes.Buffer

	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatalf("armor: %v", err)
	}

	if err = e.Serialize(w); err != nil {
		t.Fatalf("serialize: %v", err)
	}

	_ = w.Close()

	return signer{entity: e, key: registry.TrustedKey{Fingerprint: strings.ToUpper(hex.EncodeToString(e.PrimaryKey.Fingerprint)), Armored: buf.Bytes()}}
}

// trust registers the key of s in ns, through the service.
func (e *env) trust(t *testing.T, ns namespace.Name, s signer) {
	t.Helper()

	if _, err := e.svc.AddKey(context.Background(), e.caller, ns, s.key.Armored); err != nil {
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

func (s set) sumsName() string { return releasecheck.SumsName(s.name, s.version) }
func (s set) sigName() string  { return releasecheck.SignatureName(s.name, s.version) }

func buildSet(t *testing.T, sg signer, name, version string, platforms ...string) set {
	t.Helper()

	s := set{name: name, version: version, archives: map[string][]byte{}}

	var sums strings.Builder

	for _, p := range platforms {
		file := fmt.Sprintf("terraform-provider-%s_%s_%s.zip", name, version, p)
		data := []byte("zip bytes of " + file)
		sum := sha256.Sum256(data)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), file)

		s.archives[file] = data
		s.order = append(s.order, file)
	}

	s.sums = []byte(sums.String())

	var sig bytes.Buffer
	if err := openpgp.DetachSign(&sig, sg.entity, bytes.NewReader(s.sums), nil); err != nil {
		t.Fatalf("sign: %v", err)
	}

	s.sig = sig.Bytes()

	return s
}

// stage stores every file of s and returns the request that names them.
func (e *env) stage(t *testing.T, ns namespace.Name, s set) service.PublishRequest {
	t.Helper()

	put := func(data []byte) service.Staged {
		t.Helper()

		st, err := e.svc.Stage(context.Background(), e.caller, ns, bytes.NewReader(data))
		if err != nil {
			t.Fatalf("Stage: %v", err)
		}

		return st
	}

	req := service.PublishRequest{Namespace: ns, Name: s.name, Version: s.version, Protocols: []string{"5.0"}}
	req.Files = append(req.Files,
		service.StagedFile{Role: service.RoleSums, Name: s.sumsName(), Token: put(s.sums).Token},
		service.StagedFile{Role: service.RoleSignature, Name: s.sigName(), Token: put(s.sig).Token},
	)

	for _, name := range s.order {
		req.Files = append(req.Files, service.StagedFile{Role: service.RoleArchive, Name: name, Token: put(s.archives[name]).Token})
	}

	return req
}

// listing returns the keys under prefix, below the blob root.
func (e *env) listing(t *testing.T, id namespace.ID, dir string) []string {
	t.Helper()

	objects, err := e.blobs.List(context.Background(), "namespaces/"+id.String()+"/registry/"+dir)
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}

	var out []string
	for _, o := range objects {
		out = append(out, o.Key.String())
	}

	return out
}

func TestPublishThenRead(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, "acme", sg)

	s := buildSet(t, sg, "payments", "1.0.0", "linux_amd64", "darwin_arm64")
	req := e.stage(t, "acme", s)

	rel, err := e.svc.Publish(context.Background(), e.caller, req)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if rel.KeyFingerprint != sg.key.Fingerprint || rel.PublishedBy != "user:ada" || len(rel.Archives) != 2 ||
		!slices.Equal(rel.Protocols, []string{"5.0"}) {
		t.Fatalf("release = %+v", rel)
	}

	// The staged files are gone and the content keys hold the three kinds of file.
	if got := e.listing(t, e.ids["acme"], "staging"); len(got) != 0 {
		t.Fatalf("staged files left: %v", got)
	}

	if got := e.listing(t, e.ids["acme"], "sha256"); len(got) != 4 {
		t.Fatalf("content keys = %v, want 4 (sums, signature, 2 archives)", got)
	}

	// Each file opens through the release and holds the bytes that were uploaded.
	for _, a := range rel.Archives {
		file, r, openErr := e.svc.Open(context.Background(), "acme", "payments", "1.0.0", a.SHA256)
		if openErr != nil {
			t.Fatalf("Open %s: %v", a.FileName, openErr)
		}

		data, _ := io.ReadAll(r)
		_ = r.Close()

		if !bytes.Equal(data, s.archives[a.FileName]) || file.Name != a.FileName {
			t.Fatalf("Open %s returned other bytes", a.FileName)
		}
	}

	if len(e.store.audits) != 2 { // the key and the release
		t.Fatalf("%d audit entries, want 2", len(e.store.audits))
	}

	last := e.store.audits[len(e.store.audits)-1]
	if last.Action != "registry:publish" || last.Namespace != "acme" || last.Subject != "user:ada" {
		t.Fatalf("audit entry = %+v", last)
	}
}

func TestPublishRepeat(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, "acme", sg)

	s := buildSet(t, sg, "payments", "1.0.0", "linux_amd64")

	first, err := e.svc.Publish(context.Background(), e.caller, e.stage(t, "acme", s))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	again, err := e.svc.Publish(context.Background(), e.caller, e.stage(t, "acme", s))
	if err != nil || !again.SameContent(first) {
		t.Fatalf("repeat = %+v, %v", again, err)
	}

	// Other bytes for the same version are refused.
	other := buildSet(t, sg, "payments", "1.0.0", "linux_amd64")
	other.archives = map[string][]byte{other.order[0]: []byte("other bytes")}
	sum := sha256.Sum256(other.archives[other.order[0]])
	other.sums = []byte(hex.EncodeToString(sum[:]) + "  " + other.order[0] + "\n")

	var sig bytes.Buffer
	if err = openpgp.DetachSign(&sig, sg.entity, bytes.NewReader(other.sums), nil); err != nil {
		t.Fatalf("sign: %v", err)
	}

	other.sig = sig.Bytes()

	if _, err = e.svc.Publish(context.Background(), e.caller, e.stage(t, "acme", other)); !errors.Is(err, registry.ErrVersionExists) {
		t.Fatalf("Publish of other bytes = %v, want ErrVersionExists", err)
	}
}

// TestPublishRefusals sends hostile input to the publish. Each case must
// refuse, must leave no content key, and must remove the staged files.
func TestPublishRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// run builds the request; it returns the error of the publish.
		run  func(t *testing.T, e *env, sg signer) error
		want error
		// text is a part of the error text, such as the name of a file.
		text string
	}{
		{
			name: "signature over other sums",
			run: func(t *testing.T, e *env, sg signer) error {
				s := buildSet(t, sg, "payments", "1.0.0", "linux_amd64")
				s.sums = append(s.sums, ' ') // changed after the signature

				return publishSet(t, e, "acme", s)
			},
			want: registry.ErrBadSignature, text: "SHA256SUMS.sig",
		},
		{
			name: "signature by a key that is not trusted",
			run: func(t *testing.T, e *env, _ signer) error {
				return publishSet(t, e, "acme", buildSet(t, newSigner(t), "payments", "1.0.0", "linux_amd64"))
			},
			want: registry.ErrUntrustedKey, text: "SHA256SUMS.sig",
		},
		{
			name: "signature by a key of another namespace",
			run: func(t *testing.T, e *env, _ signer) error {
				other := newSigner(t)
				e.trust(t, "beta", other)

				return publishSet(t, e, "acme", buildSet(t, other, "payments", "1.0.0", "linux_amd64"))
			},
			want: registry.ErrUntrustedKey,
		},
		{
			name: "archive changed after the signature",
			run: func(t *testing.T, e *env, sg signer) error {
				s := buildSet(t, sg, "payments", "1.0.0", "linux_amd64")
				s.archives[s.order[0]] = []byte("tampered")

				return publishSet(t, e, "acme", s)
			},
			want: releasecheck.ErrRefused, text: "linux_amd64.zip",
		},
		{
			name: "namespace with no trusted key",
			run: func(t *testing.T, e *env, sg signer) error {
				return publishSet(t, e, "beta", buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
			},
			want: service.ErrNoKeys,
		},
		{
			name: "provider name with a capital letter",
			run: func(t *testing.T, e *env, sg signer) error {
				return publishSet(t, e, "acme", buildSet(t, sg, "Payments", "1.0.0", "linux_amd64"))
			},
			want: registry.ErrInvalid,
		},
		{
			name: "provider name with an underscore",
			run: func(t *testing.T, e *env, sg signer) error {
				return publishSet(t, e, "acme", buildSet(t, sg, "pay_ments", "1.0.0", "linux_amd64"))
			},
			want: registry.ErrInvalid,
		},
		{
			name: "provider name with a double dash",
			run: func(t *testing.T, e *env, sg signer) error {
				return publishSet(t, e, "acme", buildSet(t, sg, "pay--ments", "1.0.0", "linux_amd64"))
			},
			want: registry.ErrInvalid,
		},
		{
			name: "version with a path",
			run: func(t *testing.T, e *env, sg signer) error {
				return publishSet(t, e, "acme", buildSet(t, sg, "payments", "../1.0.0", "linux_amd64"))
			},
			want: registry.ErrInvalid,
		},
		{
			name: "archive of another provider",
			run: func(t *testing.T, e *env, sg signer) error {
				s := buildSet(t, sg, "ledger", "1.0.0", "linux_amd64")
				s.name = "payments" // the file names still say ledger

				return publishSet(t, e, "acme", s)
			},
			want: releasecheck.ErrRefused,
		},
		{
			name: "unknown namespace",
			run: func(t *testing.T, e *env, sg signer) error {
				req := e.stage(t, "acme", buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
				req.Namespace = "gamma"

				_, err := e.svc.Publish(context.Background(), e.caller, req)

				return err
			},
			want: namespace.ErrNotFound,
		},
		{
			name: "token of another namespace",
			run: func(t *testing.T, e *env, sg signer) error {
				req := e.stage(t, "acme", buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
				req.Namespace = "beta"

				_, err := e.svc.Publish(context.Background(), e.caller, req)

				return err
			},
			want: service.ErrNotStaged,
		},
		{
			name: "token that is a path",
			run: func(t *testing.T, e *env, sg signer) error {
				req := e.stage(t, "acme", buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
				req.Files[0].Token = "../../../sha256/" + strings.Repeat("a", 20)

				_, err := e.svc.Publish(context.Background(), e.caller, req)

				return err
			},
			want: service.ErrBadRequest,
		},
		{
			name: "token used for two files",
			run: func(t *testing.T, e *env, sg signer) error {
				req := e.stage(t, "acme", buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
				req.Files[2].Token = req.Files[0].Token

				_, err := e.svc.Publish(context.Background(), e.caller, req)

				return err
			},
			want: service.ErrBadRequest,
		},
		{
			name: "no signature file",
			run: func(t *testing.T, e *env, sg signer) error {
				req := e.stage(t, "acme", buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
				req.Files = slices.DeleteFunc(req.Files, func(f service.StagedFile) bool { return f.Role == service.RoleSignature })

				_, err := e.svc.Publish(context.Background(), e.caller, req)

				return err
			},
			want: service.ErrBadRequest,
		},
		{
			name: "unknown role",
			run: func(t *testing.T, e *env, sg signer) error {
				req := e.stage(t, "acme", buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
				req.Files[2].Role = "script"

				_, err := e.svc.Publish(context.Background(), e.caller, req)

				return err
			},
			want: service.ErrBadRequest,
		},
		{
			name: "plugin protocols that fall",
			run: func(t *testing.T, e *env, sg signer) error {
				req := e.stage(t, "acme", buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
				req.Protocols = []string{"6.0", "5.0"}

				_, err := e.svc.Publish(context.Background(), e.caller, req)

				return err
			},
			want: registry.ErrInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, false)
			sg := newSigner(t)
			e.trust(t, "acme", sg)

			err := tc.run(t, e, sg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Publish = %v, want %v", err, tc.want)
			}

			if tc.text != "" && !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("error %q does not name %q", err, tc.text)
			}

			for name, id := range e.ids {
				if got := e.listing(t, id, "sha256"); len(got) != 0 {
					t.Fatalf("namespace %s holds content keys after a refusal: %v", name, got)
				}
			}

			if len(e.store.releases) != 0 {
				t.Fatalf("a refused publish stored a release: %v", e.store.releases)
			}
		})
	}
}

// publishSet stages s in ns and publishes it.
func publishSet(t *testing.T, e *env, ns namespace.Name, s set) error {
	t.Helper()

	req := e.stage(t, ns, s)
	_, err := e.svc.Publish(context.Background(), e.caller, req)

	return err
}

// TestPublishRefusesBytesThatChange holds the rule of the last layer: a staged
// file whose bytes change between the check and the copy never reaches a
// content key.
func TestPublishRefusesBytesThatChange(t *testing.T) {
	t.Parallel()

	e := newEnv(t, true)
	sg := newSigner(t)
	e.trust(t, "acme", sg)

	// The wrapper flips the first byte of a staged file on its third read: the
	// first read is the measure, the second the copy.
	err := publishSet(t, e, "acme", buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
	if err == nil {
		t.Fatal("Publish succeeded for bytes that changed")
	}

	if !errors.Is(err, service.ErrIntegrity) {
		t.Fatalf("Publish = %v, want ErrIntegrity", err)
	}

	for _, key := range e.listing(t, e.ids["acme"], "sha256") {
		r, getErr := e.blobs.Get(context.Background(), blob.Key(key))
		if getErr != nil {
			continue
		}

		data, _ := io.ReadAll(r)
		_ = r.Close()

		sum := sha256.Sum256(data)
		if !strings.HasSuffix(key, hex.EncodeToString(sum[:])) {
			t.Fatalf("content key %s holds bytes with another hash", key)
		}
	}

	if len(e.store.releases) != 0 {
		t.Fatalf("a refused publish stored a release: %v", e.store.releases)
	}
}

func TestStageLimits(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	if _, err := e.svc.Stage(context.Background(), e.caller, "acme", bytes.NewReader(make([]byte, 1<<20+1))); !errors.Is(err, service.ErrTooLarge) {
		t.Fatalf("Stage of a large file = %v, want ErrTooLarge", err)
	}

	if _, err := e.svc.Stage(context.Background(), e.caller, "acme", bytes.NewReader(nil)); !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("Stage of an empty file = %v, want ErrBadRequest", err)
	}

	if _, err := e.svc.Stage(context.Background(), e.caller, "gamma", bytes.NewReader([]byte("x"))); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("Stage in an unknown namespace = %v, want ErrNotFound", err)
	}

	if got := e.listing(t, e.ids["acme"], "staging"); len(got) != 0 {
		t.Fatalf("a refused stage left files: %v", got)
	}

	st, err := e.svc.Stage(context.Background(), e.caller, "acme", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	sum := sha256.Sum256([]byte("hello"))
	if st.SHA256 != hex.EncodeToString(sum[:]) || st.Size != 5 {
		t.Fatalf("Stage = %+v, want the server's own hash and size", st)
	}
}

func TestStageSweepsOldFiles(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	first, err := e.svc.Stage(context.Background(), e.caller, "acme", strings.NewReader("old"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	if got := e.listing(t, e.ids["acme"], "staging"); len(got) != 1 {
		t.Fatalf("staged files = %v, want 1", got)
	}

	// A later stage, after the grace period, removes the old file.
	e.svc = rebuild(t, e, e.now.Add(service.StagingGrace+time.Hour))

	if _, err = e.svc.Stage(context.Background(), e.caller, "acme", strings.NewReader("new")); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	got := e.listing(t, e.ids["acme"], "staging")
	if len(got) != 1 || strings.HasSuffix(got[0], first.Token) {
		t.Fatalf("staged files = %v, want only the new one", got)
	}
}

// rebuild returns a service over the same stores with a later clock.
func rebuild(t *testing.T, e *env, at time.Time) *service.Service {
	t.Helper()

	ns := nsStub{}
	for name, id := range e.ids {
		ns[name] = namespace.Namespace{
			CreatedAt: e.now, ID: id, Name: name, HomeRegion: "eu-west", FailoverVersion: 1,
			Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		}
	}

	v := pgpverify.Verifier{Now: func() time.Time { return at }}

	svc, err := service.New(service.Config{
		Store: e.store, Keys: e.store, Verifier: v, Inspector: v, Blobs: e.blobs, Namespaces: ns,
		Now: func() time.Time { return at },
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc
}

// TestOpenRefusals holds the rule that a content key opens only through a
// published release of the same namespace.
func TestOpenRefusals(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, "acme", sg)

	s := buildSet(t, sg, "payments", "1.0.0", "linux_amd64")

	rel, err := e.svc.Publish(context.Background(), e.caller, e.stage(t, "acme", s))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	staged, err := e.svc.Stage(context.Background(), e.caller, "acme", strings.NewReader("secret staged bytes"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	known := rel.Archives[0].SHA256

	tests := []struct {
		name               string
		ns                 namespace.Name
		provider, ver, sum string
	}{
		{"the hash of a staged file", "acme", "payments", "1.0.0", staged.SHA256},
		{"a release of another namespace", "beta", "payments", "1.0.0", known},
		{"another version", "acme", "payments", "2.0.0", known},
		{"another provider", "acme", "ledger", "1.0.0", known},
		{"a hash that is not a hash", "acme", "payments", "1.0.0", "../../etc/passwd"},
		{"an unknown namespace", "gamma", "payments", "1.0.0", known},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, r, err := e.svc.Open(context.Background(), tc.ns, tc.provider, tc.ver, tc.sum)
			if r != nil {
				_ = r.Close()
			}

			if err == nil {
				t.Fatal("Open succeeded")
			}

			if !errors.Is(err, registry.ErrNotFound) && !errors.Is(err, registry.ErrInvalid) {
				t.Fatalf("Open = %v, want ErrNotFound or ErrInvalid", err)
			}
		})
	}
}

func TestSigningKeyFailsClosedAfterRemoval(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, "acme", sg)

	rel, err := e.svc.Publish(context.Background(), e.caller, e.stage(t, "acme", buildSet(t, sg, "payments", "1.0.0", "linux_amd64")))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if key, keyErr := e.svc.SigningKey(context.Background(), rel); keyErr != nil || key.Fingerprint != sg.key.Fingerprint {
		t.Fatalf("SigningKey = %+v, %v", key, keyErr)
	}

	if err = e.svc.RemoveKey(context.Background(), e.caller, "acme", sg.key.Fingerprint); err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	if _, err = e.svc.SigningKey(context.Background(), rel); !errors.Is(err, registry.ErrKeyNotFound) {
		t.Fatalf("SigningKey after removal = %v, want ErrKeyNotFound", err)
	}

	if err = e.svc.RemoveKey(context.Background(), e.caller, "acme", "../x"); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("RemoveKey with a bad fingerprint = %v, want ErrInvalid", err)
	}
}

func TestAddKeyRefusals(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	for name, armored := range map[string][]byte{
		"garbage": []byte("not a key"),
		"empty":   nil,
	} {
		if _, err := e.svc.AddKey(context.Background(), e.caller, "acme", armored); !errors.Is(err, registry.ErrBadKey) {
			t.Fatalf("%s: AddKey = %v, want ErrBadKey", name, err)
		}
	}

	if _, err := e.svc.AddKey(context.Background(), e.caller, "gamma", newSigner(t).key.Armored); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("AddKey in an unknown namespace = %v, want ErrNotFound", err)
	}

	if len(e.store.keys["acme"]) != 0 {
		t.Fatal("a refused key was stored")
	}
}

func TestCallerWithoutSubject(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	anon := &service.Caller{Principal: identity.Principal{}, By: e.caller.By}

	if _, err := e.svc.Stage(context.Background(), anon, "acme", strings.NewReader("x")); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("Stage = %v, want ErrUnauthenticated", err)
	}

	if _, err := e.svc.Stage(context.Background(), nil, "acme", strings.NewReader("x")); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("Stage with no caller = %v, want ErrUnauthenticated", err)
	}
}
