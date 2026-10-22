package service_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/pgpfixture"
)

// readAll reads and closes r.
func readAll(t *testing.T, r io.ReadCloser) []byte {
	t.Helper()

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	closeQuietly(r)

	return data
}

func TestPublishThenRead(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	s := buildSet(t, sg, "payments", "1.0.0", "linux_amd64", "darwin_arm64")

	rel, err := e.publish(t, acme, s)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if rel.KeyFingerprint != sg.Key.Fingerprint || rel.PublishedBy != subject || len(rel.Archives) != 2 ||
		!slices.Equal(rel.Protocols, []string{"5.0"}) {
		t.Fatalf("release = %+v", rel)
	}

	// The staged files are gone and the content keys hold the three kinds of file.
	if got := e.listing(t, e.ids[acme], "staging"); len(got) != 0 {
		t.Fatalf("staged files left: %v", got)
	}

	if got := e.listing(t, e.ids[acme], "sha256"); len(got) != 4 {
		t.Fatalf("content keys = %v, want 4 (sums, signature, 2 archives)", got)
	}

	e.checkArchivesOpen(t, rel, s)
	e.checkAuditOfPublish(t)
}

// checkArchivesOpen proves that each archive opens through the release and
// holds the bytes that were uploaded.
func (e *env) checkArchivesOpen(t *testing.T, rel registry.Release, s *set) {
	t.Helper()

	for _, a := range rel.Archives {
		file, r, err := e.svc.Open(context.Background(), acme, "payments", "1.0.0", a.SHA256)
		if err != nil {
			t.Fatalf("Open %s: %v", a.FileName, err)
		}

		if !bytes.Equal(readAll(t, r), s.archives[a.FileName]) || file.Name != a.FileName {
			t.Fatalf("Open %s returned other bytes", a.FileName)
		}
	}
}

// checkAuditOfPublish proves that the key and the release wrote one entry each.
func (e *env) checkAuditOfPublish(t *testing.T) {
	t.Helper()

	if len(e.store.audits) != 2 { // the key and the release
		t.Fatalf("%d audit entries, want 2", len(e.store.audits))
	}

	last := e.store.audits[len(e.store.audits)-1]
	if last.Action != "registry:publish" || last.Namespace != acme || last.Subject != subject {
		t.Fatalf("audit entry = %+v", last)
	}
}

func TestPublishRepeat(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	s := buildSet(t, sg, "payments", "1.0.0", "linux_amd64")

	first, err := e.publish(t, acme, s)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	again, err := e.publish(t, acme, s)
	if err != nil || !again.SameContent(first) {
		t.Fatalf("repeat = %+v, %v", again, err)
	}

	// Other bytes for the same version are refused.
	other := buildSet(t, sg, "payments", "1.0.0", "linux_amd64")
	other.archives[other.order[0]] = []byte("other bytes")
	other.sums = []byte(hashOf(other.archives[other.order[0]]) + "  " + other.order[0] + "\n")
	other.sig = sg.Detach(t, other.sums)

	if _, err = e.publish(t, acme, other); !errors.Is(err, registry.ErrVersionExists) {
		t.Fatalf("Publish of other bytes = %v, want ErrVersionExists", err)
	}
}

// refusal is one hostile publish. build stages the files and returns the error
// of the publish.
type refusal struct {
	name  string
	build func(t *testing.T, e *env, sg pgpfixture.Signer) error
	want  error
	// text is a part of the error text, such as the name of a file.
	text string
}

