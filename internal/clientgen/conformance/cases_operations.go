package conformance

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// operationCases returns the bodies of the cases of the group operations.
func operationCases() map[string]caseFunc {
	return map[string]caseFunc{
		"operations/every-operation-is-called":                              everyOperationIsCalled,
		"operations/public-operation-sends-no-token":                        publicSendsNoToken,
		"operations/signed-in-operation-sends-bearer-token":                 signedInSendsToken,
		"operations/path-parameter-is-escaped":                              pathParameterIsEscaped,
		"operations/user-agent-names-client-description-and-server-release": userAgentNamesVersions,
		"operations/token-is-not-sent-after-redirect-to-another-host":       tokenStaysOnTheHost,
	}
}

// everyOperationIsCalled calls each operation of the client once and checks
// the status, the error code and the body.
func everyOperationIsCalled(ctx context.Context, env Env) error {
	table, err := fixtures(ctx, env)
	if err != nil {
		return err
	}

	for _, op := range env.Operations {
		fx, ok := table[op]
		if !ok {
			return failf("the suite has no call for the operation %s", op)
		}

		if checkErr := checkFixture(ctx, env, op, fx); checkErr != nil {
			return checkErr
		}
	}

	return nil
}

func publicSendsNoToken(ctx context.Context, env Env) error {
	if err := need(env, opCapabilities); err != nil {
		return err
	}

	ex, err := callOnce(ctx, env, callOf(opCapabilities, nil))
	if err != nil {
		return err
	}

	if _, has := ex.Headers["Authorization"]; has {
		return failf("a public operation sent an Authorization header")
	}

	return nil
}

func signedInSendsToken(ctx context.Context, env Env) error {
	if err := need(env, opListSinks); err != nil {
		return err
	}

	ex, err := callOnce(ctx, env, sinksCall())
	if err != nil {
		return err
	}

	if got := ex.Headers["Authorization"]; got != "Bearer <redacted>" {
		return failf("want a Bearer token, got %q", got)
	}

	return nil
}

func pathParameterIsEscaped(ctx context.Context, env Env) error {
	if err := need(env, opGetSink); err != nil {
		return err
	}

	ex, err := callOnce(ctx, env, sinkCall("a/b c%"))
	if err != nil {
		return err
	}

	want := sinksPath + "/" + escapedSegment()
	if ex.Path != want {
		return failf("want the path %s, got %s", want, ex.Path)
	}

	return nil
}

func userAgentNamesVersions(ctx context.Context, env Env) error {
	if err := need(env, opCapabilities); err != nil {
		return err
	}

	ex, err := callOnce(ctx, env, callOf(opCapabilities, nil))
	if err != nil {
		return err
	}

	ua := ex.Headers["User-Agent"]

	for _, part := range []string{env.Info.ClientVersion, env.Info.DescriptionVersion, env.Info.ServerRelease} {
		if part == "" || !strings.Contains(ua, part) {
			return failf("the User-Agent %q does not name %q", ua, part)
		}
	}

	return nil
}

func tokenStaysOnTheHost(ctx context.Context, env Env) error {
	if err := need(env, opListSinks); err != nil {
		return err
	}

	env.Proxy.Reset()
	env.Proxy.Inject(faultOf(FaultRedirect, opListSinks))

	// The answer of the second listener is 204 with no body, so the client may
	// report it as an invalid answer. The error of the call is not the verdict:
	// the exchanges are.
	res, err := env.Adapter.Call(ctx, sinksCall())
	if err != nil && !errors.Is(err, ErrRequestRefused) {
		return err
	}

	// The fault must fire. A run in which the redirect does not happen proves
	// nothing about the token.
	redirected := false

	for _, exchange := range env.Proxy.Exchanges() {
		if exchange.Status == http.StatusTemporaryRedirect {
			redirected = true
		}
	}

	if !redirected {
		return failf("the proxy did not redirect the request, so the case proves nothing")
	}

	other := env.Proxy.OtherExchanges()

	switch len(other) {
	case 0:
		// A client may refuse the redirect and send nothing to the other
		// host. Then the call must fail as a transport error.
		if err != nil || res.Error == nil || !res.Error.Transport {
			return failf("want a transport error when the client does not follow the redirect, got %+v", res)
		}
	case 1:
		if _, has := other[0].Headers["Authorization"]; has {
			return failf("the client sent the token to another host after a redirect")
		}
	default:
		return failf("want at most 1 request on the other host, got %d", len(other))
	}

	return nil
}
