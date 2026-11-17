package proxysessionsvc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/proxysessionsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/secret"
)

func TestNewRefusesMissingDependency(t *testing.T) {
	t.Parallel()

	_, err := proxysessionsvc.New(proxysessionsvc.Config{
		Settings: nil, Rules: nil, Secrets: nil, Namespaces: nil, Operations: nil, Claims: nil, Jobs: nil,
		Units: nil, Audit: nil, Logger: nil, Now: nil,
	})
	if !errors.Is(err, proxysessionsvc.ErrMissing) {
		t.Fatalf("New with no dependency = %v, want ErrMissing", err)
	}
}

// TestAssignmentHoldsRulesOfClaimOnly checks that the assignment holds the
// rules of the namespace and of the unit of the claim of the session, and no
// rule of another unit or another namespace.
func TestAssignmentHoldsRulesOfClaimOnly(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.rules.set(rulestore.Scope{Namespace: nsAcme, UnitID: ""}, rulesDoc("ns-rule", "api.example.com", "/v1/**"))
	f.rules.set(rulestore.Scope{Namespace: nsAcme, UnitID: unitWeb},
		rulesDoc("unit-rule", "registry.example.com", "/{{ unit.labels.team }}/**"))
	f.rules.set(rulestore.Scope{Namespace: nsAcme, UnitID: "u-sibling"}, rulesDoc("sibling", "s.example.com", "/x"))
	f.rules.set(rulestore.Scope{Namespace: nsOther, UnitID: ""}, rulesDoc("other-ns", "o.example.com", "/x"))
	mustPut(t, f, putRequest(pool(poolA, nsAcme)))

	got, err := f.svc.Assignment(t.Context(), assignmentRequest(runner.SessionKindProxy, pool(poolA, nsAcme)))
	if err != nil {
		t.Fatalf("Assignment: %v", err)
	}

	checkAssignmentRules(t, &got)
	checkAssignmentFields(t, &got)

	entries := f.audit.all()
	if len(entries) != 1 || !hasChange(entries[0], "event", string(runner.EventProxySessionOpened)) ||
		!hasChange(entries[0], "rulesDigest", got.RulesDigest) || entries[0].JobID != jobWeb {
		t.Errorf("audit entries = %+v, want one proxy-session-opened entry with the digest and the job", entries)
	}
}

// checkAssignmentRules checks that got holds the namespace rule, then the
// unit rule with the label of the unit, and no other rule.
func checkAssignmentRules(t *testing.T, got *proxysessionsvc.Assignment) {
	t.Helper()

	ids := make([]string, 0, len(got.Rules))
	for i := range got.Rules {
		ids = append(ids, got.Rules[i].Scope+"/"+got.Rules[i].ID)
	}

	if want := []string{"namespace/ns-rule", "unit/unit-rule"}; !slices.Equal(ids, want) {
		t.Fatalf("assignment rules = %v, want %v", ids, want)
	}

	if !slices.Equal(got.Rules[1].Paths, []string{"/core/**"}) {
		t.Errorf("rendered unit rule paths = %v, want the label of the unit", got.Rules[1].Paths)
	}
}

// checkAssignmentFields checks the fields of got other than the rules.
func checkAssignmentFields(t *testing.T, got *proxysessionsvc.Assignment) {
	t.Helper()

	if !strings.HasPrefix(got.RulesDigest, "sha256:") || got.Namespace != nsAcme ||
		got.ServerHost != "dustpan.example" {
		t.Errorf("assignment = %+v, want the digest, the namespace and the server host", got)
	}

	if !slices.Equal(got.AllowedRanges, []string{"10.20.0.0/16"}) || got.UpstreamURL != upstreamURL {
		t.Errorf("assignment ranges, upstream = %v, %q, want the settings of the pool",
			got.AllowedRanges, got.UpstreamURL)
	}
}

