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
	"slices"
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
	protocolV5  = "5.0"
	protocolV6  = "6.0"
	publisher   = "user-1"
	v100        = "1.0.0"
	archiveSize = 10
	sumA        = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sumB        = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sumC        = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	sumD        = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	fingerprint = "ABABABABABABABABABABABABABABABABABABABAB"
	// actionPublish is the action of the audit entry of a publish.
	actionPublish = "registry:publish"
	// fixtureT is the epoch of the fixtures, in Unix milliseconds.
	fixtureT = 1_800_000_000_000
	// addressModule is the name of the module of the address cases. It is also
	// the name of one provider, so a page of providers never holds it.
	addressModule = "payments"
	// addressSystem is the target system of the module of the address cases.
	addressSystem = "aws"
	// addressPageSize is the page size that the address case reads with.
	addressPageSize = 2
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace name and returns its current fence. Entries returns the audit
// entries that the store wrote for the namespace, oldest first. The entries
// of the key store are in it too: the suite counts only the publish entries.
type Harness struct {
	Store registry.Store
	// Keys is the key store of the same database. The suite registers the
	// signing key of a release in it before the publish, because the store
	// checks the key in the transaction of the publish.
	Keys      registry.KeyStore
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
		Protocols:       []string{protocolV5},
		PublishedBy:     publisher,
		PublishedAt:     time.UnixMilli(fixtureT).UTC(),
		Archives: []registry.Archive{{
			Platform: "linux_amd64",
			FileName: "terraform-provider-" + name + "_" + version + "_linux_amd64.zip",
			SHA256:   sumA,
			Size:     archiveSize,
		}},
		Files: nil,
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
		Action:        actionPublish,
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

// homed makes the namespace name and registers the signing key of the fixture
// releases in it. It returns the fence of the namespace.
func homed(t *testing.T, h Harness, name namespace.Name) namespace.Fence {
	t.Helper()

	f := h.Namespace(t, name)

	rec := keyRecord(name, keyOne)
	rec.Key.Fingerprint = fingerprint

	if err := h.Keys.AddKey(t.Context(), rec, f, keyEntry(name)); err != nil {
		t.Fatalf("AddKey(%s): %v", fingerprint, err)
	}

	return f
}

// publishEntries returns the audit entries of the publishes in acme.
func publishEntries(t *testing.T, h Harness) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	all := h.Entries(t, acme)

	for i := range all {
		if all[i].Action == actionPublish {
			out = append(out, all[i])
		}
	}

	return out
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
		{"the plugin protocols are kept and count as content", testProtocols},
		{"a provider with bad plugin protocols is refused", testBadProtocols},
		{"an unsigned provider is refused", testUnsignedProvider},
		{"list orders by semantic version precedence", testListOrder},
		{"addresses are read in pages", testAddresses},
		{"module addresses are read in pages", testModuleAddresses},
		{"addresses with a bad cursor", testAddressesBadCursor},
		{"addresses with a bad limit", testAddressesBadLimit},
		{"addresses with a bad kind", testAddressesBadKind},
		{"publish with a key that the namespace removed", testPublishRemovedKey},
		{"publish with a key that the namespace never trusted", testPublishUnknownKey},
	}
}

