package jobtoken_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// redactedText is what a formatted secret shows.
const redactedText = "[job token]"

// testUnit returns the unit called name in the namespace ns, with the stable
// ID id.
func testUnit(t *testing.T, ns namespace.Name, name, id string) identity.Object {
	t.Helper()

	unit, err := identity.UnitObject("pulumi", ns, name)
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	return unit.WithUnitID(id)
}

// testNamespace returns the namespace called name.
func testNamespace(t *testing.T, name string) namespace.Name {
	t.Helper()

	ns, err := namespace.ParseName(name)
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	return ns
}

// validToken returns a token that passes Validate.
func validToken(t *testing.T) jobtoken.Token {
	t.Helper()

	ns := testNamespace(t, "acme")
	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	created := time.UnixMilli(1_700_000_000_000).UTC()

	var tok jobtoken.Token

	tok.Digest = jobtoken.DigestOf(secret)
	tok.StarterID = "prn-alice"
	tok.JobID = "job-1"
	tok.Namespace = ns
	tok.Unit = testUnit(t, ns, "web/prod", "unt-1")
	tok.ClaimOperation = "op-1"
	tok.ClaimToken = 1
	tok.ExpiresAt = created.Add(time.Hour)
	tok.CreatedAt = created

	return tok
}

func TestSecret(t *testing.T) {
	t.Parallel()

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	for _, tc := range []struct {
		name  string
		check func(*testing.T)
	}{
		{name: "a new secret has the secret form", check: func(t *testing.T) {
			t.Helper()
			checkSecretForm(t, secret.Reveal())
		}},
		{name: "a secret redacts its value", check: func(t *testing.T) {
			t.Helper()
			checkSecretRedaction(t, secret)
		}},
		{name: "a value outside the secret form is refused", check: func(t *testing.T) {
			t.Helper()
			checkBadSecrets(t, secret.Reveal())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t)
		})
	}
}

// checkSecretForm proves the form of a new secret and its parse.
func checkSecretForm(t *testing.T, raw string) {
	t.Helper()

	switch {
	case !strings.HasPrefix(raw, jobtoken.SecretPrefix):
		t.Fatalf("secret = %q, want the prefix %q", raw, jobtoken.SecretPrefix)
	case len(raw) != jobtoken.SecretLength:
		t.Fatalf("secret length = %d, want %d", len(raw), jobtoken.SecretLength)
	case !jobtoken.LooksLikeSecret(raw):
		t.Fatal("a new secret does not look like a secret")
	default:
	}

	parsed, err := jobtoken.ParseSecret(raw)
	if err != nil || parsed.Reveal() != raw {
		t.Fatalf("ParseSecret = %q, %v, want the same secret", parsed, err)
	}
}

// checkSecretRedaction proves that a secret never prints its value.
func checkSecretRedaction(t *testing.T, secret jobtoken.Secret) {
	t.Helper()

	if secret.String() != redactedText || secret.GoString() != redactedText {
		t.Fatalf("formatted secret = %q, %q, want %q", secret.String(), secret.GoString(), redactedText)
	}

	if _, err := jobtoken.ParseSecret("not-a-secret"); !errors.Is(err, jobtoken.ErrInvalid) {
		t.Fatalf("ParseSecret of a bad value = %v, want ErrInvalid", err)
	}
}

// checkBadSecrets proves that a value outside the secret form is refused.
func checkBadSecrets(t *testing.T, raw string) {
	t.Helper()

	bad := []string{
		"",
		jobtoken.SecretPrefix,
		jobtoken.SecretPrefix + raw[len(jobtoken.SecretPrefix):len(raw)-1],
		raw + "a",
		"odp_" + raw[len(jobtoken.SecretPrefix):],
		jobtoken.SecretPrefix + strings.Repeat("!", jobtoken.SecretLength-len(jobtoken.SecretPrefix)),
	}

	for _, value := range bad {
		if jobtoken.LooksLikeSecret(value) {
			t.Fatalf("LooksLikeSecret(%q) = true, want false", value)
		}
	}
}

func TestDigest(t *testing.T) {
	t.Parallel()

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	d := jobtoken.DigestOf(secret)

	other := jobtoken.DigestOf(secret)
	other[0] ^= 1

	for _, tc := range []struct {
		name  string
		check func(*testing.T, jobtoken.Secret, jobtoken.Digest, jobtoken.Digest)
	}{
		{name: "the digest of a secret is not zero", check: checkDigestNotZero},
		{name: "ParseDigest reads String", check: checkDigestParse},
		{name: "two digests of one secret are equal", check: checkDigestEqual},
		{name: "Equal refuses another digest", check: checkDigestDiffers},
		{name: "the zero digest is zero", check: checkDigestZero},
		{name: "ParseDigest refuses a bad value", check: checkDigestBadParse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, secret, d, other)
		})
	}
}

