package policy_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/policy"
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

	allow := policy.Combined{Effect: policy.EffectAllow, Reasons: nil, Approvals: nil}
	none := policy.Combined{Effect: "", Reasons: nil, Approvals: nil}

	cases := []struct {
		name string
		in   []policy.Decision
		want policy.Combined
		err  error
	}{
		{name: "no decision allows", in: nil, want: allow, err: nil},
		{
			name: "two allows give allow",
			in:   []policy.Decision{effect(policy.EffectAllow, "a"), effect(policy.EffectAllow, "b")},
			want: allow,
			err:  nil,
		},
		{
			name: "one deny wins over approve and allow, with each deny reason",
			in: []policy.Decision{
				approve(2, "dba"), effect(policy.EffectDeny, "too large"),
				effect(policy.EffectAllow, "ok"), effect(policy.EffectDeny, "frozen region"),
			},
			want: policy.Combined{
				Effect: policy.EffectDeny, Reasons: []string{"too large", "frozen region"}, Approvals: nil,
			},
			err: nil,
		},
		{
			name: "two disjoint group sets stay two requirements",
			in:   []policy.Decision{approve(1, "security"), effect(policy.EffectAllow, "ok"), approve(1, "dba")},
			want: policy.Combined{
				Effect:  policy.EffectApprove,
				Reasons: []string{"approve security", "approve dba"},
				Approvals: []policy.Approvals{
					{Groups: []string{"dba"}, Count: 1},
					{Groups: []string{"security"}, Count: 1},
				},
			},
			err: nil,
		},
		{
			name: "overlapping group sets stay two requirements",
			in:   []policy.Decision{approve(1, "sre", "dba"), approve(2, "dba")},
			want: policy.Combined{
				Effect:  policy.EffectApprove,
				Reasons: []string{"approve sre,dba", "approve dba"},
				Approvals: []policy.Approvals{
					{Groups: []string{"dba"}, Count: 2},
					{Groups: []string{"dba", "sre"}, Count: 1},
				},
			},
			err: nil,
		},
		{
			name: "equal group sets merge with the highest count",
			in:   []policy.Decision{approve(1, "sre", "dba"), approve(3, "dba", "sre", "dba")},
			want: policy.Combined{
				Effect:    policy.EffectApprove,
				Reasons:   []string{"approve sre,dba", "approve dba,sre,dba"},
				Approvals: []policy.Approvals{{Groups: []string{"dba", "sre"}, Count: 3}},
			},
			err: nil,
		},
		{
			name: "approve with no approvals is an error, not an allow",
			in:   []policy.Decision{effect(policy.EffectAllow, "ok"), effect(policy.EffectApprove, "x")},
			want: none,
			err:  policy.ErrInvalidDecision,
		},
		{
			name: "approve with zero approvals is an error",
			in:   []policy.Decision{approve(0, "dba")},
			want: none,
			err:  policy.ErrInvalidDecision,
		},
		{
			name: "an empty effect is an error",
			in:   []policy.Decision{effect("", "x")},
			want: none,
			err:  policy.ErrInvalidDecision,
		},
		{
			name: "an unknown effect is an error, also next to a deny",
			in:   []policy.Decision{effect(policy.EffectDeny, "no"), effect("maybe", "x")},
			want: none,
			err:  policy.ErrInvalidDecision,
		},
		{
			name: "allow with approvals is an error",
			in: []policy.Decision{{
				Effect: policy.EffectAllow, Reasons: nil,
				Approvals: &policy.Approvals{Groups: nil, Count: 1}, Engine: "", PolicyVersion: "",
			}},
			want: none,
			err:  policy.ErrInvalidDecision,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := policy.Combine(tc.in)
			if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
				t.Fatalf("Combine() = %+v, %v, want error %v", got, err, tc.err)
			}

			if err == nil && !reflect.DeepEqual(got, tc.want) {
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

func TestAnswerErrorsHoldNoBody(t *testing.T) {
	t.Parallel()

	const marker = "s3cr3t-marker"

	bodies := []string{
		`{"result":{"effect":"` + marker + `"}}`,
		`{"result":{"effect":"allow","` + marker + `":1}}`,
		`{"result":{"effect":"allow","reasons":` + marker + `}}`,
		marker,
	}

	for _, b := range bodies {
		_, err := policy.ParseOPAAnswer([]byte(b), "")
		if !errors.Is(err, policy.ErrInvalidDecision) || strings.Contains(err.Error(), marker) {
			t.Fatalf("ParseOPAAnswer(%s) = %v, want ErrInvalidDecision with no part of the body", b, err)
		}
	}
}

func TestWebhookCallDocumentIsJSON(t *testing.T) {
	t.Parallel()

	call := policy.WebhookCall{
		Kind: policy.KindCatalogRequest, Document: json.RawMessage(`{"name":"orders-db"}`), SchemaVersion: 1,
	}

	got, err := json.Marshal(call)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}

	const want = `{"kind":"catalog.request","document":{"name":"orders-db"},"schemaVersion":1}`
	if string(got) != want {
		t.Fatalf("Marshal() = %s, want %s", got, want)
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

// TestVerifyFailsClosed sends hostile headers and secrets straight to Verify,
// as if the caller had checked nothing.
func TestVerifyFailsClosed(t *testing.T) {
	t.Parallel()

	secret := []byte(strings.Repeat("s", policy.MinSecretBytes))
	body := []byte(`{"kind":"job.start","schemaVersion":1,"document":{}}`)
	at := time.Unix(1_790_000_000, 0)

	good, err := policy.Sign(body, at, secret)
	if err != nil {
		t.Fatalf("Sign() = %v", err)
	}

	_, sig, _ := strings.Cut(good, ",")
	ts := "t=1790000000"

	// A signature that anyone can make with an empty key.
	forged := func(key []byte) string {
		m := hmac.New(sha256.New, key)
		m.Write([]byte("1790000000."))
		m.Write(body)

		return "t=1790000000,v1=" + hex.EncodeToString(m.Sum(nil))
	}

	cases := []struct {
		name   string
		header string
		secret []byte
		want   error
	}{
		{"an empty secret", forged(nil), nil, policy.ErrSecret},
		{"a short secret", forged([]byte("short")), []byte("short"), policy.ErrSecret},
		{
			"a header above the limit",
			good + "," + strings.Repeat("x=y,", policy.MaxSignatureHeaderBytes), secret, policy.ErrSignature,
		},
		{"t two times", ts + "," + ts + "," + sig, secret, policy.ErrSignature},
		{"t two times with another time", ts + ",t=1," + sig, secret, policy.ErrSignature},
		{"no v1", ts, secret, policy.ErrSignature},
		{"too many v1", ts + strings.Repeat(","+sig, policy.MaxSignatures+1), secret, policy.ErrSignature},
		{"a time with a plus sign", "t=+1790000000," + sig, secret, policy.ErrSignature},
		{"a time with a leading zero", "t=01790000000," + sig, secret, policy.ErrSignature},
		{"a time with a space", "t= 1790000000," + sig, secret, policy.ErrSignature},
		{"an empty header", "", secret, policy.ErrSignature},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := policy.Verify(tc.header, body, tc.secret, at)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Verify() = %v, want %v", err, tc.want)
			}

			if strings.Contains(err.Error(), "x=y") {
				t.Fatalf("Verify() = %q, the error quotes the header", err)
			}
		})
	}

	if err := policy.Verify(good, body, secret, at); err != nil {
		t.Fatalf("Verify(good) = %v, want nil", err)
	}
}

