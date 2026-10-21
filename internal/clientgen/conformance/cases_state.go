package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
)

// keyPattern is the shape of a valid Idempotency-Key: 1 to 255 printable ASCII
// characters.
func keyPattern() *regexp.Regexp { return regexp.MustCompile(`^[\x20-\x7e]{1,255}$`) }

// stateCases returns the bodies of the cases of the groups idempotency,
// concurrency, compatibility and version.
func stateCases() map[string]caseFunc {
	return map[string]caseFunc{
		"idempotency/mutating-call-sends-a-key":                         mutatingSendsKey,
		"idempotency/caller-key-is-sent-unchanged":                      callerKeyUnchanged,
		"idempotency/read-call-sends-no-key":                            readSendsNoKey,
		"concurrency/etag-is-returned":                                  etagIsReturned,
		"concurrency/if-match-is-sent":                                  ifMatchIsSent,
		"concurrency/stale-if-match-gives-412":                          staleIfMatchGives412,
		"compatibility/unknown-field-is-ignored":                        unknownFieldIsIgnored,
		"version/description-version-matches-the-server":                descriptionVersionMatches,
		"version/metadata-names-description-version-and-server-release": metadataNamesVersions,
	}
}

// putApp calls putGitHostApp through the adapter.
func putApp(ctx context.Context, env Env, key, ifMatch string) (CallResult, error) {
	var none CallResult

	body, err := env.appBody()
	if err != nil {
		return none, err
	}

	p := appCall(opPutApp)
	p.Body, p.IdempotencyKey, p.IfMatch = body, key, ifMatch

	return env.Adapter.Call(ctx, p)
}

// putAppOnce calls putGitHostApp after the app exists, and returns the one
// exchange of the call.
func putAppOnce(ctx context.Context, env Env, key string) (Exchange, error) {
	var none Exchange

	if err := need(env, opPutApp); err != nil {
		return none, err
	}

	if _, err := ensureApp(ctx, env); err != nil {
		return none, err
	}

	env.Proxy.Reset()

	if _, err := putApp(ctx, env, key, ""); err != nil {
		return none, err
	}

	return onlyExchange(env)
}

func mutatingSendsKey(ctx context.Context, env Env) error {
	ex, err := putAppOnce(ctx, env, "")
	if err != nil {
		return err
	}

	if got := ex.Headers["Idempotency-Key"]; !keyPattern().MatchString(got) {
		return failf("want a valid Idempotency-Key, got %q", got)
	}

	return nil
}

func callerKeyUnchanged(ctx context.Context, env Env) error {
	ex, err := putAppOnce(ctx, env, "key-from-the-caller-1")
	if err != nil {
		return err
	}

	if got := ex.Headers["Idempotency-Key"]; got != "key-from-the-caller-1" {
		return failf("want the key of the caller, got %q", got)
	}

	return nil
}

func readSendsNoKey(ctx context.Context, env Env) error {
	if err := need(env, opListSinks); err != nil {
		return err
	}

	ex, err := callOnce(ctx, env, sinksCall())
	if err != nil {
		return err
	}

	if _, has := ex.Headers["Idempotency-Key"]; has {
		return failf("a read call sent an Idempotency-Key")
	}

	return nil
}

func etagIsReturned(ctx context.Context, env Env) error {
	if err := need(env, opGetApp); err != nil {
		return err
	}

	want, err := ensureApp(ctx, env)
	if err != nil {
		return err
	}

	res, err := env.Adapter.Call(ctx, appCall(opGetApp))
	if err != nil {
		return err
	}

	if want == "" || res.ETag != want {
		return failf("want the ETag %q, got %q", want, res.ETag)
	}

	return nil
}

func ifMatchIsSent(ctx context.Context, env Env) error {
	if err := need(env, opPutApp); err != nil {
		return err
	}

	etag, err := ensureApp(ctx, env)
	if err != nil {
		return err
	}

	env.Proxy.Reset()

	res, err := putApp(ctx, env, "", etag)
	if err != nil {
		return err
	}

	ex, err := onlyExchange(env)
	if err != nil {
		return err
	}

	if got := ex.Headers["If-Match"]; got != etag {
		return failf("want If-Match %q, got %q", etag, got)
	}

	if res.Error != nil || res.ETag == "" || res.ETag == etag {
		return failf("want a new ETag after the write, got %+v", res)
	}

	return nil
}

func staleIfMatchGives412(ctx context.Context, env Env) error {
	if err := need(env, opPutApp); err != nil {
		return err
	}

	if _, err := ensureApp(ctx, env); err != nil {
		return err
	}

	// The version of an app grows by one for each write, so a version far
	// ahead of it is never current.
	res, err := putApp(ctx, env, "", staleVersionETag)
	if err != nil {
		return err
	}

	if res.Status != http.StatusPreconditionFailed || res.Error == nil ||
		res.Error.Code != http.StatusPreconditionFailed {
		return failf("want a 412 error, got %d %+v", res.Status, res.Error)
	}

	return nil
}

func unknownFieldIsIgnored(ctx context.Context, env Env) error {
	if err := need(env, opCapabilities); err != nil {
		return err
	}

	f := faultOf(FaultAddField, opCapabilities)
	f.Field, f.Value = "/addedByTheProxy", json.RawMessage(`1`)
	env.Proxy.Inject(f)

	res, err := env.Adapter.Call(ctx, callOf(opCapabilities, nil))
	if err != nil {
		return err
	}

	if res.Error != nil {
		return failf("the client failed on an unknown field: %+v", res.Error)
	}

	want, err := get(ctx, env, capabilitiesPath, withoutToken)
	if err != nil {
		return err
	}

	if !sameJSON(res.Body, want.body) {
		return failf("the body of the client is %s and the body of the server is %s", res.Body, want.body)
	}

	return nil
}

// describedInfo is the part of the description that the case reads.
type describedInfo struct {
	Version string `json:"version"`
}

// describedDocument is the part of the description that the case reads.
type describedDocument struct {
	Info describedInfo `json:"info"`
}

// describedVersion reads info.version of the description that the server serves.
func describedVersion(ctx context.Context, env Env) (string, error) {
	raw, err := env.Server.Description(ctx)
	if err != nil {
		return "", err
	}

	var doc describedDocument
	if err = json.Unmarshal(raw, &doc); err != nil {
		return "", failf("the description is not JSON: %v", err)
	}

	return doc.Info.Version, nil
}

func descriptionVersionMatches(ctx context.Context, env Env) error {
	want, err := describedVersion(ctx, env)
	if err != nil {
		return err
	}

	if want == "" || env.Info.DescriptionVersion != want {
		return failf("the client was made from description %q and the server serves %q",
			env.Info.DescriptionVersion, want)
	}

	return nil
}

func metadataNamesVersions(_ context.Context, env Env) error {
	i := env.Info
	if i.Client == "" || i.ClientVersion == "" || i.DescriptionVersion == "" {
		return failf("the client metadata is not complete: %+v", i)
	}

	if i.ServerRelease != env.Server.Release() {
		return failf(
			"the client names the server release %q and the server is %q",
			i.ServerRelease,
			env.Server.Release(),
		)
	}

	return nil
}