func testPublishGet(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
	r := provider("payments", v100)
	mustPublish(t, h, r, f)

	got, err := h.Store.Get(t.Context(), r.Address, r.Version)
	if err != nil || !got.SameContent(r) || got.PublishedBy != r.PublishedBy {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}

func testGetMissing(t *testing.T, h Harness) {
	t.Helper()

	homed(t, h, acme)

	_, err := h.Store.Get(t.Context(), address(acme, registry.KindProvider, "payments", ""), v100)
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func testGetBadVersion(t *testing.T, h Harness) {
	t.Helper()

	homed(t, h, acme)

	_, err := h.Store.Get(t.Context(), address(acme, registry.KindProvider, "payments", ""), "../1")
	if !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("Get = %v, want ErrInvalid", err)
	}
}

func testPublishInvalid(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
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

	f := homed(t, h, acme)
	r := provider("payments", v100)

	err := h.Store.Publish(t.Context(), r, stale(f), entry(acme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Publish = %v, want ErrFenced", err)
	}

	expectNoReleases(t, h, r.Address)
}

func testPublishOtherFence(t *testing.T, h Harness) {
	t.Helper()

	homed(t, h, acme)
	other := homed(t, h, beta)
	r := provider("payments", v100)

	err := h.Store.Publish(t.Context(), r, other, entry(acme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Publish = %v, want ErrFenced", err)
	}

	expectNoReleases(t, h, r.Address)
}

func testPublishRepeat(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
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

	f := homed(t, h, acme)
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

	fa := homed(t, h, acme)
	homed(t, h, beta)
	mustPublish(t, h, provider("payments", v100), fa)

	_, err := h.Store.Get(t.Context(), address(beta, registry.KindProvider, "payments", ""), v100)
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("Get in another namespace = %v, want ErrNotFound", err)
	}
}

func testKinds(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
	mustPublish(t, h, provider("vpc", v100), f)

	mod := registry.Release{
		Address:         address(acme, registry.KindModule, "vpc", "aws"),
		Version:         v100,
		SumsFile:        "",
		SumsSHA256:      "",
		SignatureFile:   "",
		SignatureSHA256: "",
		KeyFingerprint:  "",
		Protocols:       nil,
		PublishedBy:     publisher,
		PublishedAt:     time.UnixMilli(fixtureT).UTC(),
		Archives:        []registry.Archive{{Platform: "", FileName: "vpc.zip", SHA256: sumB, Size: 1}},
		Files:           nil,
	}
	mustPublish(t, h, mod, f)

	got, err := h.Store.Get(t.Context(), mod.Address, v100)
	if err != nil || got.Archives[0].SHA256 != sumB {
		t.Fatalf("Get module = %+v, %v", got, err)
	}
}

func testListOrder(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)

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

	f := homed(t, h, acme)
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

	if n := len(publishEntries(t, h)); n != 1 {
		t.Fatalf("%d audit entries, want 1", n)
	}
}

func testAuditOnPublish(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
	mustPublish(t, h, provider("payments", v100), f)

	got := publishEntries(t, h)
	if len(got) != 1 || got[0].Action != actionPublish || got[0].Namespace != acme {
		t.Fatalf("entries = %+v, want one registry:publish entry", got)
	}
}

func testAuditOnRefusal(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
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

	if n := len(publishEntries(t, h)); n != 1 {
		t.Fatalf("%d audit entries after refusals, want 1", n)
	}
}

// testAuditOnRepeat holds the rule for an identical repeat: the store changes
// nothing, and that includes the audit log.
func testAuditOnRepeat(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
	r := provider("payments", v100)
	mustPublish(t, h, r, f)
	mustPublish(t, h, r, f)

	if n := len(publishEntries(t, h)); n != 1 {
		t.Fatalf("%d audit entries after a repeat, want 1", n)
	}
}

func testGetCopy(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
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

	f := homed(t, h, acme)
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

func testProtocols(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
	r := provider("payments", v100)
	r.Protocols = []string{protocolV5, protocolV6}
	mustPublish(t, h, r, f)

	got, err := h.Store.Get(t.Context(), r.Address, v100)
	if err != nil || !slices.Equal(got.Protocols, []string{protocolV5, protocolV6}) {
		t.Fatalf("Get = %+v, %v; want the protocols 5.0 and 6.0", got, err)
	}

	other := r
	other.Protocols = []string{protocolV5}

	if err := h.Store.Publish(t.Context(), other, f, entry(acme)); !errors.Is(err, registry.ErrVersionExists) {
		t.Fatalf("Publish with other protocols = %v, want ErrVersionExists", err)
	}
}

func testBadProtocols(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)

	for name, protocols := range map[string][]string{
		"none":          nil,
		"empty text":    {""},
		"not a version": {"five"},
		"with a comma":  {"5.0,6.0"},
		"repeated":      {protocolV5, protocolV5},
		"falling":       {protocolV6, protocolV5},
		"too many":      {"1.0", "2.0", "3.0", "4.0", protocolV5},
	} {
		r := provider("payments", v100)
		r.Protocols = protocols

		if err := h.Store.Publish(t.Context(), r, f, entry(acme)); !errors.Is(err, registry.ErrInvalid) {
			t.Fatalf("%s: Publish = %v, want ErrInvalid", name, err)
		}
	}
}

func testUnsignedProvider(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
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

	// Several providers with several versions each: a second version of one
	// provider does not make a second address.
	want := []string{"delta", "eta", "ledger", addressModule, "vault"}

	publishAddresses(t, h)

	got := readAddresses(t, h, want)

	if !slices.Equal(got, want) {
		t.Fatalf("addresses = %v, want %v", got, want)
	}

	// The module of acme and the provider of beta stay out of the pages of
	// the providers of acme.
	modules, _, err := h.Store.Addresses(t.Context(), acme, registry.KindModule, "", registry.MaxAddressPage)
	if err != nil || len(modules) != 1 || modules[0].Name != addressModule || modules[0].System != addressSystem {
		t.Fatalf("module addresses = %+v, %v, want the module of acme", modules, err)
	}

	inBeta, _, err := h.Store.Addresses(t.Context(), beta, registry.KindProvider, "", registry.MaxAddressPage)
	if err != nil || len(inBeta) != 1 || inBeta[0].Name != "other" {
		t.Fatalf("providers of beta = %+v, %v, want the provider of beta", inBeta, err)
	}
}

// publishAddresses publishes the fixtures of the address cases: five providers
// of acme with two versions each, one provider of beta, and one module of acme
// that shares the name of a provider.
func publishAddresses(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
	fb := homed(t, h, beta)

	for _, name := range []string{"vault", "delta", addressModule, "eta", "ledger"} {
		mustPublish(t, h, provider(name, v100), f)
		mustPublish(t, h, provider(name, "1.1.0"), f)
	}

	other := provider("other", v100)
	other.Address = address(beta, registry.KindProvider, "other", "")
	mustPublish(t, h, other, fb)

	mustPublish(t, h, moduleRelease(addressModule, addressSystem), f)
}

// moduleRelease returns a valid module release of the namespace acme.
func moduleRelease(name, system string) registry.Release {
	return registry.Release{
		Address:         address(acme, registry.KindModule, name, system),
		Version:         v100,
		SumsFile:        "",
		SumsSHA256:      "",
		SignatureFile:   "",
		SignatureSHA256: "",
		KeyFingerprint:  "",
		Protocols:       nil,
		PublishedBy:     publisher,
		PublishedAt:     time.UnixMilli(fixtureT).UTC(),
		Archives:        []registry.Archive{{Platform: "", FileName: name + ".zip", SHA256: sumB, Size: 1}},
		Files:           nil,
	}
}

// testModuleAddresses proves that the pages of the modules of a namespace
// hold every module once, in order, also when modules share a name.
func testModuleAddresses(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)

	// Two modules share the name payments and one name holds a dash, so a
	// cursor of a name alone cannot page them.
	want := []string{
		"net/" + addressSystem,
		addressModule + "/" + addressSystem,
		addressModule + "/gcp",
		"payments-core/" + addressSystem,
	}

	for _, a := range [][2]string{
		{addressModule, addressSystem}, {addressModule, "gcp"}, {"payments-core", addressSystem}, {"net", addressSystem},
	} {
		mustPublish(t, h, moduleRelease(a[0], a[1]), f)
	}

	if got := readModuleAddresses(t, h, len(want)); !slices.Equal(got, want) {
		t.Fatalf("module addresses = %v, want %v", got, want)
	}
}

