//go:build e2e

package e2e_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// workloadAgent is the User-Agent of the exchange requests.
const workloadAgent = "opendustpan-e2e-workload"

// workloadAudience is the audience of the rules and the tokens of the
// scenario.
const workloadAudience = "https://dustpan.e2e.example.com"

// workloadSubject is the subject of a token of the main branch of the
// repository of the scenario.
const workloadSubject = "repo:acme/infra:ref:refs/heads/main"

// exchangePath is the exchange route of the e2e namespace.
const exchangePath = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/trust-rules/exchange"

// TestWorkloadTokenExchange drives the token exchange through the server, on
// each store: a fake CI issuer signs a token, the server reads the keys of
// the issuer through its egress rules, and exchanges the token for an
// access token of a service principal of the namespace. OpenTofu applies and
// the Pulumi CLI updates with the exchanged token. A token that matches no
// rule, a token of another audience, an expired token and a token with alg
// none are refused, each with 401.
func TestWorkloadTokenExchange(t *testing.T) {
	t.Parallel()

	if e2e.UseBinaryServer() {
		t.Skip("the fake issuer is on the loopback, which only the in-process server may reach")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiVersion, tofuVersion := e2e.Pulumi().Single(), e2e.OpenTofu().Single()
	if pulumiVersion == "" || tofuVersion == "" {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	pulumi, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersion)
	if err != nil {
		t.Fatalf("install the Pulumi CLI: %v", err)
	}

	tofu, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install OpenTofu: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			skipWithoutStore(t, db)

			issuer := e2e.NewOIDCIssuerFake(t)
			env := e2e.NewEnvWithIssuer(t.Context(), t, pulumi, db, issuer)

			workloadScenario(t, env, issuer, tofu)
		})
	}
}

// exchangeAnswer is the answer of an exchange.
type exchangeAnswer struct {
	Token     string `json:"token"`
	TokenID   string `json:"id"`
	ExpiresAt string `json:"expiresAt"`
	Principal string `json:"principal"`
	Rule      string `json:"rule"`
}

func workloadScenario(t *testing.T, env *e2e.Env, issuer *e2e.OIDCIssuerFake, tofu e2e.Install) {
	t.Helper()

	ctx := t.Context()

	var worker e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + e2e.Namespace + "/principals", `{"subject":"ci-deployer"}`,
		http.StatusCreated,
	}, &worker)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + e2e.Namespace + "/grants",
		`{"subject":"principal:` + worker.ID + `","role":"maintainer"}`, http.StatusCreated,
	}, nil)

	var rule string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: workloadAgent, Name: "create a discovery rule for the main branch", Method: http.MethodPost,
		Path: trustRulesPath, Body: `{"name":"main","issuer":"` + issuer.URL() + `","audience":"` + workloadAudience +
			`","keySource":"discovery","principal":"` + worker.ID +
			`","conditions":[{"claim":"sub","values":["` + workloadSubject + `"]}]}`,
		Want: http.StatusCreated, Check: captureID(&rule), Scheme: "token",
	})

	now := time.Now()
	claims := func(sub, aud string, exp time.Time) map[string]any {
		return map[string]any{
			"iss": issuer.URL(), "aud": aud, "sub": sub, "iat": now.Unix(), "nbf": now.Unix(), "exp": exp.Unix(),
			"jti": "run-" + sub,
		}
	}

	// The refusals come first, so no exchanged token exists while they run.
	refused := map[string]string{
		"a token of another branch": issuer.Sign(t, claims("repo:acme/infra:ref:refs/heads/feature",
			workloadAudience, now.Add(10*time.Minute))),
		"a token of another audience": issuer.Sign(t, claims(workloadSubject, "https://other.example.com",
			now.Add(10*time.Minute))),
		"an expired token":      issuer.Sign(t, claims(workloadSubject, workloadAudience, now.Add(-10*time.Minute))),
		"a token with alg none": unsignedToken(t, claims(workloadSubject, workloadAudience, now.Add(10*time.Minute))),
	}

	for name, tok := range refused {
		exchange(ctx, t, env, exchangeCall{
			out: nil, name: "exchange is refused: " + name, tok: tok,
			want: http.StatusUnauthorized,
		})
	}

	var got exchangeAnswer

	exchange(ctx, t, env, exchangeCall{
		out: &got, name: "exchange a token of the main branch", want: http.StatusCreated,
		tok: issuer.Sign(t, claims(workloadSubject, workloadAudience, now.Add(10*time.Minute))),
	})

	if got.Token == "" || got.Principal != worker.ID || got.Rule != rule {
		t.Fatalf("exchange answer = %+v, want a token of %s through %s", got, worker.ID, rule)
	}

	checkExchangedLifetime(t, got, now)

	if issuer.Requests() == 0 {
		t.Fatal("the server read no key of the issuer")
	}

	workloadRunsTofu(ctx, t, env, tofu, got.Token)
	workloadRunsPulumi(ctx, t, env, got.Token)
}

