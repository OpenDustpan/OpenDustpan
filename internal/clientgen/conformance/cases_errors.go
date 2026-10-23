package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// errorCases returns the bodies of the cases of the group errors.
// mediaJSON is the media type of every body that the harness sends or injects.
const mediaJSON = "application/json"

func errorCases() map[string]caseFunc {
	return map[string]caseFunc{
		"errors/error-shape-is-decoded":                                errorShapeIsDecoded,
		"errors/forbidden-error-is-decoded":                            forbiddenIsDecoded,
		"errors/response-with-wrong-type-is-rejected":                  wrongTypeIsRejected,
		"errors/response-without-required-field-is-rejected":           missingFieldIsRejected,
		"errors/response-with-null-required-is-rejected":               nullRequiredIsRejected,
		"errors/error-body-that-is-not-json":                           errorBodyNotJSON,
		"errors/success-status-outside-the-description-is-rejected":    undescribedSuccessStatus,
		"errors/two-success-statuses-are-accepted-and-others-rejected": twoSuccessStatuses,
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

// gitHostAppAnswer is a valid answer of putGitHostApp.
const gitHostAppAnswer = `{"kind": "github", "appID": "1", "apiAddress": "https://api.github.com", "hookID": "2",
	"privateKey": {"setID": "a", "createdAt": "2026-10-22T19:20:44Z", "set": true},
	"eventSecret": {"setID": "b", "createdAt": "2026-10-22T19:20:44Z", "set": true},
	"createdAt": "2026-10-22T19:20:44Z", "updatedAt": "2026-10-22T19:20:44Z", "version": 1}`

// gitHostAppRequest is a valid request body of putGitHostApp.
const gitHostAppRequest = `{"appID": "1", "apiAddress": "https://api.github.com", "privateKey": "k", "eventSecret": "s"}`

// putAppCall makes the call of putGitHostApp.
func putAppCall() CallParams {
	p := callOf(opPutApp, map[string]string{"kind": "github"})
	p.Body = json.RawMessage(gitHostAppRequest)

	return p
}

// injectedPut makes the proxy answer the next putGitHostApp call with a status
// and a body, calls the operation, and returns the result.
func injectedPut(ctx context.Context, env Env, status int, body string) (CallResult, error) {
	f := faultOf(FaultStatus, opPutApp)
	f.Status, f.ContentType, f.Body = status, mediaJSON, []byte(body)
	env.Proxy.Inject(f)

	return env.Adapter.Call(ctx, putAppCall())
}

// twoSuccessStatuses checks an operation that the description answers with
// two success statuses. putGitHostApp answers 201 for a first write and 200
// for a replacement, so the client must accept both with their body, and must
// refuse every other 2xx status, also one that holds the true body. The proxy
// answers in place of the server, so the case needs no Git host.
func twoSuccessStatuses(ctx context.Context, env Env) error {
	if err := need(env, opPutApp); err != nil {
		return err
	}

	for _, status := range []int{http.StatusOK, http.StatusCreated} {
		res, err := injectedPut(ctx, env, status, gitHostAppAnswer)
		if err != nil {
			return err
		}

		if res.Error != nil || res.Status != status || !sameJSON(res.Body, []byte(gitHostAppAnswer)) {
			return failf("want the accepted status %d with the body, got %+v", status, res)
		}
	}

	others := []int{
		http.StatusAccepted, http.StatusNonAuthoritativeInfo, http.StatusNoContent, http.StatusResetContent,
		http.StatusPartialContent, 299,
	}

	for _, status := range others {
		if err := refusedPut(ctx, env, status); err != nil {
			return err
		}
	}

	return nil
}

// refusedPut answers putGitHostApp with a 2xx status that the description does
// not give, and checks that the client refuses it. The answer holds the true
// body, except for a status that has no body, so that only the status is wrong.
func refusedPut(ctx context.Context, env Env, status int) error {
	body := gitHostAppAnswer
	if status == http.StatusNoContent || status == http.StatusResetContent {
		body = ""
	}

	res, err := injectedPut(ctx, env, status, body)
	if err != nil {
		return err
	}

	if res.Error == nil || !res.Error.Invalid || res.Body != nil {
		return failf("want an invalid-answer error and no body for status %d, got %+v", status, res)
	}

	return nil
}