// readModuleAddresses reads the pages of the modules of acme one address at a
// time until the store reports no next page, and returns every key in order.
func readModuleAddresses(t *testing.T, h Harness, maxPages int) []string {
	t.Helper()

	var (
		got   []string
		after string
	)

	for pages := 0; ; pages++ {
		if pages > maxPages {
			t.Fatalf("the pages did not end after %d pages", pages)
		}

		page, next, err := h.Store.Addresses(t.Context(), acme, registry.KindModule, after, 1)
		if err != nil {
			t.Fatalf("Addresses(after %q): %v", after, err)
		}

		if len(page) == 0 {
			t.Fatalf("Addresses(after %q) = no address before the last page", after)
		}

		got = append(got, modulePageKeys(t, page, after)...)

		if next == "" {
			return got
		}

		if last := registry.AddressKey(page[len(page)-1]); next != last {
			t.Fatalf("next cursor = %q, want the key of the last address %q", next, last)
		}

		after = next
	}
}

// modulePageKeys checks one page of modules and returns its keys.
func modulePageKeys(t *testing.T, page []registry.Address, after string) []string {
	t.Helper()

	keys := make([]string, 0, len(page))

	for i, a := range page {
		if a.Namespace != acme || a.Kind != registry.KindModule {
			t.Fatalf("Addresses(after %q)[%d] = %+v, want a module of acme", after, i, a)
		}

		if i > 0 && registry.CompareAddresses(page[i-1], a) >= 0 {
			t.Fatalf("Addresses(after %q) = %+v, want addresses in order", after, page)
		}

		keys = append(keys, registry.AddressKey(a))
	}

	return keys
}