func checkDigestNotZero(t *testing.T, _ jobtoken.Secret, d, _ jobtoken.Digest) {
	t.Helper()

	if d.IsZero() {
		t.Fatal("the digest of a secret is zero")
	}
}

func checkDigestParse(t *testing.T, _ jobtoken.Secret, d, _ jobtoken.Digest) {
	t.Helper()

	parsed, err := jobtoken.ParseDigest(d.String())
	if err != nil || parsed != d || !d.Equal(parsed) {
		t.Fatalf("ParseDigest = %v, %v, want the same digest", parsed, err)
	}
}

func checkDigestEqual(t *testing.T, secret jobtoken.Secret, d, _ jobtoken.Digest) {
	t.Helper()

	if other := jobtoken.DigestOf(secret); other != d || !d.Equal(other) {
		t.Fatal("two digests of one secret differ")
	}
}

func checkDigestDiffers(t *testing.T, _ jobtoken.Secret, d, other jobtoken.Digest) {
	t.Helper()

	if d.Equal(other) {
		t.Fatal("Equal reports two different digests as equal")
	}
}

func checkDigestZero(t *testing.T, _ jobtoken.Secret, _, _ jobtoken.Digest) {
	t.Helper()

	if !(jobtoken.Digest{}).IsZero() {
		t.Fatal("the zero digest is not zero")
	}
}

func checkDigestBadParse(t *testing.T, _ jobtoken.Secret, _, _ jobtoken.Digest) {
	t.Helper()

	if _, err := jobtoken.ParseDigest("zz"); !errors.Is(err, jobtoken.ErrInvalid) {
		t.Fatalf("ParseDigest of a bad value = %v, want ErrInvalid", err)
	}
}

func TestMaxLifetime(t *testing.T) {
	t.Parallel()

	steps := make([]job.Step, job.MaxSteps)
	for i := range steps {
		steps[i].Timeout = job.MaxStepTimeout
		steps[i].ChangesResources = true
	}

	if got, want := jobtoken.MaxLifetime, runner.LimitsFor(steps).Deadline; got != want {
		t.Fatalf("MaxLifetime = %v, want the deadline %v of the longest phase", got, want)
	}
}

func TestTokenValidate(t *testing.T) {
	t.Parallel()

	base := validToken(t)
	otherNS := testNamespace(t, "beta")
	otherUnit := testUnit(t, otherNS, "web/prod", "unt-2")

	cases := []struct {
		name   string
		mutate func(*jobtoken.Token)
	}{
		{name: "no digest", mutate: func(tok *jobtoken.Token) { tok.Digest = jobtoken.Digest{} }},
		{name: "no starter", mutate: func(tok *jobtoken.Token) { tok.StarterID = "" }},
		{name: "no job", mutate: func(tok *jobtoken.Token) { tok.JobID = "" }},
		{name: "no namespace", mutate: func(tok *jobtoken.Token) { tok.Namespace = "" }},
		{name: "no unit", mutate: func(tok *jobtoken.Token) { tok.Unit = identity.Object{} }},
		{name: "no unit ID", mutate: func(tok *jobtoken.Token) { tok.Unit = base.Unit.WithUnitID("") }},
		{name: "unit in another namespace", mutate: func(tok *jobtoken.Token) { tok.Unit = otherUnit }},
		{name: "no claim operation", mutate: func(tok *jobtoken.Token) { tok.ClaimOperation = "" }},
		{name: "no claim token", mutate: func(tok *jobtoken.Token) { tok.ClaimToken = 0 }},
		{name: "a negative claim token", mutate: func(tok *jobtoken.Token) { tok.ClaimToken = -1 }},
		{name: "no creation time", mutate: func(tok *jobtoken.Token) { tok.CreatedAt = time.Time{} }},
		{name: "expiry at the creation time", mutate: func(tok *jobtoken.Token) { tok.ExpiresAt = tok.CreatedAt }},
		{name: "expiry before the creation time", mutate: func(tok *jobtoken.Token) {
			tok.ExpiresAt = tok.CreatedAt.Add(-time.Hour)
		}},
		{name: "expiry after the maximum lifetime", mutate: func(tok *jobtoken.Token) {
			tok.ExpiresAt = tok.CreatedAt.Add(jobtoken.MaxLifetime + time.Millisecond)
		}},
	}

	if err := base.Validate(); err != nil {
		t.Fatalf("Validate of a valid token = %v", err)
	}

	atMax := base
	atMax.ExpiresAt = atMax.CreatedAt.Add(jobtoken.MaxLifetime)

	if err := atMax.Validate(); err != nil {
		t.Fatalf("Validate of a token at the maximum lifetime = %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tok := base
			tc.mutate(&tok)

			if err := tok.Validate(); !errors.Is(err, jobtoken.ErrInvalid) {
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			}
		})
	}
}
