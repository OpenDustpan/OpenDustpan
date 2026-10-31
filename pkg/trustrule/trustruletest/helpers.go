package trustruletest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// Values that the cases use.
const (
	// testTimeMs is the creation time of the rules of the cases, in Unix
	// milliseconds.
	testTimeMs = 1_700_000_000_000
	// nsAcme and nsOther are the namespaces of the cases.
	nsAcme  = "acme"
	nsOther = "other"
	// testIssuer is the issuer of a rule with the key source discovery.
	testIssuer = "https://token.actions.example.com"
	// testAudience is the audience of each rule.
	testAudience = "https://dustpan.example.com"
	// testSubject is the value of the condition on the subject.
	testSubject = "repo:acme/infra:ref:refs/heads/main"
	// subjectAlice is the subject of the audit entries.
	subjectAlice = "alice"
)

// newRule returns a valid rule of the namespace ns called name, for the
// principal principalID, with the key source discovery.
func newRule(ns namespace.Name, name, principalID string) trustrule.Rule {
	return trustrule.Rule{
		CreatedAt:   time.UnixMilli(testTimeMs).UTC(),
		ID:          trustrule.NewID(),
		Namespace:   ns,
		Name:        name,
		Issuer:      testIssuer,
		Audience:    testAudience,
		Keys:        trustrule.KeySourceDiscovery,
		StaticKeys:  "",
		PrincipalID: principalID,
		CreatedBy:   subjectAlice,
		Conditions: []trustrule.Condition{
			{Claim: "repository", Values: []string{"acme/infra", "acme/apps"}},
			{Claim: "sub", Values: []string{testSubject}},
		},
	}
}

// newStaticRule returns a valid rule of the namespace ns called name, with
// the key source static and one Ed25519 key.
func newStaticRule(t *testing.T, ns namespace.Name, name, principalID string) trustrule.Rule {
	t.Helper()

	r := newRule(ns, name, principalID)
	r.Issuer = "ci-cluster-7"
	r.Keys = trustrule.KeySourceStatic
	r.StaticKeys = ed25519KeySet(t)

	return r
}

// ed25519KeySet returns a JWKS with one new Ed25519 public key.
func ed25519KeySet(t *testing.T) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","use":"sig","kid":"k1","x":%q}]}`,
		base64.RawURLEncoding.EncodeToString(pub))
}

// workload returns the ID of a new service principal of the namespace ns.
func workload(t *testing.T, h Harness, ns namespace.Name) string {
	t.Helper()

	return h.Principal(t, ns, principal.KindService)
}

// mustCreate stores r with the current fence of its namespace.
func mustCreate(t *testing.T, h Harness, r trustrule.Rule) {
	t.Helper()

	fence := h.Namespace(t, r.Namespace.String())

	if err := h.Store.Create(t.Context(), r, fence, auditEntry(r.Namespace, "create", r.ID)); err != nil {
		t.Fatalf("create rule %q: %v", r.Name, err)
	}
}

// auditEntry returns a valid audit entry for a write of the rule id in ns.
func auditEntry(ns namespace.Name, action string, id trustrule.ID) audit.Entry {
	return audit.Entry{
		At:            time.UnixMilli(1),
		Namespace:     ns,
		Subject:       subjectAlice,
		IDPSubject:    "",
		TokenID:       "",
		Action:        "trust-rule:" + action,
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
		ObjectID:      string(id),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}
}

// staleFence returns a fence of the namespace of f that is not the current
// one.
func staleFence(f namespace.Fence) namespace.Fence {
	return namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}
}

// auditCount returns the number of audit entries of trust rule changes of
// the namespace ns.
func auditCount(t *testing.T, h Harness, ns string) int {
	t.Helper()

	return len(ruleEntries(t, h, ns))
}

// ruleEntries returns the audit entries of trust rule changes of the
// namespace ns, oldest first. The entries of the principals that the cases
// make are left out.
func ruleEntries(t *testing.T, h Harness, ns string) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	entries := h.Audit(t, ns)

	for i := range entries {
		if strings.HasPrefix(entries[i].Action, "trust-rule:") {
			out = append(out, entries[i])
		}
	}

	return out
}

// mustGet returns the rule id of the namespace acme, or fails the test.
func mustGet(t *testing.T, h Harness, id trustrule.ID) trustrule.Rule {
	t.Helper()

	got, err := h.Store.Get(t.Context(), nsAcme, id)
	if err != nil {
		t.Fatalf("get rule %s: %v", id, err)
	}

	return got
}

// mustBeGone fails the test when the rule id of ns exists.
func mustBeGone(t *testing.T, h Harness, ns namespace.Name, id trustrule.ID) {
	t.Helper()

	if _, err := h.Store.Get(t.Context(), ns, id); !errors.Is(err, trustrule.ErrNotFound) {
		t.Fatalf("get rule %s = %v, want ErrNotFound", id, err)
	}
}

// sameRule fails the test when got is not want.
func sameRule(t *testing.T, got, want trustrule.Rule) {
	t.Helper()

	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("created at = %v, want %v", got.CreatedAt, want.CreatedAt)
	}

	got.CreatedAt, want.CreatedAt = time.Time{}, time.Time{}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("rule = %+v\nwant %+v", got, want)
	}
}