// readAddresses reads the pages of the providers of acme with the address page
// size until the store reports no next page, and returns every name in order.
func readAddresses(t *testing.T, h Harness, want []string) []string {
	t.Helper()

	var got []string

	after := ""

	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatalf("the pages did not end after %d pages", pages)
		}

		page, next, err := h.Store.Addresses(t.Context(), acme, registry.KindProvider, after, addressPageSize)
		if err != nil {
			t.Fatalf("Addresses(after %q): %v", after, err)
		}

		if len(page) == 0 {
			t.Fatalf("Addresses(after %q) = no address before the last page", after)
		}

		got = append(got, addressNames(t, page, after)...)

		if next == "" {
			return got
		}

		if next != page[len(page)-1].Name {
			t.Fatalf("next cursor = %q, want the last name %q", next, page[len(page)-1].Name)
		}

		after = next
	}
}

// addressNames checks one page of providers and returns its names.
func addressNames(t *testing.T, page []registry.Address, after string) []string {
	t.Helper()

	names := make([]string, 0, len(page))

	for i, a := range page {
		if a.Namespace != acme || a.Kind != registry.KindProvider || a.System != "" {
			t.Fatalf("Addresses(after %q)[%d] = %+v, want a provider of acme", after, i, a)
		}

		if a.Name <= after {
			t.Fatalf("Addresses(after %q) returned %q, want a name after the cursor", after, a.Name)
		}

		if i > 0 && page[i-1].Name >= a.Name {
			t.Fatalf("Addresses(after %q) = %+v, want names in order", after, page)
		}

		names = append(names, a.Name)
	}

	return names
}

// testAddressesBadCursor proves that a cursor that does not hold the name,
// and for a module the system, of an address of the kind is refused.
func testAddressesBadCursor(t *testing.T, h Harness) {
	t.Helper()

	homed(t, h, acme)

	for _, tc := range []struct {
		kind   registry.Kind
		cursor string
	}{
		{registry.KindProvider, "a/b"},
		{registry.KindProvider, "Payments"},
		{registry.KindModule, "vpc"},
		{registry.KindModule, "vpc/AWS"},
		{registry.KindModule, "/aws"},
		{registry.KindModule, "vpc/"},
	} {
		_, _, err := h.Store.Addresses(t.Context(), acme, tc.kind, tc.cursor, registry.MaxAddressPage)
		if !errors.Is(err, registry.ErrInvalid) {
			t.Fatalf("Addresses(%s, cursor %q) = %v, want ErrInvalid", tc.kind, tc.cursor, err)
		}
	}
}

func testAddressesBadLimit(t *testing.T, h Harness) {
	t.Helper()

	homed(t, h, acme)

	for _, limit := range []int{0, -1, registry.MaxAddressPage + 1} {
		_, _, err := h.Store.Addresses(t.Context(), acme, registry.KindProvider, "", limit)
		if !errors.Is(err, registry.ErrInvalid) {
			t.Fatalf("Addresses(limit %d) = %v, want ErrInvalid", limit, err)
		}
	}
}

func testAddressesBadKind(t *testing.T, h Harness) {
	t.Helper()

	homed(t, h, acme)

	_, _, err := h.Store.Addresses(t.Context(), acme, "plugin", "", registry.MaxAddressPage)
	if !errors.Is(err, registry.ErrInvalid) {
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

// testPublishRemovedKey holds the rule that the store checks the signing key in
// the transaction of the publish: a key removed after the checks of the service
// refuses the publish, and nothing is stored or audited.
func testPublishRemovedKey(t *testing.T, h Harness) {
	t.Helper()

	f := homed(t, h, acme)
	r := provider("payments", v100)

	if err := h.Keys.RemoveKey(t.Context(), acme, fingerprint, f, keyEntry(acme)); err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	err := h.Store.Publish(t.Context(), r, f, entry(acme))
	if !errors.Is(err, registry.ErrKeyNotFound) {
		t.Fatalf("Publish = %v, want ErrKeyNotFound", err)
	}

	expectNoReleases(t, h, r.Address)

	if n := len(publishEntries(t, h)); n != 0 {
		t.Fatalf("%d publish audit entries after a refusal, want 0", n)
	}
}

// testPublishUnknownKey holds the same rule for a key of another namespace: a
// key is trusted for the namespace that registered it only.
func testPublishUnknownKey(t *testing.T, h Harness) {
	t.Helper()

	homed(t, h, acme)
	other := h.Namespace(t, beta)

	r := provider("payments", v100)
	r.Address = address(beta, registry.KindProvider, "payments", "")

	err := h.Store.Publish(t.Context(), r, other, entry(beta))
	if !errors.Is(err, registry.ErrKeyNotFound) {
		t.Fatalf("Publish = %v, want ErrKeyNotFound", err)
	}

	expectNoReleases(t, h, r.Address)
}
