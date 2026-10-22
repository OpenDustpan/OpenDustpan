package conformance

import (
	"context"
	"net/http"
	"strings"
)

// errorCases returns the bodies of the cases of the group errors.
// mediaJSON is the media type of every body that the harness sends or injects.
const mediaJSON = "application/json"

func errorCases() map[string]caseFunc {
	return map[string]caseFunc{
		"errors/error-shape-is-decoded":                             errorShapeIsDecoded,
		"errors/forbidden-error-is-decoded":                         forbiddenIsDecoded,
		"errors/response-with-wrong-type-is-rejected":               wrongTypeIsRejected,
		"errors/response-without-required-field-is-rejected":        missingFieldIsRejected,
		"errors/response-with-null-required-is-rejected":            nullRequiredIsRejected,
		"errors/error-body-that-is-not-json":                        errorBodyNotJSON,
		"errors/success-status-outside-the-description-is-rejected": undescribedSuccessStatus,
	}
}

// checkOperation runs the fixture of one operation that the client covers.
func checkOperation(ctx context.Context, env Env, op string) error {
	if err := need(env, op); err != nil {
		return err
	}

	table, err := fixtures(ctx, env)
	if err != nil {
		return err
	}

	return checkFixture(ctx, env, op, table[op])
}

func errorShapeIsDecoded(ctx context.Context, env Env) error {
	return checkOperation(ctx, env, opGetSink)
}

func forbiddenIsDecoded(ctx context.Context, env Env) error {
	return checkOperation(ctx, env, opPreferences)
}

// invalidAnswer injects a 200 answer for getCapabilities and expects the client
// to reject it.
func invalidAnswer(ctx context.Context, env Env, body string) error {
	if err := need(env, opCapabilities); err != nil {
		return err
	}

	f := faultOf(FaultStatus, opCapabilities)
	f.Status, f.ContentType, f.Body = http.StatusOK, mediaJSON, []byte(body)
	env.Proxy.Inject(f)

	res, err := env.Adapter.Call(ctx, callOf(opCapabilities, nil))
	if err != nil {
		return err
	}

	if res.Error == nil || !res.Error.Invalid || res.Body != nil {
		return failf("want an invalid-answer error and no body for %s, got %+v", body, res)
	}

	return nil
}

func wrongTypeIsRejected(ctx context.Context, env Env) error {
	return invalidAnswer(ctx, env, `{"apiVersion": 5}`)
}

// nullRequiredIsRejected checks that null for a required member that cannot be
// null is a wrong type, and not a zero value.
func nullRequiredIsRejected(ctx context.Context, env Env) error {
	return invalidAnswer(ctx, env, `{"apiVersion": null, "installationKind": "dev"}`)
}

func missingFieldIsRejected(ctx context.Context, env Env) error {
	return invalidAnswer(ctx, env, `{"installationKind": "dev"}`)
}

// undescribedSuccessStatus injects a 202 and a 204 for getCapabilities, which
// the description answers with 200 only. The 202 holds the true body, so that
// only the status is wrong. The client must reject both.
func undescribedSuccessStatus(ctx context.Context, env Env) error {
	if err := need(env, opCapabilities); err != nil {
		return err
	}

	want, err := get(ctx, env, capabilitiesPath, withoutToken)
	if err != nil {
		return err
	}

	for _, status := range []int{http.StatusAccepted, http.StatusNoContent} {
		f := faultOf(FaultStatus, opCapabilities)
		f.Status, f.ContentType = status, mediaJSON

		if status == http.StatusAccepted {
			f.Body = want.body
		}

		env.Proxy.Inject(f)

		res, callErr := env.Adapter.Call(ctx, callOf(opCapabilities, nil))
		if callErr != nil {
			return callErr
		}

		if res.Error == nil || !res.Error.Invalid || res.Body != nil {
			return failf("want an invalid-answer error and no body for status %d, got %+v", status, res)
		}
	}

	return nil
}

func errorBodyNotJSON(ctx context.Context, env Env) error {
	if err := need(env, opCapabilities); err != nil {
		return err
	}

	f := faultOf(FaultStatus, opCapabilities)
	f.Status, f.ContentType, f.Body = http.StatusBadGateway, "text/plain", []byte("bad gateway")
	env.Proxy.Inject(f)

	res, err := env.Adapter.Call(ctx, callOf(opCapabilities, nil))
	if err != nil {
		return err
	}

	e := res.Error
	if e == nil || e.Code != http.StatusBadGateway || e.Transport || !strings.Contains(e.Message, "bad gateway") {
		return failf("want a 502 server error with the text, got %+v", e)
	}

	return nil
}