// TestAssignmentRulesNotRenderable checks that a rule with a template that has
// no value before the job starts fails closed with the fixed reason.
func TestAssignmentRulesNotRenderable(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/x/{{ inputs.folder }}/**", "/x/{{ profile.grafana.org }}/**"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.rules.set(rulestore.Scope{Namespace: nsAcme, UnitID: unitWeb}, rulesDoc("in", "api.example.com", path))

			_, err := f.svc.Assignment(t.Context(), assignmentRequest(runner.SessionKindProxy, pool(poolA, nsAcme)))
			if !errors.Is(err, proxysessionsvc.ErrRulesNotRenderable) {
				t.Fatalf("Assignment with %s = %v, want ErrRulesNotRenderable", path, err)
			}

			if strings.Contains(err.Error(), "inputs.") || strings.Contains(err.Error(), "profile.") {
				t.Errorf("error = %q, want the fixed reason only", err)
			}

			entries := f.audit.all()
			if len(entries) != 1 || entries[0].Outcome != audit.OutcomeDenied ||
				entries[0].Reason != proxysessionsvc.ReasonRulesNotRenderable {
				t.Errorf("audit entries = %+v, want one denied entry with rules-not-renderable", entries)
			}
		})
	}
}

// TestCredentialForLiveProxySession checks the credential path of a live proxy
// session, and its reveal entry.
func TestCredentialForLiveProxySession(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	mustPut(t, f, putRequest(pool(poolA, nsAcme)))

	got, err := f.svc.Credential(t.Context(), sessionRequest(runner.SessionKindProxy, pool(poolA, nsAcme)))
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}

	if got.Username != upstreamUser || got.Password != upstreamPassword {
		t.Errorf("Credential = %q, want the saved user name and password", got.Username)
	}

	entries := f.audit.all()
	if len(entries) != 1 || entries[0].Kind != audit.KindReveal ||
		!hasChange(entries[0], "event", string(runner.EventProxyCredentialIssued)) ||
		!hasChange(entries[0], "pool", poolA) || !hasChange(entries[0], "session", "rss-1") {
		t.Errorf("audit entries = %+v, want one reveal entry with the pool and the session", entries)
	}
}

// TestCredentialFailsWhenRevealIsNotRecorded checks that the credential is not
// given when its reveal entry cannot be written.
func TestCredentialFailsWhenRevealIsNotRecorded(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	mustPut(t, f, putRequest(pool(poolA, nsAcme)))
	f.audit.setFail(true)

	got, err := f.svc.Credential(t.Context(), sessionRequest(runner.SessionKindProxy, pool(poolA, nsAcme)))
	if !errors.Is(err, errAuditAppend) || got.Password != "" {
		t.Fatalf("Credential with a lost entry = %q, %v, want no credential and the append error", got.Password, err)
	}
}

// TestSealScopeIsPool checks that the credential is sealed under the scope of
// the namespace and the stable pool ID: the sealed bytes of pool A do not
// unseal under pool B, as the service or as the secret service.
func TestSealScopeIsPool(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	mustPut(t, f, putRequest(pool(poolA, nsAcme)))

	rec, err := f.settings.ProxySettings(t.Context(), poolA)
	if err != nil {
		t.Fatalf("read settings of pool A: %v", err)
	}

	if strings.Contains(string(rec.Credential), upstreamPassword) {
		t.Fatal("the stored credential holds the password in clear text")
	}

	scopeA, err := secret.NewScope(nsAcme, poolA)
	if err != nil {
		t.Fatalf("scope A: %v", err)
	}

	if _, err = f.secrets.Decrypt(t.Context(), scopeA, [][]byte{rec.Credential}); err != nil {
		t.Errorf("Decrypt under (acme, %s) = %v, want the credential", poolA, err)
	}

	scopeB, err := secret.NewScope(nsAcme, poolB)
	if err != nil {
		t.Fatalf("scope B: %v", err)
	}

	if _, err = f.secrets.Decrypt(t.Context(), scopeB, [][]byte{rec.Credential}); err == nil {
		t.Errorf("Decrypt of the credential of pool A under (acme, %s) succeeded, want a failure", poolB)
	}

	// The row of pool A copied to pool B, as a hostile restore could do.
	f.settings.put(poolB, rec)

	_, err = f.svc.Credential(t.Context(), sessionRequest(runner.SessionKindProxy, pool(poolB, nsAcme)))
	if !errors.Is(err, proxysessionsvc.ErrUnseal) {
		t.Errorf("Credential of pool B with the sealed bytes of pool A = %v, want ErrUnseal", err)
	}
}

// TestCredentialRefusesAHostileStoredUser checks the layer that re-checks a
// stored credential after it unseals. The fake store holds a credential that
// is sealed under the pool scope, as a restore or another version could write
// it, with a user name that has a colon or a control character. The service
// must refuse it with one denied entry and no credential.
func TestCredentialRefusesAHostileStoredUser(t *testing.T) {
	t.Parallel()

	for name, user := range map[string]string{
		"colon":             "ops:admin",
		"control character": "ops\x07admin",
		"newline":           "ops\nadmin",
		"empty":             "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			sealHostileUser(t, f, user)

			before := len(f.audit.all())

			got, err := f.svc.Credential(t.Context(), sessionRequest(runner.SessionKindProxy, pool(poolA, nsAcme)))
			if !errors.Is(err, proxysessionsvc.ErrUnseal) || got.Password != "" {
				t.Fatalf("Credential = %q, %v, want no credential and ErrUnseal", got.Password, err)
			}

			entries := f.audit.all()[before:]
			if len(entries) != 1 || entries[0].Outcome != audit.OutcomeDenied ||
				entries[0].Reason != proxysessionsvc.ReasonUnsealFailed {
				t.Errorf("audit entries = %+v, want one denied entry with the reason %s",
					entries, proxysessionsvc.ReasonUnsealFailed)
			}
		})
	}
}

// sealHostileUser replaces the stored credential of pool A with one that is
// sealed under the pool scope and holds the user name user.
func sealHostileUser(t *testing.T, f fixture, user string) {
	t.Helper()

	mustPut(t, f, putRequest(pool(poolA, nsAcme)))

	rec, err := f.settings.ProxySettings(t.Context(), poolA)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	scope, err := secret.NewScope(nsAcme, poolA)
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	plain, err := json.Marshal(map[string]string{"username": user, "password": upstreamPassword})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	sealed, err := f.secrets.Encrypt(t.Context(), scope, [][]byte{plain})
	if err != nil || len(sealed) != 1 {
		t.Fatalf("seal: %v", err)
	}

	rec.Credential = sealed[0]
	f.settings.put(poolA, rec)
}

// TestCheckSealedSizeRefusesAnOversizeForm calls the size check straight, as
// if the body limit and the check of the credential did not run.
func TestCheckSealedSizeRefusesAnOversizeForm(t *testing.T) {
	t.Parallel()

	const limit = 8 << 10

	for name, tc := range map[string]struct {
		size int
		ok   bool
	}{
		"empty":         {size: 0, ok: true},
		"at the limit":  {size: limit, ok: true},
		"over by one":   {size: limit + 1, ok: false},
		"far too large": {size: 1 << 20, ok: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := proxysessionsvc.CheckSealedSize(make([]byte, tc.size))

			switch {
			case tc.ok && err != nil:
				t.Errorf("size %d: err = %v, want nil", tc.size, err)
			case !tc.ok && !errors.Is(err, runner.ErrInvalid):
				t.Errorf("size %d: err = %v, want ErrInvalid", tc.size, err)
			default:
			}
		})
	}
}

// sweepFixture is a fixture whose claim has no job, as the claim of a sweep.
func sweepFixture(t *testing.T) fixture {
	t.Helper()

	return newFixtureWith(t, "", job.OperationPlan)
}

// unknownOperationFixture is a fixture whose job has an operation that the
// job proxy does not know.
func unknownOperationFixture(t *testing.T) fixture {
	t.Helper()

	return newFixtureWith(t, jobWeb, job.Operation("unknown"))
}

// TestServiceHostile is the table of hostile cases of the service layer. Each
// case calls the service straight, as if the route wrapper did not check.
func TestServiceHostile(t *testing.T) {
	t.Parallel()

	type call func(ctx context.Context, f fixture) error

	assignment := func(kind runner.SessionKind, ns namespace.Name) call {
		return func(ctx context.Context, f fixture) error {
			_, err := f.svc.Assignment(ctx, assignmentRequest(kind, pool(poolA, ns)))

			return err
		}
	}

	credential := func(kind runner.SessionKind, ns namespace.Name) call {
		return func(ctx context.Context, f fixture) error {
			_, err := f.svc.Credential(ctx, sessionRequest(kind, pool(poolA, ns)))

			return err
		}
	}

	ended := func(f fixture) {
		op := liveOp()
		op.State = durable.StateSucceeded
		f.ops.set(op)
	}
	takenOver := func(f fixture) {
		op := liveOp()
		op.Claim.Token = claimToken + 1
		f.ops.set(op)
	}
	otherRunner := func(f fixture) {
		op := liveOp()
		op.Claim.Owner = "rnr-b"
		f.ops.set(op)
	}
	none := func(fixture) {}

	cases := []struct {
		name    string
		prepare func(fixture)
		call    call
		want    error
		reason  string
		fixture func(*testing.T) fixture
	}{
		{
			name: "assignment with a launcher session", prepare: none,
			call: assignment(runner.SessionKindLauncher, nsAcme),
			want: proxysessionsvc.ErrWrongSessionKind, reason: proxysessionsvc.ReasonWrongSessionKind,
			fixture: newFixture,
		},
		{
			name: "assignment with a session of no kind", prepare: none, call: assignment("", nsAcme),
			want: proxysessionsvc.ErrWrongSessionKind, reason: proxysessionsvc.ReasonWrongSessionKind,
			fixture: newFixture,
		},
		{
			name: "credential with a launcher session", prepare: none,
			call: credential(runner.SessionKindLauncher, nsAcme),
			want: proxysessionsvc.ErrWrongSessionKind, reason: proxysessionsvc.ReasonWrongSessionKind,
			fixture: newFixture,
		},
		{
			name: "assignment after the claim ended", prepare: ended, call: assignment(runner.SessionKindProxy, nsAcme),
			want: runner.ErrClaimLost, reason: proxysessionsvc.ReasonClaimLost,
			fixture: newFixture,
		},
		{
			name: "credential after the claim ended", prepare: ended, call: credential(runner.SessionKindProxy, nsAcme),
			want: runner.ErrClaimLost, reason: proxysessionsvc.ReasonClaimLost,
			fixture: newFixture,
		},
		{
			name: "credential after a takeover", prepare: takenOver, call: credential(runner.SessionKindProxy, nsAcme),
			want: runner.ErrClaimLost, reason: proxysessionsvc.ReasonClaimLost,
			fixture: newFixture,
		},
		{
			name: "assignment of a claim of another runner", prepare: otherRunner,
			call: assignment(runner.SessionKindProxy, nsAcme),
			want: runner.ErrClaimLost, reason: proxysessionsvc.ReasonClaimLost,
			fixture: newFixture,
		},
		{
			name: "assignment with a pool of another namespace", prepare: none,
			call: assignment(runner.SessionKindProxy, nsOther), want: runner.ErrNotFound,
			reason:  proxysessionsvc.ReasonNotJobOfPool,
			fixture: newFixture,
		},
		{
			name: "credential with a pool of another namespace", prepare: none,
			call: credential(runner.SessionKindProxy, nsOther), want: runner.ErrNotFound,
			reason:  proxysessionsvc.ReasonNotJobOfPool,
			fixture: newFixture,
		},
		{
			name:    "assignment of the claim of a sweep",
			prepare: none,
			call:    assignment(runner.SessionKindProxy, nsAcme),
			want:    runner.ErrInvalid,
			reason:  proxysessionsvc.ReasonNoProxyOperation,
			fixture: sweepFixture,
		},
		{
			name:    "credential of the claim of a sweep",
			prepare: none,
			call:    credential(runner.SessionKindProxy, nsAcme),
			want:    runner.ErrInvalid,
			reason:  proxysessionsvc.ReasonNoProxyOperation,
			fixture: sweepFixture,
		},
		{
			name: "assignment of a job with an unknown operation", prepare: none,
			call: assignment(runner.SessionKindProxy, nsAcme),
			want: runner.ErrInvalid, reason: proxysessionsvc.ReasonNoProxyOperation, fixture: unknownOperationFixture,
		},
		{
			name: "credential of a job with an unknown operation", prepare: none,
			call: credential(runner.SessionKindProxy, nsAcme),
			want: runner.ErrInvalid, reason: proxysessionsvc.ReasonNoProxyOperation, fixture: unknownOperationFixture,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := tc.fixture(t)
			mustPut(t, f, putRequest(pool(poolA, nsAcme)))
			tc.prepare(f)

			err := tc.call(t.Context(), f)
			if !errors.Is(err, tc.want) {
				t.Fatalf("call = %v, want %v", err, tc.want)
			}

			if tc.reason == "" {
				return
			}

			entries := f.audit.all()
			if len(entries) != 1 || entries[0].Outcome != audit.OutcomeDenied || entries[0].Reason != tc.reason {
				t.Errorf("audit entries = %+v, want one denied entry with the reason %s", entries, tc.reason)
			}
		})
	}
}

// TestCredentialNoUpstream checks the credential path of a pool with no
// upstream proxy and of a pool with an upstream proxy and no credential.
func TestCredentialNoUpstream(t *testing.T) {
	t.Parallel()

	noCredential := putRequest(pool(poolA, nsAcme))
	noCredential.Credential = proxysessionsvc.CredentialChange{Present: false, Value: nil}

	for name, req := range map[string]*proxysessionsvc.PutRequest{"no settings": nil, "no credential": noCredential} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			if req != nil {
				mustPut(t, f, req)
			}

			_, err := f.svc.Credential(t.Context(), sessionRequest(runner.SessionKindProxy, pool(poolA, nsAcme)))
			if !errors.Is(err, proxysessionsvc.ErrNoUpstream) {
				t.Fatalf("Credential = %v, want ErrNoUpstream", err)
			}

			entries := f.audit.all()
			if len(entries) != 1 || entries[0].Reason != proxysessionsvc.ReasonNoUpstream {
				t.Errorf("audit entries = %+v, want one denied entry with no-upstream", entries)
			}
		})
	}
}

// TestNoSecretInTexts makes each path of the service fail and succeed, and
// scans every error and every audit entry for the upstream URL and the
// credential.
func TestNoSecretInTexts(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	mustPut(t, f, putRequest(pool(poolA, nsAcme)))

	var texts []string

	add := func(err error) {
		if err != nil {
			texts = append(texts, err.Error())
		}
	}

	_, err := f.svc.Credential(t.Context(), sessionRequest(runner.SessionKindProxy, pool(poolA, nsAcme)))
	add(err)
	_, err = f.svc.Assignment(t.Context(), assignmentRequest(runner.SessionKindLauncher, pool(poolA, nsAcme)))
	add(err)

	// A save with a bad range, a bad URL that holds the password, and a stale
	// version.
	bad := putRequest(pool(poolA, nsAcme))
	bad.AllowedRanges = []string{"127.0.0.0/8"}
	_, err = f.svc.PutSettings(t.Context(), bad)
	add(err)

	bad = putRequest(pool(poolA, nsAcme))
	bad.UpstreamURL = "https://" + upstreamUser + ":" + upstreamPassword + "@proxy.example.com"
	_, err = f.svc.PutSettings(t.Context(), bad)
	add(err)

	bad = putRequest(pool(poolA, nsAcme))
	bad.IfMatch = "9"
	_, err = f.svc.PutSettings(t.Context(), bad)
	add(err)

	// A credential that does not unseal.
	rec, err := f.settings.ProxySettings(t.Context(), poolA)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	rec.Credential = append(rec.Credential[:len(rec.Credential)-1], rec.Credential[len(rec.Credential)-1]^1)
	f.settings.put(poolA, rec)

	_, err = f.svc.Credential(t.Context(), sessionRequest(runner.SessionKindProxy, pool(poolA, nsAcme)))
	if !errors.Is(err, proxysessionsvc.ErrUnseal) {
		t.Errorf("Credential of a changed sealed value = %v, want ErrUnseal", err)
	}

	add(err)

	texts = append(texts, entryTexts(append(f.audit.all(), f.settings.auditEntries()...))...)

	for _, text := range texts {
		checkNoSecret(t, text)
	}
}

// entryTexts returns the texts of the entries: each entry and the old and new
// value of each change.
func entryTexts(entries []audit.Entry) []string {
	var texts []string

	for i := range entries {
		texts = append(texts, fmt.Sprintf("%+v", entries[i]))

		for _, c := range entries[i].Changes {
			texts = append(texts, c.New)
			if c.Old != nil {
				texts = append(texts, *c.Old)
			}
		}
	}

	return texts
}

// checkNoSecret fails the test when text holds the upstream URL, its host or a
// part of the credential.
func checkNoSecret(t *testing.T, text string) {
	t.Helper()

	for _, secretText := range []string{upstreamURL, "proxy.example.com", upstreamPassword, upstreamUser} {
		if strings.Contains(text, secretText) {
			t.Errorf("text %q holds %q, want no upstream URL and no credential", text, secretText)
		}
	}
}

// TestSettingsDefault checks the effective settings of a pool with no row.
func TestSettingsDefault(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	def, err := f.svc.Settings(t.Context(), pool(poolA, nsAcme))
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}

	if def.Source != proxysessionsvc.SourceDefault || def.Version != 0 || def.AllowedRanges == nil ||
		len(def.AllowedRanges) != 0 || def.UpstreamURL != "" || def.CredentialSet {
		t.Errorf("Settings of a pool with no row = %+v, want the default", def)
	}
}

// TestPutSettings checks the save of the settings: the version, the
// write-only credential and the audit entry.
func TestPutSettings(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := pool(poolA, nsAcme)

	saved := mustPut(t, f, putRequest(p))
	if saved.Source != proxysessionsvc.SourceSet || saved.Version != 1 || !saved.CredentialSet {
		t.Errorf("PutSettings = %+v, want source set, version 1 and a credential", saved)
	}

	// A save with no credential field keeps the stored credential.
	keep := putRequest(p)
	keep.Credential = proxysessionsvc.CredentialChange{Present: false, Value: nil}
	keep.IfMatch = "1"

	if got := mustPut(t, f, keep); !got.CredentialSet || got.Version != 2 {
		t.Errorf("PutSettings with no credential = %+v, want the credential kept, version 2", got)
	}

	// A save with a null credential removes it.
	remove := putRequest(p)
	remove.Credential = proxysessionsvc.CredentialChange{Present: true, Value: nil}

	if got := mustPut(t, f, remove); got.CredentialSet {
		t.Errorf("PutSettings with a null credential = %+v, want no credential", got)
	}

	entries := f.settings.auditEntries()
	if len(entries) != 3 || !hasChange(entries[0], "event", string(runner.EventPoolProxySettingsChanged)) ||
		!hasChange(entries[0], "credentialChanged", "true") || !hasChange(entries[1], "credentialChanged", "false") {
		t.Errorf("audit entries of the saves = %+v, want three pool-proxy-settings-changed entries", entries)
	}
}

// TestPutSettingsHostile is the table of hostile saves.
func TestPutSettingsHostile(t *testing.T) {
	t.Parallel()

	credential := func(user, pass string) func(*proxysessionsvc.PutRequest) {
		return func(r *proxysessionsvc.PutRequest) {
			r.Credential.Value = &proxysessionsvc.Credential{Username: user, Password: pass}
		}
	}

	lineFeed := "p\nX-Header: 1"
	long := strings.Repeat("p", proxysessionsvc.MaxPasswordBytes+1)

	cases := []struct {
		name   string
		change func(*proxysessionsvc.PutRequest)
		want   error
	}{
		{
			name: "loopback range", want: runner.ErrInvalid,
			change: func(r *proxysessionsvc.PutRequest) { r.AllowedRanges = []string{"127.0.0.0/8"} },
		},
		{
			name: "metadata range", want: runner.ErrInvalid,
			change: func(r *proxysessionsvc.PutRequest) { r.AllowedRanges = []string{"169.254.0.0/16"} },
		},
		{
			name: "ftp URL", want: runner.ErrInvalid,
			change: func(r *proxysessionsvc.PutRequest) { r.UpstreamURL = "ftp://proxy.example.com" },
		},
		{
			name: "credential with no URL", want: runner.ErrInvalid,
			change: func(r *proxysessionsvc.PutRequest) { r.UpstreamURL = "" },
		},
		{name: "empty user name", want: runner.ErrInvalid, change: credential("", "p")},
		{name: "user name with a colon", want: runner.ErrInvalid, change: credential("a:b", "p")},
		{name: "password with a line feed", want: runner.ErrInvalid, change: credential("a", lineFeed)},
		{name: "long password", want: runner.ErrInvalid, change: credential("a", long)},
		{
			name: "stale If-Match", want: runner.ErrConflict,
			change: func(r *proxysessionsvc.PutRequest) { r.IfMatch = "4" },
		},
		{
			name: "missing pool", want: runner.ErrNotFound,
			change: func(r *proxysessionsvc.PutRequest) { r.Pool = pool("rp-none", nsAcme) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			req := putRequest(pool(poolA, nsAcme))
			tc.change(req)

			if _, err := f.svc.PutSettings(t.Context(), req); !errors.Is(err, tc.want) {
				t.Fatalf("PutSettings = %v, want %v", err, tc.want)
			}

			if n := len(f.settings.auditEntries()); n != 0 {
				t.Errorf("saves after a refused save = %d, want 0", n)
			}
		})
	}
}

// hasChange reports whether e holds the change key with the new value.
func hasChange(e audit.Entry, key, value string) bool {
	return slices.ContainsFunc(e.Changes, func(c audit.Change) bool { return c.Key == key && c.New == value })
}