// checkExchangedLifetime fails unless the exchanged token expires one hour
// after the exchange.
func checkExchangedLifetime(t *testing.T, got exchangeAnswer, at time.Time) {
	t.Helper()

	exp, err := time.Parse(time.RFC3339, got.ExpiresAt)
	if err != nil {
		t.Fatalf("expiresAt %q: %v", got.ExpiresAt, err)
	}

	if d := exp.Sub(at); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("the exchanged token lives %s, want 1h", d)
	}
}

// exchangeCall is one exchange of the scenario: its step name, the token, the
// status that it must answer, and where to decode the answer, or nil.
type exchangeCall struct {
	out  *exchangeAnswer
	name string
	tok  string
	want int
}

// exchange sends one exchange and decodes the answer.
func exchange(ctx context.Context, t *testing.T, env *e2e.Env, c exchangeCall) {
	t.Helper()

	name, tok, want, out := c.name, c.tok, c.want, c.out

	body, err := json.Marshal(map[string]string{"token": tok})
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}

	// The exchange needs no credential: the token of the workload is the
	// credential, so the request has no Authorization header.
	env.APIStep(ctx, t, "", e2e.APIStepSpec{
		UserAgent: workloadAgent, Name: name, Method: http.MethodPost, Path: exchangePath, Body: string(body),
		Want: want, Scheme: "token",
		Check: func(answer string) error {
			if out == nil {
				return nil
			}

			return json.Unmarshal([]byte(answer), out)
		},
	})
}

// unsignedToken returns a token with alg none and no signature.
func unsignedToken(t *testing.T, claims map[string]any) string {
	t.Helper()

	header := `{"alg":"none","typ":"JWT"}`

	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encode claims: %v", err)
	}

	enc := base64.RawURLEncoding

	return enc.EncodeToString([]byte(header)) + "." + enc.EncodeToString(body) + "."
}

// workloadRunsTofu runs tofu init and tofu apply with the exchanged token.
func workloadRunsTofu(ctx context.Context, t *testing.T, env *e2e.Env, tofu e2e.Install, token string) {
	t.Helper()

	dir := env.TofuDirWithToken(t, token)

	for _, args := range [][]string{
		{"init", "-input=false", "-no-color"},
		{"apply", "-auto-approve", "-input=false", "-no-color"},
	} {
		res, err := env.RunTofuIn(ctx, tofu, dir, args...)
		if err == nil && args[0] == "apply" && !strings.Contains(res.Stdout, "Apply complete") {
			t.Fatalf("tofu apply with the exchanged token did not complete:\n%s", res.Stdout)
		}

		if err != nil || res.ExitCode != 0 {
			t.Fatalf("tofu %s with the exchanged token: %v\n%s\n%s", strings.Join(args, " "), err, res.Stdout,
				res.Stderr)
		}
	}
}

// workloadRunsPulumi runs pulumi up with the exchanged token.
func workloadRunsPulumi(ctx context.Context, t *testing.T, env *e2e.Env, token string) {
	t.Helper()

	stack := e2e.Namespace + "/" + e2e.Project + "/workload"

	// The program of the project reads the stack other through a stack
	// reference, so the administrator makes it first.
	env.Step(ctx, t, step("init the stack of the workload", nil, "stack", "init", stack, "--no-select"))
	env.Step(ctx, t, step("init the stack that the program reads", nil, "stack", "init",
		e2e.Namespace+"/"+e2e.Project+"/other", "--no-select"))
	env.Step(ctx, t, withEnv(step("pulumi up with the exchanged token", nil,
		"up", "--yes", "--non-interactive", "--stack", stack), "PULUMI_ACCESS_TOKEN="+token))
}
