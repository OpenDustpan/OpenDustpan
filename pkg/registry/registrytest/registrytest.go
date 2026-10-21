// Package registrytest is the conformance suite of the ports of package
// registry. A new Store adapter runs RunStore with a StoreFactory. A new
// SignatureVerifier adapter runs RunVerifier with a VerifierFactory.
//
// The cases for Store are complete and run on the memory reference in this
// package's tests. The cases for SignatureVerifier are a skeleton: they skip
// until an adapter and a key fixture exist.
package registrytest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// The namespaces of the fixtures.
const (
	acme namespace.Name = "acme"
	beta namespace.Name = "beta"
)

const (
	publisher   = "user-1"
	v100        = "1.0.0"
	archiveSize = 10
	sumA        = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sumB        = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sumC        = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	sumD        = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	fingerprint = "ABABABABABABABABABABABABABABABABABABABAB"
	// fixtureT is the epoch of the fixtures, in Unix milliseconds.
	fixtureT = 1_800_000_000_000
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace name and returns its current fence. Entries returns the audit
// entries that the store wrote for the namespace, oldest first.
type Harness struct {
	Store     registry.Store
	Namespace func(t *testing.T, name namespace.Name) namespace.Fence
	Entries   func(t *testing.T, name namespace.Name) []audit.Entry
}

// StoreFactory returns a fresh harness for one subtest.
type StoreFactory func(t *testing.T) Harness

type storeCase struct {
	name string
	test func(t *testing.T, h Harness)
}

// RunStore runs every case of the Store suite.
func RunStore(t *testing.T, newHarness StoreFactory) {
	t.Helper()

	for _, tc := range storeCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newHarness(t))
		})
	}
}

// provider returns a valid provider release of the namespace acme.
func provider(name, version string) registry.Release {
	return registry.Release{
		Address:         address(acme, registry.KindProvider, name, ""),
		Version:         version,
		SumsFile:        "terraform-provider-" + name + "_" + version + "_SHA256SUMS",
		SumsSHA256:      sumA,
		SignatureFile:   "terraform-provider-" + name + "_" + version + "_SHA256SUMS.sig",
		SignatureSHA256: sumB,
		KeyFingerprint:  fingerprint,
		PublishedBy:     publisher,
		PublishedAt:     time.UnixMilli(fixtureT).UTC(),
		Archives: []registry.Archive{{
			Platform: "linux_amd64",
			FileName: "terraform-provider-" + name + "_" + version + "_linux_amd64.zip",
			SHA256:   sumA,
			Size:     archiveSize,
		}},
	}
}

// address returns an address of namespace ns.
func address(ns namespace.Name, kind registry.Kind, name, system string) registry.Address {
	return registry.Address{Namespace: ns, Kind: kind, Name: name, System: system}
}

// stale returns a fence that does not match the one the store holds.
func stale(f namespace.Fence) namespace.Fence {
	return namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}
}

// entry returns the audit entry of a publish into ns. It passes the checks of
// a real audit store: an object, a channel, a kind and an outcome.
func entry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At:            time.UnixMilli(fixtureT).UTC(),
		Namespace:     ns,
		Subject:       "user:ada",
		Action:        "registry:publish",
		IDPSubject:    "",
		TokenID:       "",
		Object:        ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

func storeCases() []storeCase {
	return []storeCase{
		{"publish then get", testPublishGet},
		{"get of a missing version", testGetMissing},
		{"get with a bad version", testGetBadVersion},
		{"publish of an invalid release", testPublishInvalid},
		{"publish with a stale fence", testPublishStale},
		{"publish with the fence of another namespace", testPublishOtherFence},
		{"repeat with the same content", testPublishRepeat},
		{"conflict with other hashes", testPublishConflict},
		{"namespaces do not share an address", testIsolation},
		{"a provider and a module of one name are two addresses", testKinds},
		{"concurrent publishes of one version, other content", testPublishConcurrent},
		{"a publish writes one audit entry", testAuditOnPublish},
		{"a refused publish writes no audit entry", testAuditOnRefusal},
		{"an identical repeat writes no audit entry", testAuditOnRepeat},
		{"get returns a copy", testGetCopy},
		{"a signature is kept and counts as content", testSignature},
		{"an unsigned provider is refused", testUnsignedProvider},
		{"list orders by semantic version precedence", testListOrder},
		{"addresses lists each address once", testAddresses},
		{"addresses with a bad kind", testAddressesBadKind},
	}
}

func testPublishGet(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	r := provider("payments", v100)
	mustPublish(t, h, r, f)

	got, err := h.Store.Get(t.Context(), r.Address, r.Version)
	if err != nil || !got.SameContent(r) || got.PublishedBy != r.PublishedBy {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}

func testGetMissing(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme)

	_, err := h.Store.Get(t.Context(), address(acme, registry.KindProvider, "payments", ""), v100)
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func testGetBadVersion(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme)

	_, err := h.Store.Get(t.Context(), address(acme, registry.KindProvider, "payments", ""), "../1")
	if !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("Get = %v, want ErrInvalid", err)
	}
}