// TestDecisionGroups sends approver groups that a caller must not resolve.
func TestDecisionGroups(t *testing.T) {
	t.Parallel()

	tooMany := make([]string, policy.MaxApprovalGroups+1)
	for i := range tooMany {
		tooMany[i] = "g" + strings.Repeat("a", i%5)
	}

	cases := []struct {
		name   string
		groups []string
		ok     bool
	}{
		{"none", nil, true},
		{"two names", []string{"dba", "security"}, true},
		{"the most groups", tooMany[:policy.MaxApprovalGroups], true},
		{"above the limit", tooMany, false},
		{"an empty name", []string{""}, false},
		{"a name with a space", []string{"bad name"}, false},
		{"a name with a newline", []string{"dba\nx"}, false},
		{"a name with a slash", []string{"../dba"}, false},
		{"a long name", []string{strings.Repeat("a", 101)}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := approve(1, tc.groups...).Validate()
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, policy.ErrInvalidDecision)) {
				t.Fatalf("Validate() = %v, want ok %v", err, tc.ok)
			}

			if err != nil && strings.Contains(err.Error(), "bad name") {
				t.Fatalf("Validate() = %q, the error quotes a group", err)
			}

			_, cerr := policy.Combine([]policy.Decision{approve(1, tc.groups...)})
			if tc.ok != (cerr == nil) {
				t.Fatalf("Combine() = %v, want ok %v", cerr, tc.ok)
			}
		})
	}
}

func TestParseWebhookAnswerRefusesBadGroups(t *testing.T) {
	t.Parallel()

	body := []byte(`{"effect":"approve","approvals":{"groups":["bad name"],"count":1}}`)
	if _, err := policy.ParseWebhookAnswer(body); !errors.Is(err, policy.ErrInvalidDecision) {
		t.Fatalf("ParseWebhookAnswer() = %v, want ErrInvalidDecision", err)
	}
}

