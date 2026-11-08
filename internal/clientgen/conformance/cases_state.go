package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
)

// keyPattern is the shape of a valid Idempotency-Key: 1 to 255 printable ASCII
// characters.
func keyPattern() *regexp.Regexp { return regexp.MustCompile(`^[\x20-\x7e]{1,255}$`) }

// codePreconditionFailed is the documented errorCode of a request whose
// If-Match header names a version that is not current. The published
// description lists it as an errorCode.
const codePreconditionFailed = "request.precondition-failed"

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
		"concurrency/missing-required-if-match-gives-428":               missingIfMatchGives428,
		"compatibility/unknown-field-is-ignored":                        unknownFieldIsIgnored,
		"version/description-version-matches-the-server":                descriptionVersionMatches,
		"version/metadata-names-description-version-and-server-release": metadataNamesVersions,
	}
}

// createOnce calls createProfile, and returns the one exchange of the call.
func createOnce(ctx context.Context, env Env, key string) (Exchange, error) {
	var none Exchange

	if err := need(env, opCreate); err != nil {
		return none, err
	}

	env.Proxy.Reset()

	if _, err := env.Adapter.Call(ctx, createCall(env, key)); err != nil {
		return none, err
	}

	return onlyExchange(env)
}

func mutatingSendsKey(ctx context.Context, env Env) error {
	ex, err := createOnce(ctx, env, "")
	if err != nil {
		return err
	}

	if got := ex.Headers["Idempotency-Key"]; !keyPattern().MatchString(got) {
		return failf("want a valid Idempotency-Key, got %q", got)
	}

	return nil
}

func callerKeyUnchanged(ctx context.Context, env Env) error {
	ex, err := createOnce(ctx, env, "key-from-the-caller-1")
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
	if err := need(env, opGetProfile); err != nil {
		return err
	}

	want, err := ensureProfile(ctx, env)
	if err != nil {
		return err
	}

	res, err := env.Adapter.Call(ctx, profileCall(opGetProfile, seedProfile))
	if err != nil {
		return err
	}

	if want == "" || res.ETag != want {
		return failf("want the ETag %q, got %q", want, res.ETag)
	}

	return nil
}

func ifMatchIsSent(ctx context.Context, env Env) error {
	if err := need(env, opRevise); err != nil {
		return err
	}

	etag, err := ensureProfile(ctx, env)
	if err != nil {
		return err
	}

	env.Proxy.Reset()

	res, err := env.Adapter.Call(ctx, reviseCall(env, etag))
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
	if err := need(env, opRevise); err != nil {
		return err
	}

	if _, err := ensureProfile(ctx, env); err != nil {
		return err
	}

	res, err := env.Adapter.Call(ctx, reviseCall(env, staleVersionETag))
	if err != nil {
		return err
	}

	if err := wantStatus(res, http.StatusPreconditionFailed); err != nil {
		return err
	}

	// wantStatus checks the error too. The check stays here so that the
	// members below are always safe to read.
	e := res.Error
	if e == nil || e.ErrorCode != codePreconditionFailed || e.Message == "" {
		return failf("want a 412 with errorCode %s and a message, got %+v", codePreconditionFailed, e)
	}

	return nil
}

// codeInvalidParams is the JSON-RPC code that an adapter uses for a call that
// the client refuses because of its own arguments.
const codeInvalidParams = -32602

func missingIfMatchGives428(ctx context.Context, env Env) error {
	if err := need(env, opRevise); err != nil {
		return err
	}

	if _, err := ensureProfile(ctx, env); err != nil {
		return err
	}

	// Only exchanges of the call under test count.
	env.Proxy.Reset()

	res, err := env.Adapter.Call(ctx, reviseCall(env, ""))
	if errors.Is(err, ErrRequestRefused) {
		if refusalErr := refusedBeforeTheServer(env, err); refusalErr != nil {
			return refusalErr
		}

		return sentWithIfMatch(ctx, env)
	}

	if err != nil {
		return err
	}

	return wantStatus(res, http.StatusPreconditionRequired)
}

// refusedBeforeTheServer accepts the refusal of a client that refuses the
// call itself, because the operation needs If-Match. The refusal must be an
// invalid-params error, and the request must not have reached the server. A
// client that sends the call and then fails does not pass.
func refusedBeforeTheServer(env Env, err error) error {
	var rpc *RPCError

	if !errors.As(err, &rpc) || rpc.Code != codeInvalidParams {
		return failf("want a refusal with code %d, got %v", codeInvalidParams, err)
	}

	if n := len(env.Proxy.Exchanges()); n != 0 {
		return failf("the client refused the call after it sent %d request(s) to the server", n)
	}

	return nil
}

// sentWithIfMatch is the control of a refusal of the call with no If-Match: the
// same call with an If-Match value must reach the server. A client that refuses
// the call for another argument, for example its body, refuses this call too,
// and does not pass. The value is stale, so the server changes nothing.
func sentWithIfMatch(ctx context.Context, env Env) error {
	env.Proxy.Reset()

	_, err := env.Adapter.Call(ctx, reviseCall(env, staleVersionETag))
	if errors.Is(err, ErrRequestRefused) {
		return failf("the client refused the call with If-Match too, so the refusal is not for If-Match: %v", err)
	}

	if err != nil {
		return err
	}

	if n := len(env.Proxy.Exchanges()); n == 0 {
		return failf("the call with If-Match did not reach the server")
	}

	return nil
}

// wantStatus checks that a call ended with an error of the given status.
func wantStatus(res CallResult, status int) error {
	if res.Status != status || res.Error == nil || res.Error.Code != status {
		return failf("want a %d error, got %d %+v", status, res.Status, res.Error)
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
