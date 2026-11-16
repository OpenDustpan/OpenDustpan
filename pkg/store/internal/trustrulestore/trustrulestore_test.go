package trustrulestore_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/trustrulestore"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// validRule returns a rule that passes Validate.
func validRule() trustrule.Rule {
	return trustrule.Rule{
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), ID: trustrule.NewID(), Namespace: "acme",
		Name: "deploy", Issuer: "https://token.actions.example.com", Audience: "https://dustpan.example.com",
		Keys: trustrule.KeySourceDiscovery, StaticKeys: "", PrincipalID: "prn-worker", CreatedBy: "ada",
		Revision: 0, SpiffeIDPattern: "", Bundle: "",
		Conditions: []trustrule.Condition{{Claim: "sub", Values: []string{"repo:acme/infra:ref:refs/heads/main"}}},
	}
}

func TestRowRoundTrip(t *testing.T) {
	t.Parallel()

	r := validRule()

	row, err := trustrulestore.CheckCreate(&r, namespace.Fence{Namespace: "acme", FailoverVersion: 1})
	if err != nil {
		t.Fatalf("CheckCreate = %v", err)
	}

	got, err := trustrulestore.Rule(row)
	if err != nil {
		t.Fatalf("Rule = %v", err)
	}

	if got.ID != r.ID || got.Conditions[0].Values[0] != r.Conditions[0].Values[0] || !got.CreatedAt.Equal(r.CreatedAt) {
		t.Fatalf("round trip = %+v, want %+v", got, r)
	}
}

// TestRuleRefusesACorruptRow reads rows that a backup, a restore or another
// version could have written. Each one gives ErrCorrupt.
func TestRuleRefusesACorruptRow(t *testing.T) {
	t.Parallel()

	r := validRule()

	good, err := trustrulestore.RowOf(&r)
	if err != nil {
		t.Fatalf("RowOf = %v", err)
	}

	cases := map[string]func(row *trustrulestore.Row){
		"conditions that are not JSON":   func(row *trustrulestore.Row) { row.ConditionsJSON = "sub=x" },
		"an unknown field":               func(row *trustrulestore.Row) { row.ConditionsJSON = `[{"claim":"sub","values":["x"],"glob":true}]` },
		"text after the conditions":      func(row *trustrulestore.Row) { row.ConditionsJSON = `[{"claim":"sub","values":["x"]}] []` },
		"no condition on the subject":    func(row *trustrulestore.Row) { row.ConditionsJSON = `[{"claim":"ref","values":["x"]}]` },
		"an unknown key source":          func(row *trustrulestore.Row) { row.KeySource = "jku" },
		"an http issuer":                 func(row *trustrulestore.Row) { row.Issuer = "http" + "://token.actions.example.com" },
		"a namespace that is not a name": func(row *trustrulestore.Row) { row.Namespace = "Not A Name" },
		"an ID of another kind":          func(row *trustrulestore.Row) { row.ID = "tok-1" },
		"a revision below one":           func(row *trustrulestore.Row) { row.Revision = 0 },
	}

	for name, change := range cases {
		row := good
		change(&row)

		if _, err := trustrulestore.Rule(row); !errors.Is(err, trustrulestore.ErrCorrupt) {
			t.Errorf("%s: Rule = %v, want ErrCorrupt", name, err)
		}
	}
}

// TestCheckCreateRefusesTheFenceOfAnotherNamespace sends a rule with the
// fence of another namespace, as if the service had sent the wrong fence.
func TestCheckCreateRefusesTheFenceOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	r := validRule()
	acme := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	other := namespace.Fence{Namespace: "other", FailoverVersion: 1}

	_, err := trustrulestore.CheckCreate(&r, other)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("CheckCreate = %v, want ErrFenced", err)
	}

	err = trustrulestore.CheckDelete("acme", r.ID, other)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("CheckDelete = %v, want ErrFenced", err)
	}

	err = trustrulestore.CheckDelete("acme", "trr-a/b", acme)
	if !errors.Is(err, trustrule.ErrInvalid) {
		t.Fatalf("CheckDelete of an invalid ID = %v, want ErrInvalid", err)
	}

	r.Conditions = nil

	_, err = trustrulestore.CheckCreate(&r, acme)
	if !errors.Is(err, trustrule.ErrInvalid) {
		t.Fatalf("CheckCreate of an invalid rule = %v, want ErrInvalid", err)
	}
}