func testPublishInvalid(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	r := provider("payments", v100)
	r.Archives[0].SHA256 = "nothex"

	err := h.Store.Publish(t.Context(), r, f, entry(acme))
	if !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("Publish = %v, want ErrInvalid", err)
	}

	expectNoReleases(t, h, r.Address)
}

func testPublishStale(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	r := provider("payments", v100)

	err := h.Store.Publish(t.Context(), r, stale(f), entry(acme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Publish = %v, want ErrFenced", err)
	}

	expectNoReleases(t, h, r.Address)
}

func testPublishOtherFence(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme)
	other := h.Namespace(t, beta)
	r := provider("payments", v100)

	err := h.Store.Publish(t.Context(), r, other, entry(acme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Publish = %v, want ErrFenced", err)
	}

	expectNoReleases(t, h, r.Address)
}

func testPublishRepeat(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	r := provider("payments", v100)
	mustPublish(t, h, r, f)

	again := r
	again.PublishedBy = "user-2"
	again.PublishedAt = r.PublishedAt.Add(time.Hour)
	mustPublish(t, h, again, f)

	got, err := h.Store.Get(t.Context(), r.Address, r.Version)
	if err != nil || got.PublishedBy != publisher || !got.PublishedAt.Equal(r.PublishedAt) {
		t.Fatalf("the repeat changed the record: %+v, %v", got, err)
	}
}

func testPublishConflict(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	r := provider("payments", v100)
	mustPublish(t, h, r, f)

	other := provider("payments", v100)
	other.Archives[0].SHA256 = sumB

	err := h.Store.Publish(t.Context(), other, f, entry(acme))
	if !errors.Is(err, registry.ErrVersionExists) {
		t.Fatalf("Publish = %v, want ErrVersionExists", err)
	}

	got, err := h.Store.Get(t.Context(), r.Address, r.Version)
	if err != nil || got.Archives[0].SHA256 != sumA {
		t.Fatalf("the conflict changed the record: %+v, %v", got, err)
	}
}

func testIsolation(t *testing.T, h Harness) {
	t.Helper()

	fa := h.Namespace(t, acme)
	h.Namespace(t, beta)
	mustPublish(t, h, provider("payments", v100), fa)

	_, err := h.Store.Get(t.Context(), address(beta, registry.KindProvider, "payments", ""), v100)
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("Get in another namespace = %v, want ErrNotFound", err)
	}
}

func testKinds(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	mustPublish(t, h, provider("vpc", v100), f)

	mod := registry.Release{
		Address:         address(acme, registry.KindModule, "vpc", "aws"),
		Version:         v100,
		SumsFile:        "",
		SumsSHA256:      "",
		SignatureFile:   "",
		SignatureSHA256: "",
		KeyFingerprint:  "",
		PublishedBy:     publisher,
		PublishedAt:     time.UnixMilli(fixtureT).UTC(),
		Archives:        []registry.Archive{{Platform: "", FileName: "vpc.zip", SHA256: sumB, Size: 1}},
	}
	mustPublish(t, h, mod, f)

	got, err := h.Store.Get(t.Context(), mod.Address, v100)
	if err != nil || got.Archives[0].SHA256 != sumB {
		t.Fatalf("Get module = %+v, %v", got, err)
	}
}

func testListOrder(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)

	want := []string{"1.0.0-rc.1", v100, "1.2.0", "1.9.0", "1.10.0"}
	for _, v := range []string{"1.10.0", "1.2.0", v100, "1.9.0", "1.0.0-rc.1"} {
		mustPublish(t, h, provider("payments", v), f)
	}

	got, err := h.Store.List(t.Context(), address(acme, registry.KindProvider, "payments", ""))
	if err != nil || len(got) != len(want) {
		t.Fatalf("List = %+v, %v", got, err)
	}

	for i, v := range want {
		if got[i].Version != v {
			t.Fatalf("List[%d] = %s, want %s (semantic version precedence)", i, got[i].Version, v)
		}
	}
}

func testPublishConcurrent(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	hashes := []string{sumA, sumB, sumC, sumD}
	errs := make([]error, len(hashes))

	var wg sync.WaitGroup

	for i, sum := range hashes {
		r := provider("payments", v100)
		r.Archives[0].SHA256 = sum

		wg.Go(func() {
			errs[i] = h.Store.Publish(t.Context(), r, f, entry(acme))
		})
	}

	wg.Wait()

	wins := 0

	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, registry.ErrVersionExists):
			t.Fatalf("Publish = %v, want nil or ErrVersionExists", err)
		default:
		}
	}

	if wins != 1 {
		t.Fatalf("%d publishes won, want exactly 1", wins)
	}

	if n := len(h.Entries(t, acme)); n != 1 {
		t.Fatalf("%d audit entries, want 1", n)
	}
}