// withRequest stages a good set, changes the request, and publishes it.
func withRequest(change func(req *service.PublishRequest)) func(*testing.T, *env, pgpfixture.Signer) error {
	return func(t *testing.T, e *env, sg pgpfixture.Signer) error {
		t.Helper()

		req := e.stage(t, acme, buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
		change(&req)

		_, err := e.svc.Publish(context.Background(), e.caller, req)

		return err
	}
}

// withSet builds a set, changes it, and stages and publishes it in acme.
func withSet(name, version string, change func(s *set)) func(*testing.T, *env, pgpfixture.Signer) error {
	return func(t *testing.T, e *env, sg pgpfixture.Signer) error {
		t.Helper()

		s := buildSet(t, sg, name, version, "linux_amd64")
		change(s)

		_, err := e.publish(t, acme, s)

		return err
	}
}

func noChange(*set) {}

func refusals() []refusal {
	//nolint:exhaustruct_v5 // Each case names only the fields that it tests.
	return []refusal{
		{
			name: "signature over other sums",
			build: withSet("payments", "1.0.0", func(s *set) {
				s.sums = append(s.sums, ' ') // changed after the signature
			}),
			want: registry.ErrBadSignature, text: "SHA256SUMS.sig",
		},
		{
			name: "signature by a key that is not trusted",
			build: func(t *testing.T, e *env, _ pgpfixture.Signer) error {
				t.Helper()

				_, err := e.publish(t, acme, buildSet(t, newSigner(t), "payments", "1.0.0", "linux_amd64"))

				return err
			},
			want: registry.ErrUntrustedKey, text: "SHA256SUMS.sig",
		},
		{
			name: "signature by a key of another namespace",
			build: func(t *testing.T, e *env, _ pgpfixture.Signer) error {
				t.Helper()

				other := newSigner(t)
				e.trust(t, beta, other)

				_, err := e.publish(t, acme, buildSet(t, other, "payments", "1.0.0", "linux_amd64"))

				return err
			},
			want: registry.ErrUntrustedKey,
		},
		{
			name: "archive changed after the signature",
			build: withSet("payments", "1.0.0", func(s *set) {
				s.archives[s.order[0]] = []byte("tampered")
			}),
			want: releasecheck.ErrRefused, text: "linux_amd64.zip",
		},
		{
			name: "namespace with no trusted key",
			build: func(t *testing.T, e *env, sg pgpfixture.Signer) error {
				t.Helper()

				_, err := e.publish(t, beta, buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))

				return err
			},
			want: service.ErrNoKeys,
		},
		{
			name:  "provider name with a capital letter",
			build: withSet("Payments", "1.0.0", noChange),
			want:  registry.ErrInvalid,
		},
		{
			name:  "provider name with an underscore",
			build: withSet("pay_ments", "1.0.0", noChange),
			want:  registry.ErrInvalid,
		},
		{
			name:  "provider name with a double dash",
			build: withSet("pay--ments", "1.0.0", noChange),
			want:  registry.ErrInvalid,
		},
		{
			name:  "version with a path",
			build: withSet("payments", "../1.0.0", noChange),
			want:  registry.ErrInvalid,
		},
		{
			name: "archive of another provider",
			build: func(t *testing.T, e *env, sg pgpfixture.Signer) error {
				t.Helper()

				s := buildSet(t, sg, "ledger", "1.0.0", "linux_amd64")
				s.name = "payments" // the file names still say ledger

				_, err := e.publish(t, acme, s)

				return err
			},
			want: releasecheck.ErrRefused,
		},
		{
			name:  "unknown namespace",
			build: withRequest(func(req *service.PublishRequest) { req.Namespace = "gamma" }),
			want:  namespace.ErrNotFound,
		},
		{
			name:  "token of another namespace",
			build: withRequest(func(req *service.PublishRequest) { req.Namespace = beta }),
			want:  service.ErrNotStaged,
		},
		{
			name: "token that is a path",
			build: withRequest(func(req *service.PublishRequest) {
				req.Files[0].Token = "../../../sha256/" + strings.Repeat("a", 20)
			}),
			want: service.ErrBadRequest,
		},
		{
			name:  "token used for two files",
			build: withRequest(func(req *service.PublishRequest) { req.Files[2].Token = req.Files[0].Token }),
			want:  service.ErrBadRequest,
		},
		{
			name: "no signature file",
			build: withRequest(func(req *service.PublishRequest) {
				req.Files = slices.DeleteFunc(req.Files, func(f service.StagedFile) bool {
					return f.Role == service.RoleSignature
				})
			}),
			want: service.ErrBadRequest,
		},
		{
			name:  "unknown role",
			build: withRequest(func(req *service.PublishRequest) { req.Files[2].Role = "script" }),
			want:  service.ErrBadRequest,
		},
		{
			name:  "plugin protocols that fall",
			build: withRequest(func(req *service.PublishRequest) { req.Protocols = []string{"6.0", "5.0"} }),
			want:  registry.ErrInvalid,
		},
	}
}

// checkRefusal runs one hostile publish and proves that it refused, left no
// content key and stored no release.
func checkRefusal(t *testing.T, tc refusal) {
	t.Helper()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	err := tc.build(t, e, sg)
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
}

// TestPublishRefusals sends hostile input to the publish. Each case must
// refuse, must leave no content key, and must store no release.
func TestPublishRefusals(t *testing.T) {
	t.Parallel()

	for _, tc := range refusals() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRefusal(t, tc)
		})
	}
}

// TestPublishRefusesBytesThatChange holds the rule of the last layer: a staged
// file whose bytes change between the check and the copy never reaches a
// content key.
func TestPublishRefusesBytesThatChange(t *testing.T) {
	t.Parallel()

	e := newEnv(t, true)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	// The wrapper flips the first byte of a staged file on its second read: the
	// first read is the measure, the second the copy.
	_, err := e.publish(t, acme, buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
	if !errors.Is(err, service.ErrIntegrity) {
		t.Fatalf("Publish = %v, want ErrIntegrity", err)
	}

	for _, key := range e.listing(t, e.ids[acme], "sha256") {
		r, getErr := e.blobs.Get(context.Background(), blob.Key(key))
		if getErr != nil {
			continue
		}

		if !strings.HasSuffix(key, hashOf(readAll(t, r))) {
			t.Fatalf("content key %s holds bytes with another hash", key)
		}
	}

	if len(e.store.releases) != 0 {
		t.Fatalf("a refused publish stored a release: %v", e.store.releases)
	}
}

// TestPublishRefusesAKeyRemovedBeforeTheCommit holds the rule that the key is
// checked again in the commit: a key removed after the checks of the service
// and before the write refuses the publish, and no release is stored.
func TestPublishRefusesAKeyRemovedBeforeTheCommit(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	req := e.stage(t, acme, buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))

	e.store.beforeCommit = func() {
		e.store.beforeCommit = nil

		if err := e.svc.RemoveKey(context.Background(), e.caller, acme, sg.Key.Fingerprint); err != nil {
			t.Errorf("RemoveKey: %v", err)
		}
	}

	_, err := e.svc.Publish(context.Background(), e.caller, req)
	if !errors.Is(err, registry.ErrUntrustedKey) {
		t.Fatalf("Publish = %v, want ErrUntrustedKey", err)
	}

	_, getErr := e.svc.Release(context.Background(), acme, "payments", "1.0.0")
	if !errors.Is(getErr, registry.ErrNotFound) {
		t.Fatalf("Release after the refused publish = %v, want ErrNotFound", getErr)
	}
}