// TestCheckPrincipal checks the principal of a rule as the transaction of a
// create reads it.
func TestCheckPrincipal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		p    trustrulestore.PrincipalRow
		ok   bool
	}{
		{
			"a service principal of the namespace",
			trustrulestore.PrincipalRow{Kind: "service", Namespace: "acme", DeletedAtMs: 0, Found: true},
			true,
		},
		{"no principal", trustrulestore.PrincipalRow{Kind: "", Namespace: "", DeletedAtMs: 0, Found: false}, false},
		{
			"a deleted principal",
			trustrulestore.PrincipalRow{Kind: "service", Namespace: "acme", DeletedAtMs: 1, Found: true},
			false,
		},
		{
			"a person",
			trustrulestore.PrincipalRow{Kind: "person", Namespace: "acme", DeletedAtMs: 0, Found: true},
			false,
		},
		{
			"a workload of the namespace",
			trustrulestore.PrincipalRow{Kind: "workload", Namespace: "acme", DeletedAtMs: 0, Found: true},
			false,
		},
		{
			"a kind of another version",
			trustrulestore.PrincipalRow{Kind: "robot", Namespace: "acme", DeletedAtMs: 0, Found: true},
			false,
		},
		{
			"no kind",
			trustrulestore.PrincipalRow{Kind: "", Namespace: "acme", DeletedAtMs: 0, Found: true},
			false,
		},
		{
			"a principal of the installation",
			trustrulestore.PrincipalRow{Kind: "service", Namespace: "", DeletedAtMs: 0, Found: true},
			false,
		},
		{
			"a principal of another namespace",
			trustrulestore.PrincipalRow{Kind: "service", Namespace: "other", DeletedAtMs: 0, Found: true},
			false,
		},
	}

	for _, tc := range cases {
		err := trustrulestore.CheckPrincipal(tc.p, "acme")
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, trustrule.ErrPrincipal)) {
			t.Errorf("%s: CheckPrincipal = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

// TestCheckExchangePrincipal checks the principal of a rule as the
// transaction of an exchange reads it. Each refused row is of the namespace
// and not deleted, so only its kind or its disable refuses it.
func TestCheckExchangePrincipal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		kind       string
		disabledAt int64
		ok         bool
	}{
		{"a service principal", "service", 0, true},
		{"a disabled service principal", "service", 1, false},
		{"a person", "person", 0, false},
		{"a workload", "workload", 0, false},
		{"a kind of another version", "robot", 0, false},
	}

	for _, tc := range cases {
		p := trustrulestore.PrincipalRow{Kind: tc.kind, Namespace: "acme", DeletedAtMs: 0, Found: true}

		err := trustrulestore.CheckExchangePrincipal(p, tc.disabledAt, "acme")
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, trustrule.ErrPrincipal)) {
			t.Errorf("%s: CheckExchangePrincipal = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

// bundleCAPEM returns the PEM text of one new CA certificate that is valid
// at the time now.
func bundleCAPEM(t *testing.T, now time.Time) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}

	var tmpl x509.Certificate

	tmpl.SerialNumber = serial
	tmpl.Subject.CommonName = "test ca"
	tmpl.NotBefore = now.Add(-time.Hour)
	tmpl.NotAfter = now.Add(48 * time.Hour)
	tmpl.IsCA = true
	tmpl.BasicConstraintsValid = true
	tmpl.KeyUsage = x509.KeyUsageCertSign

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	var block pem.Block

	block.Type = "CERTIFICATE"
	block.Bytes = der

	return string(pem.EncodeToMemory(&block))
}

// TestBundleRefusesACorruptRow reads bundle rows that a backup, a restore or
// another version could have written. Each one gives ErrCorrupt.
func TestBundleRefusesACorruptRow(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_700_000_000_000).UTC()
	vs := []trustrulestore.VersionRow{{Version: 1, PEM: bundleCAPEM(t, now), CreatedAtMs: now.UnixMilli(), OverlapEndsAtMs: 0}}
	good := trustrulestore.BundleRow{
		ID: string(trustrule.NewBundleID()), Namespace: "acme", Active: 1, Revision: 1,
		CreatedAtMs: now.UnixMilli(),
	}

	if _, err := trustrulestore.Bundle(good, vs); err != nil {
		t.Fatalf("Bundle of the row the store wrote = %v, want nil", err)
	}

	cases := map[string]func(row *trustrulestore.BundleRow){
		"a namespace that is not a name": func(row *trustrulestore.BundleRow) { row.Namespace = "Not A Name" },
		"an ID of another kind":          func(row *trustrulestore.BundleRow) { row.ID = "trr-1" },
		"a revision below one":           func(row *trustrulestore.BundleRow) { row.Revision = 0 },
	}

	for name, change := range cases {
		row := good
		change(&row)

		if _, err := trustrulestore.Bundle(row, vs); !errors.Is(err, trustrulestore.ErrCorrupt) {
			t.Errorf("%s: Bundle = %v, want ErrCorrupt", name, err)
		}
	}
}

// TestCheckBundleOfNamespace sends the bundle of another namespace to the
// check of GetBundle, as if the query had selected the row of another
// namespace.
func TestCheckBundleOfNamespace(t *testing.T) {
	t.Parallel()

	b := trustrule.Bundle{ID: trustrule.NewBundleID(), Namespace: "other", Active: 0, Versions: nil, Revision: 0, CreatedAt: time.Time{}}

	if err := trustrulestore.CheckBundleOfNamespace(b, "acme"); !errors.Is(err, trustrule.ErrNotFound) {
		t.Errorf("check of the bundle of another namespace = %v, want ErrNotFound", err)
	}

	if err := trustrulestore.CheckBundleOfNamespace(b, "other"); err != nil {
		t.Errorf("check of the bundle of its own namespace = %v, want nil", err)
	}
}