func testAuditOnPublish(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	mustPublish(t, h, provider("payments", v100), f)

	got := h.Entries(t, acme)
	if len(got) != 1 || got[0].Action != "registry:publish" || got[0].Namespace != acme {
		t.Fatalf("entries = %+v, want one registry:publish entry", got)
	}
}

func testAuditOnRefusal(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	mustPublish(t, h, provider("payments", v100), f)

	invalid := provider("ledger", v100)
	invalid.Archives[0].SHA256 = "nothex"

	conflict := provider("payments", v100)
	conflict.Archives[0].SHA256 = sumB

	refused := map[string]func() error{
		"invalid":  func() error { return h.Store.Publish(t.Context(), invalid, f, entry(acme)) },
		"stale":    func() error { return h.Store.Publish(t.Context(), provider("ledger", v100), stale(f), entry(acme)) },
		"conflict": func() error { return h.Store.Publish(t.Context(), conflict, f, entry(acme)) },
	}

	for name, publish := range refused {
		if err := publish(); err == nil {
			t.Fatalf("%s publish succeeded", name)
		}
	}

	if n := len(h.Entries(t, acme)); n != 1 {
		t.Fatalf("%d audit entries after refusals, want 1", n)
	}
}

// testAuditOnRepeat holds the rule for an identical repeat: the store changes
// nothing, and that includes the audit log.
func testAuditOnRepeat(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	r := provider("payments", v100)
	mustPublish(t, h, r, f)
	mustPublish(t, h, r, f)

	if n := len(h.Entries(t, acme)); n != 1 {
		t.Fatalf("%d audit entries after a repeat, want 1", n)
	}
}

func testGetCopy(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	r := provider("payments", v100)
	mustPublish(t, h, r, f)

	got, err := h.Store.Get(t.Context(), r.Address, r.Version)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got.Archives[0].SHA256 = sumB

	list, err := h.Store.List(t.Context(), r.Address)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}

	list[0].Archives[0].Size = 999

	again, err := h.Store.Get(t.Context(), r.Address, r.Version)
	if err != nil || !again.SameContent(r) || again.Archives[0].Size != archiveSize {
		t.Fatalf("a change of a result changed the store: %+v, %v", again, err)
	}
}

func testSignature(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	signed := provider("payments", v100)
	mustPublish(t, h, signed, f)

	got, err := h.Store.Get(t.Context(), signed.Address, v100)
	if err != nil || !got.SameContent(signed) || got.KeyFingerprint != signed.KeyFingerprint {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	// The same archives with another signature are other content.
	other := signed
	other.SignatureSHA256 = sumA

	if err := h.Store.Publish(t.Context(), other, f, entry(acme)); !errors.Is(err, registry.ErrVersionExists) {
		t.Fatalf("Publish with another signature = %v, want ErrVersionExists", err)
	}
}

func testUnsignedProvider(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	r := provider("payments", v100)
	r.SignatureFile, r.SignatureSHA256, r.KeyFingerprint = "", "", ""

	if err := h.Store.Publish(t.Context(), r, f, entry(acme)); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("Publish of an unsigned provider = %v, want ErrInvalid", err)
	}

	if _, err := h.Store.Get(t.Context(), r.Address, v100); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("Get after a refused publish = %v, want ErrNotFound", err)
	}
}

func testAddresses(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, acme)
	h.Namespace(t, beta)
	mustPublish(t, h, provider("payments", v100), f)
	mustPublish(t, h, provider("payments", "1.1.0"), f)
	mustPublish(t, h, provider("ledger", v100), f)

	got, err := h.Store.Addresses(t.Context(), acme, registry.KindProvider)
	if err != nil || len(got) != 2 || got[0].Name != "ledger" || got[1].Name != "payments" {
		t.Fatalf("Addresses = %+v, %v", got, err)
	}

	none, err := h.Store.Addresses(t.Context(), beta, registry.KindProvider)
	if err != nil || len(none) != 0 {
		t.Fatalf("Addresses of another namespace = %+v, %v", none, err)
	}
}

func testAddressesBadKind(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme)

	if _, err := h.Store.Addresses(t.Context(), acme, "plugin"); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("Addresses = %v, want ErrInvalid", err)
	}
}

func mustPublish(t *testing.T, h Harness, r registry.Release, f namespace.Fence) {
	t.Helper()

	if err := h.Store.Publish(t.Context(), r, f, entry(r.Address.Namespace)); err != nil {
		t.Fatalf("Publish(%s %s): %v", r.Address.Name, r.Version, err)
	}
}

func expectNoReleases(t *testing.T, h Harness, addr registry.Address) {
	t.Helper()

	got, err := h.Store.List(t.Context(), addr)
	if err != nil || len(got) != 0 {
		t.Fatalf("List after a refused publish = %+v, %v; want none", got, err)
	}
}