// TestVerifyErrorsHoldNoHeaderText puts a marker in each part of a header that
// Verify reads, at each place where it can refuse, and checks that Verify
// returns the sentinel of that place and that the error holds no marker.
func TestVerifyErrorsHoldNoHeaderText(t *testing.T) {
	t.Parallel()

	const marker = "MARKER-7f3a"

	secret := []byte(strings.Repeat("s", policy.MinSecretBytes))
	body := []byte("{}")
	at := time.Unix(1_790_000_000, 0)

	good, err := policy.Sign(body, at, secret)
	if err != nil {
		t.Fatalf("Sign() = %v", err)
	}

	_, sig, _ := strings.Cut(good, ",")
	ts := "t=1790000000"

	cases := []struct {
		name   string
		header string
		want   error
	}{
		{"a part with no '='", good + "," + marker, policy.ErrSignature},
		{"the marker as the only part", marker, policy.ErrSignature},
		{"a v1 that is not hex", ts + ",v1=" + marker, policy.ErrSignature},
		{"a t that is not a number", "t=" + marker + ",v1=00", policy.ErrSignature},
		{"a t with the marker inside", ts + marker + ",v1=00", policy.ErrSignature},
		{"a repeated t with a marker", good + ",t=" + marker, policy.ErrSignature},
		{
			"a fifth v1 that holds the marker",
			ts + strings.Repeat(","+sig, policy.MaxSignatures) + ",v1=" + marker, policy.ErrSignature,
		},
		{"no v1, with the marker as a key", ts + "," + marker + "=x", policy.ErrSignature},
		{
			"a header above the limit, the marker at its end",
			ts + strings.Repeat(",x=y", policy.MaxSignatureHeaderBytes/4) + "," + marker, policy.ErrSignature,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := policy.Verify(tc.header, body, secret, at)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Verify() = %v, want %v", err, tc.want)
			}

			if strings.Contains(err.Error(), marker) {
				t.Fatalf("Verify() = %q, the error holds the marker", err)
			}
		})
	}
}

// TestVerifyHeaderSizeLimit checks the size limit alone: the header is valid,
// with the right time and the right signature, and only the padding of unknown
// keys makes it too large.
func TestVerifyHeaderSizeLimit(t *testing.T) {
	t.Parallel()

	secret := []byte(strings.Repeat("s", policy.MinSecretBytes))
	body := []byte("{}")
	at := time.Unix(1_790_000_000, 0)

	good, err := policy.Sign(body, at, secret)
	if err != nil {
		t.Fatalf("Sign() = %v", err)
	}

	// The header at the limit passes, and one byte more is refused.
	atLimit := good + "," + strings.Repeat("k", policy.MaxSignatureHeaderBytes-len(good)-len(",")-len("=")) + "="
	if len(atLimit) != policy.MaxSignatureHeaderBytes {
		t.Fatalf("test header has %d bytes, want %d", len(atLimit), policy.MaxSignatureHeaderBytes)
	}

	if err := policy.Verify(atLimit, body, secret, at); err != nil {
		t.Fatalf("Verify(at the limit) = %v, want nil", err)
	}

	if err := policy.Verify(atLimit+"x", body, secret, at); !errors.Is(err, policy.ErrSignature) {
		t.Fatalf("Verify(one byte above the limit) = %v, want ErrSignature", err)
	}
}

func TestVerifyHugeTime(t *testing.T) {
	t.Parallel()

	secret := []byte(strings.Repeat("s", policy.MinSecretBytes))
	at := time.Unix(1_790_000_000, 0)

	cases := []struct {
		name   string
		header string
		want   error
	}{
		{"the largest time", "t=9223372036854775807,v1=00", policy.ErrSignatureTime},
		{"the smallest time", "t=-9223372036854775808,v1=00", policy.ErrSignatureTime},
		{"a time above 64 bits", "t=99999999999999999999,v1=00", policy.ErrSignature},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := policy.Verify(tc.header, []byte("{}"), secret, at); !errors.Is(err, tc.want) {
				t.Fatalf("Verify() = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestCombineGroupsStayBounded checks that the groups of a requirement never
// pass the limit of one decision, also for many decisions.
func TestCombineGroupsStayBounded(t *testing.T) {
	t.Parallel()

	ds := make([]policy.Decision, 0, 200)

	for i := range 200 {
		groups := make([]string, policy.MaxApprovalGroups)
		for j := range groups {
			groups[j] = "g" + strings.Repeat("a", (i+j)%7) + string(rune('a'+j%26))
		}

		ds = append(ds, approve(1, groups...))
	}

	got, err := policy.Combine(ds)
	if err != nil {
		t.Fatalf("Combine() = %v", err)
	}

	for _, a := range got.Approvals {
		if len(a.Groups) > policy.MaxApprovalGroups {
			t.Fatalf("a requirement has %d groups, the limit is %d", len(a.Groups), policy.MaxApprovalGroups)
		}
	}
}
