package policy_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/policy"
)

func approve(count int, groups ...string) policy.Decision {
	return policy.Decision{
		Effect:        policy.EffectApprove,
		Reasons:       []string{"approve " + strings.Join(groups, ",")},
		Approvals:     &policy.Approvals{Groups: groups, Count: count},
		Engine:        "",
		PolicyVersion: "",
	}
}

func effect(e policy.Effect, reason string) policy.Decision {
	return policy.Decision{Effect: e, Reasons: []string{reason}, Approvals: nil, Engine: "", PolicyVersion: ""}
}

func TestCombine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []policy.Decision
		want policy.Decision
	}{
		{
			name: "no decision allows",
			in:   nil,
			want: policy.Decision{
				Effect:        policy.EffectAllow,
				Reasons:       nil,
				Approvals:     nil,
				Engine:        "",
				PolicyVersion: "",
			},
		},
		{
			name: "two allows give allow",
			in:   []policy.Decision{effect(policy.EffectAllow, "a"), effect(policy.EffectAllow, "b")},
			want: policy.Decision{
				Effect:        policy.EffectAllow,
				Reasons:       nil,
				Approvals:     nil,
				Engine:        "",
				PolicyVersion: "",
			},
		},
		{
			name: "one deny wins over approve and allow, with each deny reason",
			in: []policy.Decision{
				approve(2, "dba"), effect(policy.EffectDeny, "too large"),
				effect(policy.EffectAllow, "ok"), effect(policy.EffectDeny, "frozen region"),
			},
			want: policy.Decision{
				Effect: policy.EffectDeny, Reasons: []string{"too large", "frozen region"},
				Approvals: nil, Engine: "", PolicyVersion: "",
			},
		},
		{
			name: "approve takes the highest count and every group once",
			in:   []policy.Decision{approve(1, "sre", "dba"), effect(policy.EffectAllow, "ok"), approve(2, "dba")},
			want: policy.Decision{
				Effect:    policy.EffectApprove,
				Reasons:   []string{"approve sre,dba", "approve dba"},
				Approvals: &policy.Approvals{Groups: []string{"dba", "sre"}, Count: 2},
				Engine:    "", PolicyVersion: "",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := policy.Combine(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Combine() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDecisionValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		d    policy.Decision
		ok   bool
	}{
		{"allow", effect(policy.EffectAllow, "ok"), true},
		{"deny", effect(policy.EffectDeny, "no"), true},
		{"approve with one approval", approve(1), true},
		{"approve with the most approvals", approve(policy.MaxApprovals), true},
		{"unknown effect", effect("maybe", "x"), false},
		{"approve with no approvals", effect(policy.EffectApprove, "x"), false},
		{"approve with zero approvals", approve(0), false},
		{"approve above the limit", approve(policy.MaxApprovals + 1), false},
		{
			"allow with approvals",
			policy.Decision{
				Effect: policy.EffectAllow, Reasons: nil,
				Approvals: &policy.Approvals{Groups: nil, Count: 1}, Engine: "", PolicyVersion: "",
			},
			false,
		},
		{"a reason above the limit", effect(policy.EffectDeny, strings.Repeat("x", policy.MaxReasonBytes+1)), false},
		{
			"too many reasons",
			policy.Decision{
				Effect: policy.EffectDeny, Reasons: make([]string, policy.MaxReasons+1),
				Approvals: nil, Engine: "", PolicyVersion: "",
			},
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.d.Validate()
			if tc.ok != (err == nil) {
				t.Fatalf("Validate() = %v, want ok %v", err, tc.ok)
			}

			if err != nil && !errors.Is(err, policy.ErrInvalidDecision) {
				t.Fatalf("Validate() = %v, want ErrInvalidDecision", err)
			}
		})
	}
}

func TestInputValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   policy.Input
		ok   bool
	}{
		{
			"catalog request",
			policy.Input{Kind: policy.KindCatalogRequest, SchemaVersion: 1, Document: []byte("{}")},
			true,
		},
		{"job start", policy.Input{Kind: policy.KindJobStart, SchemaVersion: 1, Document: []byte("{}")}, true},
		{"unknown kind", policy.Input{Kind: "unit.read", SchemaVersion: 1, Document: []byte("{}")}, false},
		{"version 0", policy.Input{Kind: policy.KindJobStart, SchemaVersion: 0, Document: []byte("{}")}, false},
		{"empty document", policy.Input{Kind: policy.KindJobStart, SchemaVersion: 1, Document: nil}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.in.Validate()
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, policy.ErrInvalidInput)) {
				t.Fatalf("Validate() = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

func TestSignVerify(t *testing.T) {
	t.Parallel()

	secret := []byte(strings.Repeat("s", policy.MinSecretBytes))
	older := []byte(strings.Repeat("o", policy.MinSecretBytes))
	other := []byte(strings.Repeat("x", policy.MinSecretBytes))
	body := []byte(`{"kind":"catalog.request","schemaVersion":1,"document":{}}`)
	at := time.Unix(1_790_000_000, 0)

	header, err := policy.Sign(body, at, secret, older)
	if err != nil {
		t.Fatalf("Sign() = %v", err)
	}

	cases := []struct {
		name   string
		header string
		body   []byte
		secret []byte
		now    time.Time
		want   error
	}{
		{"newest secret matches", header, body, secret, at, nil},
		{"older secret of a rotation matches", header, body, older, at.Add(time.Minute), nil},
		{"at the edge of the tolerance", header, body, secret, at.Add(policy.SignatureTolerance), nil},
		{"another secret", header, body, other, at, policy.ErrSignature},
		{"a changed body", header, append([]byte(" "), body...), secret, at, policy.ErrSignature},
		{"too old", header, body, secret, at.Add(policy.SignatureTolerance + time.Second), policy.ErrSignatureTime},
		{"too new", header, body, secret, at.Add(-policy.SignatureTolerance - time.Second), policy.ErrSignatureTime},
		{"no time", "v1=00", body, secret, at, policy.ErrSignature},
		{"no equal sign", "t1790000000", body, secret, at, policy.ErrSignature},
		{"not hex", "t=1790000000,v1=zz", body, secret, at, policy.ErrSignature},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := policy.Verify(tc.header, tc.body, tc.secret, tc.now); !errors.Is(got, tc.want) ||
				(tc.want == nil && got != nil) {
				t.Fatalf("Verify() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSignRefusesShortSecrets(t *testing.T) {
	t.Parallel()

	for _, secrets := range [][][]byte{nil, {[]byte("short")}} {
		if _, err := policy.Sign([]byte("{}"), time.Unix(0, 0), secrets...); !errors.Is(err, policy.ErrSecret) {
			t.Fatalf("Sign(%d secrets) = %v, want ErrSecret", len(secrets), err)
		}
	}
}

func TestSignFormat(t *testing.T) {
	t.Parallel()

	secret := []byte(strings.Repeat("k", policy.MinSecretBytes))

	header, err := policy.Sign([]byte("{}"), time.Unix(1700000000, 0), secret)
	if err != nil {
		t.Fatalf("Sign() = %v", err)
	}

	// The HMAC-SHA256 of "1700000000.{}" with 32 bytes "k", as a receiver
	// in another language computes it.
	const want = "t=1700000000,v1=0b3b35525c10b3757ffa78df69037021fc3168d8390a51a4fcb4a8d56a25b557"
	if header != want {
		t.Fatalf("Sign() = %q, want %q", header, want)
	}
}

func TestParseOPAAnswer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want policy.Effect
		err  error
	}{
		{"allow", `{"result":{"effect":"allow"}}`, policy.EffectAllow, nil},
		{"deny with reasons", `{"result":{"effect":"deny","reasons":["no large in prod"]}}`, policy.EffectDeny, nil},
		{
			"approve",
			`{"result":{"effect":"approve","approvals":{"count":2,"groups":["dba"]}}}`,
			policy.EffectApprove,
			nil,
		},
		{"undefined result is not a deny", `{}`, "", policy.ErrInvalidDecision},
		{"unknown field", `{"result":{"effect":"allow","extra":1}}`, "", policy.ErrInvalidDecision},
		{"approve with no approvals", `{"result":{"effect":"approve"}}`, "", policy.ErrInvalidDecision},
		{"not JSON", `allow`, "", policy.ErrInvalidDecision},
		{"two documents", `{"result":{"effect":"allow"}} {}`, "", policy.ErrInvalidDecision},
		{
			"too large",
			`{"result":{"effect":"allow"}}` + strings.Repeat(" ", policy.MaxAnswerBytes),
			"",
			policy.ErrInvalidDecision,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d, err := policy.ParseOPAAnswer([]byte(tc.body), "bundle-7")
			if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
				t.Fatalf("ParseOPAAnswer() = %v, want %v", err, tc.err)
			}

			if err == nil && (d.Effect != tc.want || d.Engine != "opa" || d.PolicyVersion != "bundle-7") {
				t.Fatalf("ParseOPAAnswer() = %+v, want effect %q from opa at bundle-7", d, tc.want)
			}
		})
	}
}

func TestParseWebhookAnswer(t *testing.T) {
	t.Parallel()

	d, err := policy.ParseWebhookAnswer([]byte(`{"effect":"deny","reasons":["quota"],"policyVersion":"v4"}`))
	if err != nil || d.Effect != policy.EffectDeny || d.Engine != "webhook" || d.PolicyVersion != "v4" {
		t.Fatalf("ParseWebhookAnswer() = %+v, %v", d, err)
	}

	if _, err := policy.ParseWebhookAnswer([]byte(`{"effect":"approve"}`)); !errors.Is(err, policy.ErrInvalidDecision) {
		t.Fatalf("ParseWebhookAnswer(approve with no approvals) = %v, want ErrInvalidDecision", err)
	}
}

func TestCedar(t *testing.T) {
	t.Parallel()

	resource := policy.CedarEntity{Type: policy.CedarBlueprint, ID: "payments/postgres"}
	reqs := policy.CedarRequests("alice", []string{"dev"}, policy.KindCatalogRequest, resource, []byte("{}"))

	if reqs[0].Action.ID != "catalog.request" || reqs[1].Action.ID != "catalog.request"+policy.CedarWithoutApproval {
		t.Fatalf("actions = %q, %q", reqs[0].Action.ID, reqs[1].Action.ID)
	}

	if reqs[0].Principal != (policy.CedarEntity{Type: policy.CedarPrincipal, ID: "alice"}) ||
		len(reqs[1].Parents) != 1 || reqs[1].Parents[0].Type != policy.CedarGroup {
		t.Fatalf("principal and parents = %+v, %+v", reqs[0].Principal, reqs[1].Parents)
	}

	approvals := policy.Approvals{Groups: []string{"dba"}, Count: 1}
	cases := []struct {
		first, second bool
		want          policy.Effect
	}{
		{first: false, second: false, want: policy.EffectDeny},
		{first: false, second: true, want: policy.EffectDeny},
		{first: true, second: false, want: policy.EffectApprove},
		{first: true, second: true, want: policy.EffectAllow},
	}

	for _, tc := range cases {
		d := policy.CedarDecision(tc.first, tc.second, approvals, []string{"policy0"})
		if d.Effect != tc.want {
			t.Fatalf("CedarDecision(%v, %v) = %q, want %q", tc.first, tc.second, d.Effect, tc.want)
		}

		if err := d.Validate(); err != nil {
			t.Fatalf("CedarDecision(%v, %v) is not valid: %v", tc.first, tc.second, err)
		}
	}
}
